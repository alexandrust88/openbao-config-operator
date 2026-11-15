# Connect to OpenBao

This guide points the installed operator at an OpenBao server you run: you declare the server,
give the operator an OpenBao identity, create a connection and check that it logs in. Every field
is described in the [connections reference](../reference/connections.md).

## Before you begin

- The operator is installed ([Install](install.md)).
- OpenBao is initialised, unsealed and reachable from the operator pods, normally over HTTPS.
- You have the PEM CA bundle that signed the OpenBao server certificate, unless a public CA
  signed it.
- A `bao` CLI is logged in to OpenBao with the rights to enable an auth method, write a policy and
  create a role.

## Choose the connection kind and the login

| you want | kind | who creates it | it logs in as |
|----------|------|----------------|---------------|
| each team configures its own part of OpenBao | `OpenBaoConnection` in the team's namespace | the platform team | a ServiceAccount of that namespace, with a short-lived TokenRequest token |
| the platform team configures OpenBao for everyone | `ClusterOpenBaoConnection` | the platform team | the operator's own ServiceAccount (injected identity), a named ServiceAccount, an AppRole or a token |

Both kinds support four login methods. Prefer the first two: they need no stored credential.

| login | needs in OpenBao | credential in Kubernetes |
|-------|------------------|--------------------------|
| `kubernetes` | the kubernetes auth method, which reviews tokens with the TokenReview API | none |
| `jwt` | the jwt auth method with the cluster's OIDC issuer or its public keys | none |
| `appRole` | the approle auth method | a Secret with the role ID and secret ID |
| `token` | a periodic token | a Secret, or on a `ClusterOpenBaoConnection` a file in the operator pod |

[Install: tenancy models](install.md#tenancy-models) helps you choose between per-team and shared
connections.

## 1. Declare the server

The platform team declares each OpenBao server once. Store the CA bundle in a ConfigMap (or a
Secret) that tenants cannot write:

```bash
kubectl -n openbao create configmap openbao-ca --from-file=ca.crt=ca.crt
```

Then create the `OpenBaoServer`. `allowedNamespaces` selects the namespaces whose
`OpenBaoConnection`s may use it, and `allowedAudiences` lists the token audiences they may
request:

```yaml title="examples/core/openbaoserver.yaml"
--8<-- "examples/core/openbaoserver.yaml"
```

```bash
kubectl apply -f examples/core/openbaoserver.yaml
kubectl wait openbaoserver/openbao --for=condition=Ready --timeout=2m
```

```text title="Expected output"
openbaoserver.core.openbaoconfig.io/openbao created
openbaoserver.core.openbaoconfig.io/openbao condition met
```

`Ready=True` means the server answered `sys/health` over TLS and is unsealed. The `VERSION`
column of `kubectl get openbaoserver` shows the OpenBao version.

!!! warning "No plain HTTP"
    An `http://` address is refused unless the server sets `allowInsecureHTTP: true`. That is for a
    local dev server only, as in the [quickstart](quickstart.md): every token and secret input
    then crosses the network in clear text. [Hardening](../security/hardening.md#r-config-03-forbid-the-escape-hatches-in-production)
    shows how to forbid it in production.

## 2. Give the operator an identity in OpenBao

Write a policy that allows only what the connection will manage
([OpenBao policy for the operator](../operate/openbao-policy.md) has templates per kind), then
create the login for it. The commands below follow the tenant example of step 3: team `team-a`
works in the OpenBao namespace `tenants/team-a` and logs in there.

=== "Kubernetes auth"

    ```bash
    bao auth enable -namespace=tenants/team-a kubernetes
    bao write -namespace=tenants/team-a auth/kubernetes/config \
      kubernetes_host=https://kubernetes.default.svc:443
    bao policy write -namespace=tenants/team-a team-a-config team-a-config.hcl
    bao write -namespace=tenants/team-a auth/kubernetes/role/team-a-configurer \
      bound_service_account_names=openbao-configurer \
      bound_service_account_namespaces=team-a \
      audience=openbao \
      token_policies=team-a-config \
      token_ttl=1h token_max_ttl=4h
    ```

    OpenBao reviews the token with its own ServiceAccount when it runs in the same cluster; bind
    that ServiceAccount to the `system:auth-delegator` ClusterRole. When OpenBao runs elsewhere,
    also pass `kubernetes_ca_cert=@ca.crt` and a `token_reviewer_jwt` of a ServiceAccount that may
    create TokenReviews.

=== "JWT auth"

    OpenBao fetches the cluster's discovery document and signing keys without credentials, and by
    default the API server serves them only to authenticated callers. Allow anonymous reads; the
    binding exposes only the issuer metadata and the public keys:

    ```bash
    kubectl create clusterrolebinding oidc-reviewer \
      --clusterrole=system:service-account-issuer-discovery --group=system:unauthenticated
    ```

    ```bash
    bao auth enable -namespace=tenants/team-a -path=jwt-k8s jwt
    bao write -namespace=tenants/team-a auth/jwt-k8s/config \
      oidc_discovery_url=https://kubernetes.default.svc.cluster.local \
      oidc_discovery_ca_pem=@ca.crt \
      bound_issuer=https://kubernetes.default.svc.cluster.local
    bao policy write -namespace=tenants/team-a team-a-config team-a-config.hcl
    bao write -namespace=tenants/team-a auth/jwt-k8s/role/team-a-configurer \
      role_type=jwt \
      user_claim=sub \
      bound_audiences=openbao \
      bound_subject=system:serviceaccount:team-a:openbao-configurer \
      token_policies=team-a-config \
      token_ttl=1h token_max_ttl=4h
    ```

    `kubectl get --raw /.well-known/openid-configuration` shows the cluster's issuer. If OpenBao
    cannot reach the discovery URL, set `jwt_validation_pubkeys` to the keys from
    `kubectl get --raw /openid/v1/jwks` instead.

=== "AppRole"

    ```bash
    bao auth enable -namespace=tenants/team-a approle
    bao policy write -namespace=tenants/team-a team-a-config team-a-config.hcl
    bao write -namespace=tenants/team-a auth/approle/role/team-a-configurer \
      token_policies=team-a-config token_ttl=1h token_max_ttl=4h secret_id_ttl=720h
    role_id=$(bao read -namespace=tenants/team-a -field=role_id auth/approle/role/team-a-configurer/role-id)
    secret_id=$(bao write -namespace=tenants/team-a -f -field=secret_id auth/approle/role/team-a-configurer/secret-id)
    kubectl create secret generic openbao-approle -n team-a \
      --from-literal=role_id="$role_id" --from-literal=secret_id="$secret_id"
    ```

    Write a new secret ID into the Secret before `secret_id_ttl` ends. The connection reads it at
    the next login, without a restart.

=== "Token"

    ```bash
    bao policy write -namespace=tenants/team-a team-a-config team-a-config.hcl
    bao token create -namespace=tenants/team-a -orphan -period=24h -policy=team-a-config -field=token \
      | kubectl create secret generic openbao-token -n team-a --from-file=token=/dev/stdin
    ```

    Use a periodic token: the operator renews a renewable token after a third of its TTL, and
    cannot renew any other.

If the connection works in the root namespace, leave out every `-namespace` flag.

## 3. Create the connection

=== "Tenant connection"

    The platform team creates the namespace, its ServiceAccount and the connection. The namespace
    needs the label that the server's `allowedNamespaces` selects:

    ```bash
    kubectl create namespace team-a
    kubectl label namespace team-a openbaoconfig.io/tenant=true
    kubectl -n team-a create serviceaccount openbao-configurer
    ```

    ```yaml title="examples/core/openbaoconnection.yaml"
    --8<-- "examples/core/openbaoconnection.yaml"
    ```

    For another login method, replace `auth` with one of these blocks; every Secret and
    ServiceAccount is in the connection's namespace:

    ```yaml
    auth:
      jwt:
        mountPath: jwt-k8s
        role: team-a-configurer
        serviceAccountRef:
          name: openbao-configurer
        audiences:
          - openbao
    ```

    ```yaml
    auth:
      appRole:
        roleIDSecretRef:
          name: openbao-approle
          key: role_id
        secretIDSecretRef:
          name: openbao-approle
          key: secret_id
    ```

    ```yaml
    auth:
      token:
        secretRef:
          name: openbao-token
          key: token
    ```

=== "Cluster connection"

    A `ClusterOpenBaoConnection` is usable from every namespace its `allowedNamespaces` selects.
    With Kubernetes or JWT auth and no `serviceAccountRef`, it logs in as the operator itself (see
    [below](#log-in-as-the-operator)):

    ```yaml title="examples/core/clusteropenbaoconnection-injectedidentity.yaml"
    --8<-- "examples/core/clusteropenbaoconnection-injectedidentity.yaml"
    ```

    To read a token from a file in the operator pod, such as an agent sidecar's sink, use
    `auth.token.filesystem`:

    ```yaml title="examples/core/clusteropenbaoconnection-filesystem.yaml"
    --8<-- "examples/core/clusteropenbaoconnection-filesystem.yaml"
    ```

Apply the connection:

```bash
kubectl apply -f examples/core/openbaoconnection.yaml
```

### Log in as the operator

A `ClusterOpenBaoConnection` with `kubernetes` or `jwt` auth and no `serviceAccountRef` presents
the operator's own ServiceAccount token. The chart mounts a projected token for the audience
`openbao-config-operator` at `/var/run/secrets/openbao/token`, which is the default `tokenPath`.
Create the OpenBao role for the chart's ServiceAccount and namespace, with that audience:

```bash
bao write auth/kubernetes/role/openbao-config-operator \
  bound_service_account_names=openbao-config-operator \
  bound_service_account_namespaces=openbao-config-operator \
  audience=openbao-config-operator \
  token_policies=openbao-config-operator \
  token_ttl=1h token_max_ttl=4h
```

The ServiceAccount name is the chart's full name: the release name `openbao-config-operator`
unless you set `fullnameOverride` or `serviceAccount.name`. [OpenBao self-init](../guides/self-init.md)
creates this role at OpenBao's first boot, without a root token.

## 4. Test the connection

```bash
kubectl -n team-a wait openbaoconnection/openbao --for=condition=Ready --timeout=2m
kubectl -n team-a get openbaoconnection openbao
```

```text title="Expected output"
openbaoconnection.core.openbaoconfig.io/openbao condition met
```

The `get` shows `Ready` `True`, the server and its OpenBao version. `Ready=True` with the reason
`Available` means the server admits the connection, the login worked and
`auth/token/lookup-self` succeeded. The operator checks again every minute, and when the server or
the namespace labels change.

Check what the identity may do before you create resources. Log in as the identity, then ask
OpenBao about the paths your resources will write:

```bash
bao token capabilities -namespace=tenants/team-a sys/policies/acl/team-a-app
```

A resource that hits a missing capability reports `PermissionDenied`.

## Troubleshooting

| symptom | check |
|---------|-------|
| server `Ready=False`, reason `Unreachable` | the `address`, DNS, the chart's NetworkPolicy egress to OpenBao (`networkPolicy.openbao`), and `x509` errors in the message: the CA bundle |
| server `Ready=False`, reason `ReferenceNotReady` | the CA bundle ConfigMap or Secret and its key (default `ca.crt`) exist in the namespace you named |
| server `Ready=False`, reason `Sealed` | unseal OpenBao; resources wait meanwhile |
| connection reason `ConnectionDenied` | the server's `allowedNamespaces` against `kubectl get namespace team-a --show-labels`, and `allowedAudiences` against the connection's `audiences` |
| connection reason `CredentialsNotFound` | the names, keys and namespaces of the Secrets in `spec.auth`; for a token file, the volume mount |
| connection reason `AuthenticationFailed` | `kubectl -n team-a describe openbaoconnection openbao` shows the OpenBao error: the role name, its bound ServiceAccount, namespace and audience, the `system:auth-delegator` binding, and `authNamespace` |
| connection reason `ReferenceNotReady` | the connection's OpenBao `namespace` exists: `bao namespace list` in its parent |

[Status and events](../reference/status-and-events.md#conditions-of-connections-and-servers) has every reason.

## Related

- [Connections reference](../reference/connections.md): every field, auth mode and status value.
- API reference: [OpenBaoServer](../reference/api/core.md#openbaoserver),
  [OpenBaoConnection](../reference/api/core.md#openbaoconnection),
  [ClusterOpenBaoConnection](../reference/api/core.md#clusteropenbaoconnection).
- [Namespaces and tenants](../guides/namespaces-and-tenants.md): OpenBao namespaces,
  `authNamespace` and one connection per team.
