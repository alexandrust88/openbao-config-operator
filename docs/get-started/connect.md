---
title: Connect the operator to an existing OpenBao server
description: Declare a BaoServer, choose a scoped connection identity and verify authentication before managing configuration.
---

# Connect to OpenBao

This guide points OpenBao Config Operator at an OpenBao server you run: you declare the server,
give the operator an OpenBao identity, create a connection and check that it logs in. It builds the
connection every guide uses: the `BaoConnection` named `openbao` in the Kubernetes namespace
`openbao-config`, working in the root OpenBao namespace. Every field is described in the
[connections reference](../reference/connections.md).

An OpenBao namespace is an isolated part of one OpenBao server, with its own policies, auth methods
and mounts; it is not a Kubernetes namespace. A tenant is a team that gets a Kubernetes namespace and
an OpenBao namespace of its own. In the [quickstart](quickstart.md), `openbao-config` is the name of
a ServiceAccount; on this page and in the guides it is the name of a Kubernetes namespace.

## Before you begin

- The operator is installed ([Install](install.md)).
- OpenBao is initialised, unsealed and reachable from the operator pods, normally over HTTPS.
- You have the PEM CA bundle that signed the OpenBao server certificate, saved as `ca.crt`, unless
  a public CA signed it.
- A `bao` CLI is logged in to OpenBao with the rights to enable an auth method, write a policy and
  create a role.

## Choose the connection kind and the login

| you want | kind | who creates it | it logs in as |
|----------|------|----------------|---------------|
| the resources of one Kubernetes namespace configure OpenBao, as in the guides (`openbao-config`), or each tenant configures its own part | `BaoConnection` in that Kubernetes namespace | the platform team | a ServiceAccount of that Kubernetes namespace, or the AppRole or token configured for that connection |
| the platform team configures OpenBao for everyone | `ClusterBaoConnection` | the platform team | the operator's own ServiceAccount (injected identity), a named ServiceAccount, an AppRole or a token |

Both kinds support four login methods. For workloads in Kubernetes, prefer a short-lived
ServiceAccount token when the OpenBao deployment can validate it. The right method depends on
network reachability and which system owns credential rotation.

| Login | When to use it | Credential location | Required OpenBao setup | Production guidance |
| --- | --- | --- | --- | --- |
| `kubernetes` | OpenBao can call the cluster's TokenReview API | Short-lived TokenRequest token; no stored login Secret | Kubernetes auth mount, reviewer access and a role bound to the ServiceAccount, namespace and audience | A practical default for in-cluster identity; restrict the role and use a dedicated audience |
| `jwt` | OpenBao can verify the cluster's issuer and signing keys without TokenReview | Short-lived TokenRequest token; no stored login Secret | JWT auth mount with trusted discovery/JWKS or public keys, plus issuer, audience and subject bindings | Useful across clusters; ensure signing-key rotation and issuer reachability are handled |
| `appRole` | Kubernetes identity is unavailable or an existing credential-delivery system manages AppRole | Role ID and Secret ID in Kubernetes Secrets | AppRole mount and a least-privilege role | Arrange Secret ID rotation before expiry; avoid copying an unbounded credential between teams |
| `token` | An external system already supplies and rotates a scoped OpenBao token | Kubernetes Secret; a cluster connection can instead read a token file in the operator pod | An appropriately scoped token, normally periodic for an ongoing connection | Use a managed token lifecycle and narrow policy; never use the root token for the operator |

For namespaced connections, every referenced Secret and ServiceAccount belongs to the connection's
Kubernetes namespace. A cluster connection can use the operator's projected identity; a tenant
connection cannot. The [connection reference](../reference/connections.md#authentication-methods)
lists the fields for both kinds.

[Install options: tenancy models](../operate/install-options.md#tenancy-models) helps you choose between per-team and shared
connections.

## 1. Declare the server

The platform team declares each OpenBao server once. Create the Kubernetes namespace
`openbao-config`, label it so the server admits its connections, and store the CA bundle in it.
The label `obco.dev/tenant=true` has no meaning of its own: it is the label the server's
`allowedNamespaces` selects below, so the platform's own Kubernetes namespace needs it as much as a
tenant's does.

```bash
kubectl create namespace openbao-config
kubectl label namespace openbao-config obco.dev/tenant=true
kubectl -n openbao-config create configmap openbao-ca --from-file=ca.crt=ca.crt
```

Then declare the `BaoServer`. Save this as `baoserver.yaml`:

```yaml
apiVersion: core.obco.dev/v1alpha1
kind: BaoServer
metadata:
  name: openbao
spec:
  address: https://<openbao-host>:8200
  caBundle:
    configMapRef:
      namespace: openbao-config
      name: openbao-ca
  allowedNamespaces:
    matchLabels:
      obco.dev/tenant: "true"
  allowedAudiences:
    - openbao
```

Replace `<openbao-host>` with the host name of the OpenBao API as the operator pods reach it, for
example `openbao.openbao.svc`; change the port if yours differs. Leave out `caBundle` when a public
CA signed the server certificate. `allowedNamespaces` admits the `BaoConnection`s of the
Kubernetes namespaces with this label, and `allowedAudiences` lists the token audiences they may
request. Neither field applies to a `ClusterBaoConnection`, which has its own
`allowedNamespaces`.

```bash
kubectl apply -f baoserver.yaml
kubectl wait baoserver/openbao --for=condition=Ready --timeout=2m
```

```text title="Expected output"
baoserver.core.obco.dev/openbao created
baoserver.core.obco.dev/openbao condition met
```

`Ready=True` means the server answered `sys/health` over TLS and is unsealed. The `VERSION`
column of `kubectl get baoserver` shows the OpenBao version.

!!! warning "No plain HTTP"
    An `http://` address is refused unless the server sets `allowInsecureHTTP: true`. That is for a
    local dev server only, as in the [quickstart](quickstart.md): every token and secret input
    then crosses the network in clear text. [Hardening](../security/hardening.md#r-config-03-forbid-the-escape-hatches-in-production)
    shows how to forbid it in production.

## 2. Give the operator an identity in OpenBao

The connection logs in as the ServiceAccount `openbao-configurer` of the Kubernetes namespace
`openbao-config`. Create it now; the AppRole and token logins below store their Secrets in the same
Kubernetes namespace:

```bash
kubectl -n openbao-config create serviceaccount openbao-configurer
```

Save this policy as `openbao-configurer.hcl`. It lets the connection inspect and renew its own
token, which is all the connection check in step 4 needs:

```hcl
path "auth/token/lookup-self" {
  capabilities = ["read"]
}
path "auth/token/renew-self" {
  capabilities = ["update"]
}
```

Extend it with the paths each guide lists under Before you begin; the
[OpenBao policy for the operator](../operate/openbao-policy.md) page has them per kind. To extend
it later, append the guide's HCL to `openbao-configurer.hcl` and run
`bao policy write openbao-configurer openbao-configurer.hcl` again; OpenBao applies the new policy to
tokens already issued from their next request.

Then create the login for the method you chose. Leave out `bao auth enable` when the mount already
exists.

=== "Kubernetes auth"

    ```bash
    bao auth enable kubernetes
    bao write auth/kubernetes/config kubernetes_host=https://kubernetes.default.svc:443
    bao policy write openbao-configurer openbao-configurer.hcl
    bao write auth/kubernetes/role/openbao-configurer \
      bound_service_account_names=openbao-configurer \
      bound_service_account_namespaces=openbao-config \
      audience=openbao \
      token_policies=openbao-configurer \
      token_ttl=1h token_max_ttl=4h
    ```

    OpenBao reviews the token with its own ServiceAccount when it runs in the same cluster, so
    that ServiceAccount needs the `system:auth-delegator` ClusterRole. The OpenBao Helm chart
    creates this binding by default (`server.authDelegator.enabled: true`); otherwise create it:

    ```bash
    kubectl create clusterrolebinding openbao-auth-delegator \
      --clusterrole=system:auth-delegator \
      --serviceaccount=<openbao-namespace>:<openbao-serviceaccount>
    ```

    Replace `<openbao-namespace>` with the Kubernetes namespace OpenBao runs in and
    `<openbao-serviceaccount>` with its ServiceAccount, which the OpenBao Helm chart names after the
    release (`openbao` by default). When OpenBao runs elsewhere, also pass
    `kubernetes_ca_cert=@kubernetes-ca.crt`, the CA of the cluster's API server, and a
    `token_reviewer_jwt` of a ServiceAccount that may create TokenReviews.

=== "JWT auth"

    OpenBao fetches the cluster's discovery document and signing keys without credentials, and by
    default the API server serves them only to authenticated callers. Allow anonymous reads; the
    binding exposes only the issuer metadata and the public keys:

    ```bash
    kubectl create clusterrolebinding oidc-reviewer \
      --clusterrole=system:service-account-issuer-discovery --group=system:unauthenticated
    ```

    `kubernetes-ca.crt` is the CA of the cluster's API server:

    ```bash
    bao auth enable -path=jwt-k8s jwt
    bao write auth/jwt-k8s/config \
      oidc_discovery_url=https://kubernetes.default.svc.cluster.local \
      oidc_discovery_ca_pem=@kubernetes-ca.crt \
      bound_issuer=https://kubernetes.default.svc.cluster.local
    bao policy write openbao-configurer openbao-configurer.hcl
    bao write auth/jwt-k8s/role/openbao-configurer \
      role_type=jwt \
      user_claim=sub \
      bound_audiences=openbao \
      bound_subject=system:serviceaccount:openbao-config:openbao-configurer \
      token_policies=openbao-configurer \
      token_ttl=1h token_max_ttl=4h
    ```

    `kubectl get --raw /.well-known/openid-configuration` shows the cluster's issuer. If OpenBao
    cannot reach discovery, configure a reachable JWKS endpoint with `jwks_url`, or maintain
    PEM-formatted public keys in `jwt_validation_pubkeys`. The JSON returned by
    `kubectl get --raw /openid/v1/jwks` is a JWKS document, not a PEM public key; do not pass it
    directly to `jwt_validation_pubkeys`.

=== "AppRole"

    ```bash
    bao auth enable approle
    bao policy write openbao-configurer openbao-configurer.hcl
    bao write auth/approle/role/openbao-configurer \
      token_policies=openbao-configurer token_ttl=1h token_max_ttl=4h secret_id_ttl=720h
    role_id=$(bao read -field=role_id auth/approle/role/openbao-configurer/role-id)
    secret_id=$(bao write -f -field=secret_id auth/approle/role/openbao-configurer/secret-id)
    kubectl create secret generic openbao-approle -n openbao-config \
      --from-literal=role_id="$role_id" --from-literal=secret_id="$secret_id"
    ```

    Write a new secret ID into the Secret before `secret_id_ttl` ends. The connection reads it at
    the next login, without a restart.

=== "Token"

    ```bash
    bao policy write openbao-configurer openbao-configurer.hcl
    bao token create -orphan -period=24h -policy=openbao-configurer -field=token \
      | kubectl create secret generic openbao-token -n openbao-config --from-file=token=/dev/stdin
    ```

    Use a periodic token: the operator renews a renewable token after a third of its TTL, and
    cannot renew any other.

For a tenant whose connection logs in inside its own OpenBao namespace, add
`-namespace=<openbao-namespace>` to every `bao` command and bind the tenant's Kubernetes namespace
instead; [Namespaces and tenants](../guides/namespaces-and-tenants.md) has the rest.

## 3. Create the connection

Save the manifest for your login method as `connection.yaml`. It has no `spec.openbaoNamespace`, so the
connection works in the root OpenBao namespace:

=== "Kubernetes auth"

    ```yaml
    apiVersion: core.obco.dev/v1alpha1
    kind: BaoConnection
    metadata:
      name: openbao
      namespace: openbao-config
    spec:
      serverRef:
        name: openbao
      auth:
        kubernetes:
          role: openbao-configurer
          serviceAccountRef:
            name: openbao-configurer
          audiences:
            - openbao
    ```

=== "JWT auth"

    ```yaml
    apiVersion: core.obco.dev/v1alpha1
    kind: BaoConnection
    metadata:
      name: openbao
      namespace: openbao-config
    spec:
      serverRef:
        name: openbao
      auth:
        jwt:
          mountPath: jwt-k8s
          role: openbao-configurer
          serviceAccountRef:
            name: openbao-configurer
          audiences:
            - openbao
    ```

=== "AppRole"

    ```yaml
    apiVersion: core.obco.dev/v1alpha1
    kind: BaoConnection
    metadata:
      name: openbao
      namespace: openbao-config
    spec:
      serverRef:
        name: openbao
      auth:
        appRole:
          roleIDSecretRef:
            name: openbao-approle
            key: role_id
          secretIDSecretRef:
            name: openbao-approle
            key: secret_id
    ```

=== "Token"

    ```yaml
    apiVersion: core.obco.dev/v1alpha1
    kind: BaoConnection
    metadata:
      name: openbao
      namespace: openbao-config
    spec:
      serverRef:
        name: openbao
      auth:
        token:
          secretRef:
            name: openbao-token
            key: token
    ```

```bash
kubectl apply -f connection.yaml
```

### Use a cluster connection instead

A `ClusterBaoConnection` is usable from every Kubernetes namespace its `allowedNamespaces`
selects. With Kubernetes or JWT auth and no `serviceAccountRef`, it logs in as the operator itself.
Save this as `cluster-connection.yaml`:

```yaml
apiVersion: core.obco.dev/v1alpha1
kind: ClusterBaoConnection
metadata:
  name: platform
spec:
  serverRef:
    name: openbao
  auth:
    kubernetes:
      role: openbao-config-operator
      injectedIdentity:
        tokenPath: /var/run/secrets/openbao/token
  allowedNamespaces:
    matchLabels:
      obco.dev/platform: "true"
```

Its token audience `openbao-config-operator` does not need to be in the server's
`allowedAudiences`, which applies to `BaoConnection`s only. Create the OpenBao role as in
[Log in as the operator](#log-in-as-the-operator), then apply it:

```bash
kubectl apply -f cluster-connection.yaml
kubectl wait clusterbaoconnection/platform --for=condition=Ready --timeout=2m
```

A resource uses it with `connectionRef.kind: ClusterBaoConnection` and `connectionRef.name:
platform`, from a Kubernetes namespace with the label `obco.dev/platform=true`. The
[examples](../reference/examples/core.md#clusterbaoconnection) also show JWT auth and a token
read from a file in the operator pod, such as an agent sidecar's sink (`auth.token.filesystem`).

### Log in as the operator

A `ClusterBaoConnection` with `kubernetes` or `jwt` auth and no `serviceAccountRef` presents
the operator's own ServiceAccount token. The chart mounts a projected token for the audience
`openbao-config-operator` at `/var/run/secrets/openbao/token`, which is the default `tokenPath`.
Write the policy `openbao-config-operator` from
[OpenBao policy for the operator](../operate/openbao-policy.md), then create the OpenBao role for
the chart's ServiceAccount and Kubernetes namespace, with that audience:

```bash
bao write auth/kubernetes/role/openbao-config-operator \
  bound_service_account_names=openbao-config-operator \
  bound_service_account_namespaces=openbao-config-operator \
  audience=openbao-config-operator \
  token_policies=openbao-config-operator \
  token_ttl=1h token_max_ttl=4h
```

The ServiceAccount name is the chart's full name: the release name `openbao-config-operator`
unless you set `fullnameOverride` or `serviceAccount.name`. With `install.yaml`, bind the
ServiceAccount `obco-controller-manager` of the Kubernetes namespace
`obco-system` instead; it mounts the same token path with the same audience. [OpenBao self-init](../guides/self-init.md)
creates this role at OpenBao's first boot, without a root token.

## 4. Test the connection

```bash
kubectl -n openbao-config wait baoconnection/openbao --for=condition=Ready --timeout=2m
kubectl -n openbao-config get baoconnection openbao
```

```text title="Expected output"
baoconnection.core.obco.dev/openbao condition met
```

The `get` shows `Ready` `True`, the server and its OpenBao version. `Ready=True` with the reason
`Available` means the server admits the connection, the login worked and
`auth/token/lookup-self` succeeded. The operator checks again every minute, and when the server or
the Kubernetes namespace labels change.

Before you create resources, check what the identity may do. With Kubernetes auth, log in as the
connection's ServiceAccount and ask OpenBao about a path you added to `openbao-configurer.hcl`:

```bash
BAO_TOKEN=$(bao write -field=token auth/kubernetes/login role=openbao-configurer \
  jwt="$(kubectl -n openbao-config create token openbao-configurer --audience openbao)") \
  bao token capabilities <path>
```

Replace `<path>` with a path from a guide's Before you begin, for example
`sys/policies/acl/app-read`. The output lists the capabilities the policy grants on it; `deny`
means the policy does not cover the path yet. A resource that hits a missing capability reports
`PermissionDenied`.

## Troubleshooting

| symptom | check |
|---------|-------|
| server `Ready=False`, reason `Unreachable` | the `address`, DNS, the chart's NetworkPolicy egress to OpenBao (`networkPolicy.openbao`), and `x509` errors in the message: the CA bundle |
| server `Ready=False`, reason `ReferenceNotReady` | the CA bundle ConfigMap or Secret and its key (default `ca.crt`) exist in the Kubernetes namespace you named |
| server `Ready=False`, reason `Sealed` | unseal OpenBao; resources wait meanwhile |
| connection reason `ConnectionDenied` | the server's `allowedNamespaces` against `kubectl get namespace openbao-config --show-labels`, and `allowedAudiences` against the connection's `audiences` |
| connection reason `CredentialsNotFound` | the names, keys and Kubernetes namespaces of the Secrets in `spec.auth`; for a token file, the volume mount |
| connection reason `AuthenticationFailed` | `kubectl -n openbao-config describe baoconnection openbao` shows the OpenBao error: the role name, its bound ServiceAccount, Kubernetes namespace and audience, the `system:auth-delegator` binding, and `openbaoAuthNamespace` |
| connection reason `ReferenceNotReady` | with `spec.openbaoNamespace` set, that OpenBao namespace exists: `bao namespace list` in its parent |

[Status and events](../reference/status-and-events.md#conditions-of-connections-and-servers) has every reason.

## Related

- [Connections reference](../reference/connections.md): every field, auth mode and status value.
- API reference: [BaoServer](../reference/api/core.md#baoserver),
  [BaoConnection](../reference/api/core.md#baoconnection),
  [ClusterBaoConnection](../reference/api/core.md#clusterbaoconnection).
- [Manage OpenBao policies](../guides/policies.md): the first object to declare once the
  connection is `Ready`.
- [Import existing configuration](../guides/import.md): when the server is already configured.
- [Namespaces and tenants](../guides/namespaces-and-tenants.md): OpenBao namespaces,
  `openbaoAuthNamespace` and one connection per tenant.
