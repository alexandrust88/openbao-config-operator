# Troubleshooting

Start with the resource's conditions and events, then the connection, then the manager logs. The
alerts and their runbooks are in [runbooks.md](runbooks.md).

## First look

```sh
kubectl get openbao -A                                   # every kind, with Ready
kubectl get openbaoconnections -A
kubectl get clusteropenbaoconnections
kubectl describe policy team-a-app -n team-a             # conditions, status.path, events
kubectl get events -n team-a --field-selector involvedObject.name=team-a-app
kubectl logs -n openbao-config-operator deploy/openbao-config-operator
```

Only the leader reconciles. If one pod's logs are quiet, read the other's, or find the leader:

```sh
kubectl get lease -n openbao-config-operator openbao-config-operator.openbaoconfig.io \
  -o jsonpath='{.spec.holderIdentity}'
```

## Conditions

Every resource kind has two conditions:

| condition | `True` means | `False` means |
|-----------|--------------|---------------|
| `Ready` | the OpenBao object matched the spec at the last successful reconcile | the object is missing, differs, or cannot be reached; the reason says why |
| `Synced` | the last reconcile succeeded | the last reconcile failed or is waiting; the reason and message say why |

When the operator waits for something outside its control (a connection, a reference, a policy
choice), it sets both conditions to `False` with the same reason and retries without error
backoff. When a reconcile fails with an error, it sets only `Synced=False` with `ReconcileError`
and retries with exponential backoff; `Ready` keeps its last value, so a resource can be
`Ready=True` and `Synced=False` while OpenBao refuses a change.

`status.observedGeneration` lower than `metadata.generation` means the operator has not processed
the latest spec yet.

## Resource reasons

The most common reasons are below. Every reason, its retry and the events are listed in
[status and events](../reference/status-and-events.md).

| reason | condition | meaning | what to check |
|--------|-----------|---------|---------------|
| `Available` | `Ready=True` | the object matches the spec | nothing |
| `ReconcileSuccess` | `Synced=True` | the last reconcile succeeded | nothing |
| `ReconcileError` | `Synced` | OpenBao or Kubernetes returned an error | the message: a 400 is an invalid value; see [common OpenBao errors](#common-openbao-errors) |
| `PermissionDenied` | `Synced` | OpenBao answered 403: the connection's token is valid but its policy denies the request | the path in the message against the connection identity's policy ([openbao-policy.md](openbao-policy.md)) |
| `ConnectionNotReady` | both | the connection does not exist or is not `Ready` for its current generation; retried with a backoff from 2 seconds to 1 minute, and sooner when the connection changes | `kubectl get openbaoconnection <name>` and its reason ([connection reasons](#connection-reasons)) |
| `ConnectionDenied` | both | the `ClusterOpenBaoConnection`'s `allowedNamespaces` does not match this namespace, or the selector is invalid | the namespace labels and the selector |
| `ReferenceNotReady` | both | a referenced Secret, key or resource is missing or not `Ready`, or the OpenBao namespace does not exist yet; retried with a backoff from 2 to 30 seconds | the referent named in the message; the `OpenBaoNamespace` that creates the namespace |
| `Conflict` | both | `creationPolicy: Create` and the object already exists and is not ours | adopt it (`CreateOrAdopt` or `Adopt`) if it should be managed, or rename |
| `NotFound` | both | `creationPolicy: Adopt` and the object does not exist | the name, mount path and `spec.namespace`; use `CreateOrAdopt` to create it |
| `Unsupported` | both | the server cannot serve the kind: the plugin or auth method is not mounted, or raft storage is missing | the mount and the connection's `namespace`; a root-only kind in a child namespace reports `RootOnlyKind` instead |
| `InvalidSpec` | both | the spec, or a Secret it references, passed the CRD schema but can never work as written, such as a `spec.namespace` that starts with `root` or a payload that is not a JSON object | the message; fix the spec or the Secret. Errors raised by a kind's adapter are not retried until the spec or the Secret changes |
| `NamespaceChanged` | both | the object was acquired in one OpenBao namespace, but the connection now resolves to another | restore the connection's `namespace`, or recreate the resource ([below](#namespacechanged)) |
| `ReconcileInProgress` | `Synced` | an asynchronous OpenBao operation runs, such as a namespace deletion; polled every 10 seconds | wait; check the operation in OpenBao if it never ends |
| `CleanupBlocked` | `Synced` | deletion with `deletionPolicy: Delete` failed; the finalizer stays | the message; usually the connection or its credential is gone ([stuck finalizers](#stuck-finalizers)) |
| `OutOfSync` | both | a write succeeded but the read after it returned different values; retried with a backoff capped at the drift interval | the keys named in the message; write them in the form OpenBao returns them, or report the kind |
| `InUse` | `Ready` | `AccessLevel` only: deletion waits while an `AccessGrant` or `AccessLayout` cites the level | delete or change the citing resources first |

## Connection reasons

A connection has only the `Ready` condition.

| reason | meaning | what to check |
|--------|---------|---------------|
| `Available` | connected and authenticated | nothing |
| `CredentialsNotFound` | a referenced Secret, key or token file does not exist | the names, keys and namespaces in `spec.auth`; for a file, the volume mount |
| `InvalidSpec` | the spec cannot be turned into a client | the message |
| `ReferenceNotReady` | the connection's OpenBao `namespace` does not exist yet | `bao namespace list` in the parent namespace; the `OpenBaoNamespace` that creates it |
| `ServerNotReady` | the `OpenBaoServer` in `serverRef` is missing or not `Ready` | `kubectl get openbaoserver <name>`: `Unreachable` (DNS, the `address`, the NetworkPolicy egress to OpenBao, the CA bundle, `x509` in the message), `Sealed` (unseal OpenBao; resources wait meanwhile), `ReferenceNotReady` (the CA bundle Secret or ConfigMap) |
| `ConnectionDenied` | the server does not admit this `OpenBaoConnection` | the server's `allowedNamespaces` against the namespace labels, and `allowedAudiences` against the connection's `audiences`; an API server audience is always refused |
| `AuthenticationFailed` | the login or the `auth/token/lookup-self` check failed | the message; the OpenBao role, the audience, the ServiceAccount, `authNamespace` ([below](#common-openbao-errors)) |

`status.authenticated`, `status.sealed`, `status.standby` and `status.version` give more detail.
The operator probes every minute.

## Debug logging

Raise the log level through the chart:

```yaml
extraArgs:
  - --zap-log-level=debug
```

Logs name paths, kinds and the keys that differed. They never contain tokens, secret values or
OpenBao response bodies, so debug logs are safe to share. Turn debug off again afterwards; it is
verbose at scale.

## Metrics

| metric | labels | use |
|--------|--------|-----|
| `openbao_requests_total` | `connection`, `method` (`GET`, `PUT`, `POST`, `DELETE`, `LIST`), `code` (HTTP status, or `error` for a transport failure) | error rates per connection: `403` policy gaps, `5xx` server trouble, `error` network or TLS |
| `openbao_request_duration_seconds` | `connection`, `method` | OpenBao latency |
| `openbao_writes_total` | `kind` | writes to OpenBao; a steady non-zero rate means something fights the operator or a spec never converges |
| `controller_runtime_reconcile_errors_total` | `controller` | failing reconciles per kind |
| `controller_runtime_reconcile_total` | `controller`, `result` | reconcile volume |
| `workqueue_depth` | `name` | backlog per controller |

The `connection` label is the connection's UID. Map it to a name with:

```sh
kubectl get openbaoconnections -A -o custom-columns=NS:.metadata.namespace,NAME:.metadata.name,UID:.metadata.uid
kubectl get clusteropenbaoconnections -o custom-columns=NAME:.metadata.name,UID:.metadata.uid
```

Controller names are `<kind>.<group>` in lower case, such as `policy.sys.openbaoconfig.io`, and
`openbaoconnection` and `clusteropenbaoconnection` for the connections.

To find the kind behind steady-state writes:

```promql
topk(5, rate(openbao_writes_total[30m]))
```

Then look for `Updated` events on that kind; each one names the keys that differed.

## Common OpenBao errors

| message | where | cause and fix |
|---------|-------|---------------|
| `openbao: status 403: permission denied` | resource `PermissionDenied` | the connection identity's policy lacks a path or capability; `sudo` for `sys/auth/*`, `sys/config/cors` and the other paths in [openbao-policy.md](openbao-policy.md#sudo) |
| `invalid role name "<role>"` (status 400) | connection `AuthenticationFailed` | the Kubernetes auth role does not exist in the auth mount, or the login runs in the wrong namespace: check `mountPath` and `authNamespace` |
| `role "<role>" could not be found` (status 400) | connection `AuthenticationFailed` | the same for a JWT auth role |
| `service account name not authorized`, `namespace not authorized` | connection `AuthenticationFailed` | the role's `bound_service_account_names` or `bound_service_account_namespaces` do not include the connection's ServiceAccount |
| an audience error | connection `AuthenticationFailed` | the connection's `audiences` (or the projected token's audience) do not match the role's `audience` or `bound_audiences` |
| `namespace not found` (status 404) | connection and resource `ReferenceNotReady` | the OpenBao namespace does not exist yet; create it, or wait for its `OpenBaoNamespace` to be `Ready` |
| `no handler for route` (status 404) on a write | resource `ReferenceNotReady` | the mount the path is under does not exist yet, or a new namespace's mounts are still being created; create the mount, or wait for its `AuthMount` or `SecretsMount` to be `Ready` |
| `failed to update audit table` (status 400) on an `Audit` | `Audit` `ReconcileError` | the request reached a standby that serves requests itself (OpenBao 2.7 default); OpenBao does not forward a failed audit enable to the active node. Point the server address at the active node only, or set `disable_standby_reads = true` in the OpenBao server configuration |
| `x509: certificate signed by unknown authority` | server `Unreachable` | set the server's `caBundle` to the CA that signed the OpenBao listener certificate |
| `ca bundle contains no valid PEM certificate` | server `Unreachable` | the referenced key does not hold PEM certificates |
| `OpenBao answered with a redirect, which the operator never follows` | resource `ReconcileError`; connection `AuthenticationFailed` | a proxy or a standby node without request forwarding answered with 3xx; point the server's `address` at the active node or a service that forwards |

Reproduce a policy problem with the connection's identity:

```sh
bao token capabilities -namespace=<connection namespace> <path>
```

## Audit attribution

Every request the operator sends carries the header
`X-Openbao-Config-Resource: <group>/<kind>/<k8s-namespace>/<name>`
([ADR 0022](../adr/0022-audit-attribution-header.md)). OpenBao logs a request header only when it
is configured as an audited header, in the root namespace. Apply
[auditrequestheader-attribution.yaml](../../examples/sys/auditrequestheader-attribution.yaml)
through a connection whose OpenBao namespace is root; `hmac: false` keeps the value readable. The
audit log then shows `request.headers.x-openbao-config-resource` on each entry, so a write traces
to its custom resource and Git object:

```sh
jq -r 'select(.request.headers["x-openbao-config-resource"]) |
  [.time, .request.operation, .request.path, .request.headers["x-openbao-config-resource"][0]] | @tsv' audit.log
```

## AccessLayout refuses to remove access

An `AccessLayout` whose new spec would delete policies or groups it created earlier fails with
`InvalidSpec` and a message naming the paths it would delete. This guards against removing
people's access by mistake. Confirm the change with the annotation:

```sh
kubectl annotate accesslayout <name> -n <ns> openbaoconfig.io/allow-disruptive-update=true
```

Remove the annotation after the change is applied, so the next accidental removal is caught again.

## NamespaceChanged

The connection's `namespace` changed after the resource acquired its object. Writing now would
create a second object in the new namespace and leave the first one unmanaged, so the operator
stops. Either set the connection's `namespace` back, or move the resource deliberately: set
`deletionPolicy: Orphan`, delete the resource, and create it again so it acquires the object in
the new namespace.

## Stuck finalizers

A resource with `deletionPolicy: Delete` has the finalizer `openbaoconfig.io/finalizer`. Deletion
waits until the operator has deleted the OpenBao object. If it cannot, `Synced` reports
`CleanupBlocked`; the usual causes are a deleted connection, a missing credential, a sealed server
or a 403 on delete.

Fix the cause first: restore the connection or credential, or grant `delete`. The finalizer then
goes on its own.

If the OpenBao object should stay, switch to `Orphan`; the operator removes the finalizer at its
next reconcile, without touching OpenBao:

```sh
kubectl patch policy team-a-app -n team-a --type=merge -p '{"spec":{"deletionPolicy":"Orphan"}}'
```

Remove the finalizer by hand only when no manager runs and you accept that the OpenBao object
stays behind:

```sh
kubectl patch policy team-a-app -n team-a --type=json \
  -p '[{"op":"remove","path":"/metadata/finalizers"}]'
```

Then delete the object in OpenBao yourself if it should go: `status.path` holds its full path,
including the namespace. Read it before you remove the finalizer.

An `AccessLevel` held by `openbaoconfig.io/in-use` is not stuck: delete the grants and layouts
that cite it.
