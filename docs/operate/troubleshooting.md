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

## Conditions and reasons

Every managed resource has the conditions `Ready` and `Synced`; a connection or server has `Ready` only. Read the
reason and message with `kubectl describe`, then look the reason up in status and events: the
[resource reasons](../reference/status-and-events.md#reasons) and the
[connection and server reasons](../reference/status-and-events.md#conditions-of-connections-and-servers) give the
meaning, the retry and what to check for each. A resource can show `Ready=True` and `Synced=False` while OpenBao
refuses a change; [observedGeneration](../reference/status-and-events.md#observedgeneration) explains why.

## Debug logging

Raise the log level with `--zap-log-level=debug` in the chart's `extraArgs`; [observability](observability.md#logs)
lists every log flag and what the logs never contain. Turn debug off again afterwards; it is verbose at scale.

## Metrics

The metrics and their labels, how to map the `connection` label to a connection, and example queries, including
the one that finds the kind behind steady-state writes, are in [observability](observability.md#operator-metrics).
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
