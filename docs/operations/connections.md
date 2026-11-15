# Connections

An `OpenBaoServer` says where OpenBao is; a connection names that server and says how the operator
logs in. Every resource names a connection in `spec.connectionRef: {kind, name}`; `kind` defaults to
`OpenBaoConnection`.

| kind | scope | who creates it | references |
|------|-------|----------------|------------|
| `OpenBaoServer` | cluster | platform team | address, CA bundle, request timeout, rate limit, and which tenant namespaces and token audiences may use it |
| `OpenBaoConnection` | namespaced | tenant or platform team, in the tenant namespace | usable only by resources in the same namespace; every Secret and ServiceAccount it names is in that namespace |
| `ClusterOpenBaoConnection` | cluster | platform team | usable from namespaces that match `allowedNamespaces` (none when unset, all with `{}`); every reference names its namespace |

Choose a tenancy model first: [install.md](install.md#tenancy-models).

## OpenBaoServer

| field | meaning |
|-------|---------|
| `address` | OpenBao URL without `/v1`, for example `https://openbao.openbao.svc:8200` |
| `caBundle` | PEM CA bundle: exactly one of `secretRef` and `configMapRef`, each `{namespace, name, key}` |
| `allowedNamespaces` | label selector on the namespaces whose `OpenBaoConnection`s may use the server; unset allows none, `{}` allows all. `ClusterOpenBaoConnection`s are not restricted |
| `allowedAudiences` | token audiences an `OpenBaoConnection` may request (at most 16); the API server's audiences are always refused |
| `rateLimit` | `{qps, burst}` token bucket shared by every connection to the server; unset means no limit |
| `requestTimeout` | upper bound of a single request, as a duration such as `30s` |

```yaml
apiVersion: core.openbaoconfig.io/v1alpha1
kind: OpenBaoServer
metadata:
  name: openbao
spec:
  address: https://openbao.openbao.svc:8200
  caBundle:
    configMapRef:
      namespace: openbao
      name: openbao-ca
  allowedNamespaces:
    matchLabels:
      openbaoconfig.io/tenant: "true"
  allowedAudiences:
    - openbao
  rateLimit:
    qps: 50
    burst: 100
```

The operator probes the server every minute, with jitter, without a token. `status` reports
`Ready`, `version`, `sealed` and `standby`; reasons are `Available`, `Unreachable`, `Sealed`,
`ReferenceNotReady` (CA bundle missing) and `InvalidSpec`. The CA bundle replaces the system roots;
a change to its Secret or ConfigMap re-queues the server.

The API server audiences a tenant may never request are `https://kubernetes.default.svc`,
`https://kubernetes.default.svc.cluster.local`, `kubernetes.default.svc`, `rke2`, `k3s`, the service
account issuer the operator discovers at start-up, and the chart's `apiServerAudiences`. A failed
discovery is logged and leaves the other entries in place.

## Connection fields

| field | meaning |
|-------|---------|
| `serverRef.name` | the `OpenBaoServer` to connect to |
| `namespace` | base OpenBao namespace of every request made through this connection |
| `authNamespace` | OpenBao namespace that holds the auth mount used to log in; see below |
| `auth` | exactly one of `token`, `kubernetes`, `appRole` and `jwt` |
| `allowedNamespaces` | `ClusterOpenBaoConnection` only: label selector on the namespaces that may use it; unset allows none, `{}` allows all |

An `OpenBaoConnection` is `Ready=False` with reason `ConnectionDenied`, before any credential is
requested, when its server does not select its namespace or does not list one of its audiences.
The client retries 429 and 5xx responses with backoff inside the server's rate limit.

### namespace and authNamespace

`namespace` is the base for every request. A resource's own `spec.namespace` is relative to it,
so a resource can reach the base namespace and its children, never a parent or a sibling.

`authNamespace` is where the login, `auth/token/lookup-self` at login and `auth/token/renew-self`
happen:

| `authNamespace` | login happens in | use when |
|-----------------|------------------|----------|
| unset | `namespace` | the auth mount lives in the connection's own namespace |
| `""` | the root namespace | one auth mount in the root namespace serves every tenant namespace |
| `team-a` | `team-a` | the auth mount lives in another namespace than the one you configure |

The status probe checks the token with `auth/token/lookup-self` in `namespace`, not in
`authNamespace`. The identity's policy must allow that path in the connection namespace
([openbao-policy.md](openbao-policy.md#connection-probe)).

Do not change `namespace` after resources have been created through the connection. A resource
that was acquired in another namespace stops with the reason `NamespaceChanged` instead of
creating a second object elsewhere.

### TLS

The CA bundle belongs to the `OpenBaoServer` (`spec.caBundle`); connections have no TLS settings.

## Token

A token in a Secret. On a `ClusterOpenBaoConnection`, the token can also come from a file in the
operator pod, such as an agent sidecar's sink, with `auth.token.filesystem.path`. The file is read
at every login and a rewritten file replaces the client.

```yaml
apiVersion: core.openbaoconfig.io/v1alpha1
kind: OpenBaoConnection
metadata:
  name: openbao
  namespace: team-a
spec:
  serverRef:
    name: openbao
  namespace: tenants/team-a
  auth:
    token:
      secretRef:
        name: openbao-token
        key: token
```

The operator looks the token up to learn its TTL. It renews a renewable or periodic token after a
third of its TTL. It cannot renew a non-renewable token, so use a periodic token:

```sh
bao policy write -namespace=tenants/team-a team-a-config team-a-config.hcl
bao token create -namespace=tenants/team-a -orphan -period=24h -policy=team-a-config -field=token \
  | kubectl create secret generic openbao-token -n team-a --from-file=token=/dev/stdin
```

Prefer Kubernetes, JWT or AppRole auth over long-lived tokens.

## Kubernetes auth

The operator presents a ServiceAccount token to the OpenBao kubernetes auth method.

### Namespaced connection

`serviceAccountRef` and `audiences` are required, and every audience must be in the server's
`allowedAudiences`. The operator requests a token for that ServiceAccount in the connection's
namespace through the TokenRequest API, valid for 10 minutes and used for one login. The audience
keeps the token from being valid against the Kubernetes API server.

```yaml
apiVersion: core.openbaoconfig.io/v1alpha1
kind: OpenBaoConnection
metadata:
  name: openbao
  namespace: team-a
spec:
  serverRef:
    name: openbao
  namespace: tenants/team-a
  authNamespace: ""
  auth:
    kubernetes:
      mountPath: kubernetes
      role: team-a-configurer
      serviceAccountRef:
        name: openbao-configurer
      audiences:
        - openbao
```

`mountPath` defaults to `kubernetes`.

### Cluster connection

A `ClusterOpenBaoConnection` either names a ServiceAccount with
`serviceAccountRef: {namespace, name}` or logs in as the operator itself. With neither
`serviceAccountRef` nor `injectedIdentity`, it uses the injected identity with the default token
file, `/var/run/secrets/openbao/token`, which the chart's `injectedIdentity.projectedToken` values
mount with the audience `openbao-config-operator` ([install.md](install.md#injected-identity-token)).
The automounted token (`/var/run/secrets/kubernetes.io/serviceaccount/`) is a Kubernetes API
credential: CEL and the resolver refuse it. With `serviceAccountRef`, `audiences` is required and no
audience may be one the API server accepts.

```yaml
apiVersion: core.openbaoconfig.io/v1alpha1
kind: ClusterOpenBaoConnection
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
      openbaoconfig.io/platform: "true"
```

`audiences` is allowed only with `serviceAccountRef`. An injected identity token gets its
audience from the projected volume.

### OpenBao side

[self-init.md](self-init.md) does the steps below at OpenBao's first boot, with no root token.
By hand, enable the method once and point it at the cluster. When OpenBao runs in the same cluster, it can
review tokens with its own ServiceAccount:

```sh
bao auth enable -path=kubernetes kubernetes
bao write auth/kubernetes/config kubernetes_host=https://kubernetes.default.svc:443
```

When OpenBao runs elsewhere, also pass `kubernetes_ca_cert=@ca.crt` and a long-lived
`token_reviewer_jwt` of a ServiceAccount allowed to create TokenReviews.

A role per identity binds the ServiceAccount name, namespace and audience to a policy:

```sh
# tenant connection
bao write auth/kubernetes/role/team-a-configurer \
  bound_service_account_names=openbao-configurer \
  bound_service_account_namespaces=team-a \
  audience=openbao \
  token_policies=team-a-config \
  token_ttl=1h token_max_ttl=4h

# operator's injected identity (release namespace and ServiceAccount of the chart)
bao write auth/kubernetes/role/openbao-config-operator \
  bound_service_account_names=openbao-config-operator \
  bound_service_account_namespaces=openbao-config-operator \
  audience=openbao \
  token_policies=openbao-config-operator \
  token_ttl=1h token_max_ttl=4h
```

Bind exactly one ServiceAccount name and one namespace per role. A wildcard lets any matching
ServiceAccount use the policy.

## JWT auth

The same ServiceAccount token, validated by the OpenBao jwt auth method against the cluster's
OIDC issuer instead of a TokenReview. The fields and rules are those of Kubernetes auth;
`mountPath` defaults to `jwt`.

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

OpenBao side, with the cluster's issuer (`kubectl get --raw /.well-known/openid-configuration`
shows it):

```sh
bao auth enable -path=jwt-k8s jwt
bao write auth/jwt-k8s/config \
  oidc_discovery_url=https://kubernetes.default.svc.cluster.local \
  oidc_discovery_ca_pem=@ca.crt \
  bound_issuer=https://kubernetes.default.svc.cluster.local

bao write auth/jwt-k8s/role/team-a-configurer \
  role_type=jwt \
  user_claim=sub \
  bound_audiences=openbao \
  bound_subject=system:serviceaccount:team-a:openbao-configurer \
  token_policies=team-a-config \
  token_ttl=1h token_max_ttl=4h
```

If OpenBao cannot reach the discovery URL, set `jwt_validation_pubkeys` to the keys from
`kubectl get --raw /openid/v1/jwks` instead, and update them when the cluster rotates its
signing key.

## AppRole

A role ID and a secret ID, each from a Secret. `mountPath` defaults to `approle`.

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

OpenBao side:

```sh
bao auth enable approle
bao write auth/approle/role/team-a-configurer \
  token_policies=team-a-config token_ttl=1h token_max_ttl=4h \
  secret_id_ttl=720h
role_id=$(bao read -field=role_id auth/approle/role/team-a-configurer/role-id)
secret_id=$(bao write -f -field=secret_id auth/approle/role/team-a-configurer/secret-id)
kubectl create secret generic openbao-approle -n team-a \
  --from-literal=role_id="$role_id" --from-literal=secret_id="$secret_id"
```

Rotate the secret ID before `secret_id_ttl` ends by writing a new one into the Secret. The
connection picks it up at the next login without a restart.

## Status

```sh
kubectl get openbaoconnections -A
kubectl get clusteropenbaoconnections
```

| field | meaning |
|-------|---------|
| `conditions[Ready]` | `True` with reason `Available` when the server is `Ready`, admits the connection and the login works |
| `version`, `sealed`, `standby` | copied from the `OpenBaoServer` status |
| `authenticated` | the last login and `auth/token/lookup-self` succeeded |
| `lastProbeTime` | when a check last changed the status |
| `observedGeneration` | generation of the last check |

The operator checks every minute, with jitter, and when the server or (for an `OpenBaoConnection`)
the namespace labels change. A check that finds nothing new writes nothing.
Resources wait while their connection is not `Ready` for the current generation.

| reason | meaning |
|--------|---------|
| `Available` | connected and authenticated |
| `CredentialsNotFound` | a referenced Secret, ConfigMap, key or token file is missing |
| `InvalidSpec` | the spec cannot be used |
| `ReferenceNotReady` | the connection's OpenBao namespace does not exist yet; retried after 2 seconds, backing off to 30 |
| `ServerNotReady` | the `OpenBaoServer` is missing or not `Ready` (unreachable, sealed, CA bundle missing) |
| `ConnectionDenied` | the server does not select this namespace or allow one of the audiences |
| `AuthenticationFailed` | the login or the token lookup failed |

What to check for each reason is in [troubleshooting.md](troubleshooting.md#connection-reasons).

## Deletion

An `OpenBaoConnection` or `ClusterOpenBaoConnection` carries the finalizer
`openbaoconfig.io/connection-in-use` and stays while a resource that references it still holds the
engine finalizer, that is while it still has an OpenBao object to delete. Until then the connection
keeps serving its last working client, even after its credentials are gone, so deleting a whole
Namespace removes the OpenBao objects first and then the connection. An `OpenBaoServer` carries
`openbaoconfig.io/server-in-use` and stays while a connection references it.

The gauge `openbao_connection_ready{kind,namespace,name}` is 1 while a connection is `Ready`, else 0.

## Credential rotation

Credentials are read when the client logs in, never at start-up. A change to a referenced Secret
or ConfigMap re-queues the connection and replaces its cached client, so a rotated token, secret
ID or CA bundle needs no restart. A token file is versioned by its modification time.
