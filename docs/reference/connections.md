---
description: "Reference OpenBaoConnection, ClusterOpenBaoConnection and OpenBaoServer fields, credentials, TLS settings and namespace selection."
---

# Connections

Field reference for `OpenBaoServer`, `OpenBaoConnection` and `ClusterOpenBaoConnection`, the
three `core.openbaoconfig.io/v1alpha1` kinds that decide where the operator sends requests and how
it logs in. Facts come from `api/core/v1alpha1`, `internal/connection` and
`internal/controller/core`. To set up a first connection step by step, use
[connect to OpenBao](../get-started/connect.md); for the generated schema of every field, use the
[API reference](api/index.md).

| kind | scope | created by | role |
|------|-------|------------|------|
| `OpenBaoServer` | cluster | platform team | the only place that sets the address, CA bundle, timeout and rate limit; decides which namespaces and audiences tenants may use |
| `OpenBaoConnection` | namespaced | tenant or platform team, in the tenant namespace | a login usable only by resources in its own namespace; every Secret and ServiceAccount it names is in that namespace |
| `ClusterOpenBaoConnection` | cluster | platform team | a login usable by resources in the namespaces its `allowedNamespaces` selects; may log in as the operator itself |

None of the three writes to OpenBao. Resources reference a connection with
`spec.connectionRef: {kind, name}`; `kind` defaults to `OpenBaoConnection`, and both fields are
immutable.

## OpenBaoServer

```yaml
--8<-- "examples/core/openbaoserver.yaml"
```

### Spec

| field | type | default | rules |
|-------|------|---------|-------|
| `address` | string | required | URL of the OpenBao API without `/v1` and without `user:password@`; at most 2048 characters; must start with `https://` unless `allowInsecureHTTP` is `true` |
| `allowInsecureHTTP` | boolean | `false` | allow an `http://` address; tokens, login JWTs and secret inputs then cross the network in clear text |
| `caBundle.secretRef` | `{namespace, name, key}` | | PEM CA bundle in a Secret; `key` defaults to `ca.crt`. Exactly one of `secretRef` and `configMapRef` |
| `caBundle.configMapRef` | `{namespace, name, key}` | | PEM CA bundle in a ConfigMap, for example one filled by trust-manager |
| `allowedNamespaces` | label selector | unset | Kubernetes namespaces whose `OpenBaoConnection`s may use this server. Unset allows none; `{}` allows all. Does not restrict `ClusterOpenBaoConnection`s |
| `allowedAudiences` | list of strings, at most 16, unique | | token audiences an `OpenBaoConnection` may request; every audience the connection asks for must be listed |
| `rateLimit.qps` | integer, 1 to 100000 | | sustained requests per second, shared by every connection to the server |
| `rateLimit.burst` | integer, 1 to 100000 | the value of `qps` | requests allowed at once above the sustained rate |
| `requestTimeout` | duration | `60s` | maximum time of one request |

Unset `rateLimit` means no client-side limit.

### Status

The status fields (`conditions`, `version`, `sealed`, `standby`, `lastProbeTime`) and the `Ready` reasons are
listed in [status and events](status-and-events.md#connections-and-servers).

The operator probes `sys/seal-status` and `sys/health` in the root namespace, without a token,
every 1 to 1.2 minutes, and when the server or its CA bundle Secret or ConfigMap
changes. A probe that finds nothing new writes nothing.

## Connection spec

Fields shared by `OpenBaoConnection` and `ClusterOpenBaoConnection`:

| field | type | default | rules |
|-------|------|---------|-------|
| `serverRef.name` | string | required | the `OpenBaoServer` to use |
| `namespace` | string | `""` (root) | base OpenBao namespace of every request through the connection; a resource's `spec.namespace` is relative to it. Segments separated by `/`, no leading or trailing slash, no `.` or `..` segments, at most 256 characters |
| `authNamespace` | string or unset | unset | absolute OpenBao namespace that holds the auth mount used to log in |
| `auth` | object | required | exactly one of `token`, `kubernetes`, `appRole` and `jwt` |
| `allowedNamespaces` | label selector | unset | `ClusterOpenBaoConnection` only: Kubernetes namespaces whose resources may reference it. Unset allows none; `{}` allows all |

### namespace and authNamespace

| `authNamespace` | login, lookup-self at login, and renew-self happen in | typical use |
|-----------------|--------------------------------------------------------|-------------|
| unset | `namespace` | the auth mount lives in the connection's own namespace |
| `""` | the root namespace | one auth mount in the root namespace serves every tenant namespace |
| `team-a` | `team-a` | the auth mount lives in a namespace other than the one being configured |

The status probe checks the token with `auth/token/lookup-self` in `namespace`, not in
`authNamespace`, so the identity's policy must allow that path in the connection namespace
([OpenBao policy](../operate/openbao-policy.md)). A resource that was acquired in one namespace
and whose connection later resolves to another stops with `NamespaceChanged` and writes nothing.

## Authentication methods

=== "OpenBaoConnection"

    | method | fields | credential |
    |--------|--------|------------|
    | `token` | `secretRef: {name, key}` | an OpenBao token in a Secret of the connection namespace |
    | `kubernetes` | `mountPath` (default `kubernetes`), `role`, `serviceAccountRef: {name}`, `audiences` | a TokenRequest token of a ServiceAccount in the connection namespace |
    | `jwt` | `mountPath` (default `jwt`), `role`, `serviceAccountRef: {name}`, `audiences` | the same token, presented to a jwt auth mount |
    | `appRole` | `mountPath` (default `approle`), `roleIDSecretRef: {name, key}`, `secretIDSecretRef: {name, key}` | role ID and secret ID from Secrets of the connection namespace |

    For `kubernetes` and `jwt`, `serviceAccountRef` and `audiences` (1 to 16 entries) are required.

=== "ClusterOpenBaoConnection"

    | method | fields | credential |
    |--------|--------|------------|
    | `token` | exactly one of `secretRef: {namespace, name, key}` and `filesystem: {path}` | a token in a Secret, or a token file in the operator pod, such as an agent sink |
    | `kubernetes` | `mountPath` (default `kubernetes`), `role`, at most one of `serviceAccountRef: {namespace, name}` and `injectedIdentity: {tokenPath}`, `audiences` | a TokenRequest token of a named ServiceAccount, or the operator's own projected token |
    | `jwt` | `mountPath` (default `jwt`), `role`, as for `kubernetes` | the same token, presented to a jwt auth mount |
    | `appRole` | `mountPath` (default `approle`), `roleIDSecretRef` and `secretIDSecretRef`, each `{namespace, name, key}` | role ID and secret ID from Secrets |

    `audiences` is required with `serviceAccountRef` and allowed only with it. With neither
    `serviceAccountRef` nor `injectedIdentity`, the connection uses `injectedIdentity` with the
    default `tokenPath`, `/var/run/secrets/openbao/token`.

Mount paths match `^[A-Za-z0-9_-][A-Za-z0-9._-]*(/[A-Za-z0-9_-][A-Za-z0-9._-]*)*$`, at most 128
characters, without the `auth/` prefix.

### Credential sources

| source | fields | kinds | read |
|--------|--------|-------|------|
| Secret | `auth.token.secretRef`, `auth.appRole.*SecretRef` | both | at every login, so a rotated Secret needs no restart |
| TokenRequest | `serviceAccountRef` and `audiences` | both | a new token, valid for 10 minutes, for each login |
| injected identity | `injectedIdentity.tokenPath` | `ClusterOpenBaoConnection` | the file at every login; the chart mounts it with `injectedIdentity.projectedToken` |
| token file | `auth.token.filesystem.path` | `ClusterOpenBaoConnection` | the file at every login; a changed modification time replaces the cached client |

A missing Secret, key or file sets the connection's reason `CredentialsNotFound`. Credentials are
never stored in status.

### Examples

=== "Tenant, Kubernetes auth"

    ```yaml
    --8<-- "examples/core/openbaoconnection.yaml"
    ```

=== "Platform, named ServiceAccount"

    ```yaml
    --8<-- "examples/core/clusteropenbaoconnection.yaml"
    ```

=== "Platform, injected identity"

    ```yaml
    --8<-- "examples/core/clusteropenbaoconnection-injectedidentity.yaml"
    ```

=== "Platform, token file"

    ```yaml
    --8<-- "examples/core/clusteropenbaoconnection-filesystem.yaml"
    ```

## Tenant boundary rules

The resolver enforces these rules before any credential is read or requested. CEL rejects most of
them at admission too; the resolver applies them again when validation was bypassed.

| rule | enforced by | result |
|------|-------------|--------|
| a resource uses an `OpenBaoConnection` of its own namespace only | resolver (lookup in the resource's namespace) | `ConnectionNotReady` (not found) |
| a resource uses a `ClusterOpenBaoConnection` only if its `allowedNamespaces` selects the resource's namespace; unset selects none | resolver | resource `ConnectionDenied` |
| an `OpenBaoConnection` uses a server only if the server's `allowedNamespaces` selects its namespace; unset selects none | resolver | connection `ConnectionDenied` |
| every audience of an `OpenBaoConnection` is in the server's `allowedAudiences` | resolver | connection `ConnectionDenied` |
| no audience is one the Kubernetes API server accepts: `https://kubernetes.default.svc`, `https://kubernetes.default.svc.cluster.local`, `kubernetes.default.svc`, `rke2`, `k3s`, the service account issuer discovered at start-up, and the chart's `apiServerAudiences` | resolver, for both connection kinds, and for the `aud` claim of token files | connection `ConnectionDenied` |
| a namespaced Kubernetes or JWT login names a ServiceAccount and audiences | CEL and resolver | rejected at admission |
| the automounted ServiceAccount token (`/var/run/secrets/kubernetes.io/serviceaccount/`) is never sent | CEL on `tokenPath` and `filesystem.path`; resolver checks the path and the file identity again at every login | rejected |
| a namespaced connection never uses the operator's identity or files | the `OpenBaoConnection` schema has no `injectedIdentity` or `filesystem` field | not expressible |

A failed issuer discovery is logged, and the built-in and configured audiences stay denied. When
the configuration or login of a `ClusterOpenBaoConnection` fails, a resource that uses it sees only
"ClusterOpenBaoConnection `<name>` is not ready (see its status)"; the platform's Secret,
ServiceAccount and file names stay on the connection's own status.

## TLS and the CA bundle

| behaviour | detail |
|-----------|--------|
| trust | with `caBundle` set, the bundle replaces the system roots for that server; unset uses the operator image's system roots |
| reload | a change to the CA bundle Secret or ConfigMap re-queues the server and its connections and replaces their cached clients |
| minimum version | TLS 1.2 |
| invalid bundle | a bundle with no valid PEM certificate fails the client |
| proxies | `HTTP_PROXY` and `HTTPS_PROXY` are ignored: only the `OpenBaoServer` decides where tokens are sent |
| redirects | never followed; a 3xx answer is an error |

Connections have no TLS settings of their own.

## Connection status

A connection copies `version`, `sealed` and `standby` from its server and adds `authenticated`; the fields are
listed in [status and events](status-and-events.md#connections-and-servers), and the `Ready` reasons in
[conditions of connections and servers](status-and-events.md#conditions-of-connections-and-servers).

The connection controller checks every 1 to 1.2 minutes, and when the connection, its
server, a referenced Secret or (for an `OpenBaoConnection`) its namespace labels change. Resources
wait with `ConnectionNotReady` while their connection is not `Ready` for its current generation.
The gauge `openbao_connection_ready{kind,namespace,name}` is 1 while a connection is `Ready`.

## Client behaviour

The operator keeps one authenticated client per connection. The client is replaced when the
connection or server generation changes, or when the value of a credential input changes: the content of a
referenced Secret or CA bundle, or a token file's modification time. An edit that changes only a Secret's labels
or annotations keeps the client. How the client renews, logs in again, retries and revokes its token is in
[client cache](../architecture.md#client-cache); the timings are in
[constants and timings](constants.md#login-renewal-and-tokens).

Every request carries the header `X-Openbao-Config-Resource: <group>/<kind>/<namespace>/<name>` of
the resource it serves ([ADR 0022](../adr/0022-audit-attribution-header.md)).

## Finalizers and deletion

| object | finalizer | held while |
|--------|-----------|------------|
| `OpenBaoConnection`, `ClusterOpenBaoConnection` | `openbaoconfig.io/connection-in-use` | a resource that references it still holds the engine finalizer `openbaoconfig.io/finalizer`, that is, still has an OpenBao object to delete |
| `OpenBaoServer` | `openbaoconfig.io/server-in-use` | a connection references it |

While a connection is being deleted it keeps serving its last working client for those
deletions, even after its credentials are gone, so deleting a whole Kubernetes Namespace removes
the OpenBao objects first and the connection last. If the Namespace terminates and the connection
is already gone, the resource releases its finalizer with an `Orphaned` event and the OpenBao
object stays.
