# Status and events

The exact status contract of every kind: conditions, reasons, status fields, events and the
columns `kubectl get` prints. Facts on this page come from `api/common/v1alpha1/types.go`,
`api/core/v1alpha1`, `internal/engine/reconciler.go` and `internal/controller/core`. For what to do
about a failing resource step by step, use [troubleshooting](../operate/troubleshooting.md); for
metrics and alert rules, use [observability](../operate/observability.md).

## Who writes which status

| kind | status written by | conditions |
|------|-------------------|------------|
| every managed kind (`sys`, `auth`, `secrets`, `identity`, `OpenBaoEndpoint`, `AccessGrant`, `AccessLayout`) | the generic engine (`internal/engine`) | `Ready`, `Synced` |
| `AccessLevel` | its own reconciler (`internal/controller/access/accesslevel.go`); it writes nothing to OpenBao | `Ready`, `Synced` |
| `OpenBaoServer` | the server controller (`internal/controller/core/server_controller.go`) | `Ready` |
| `OpenBaoConnection`, `ClusterOpenBaoConnection` | the connection controller (`internal/controller/core/connection_controller.go`) | `Ready` |

Every status is written with a merge patch, and only when it changed. The engine and `AccessLevel`
use the field manager `openbao-config-operator`; the server and connection controllers use the
manager's default field manager.

## Conditions of managed kinds

| condition | `True` means | `False` means |
|-----------|--------------|---------------|
| `Ready` | the OpenBao object matched the spec at the end of the last pass that reached OpenBao | the object is missing, differs, cannot be reached or must not be touched; the reason says which |
| `Synced` | the last pass completed | the last pass failed or is waiting; the reason and message say why |

Each condition carries `observedGeneration`, the `metadata.generation` it describes. Messages
name paths and the keys that differed, never values.

### observedGeneration

- Every condition update also sets `status.observedGeneration` to the current generation, whether
  the pass succeeded or failed. `status.observedGeneration` alone therefore shows that the operator
  has seen a spec, not that it applied it.
- A spec is applied when `Ready` and `Synced` are both `True` and both conditions'
  `observedGeneration` equal `metadata.generation`. The engine uses the same test
  (`engine.Reconciled`) to decide that a pass is a drift check of a settled resource.
- A failed pass with an error leaves an existing `Ready` condition as it was. A resource can
  therefore show `Ready=True` (for an earlier generation) and `Synced=False` while OpenBao refuses
  a change.
- Annotation changes do not bump the generation. Only `openbaoconfig.io/paused` and
  `openbaoconfig.io/allow-destructive-delete` start a pass when they change.

### Reasons

The retry column is what the engine does after the pass. "Drift interval" means
`spec.driftInterval`, or 15 minutes for access-critical kinds and 1 hour for the rest, each with
±20% jitter. "Backoff" without a cap is controller-runtime's exponential error backoff.

| reason | `Ready` | `Synced` | meaning | retry | what to do |
|--------|---------|----------|---------|-------|------------|
| `Available` | `True` | | the object matches the spec | drift interval | nothing |
| `ReconcileSuccess` | | `True` | the pass completed | drift interval | nothing |
| `Observed` | `True` | `True` when equal | `managementPolicy: Observe`; the object exists and was read; `Synced` is `Drifted` when it differs | drift interval | nothing |
| `Drifted` | | `False` | `managementPolicy: Observe`; the object differs from the spec in the keys named in the message | drift interval | change the spec or OpenBao, or switch to `Manage` to let the operator converge it |
| `NotFound` | `False` | `False` | `creationPolicy: Adopt`, or `managementPolicy: Observe`, and the object does not exist | drift interval | check the name, mount path and `spec.namespace`; use `CreateOrAdopt` to create it |
| `Conflict` | `False` | `False` | another resource or cluster owns the path, or `creationPolicy: Create` found an object it does not own; the claim in status is dropped | drift interval | give the path one owner, or set `Adopt` or `CreateOrAdopt` to take an unowned object over |
| `ConnectionNotReady` | `False` | `False` | the connection does not exist, is being deleted or is not `Ready` for its current generation | 2 s, growing to 1 min; a connection change re-queues at once | check the connection's own reason |
| `ConnectionDenied` | `False` | `False` | the `ClusterOpenBaoConnection` does not select this namespace, or the `OpenBaoServer` no longer admits the `OpenBaoConnection` | drift interval | `allowedNamespaces` and the namespace labels |
| `ReferenceNotReady` | `False` | `False` | a referenced Secret, key or resource is missing or not `Ready`, the OpenBao namespace does not exist yet, or OpenBao has no route for the path yet (the mount is missing) | 2 s, growing to 30 s; a referent change re-queues at once | create the referent named in the message |
| `PermissionDenied` | `False` if no `Ready` yet | `False` | OpenBao answered 403: the connection's policy does not allow the request | backoff | extend the OpenBao policy of the connection identity ([OpenBao policy](../operate/openbao-policy.md)) |
| `ReconcileError` | `False` if no `Ready` yet | `False` | OpenBao or Kubernetes returned another error | backoff | read the message; a 400 is usually an invalid value |
| `InvalidSpec` | `False` | `False` | the spec, or a Secret it references, passed the schema but can never work as written | none until the spec or a referenced Secret changes | fix the spec or the Secret |
| `RootOnlyKind` | `False` | `False` | a root-only kind (`Audit`, `AuditRequestHeader`, `RateLimitQuota`, `QuotaConfig`, `Plugin`, `CORSConfig`, `UIHeader`, `Logger`, `RaftAutopilot`) resolves to a child OpenBao namespace; refused before any OpenBao call | none until the spec changes | use a connection based in the root namespace and no `spec.namespace` |
| `Unsupported` | `False` | `False` | the server cannot serve the kind, for example the plugin or mount is missing | drift interval | mount the plugin or engine, or remove the resource |
| `PluginNotRegistered` | `False` | `False` | a mount needs a plugin that is not in the server's catalog | drift interval | register the plugin (`Plugin` kind) |
| `NamespaceChanged` | `False` | `False` | the object was acquired in one OpenBao namespace and the connection now resolves to another; nothing is written | drift interval | restore the connection's `namespace`, or recreate the resource |
| `ServerChanged` | `False` | `False` | the object was acquired on another OpenBao cluster than the one the connection reaches now; nothing is written or deleted | drift interval; when detected right before a create, only `Synced` is set and the pass is retried with backoff | restore the connection, or release the resource ([architecture](../architecture.md#ownership)) |
| `OutOfSync` | `False` | `False` | a write succeeded but the read after it returned different values | backoff from 2 s, capped at the drift interval | the message names the keys; write them as OpenBao returns them, or report the kind |
| `WriteBudgetExceeded` | `False` if no `Ready` yet | `False` | the server's write budget (default 60 rewrites per minute) is spent; this rewrite waits | when the budget frees, plus a random delay of up to 1 min | nothing; investigate if it persists ([observability](../operate/observability.md)) |
| `DeleteGuarded` | `False`, except during deletion | `False` | deleting, or recreating after a create-only change, a destructive object (`SecretsMount`, `AuthMount`, `OpenBaoNamespace`, `KVSecret`, `RandomSecret`, `Audit`, `OpenBaoEndpoint`) needs the annotation `openbaoconfig.io/allow-destructive-delete: "true"` | drift interval, or no requeue during deletion; the annotation change re-queues at once | set the annotation if the data may go |
| `CleanupBlocked` | | `False` | `deletionPolicy: Delete` could not delete the OpenBao object; the finalizer stays | backoff | the message; usually the connection or its credential is gone |
| `ReconcileInProgress` | | `False` | OpenBao runs an asynchronous operation, such as a namespace deletion | every 10 s | wait |
| `SecretNotOwned` | `False` | `False` | the output Secret named in `writeSecretRef` exists and is not controlled by this resource; nothing is written to OpenBao | drift interval | rename `writeSecretRef` or delete the foreign Secret |
| `OutputNotDelivered` | `False` | `False` | a value OpenBao returned once is not in the output Secret | 2 s, growing to 30 s | the message names the Secret and how to rotate |
| `Paused` | | `False` | `openbaoconfig.io/paused: "true"` on the resource, its Kubernetes Namespace or its `OpenBaoServer`; nothing is read, written or deleted, and a deletion keeps the finalizer | on resume: at once for the resource's own annotation, within about 1 min for a Namespace or server annotation | remove the annotation |
| `InUse` | `False` | | `AccessLevel` only: its deletion waits while an `AccessGrant` or `AccessLayout` cites it (`status.citedBy`) | on change of a citing resource | change or delete the citing resources |

A blank cell means the pass does not change that condition. `Orphaned` is an event reason only
(see [events](#events)).

## Conditions of connections and servers

`OpenBaoServer`, `OpenBaoConnection` and `ClusterOpenBaoConnection` have only the `Ready`
condition. Both controllers probe every 1 to 1.2 minutes and on changes of the objects
they watch, and write the status only when it changed.

| kind | reason | meaning |
|------|--------|---------|
| `OpenBaoServer` | `Available` | reachable, initialised and unsealed |
| `OpenBaoServer` | `Unreachable` | `sys/seal-status` or `sys/health` failed: DNS, network, TLS or the address |
| `OpenBaoServer` | `Sealed` | the server is sealed or not initialised |
| `OpenBaoServer` | `ReferenceNotReady` | the CA bundle Secret, ConfigMap or key does not exist |
| `OpenBaoServer` | `InvalidSpec` | `allowedNamespaces` is not a valid selector, an `http://` address without `allowInsecureHTTP`, or another unusable value |
| connection | `Available` | the server is `Ready`, admits the connection, and login and `auth/token/lookup-self` succeeded |
| connection | `ServerNotReady` | the `OpenBaoServer` is missing or not `Ready` for its generation; the message carries the server's reason |
| connection | `ConnectionDenied` | the server does not select the connection's namespace, an audience is not in `allowedAudiences`, or an audience is one the Kubernetes API server accepts |
| connection | `CredentialsNotFound` | a referenced Secret, key or token file does not exist |
| connection | `AuthenticationFailed` | the login or the token lookup failed |
| connection | `ReferenceNotReady` | the connection's OpenBao namespace does not exist yet; retried from 2 s, growing to 30 s |
| connection | `InvalidSpec` | the spec cannot be turned into a client configuration |

Field reference for these kinds: [connections](connections.md).

## Status fields

### Common to every managed kind

| field | type | meaning |
|-------|------|---------|
| `conditions` | list of conditions, keyed by `type` | `Ready` and `Synced` (see [reasons](#reasons)) |
| `observedGeneration` | integer | the generation the operator last processed (see [observedGeneration](#observedgeneration)) |
| `path` | string | full OpenBao API path including the namespace, as last written; deletion uses it even after the spec changed. Recorded as a claim right before a create, then kept. Example: `team-a/sys/policies/acl/app-read` |
| `namespace` | string | absolute OpenBao namespace the object was written in; empty is the root namespace. A later mismatch gives `NamespaceChanged` |
| `serverID` | string | cluster id (from `sys/health`) of the OpenBao server the object was claimed or acquired on. A later mismatch gives `ServerChanged` |
| `acquired` | `Created` or `Adopted` | how the operator came to own the object; empty until owned |
| `writeOnlyHash` | string | HMAC-SHA256 of the write-only inputs under a key derived from the operator's hash key and the resource's UID, group and kind; a change forces a write. It never reveals the values |
| `id` | string | identifier OpenBao generated (entity, group or alias ID, mount accessor), for kinds that have one; other resources reference it |
| `rotationNonce` | string | the resource's rotation nonce when one-time inputs (a root credential OpenBao rotates afterwards) were last sent |

Under `managementPolicy: Observe` the engine leaves `acquired`, `path` and `namespace` unchanged,
and `serverID` too while the resource holds a path.

### Kind-specific fields

| kind | field | meaning |
|------|-------|---------|
| `AccessGrant` | `roles` | Kubernetes auth role names, one per subject in spec order |
| `AccessGrant`, `AccessLayout` | `children` | every OpenBao object the resource owns; with `deletionPolicy: Delete` exactly these are deleted |
| `AccessLevel` | `citedBy` | `<kind>/<name>` of the resources that cite the level; the `openbaoconfig.io/in-use` finalizer holds a deletion while it is not empty |
| `AppRoleSecretID` | `rolePath`, `expirationTime`, `retiredAccessors` | the role path, expiry of the current secret ID, accessors of replaced secret IDs still to destroy |
| `GroupMembership` | `groupID`, `memberEntityIDs`, `memberGroupIDs` | the group and the members this resource added (it removes only those) |
| `KVSecret` | `managedKeys`, `pathCreated` | key names written (never values); whether this resource created the secret, which decides whether a destructive delete removes the version history |
| `RandomSecret` | `refreshNonce`, `pathCreated` | the `spec.refreshNonce` of the current value; as for `KVSecret` |
| `PKIIssuer` | `keyID`, `csr` | the issuer's key ID; the CSR of an intermediate waiting to be signed |
| `SSHCA` | `publicKey` | the CA's OpenSSH public key |
| `TransitKey` | `latestVersion`, `rotatedNonce` | the key's latest version; the last applied `spec.rotateNonce` |

### Connections and servers

| field | `OpenBaoServer` | connections | meaning |
|-------|-----------------|-------------|---------|
| `conditions` | yes | yes | `Ready` only |
| `observedGeneration` | yes | yes | the generation last probed or checked |
| `version` | yes | copied from the server | OpenBao version reported by `sys/health` |
| `sealed` | yes | copied from the server | `true` when sealed or not initialised |
| `standby` | yes | copied from the server | `true` when the probed node is a standby |
| `authenticated` | | yes | the last login and `auth/token/lookup-self` succeeded |
| `lastProbeTime` | yes | yes | when a probe last changed the status |

## Events

Events are written with the `events.k8s.io/v1` API by the engine; the reporting controller is
`<kind>-controller` in lower case (for example `policy-controller`). Each event has a type, a reason
and an action. Notes name paths, owners and keys, never values. The connection, server and
`AccessLevel` controllers emit no events.

| type | reason | action | emitted when |
|------|--------|--------|--------------|
| `Normal` | `Created` | `Create` | the operator created the object: `created <path>` |
| `Normal` | `Updated` | `Update` | the operator wrote a difference: `updated <path>: <keys>`, or `write-only inputs` |
| `Normal` | `Adopted` | `Adopt` | an existing object was taken over under `Adopt` or `CreateOrAdopt` |
| `Normal` | `Recreated` | `Delete` | a destructive object was deleted to apply a create-only change (only with the destructive annotation) |
| `Normal` | `Deleted` | `Delete` | `deletionPolicy: Delete` removed the object |
| `Warning` | `Conflict` | `Acquire` | the resource entered `Conflict` (once per entry) |
| `Warning` | `Conflict` | `Delete` | deletion left the object in place: an older resource owns it, or its marker names another owner |
| `Warning` | `ServerChanged` | `Reconcile` | a pass stopped with `ServerChanged` |
| `Warning` | `ServerChanged` | `Delete` | deletion left the object on the server it was acquired on |
| `Warning` | `NamespaceChanged` | `Reconcile` | a pass stopped with `NamespaceChanged` |
| `Warning` | `Drifted` | `Observe` | an observed object differs from the spec, when the differing keys change |
| `Warning` | `OutOfSync` | `Update` | a write did not read back as written (once per entry) |
| `Warning` | `DeleteGuarded` | `Delete` | a destructive delete or recreate waits for the annotation (once per entry) |
| `Warning` | `WriteBudgetExceeded` | `Update` | a rewrite waits for the write budget (once per entry) |
| `Warning` | `Orphaned` | `Delete` | the Kubernetes Namespace is terminating and its connection is gone; the finalizer is released and the OpenBao object stays |

```sh
kubectl get events -A --field-selector reason=Updated
```

## Print columns

`kubectl get` shows these columns. Every kind is in the `openbao` category, so
`kubectl get openbao -A` lists all managed kinds.

| kinds | columns |
|-------|---------|
| every managed kind | `Ready`, `Synced`, `Age`, plus the extra columns below |
| `OpenBaoServer` | `Ready`, `Address`, `Version`, `Sealed`, `Age` |
| `OpenBaoConnection`, `ClusterOpenBaoConnection` | `Ready`, `Server`, `Version`, `Age` |

| kind | extra columns (`-o wide` only in brackets) |
|------|--------------------------------------------|
| `OpenBaoEndpoint` | `Path` |
| `Audit`, `AuthMount`, `SecretsMount`, `Group` | `Type` |
| `Plugin` | `Type`, `Version` |
| `TransitKey` | `Type`, `Version` (`status.latestVersion`) |
| `SSHRole` | `Key Type` |
| `Logger` | `Level` |
| `RateLimitQuota` | `Rate` |
| `EntityAlias`, `GroupAlias` | `Alias` |
| `AppRoleSecretID` | `Expires` |
| `GroupMembership` | (`Group`) |
| `MFAMethod` | (`Method ID`) |
| `OIDCClient` | (`Client ID`) |
| `DatabaseConnection` | (`Plugin`) |

## Conditions to alert on

The engine exports each resource's `Ready` condition as the gauge
`openbao_resource_ready{kind,namespace,name,reason}` and each connection's as
`openbao_connection_ready{kind,namespace,name}`; [observability](../operate/observability.md)
has the rules.

| condition | why it matters | suggested `for` |
|-----------|----------------|-----------------|
| any connection `Ready=False` | every resource of that connection stops converging | 10m |
| `OpenBaoServer` `Ready=False` with `Sealed` or `Unreachable` | every connection to that server stops | 5m |
| `Ready=False` with `Conflict`, `NamespaceChanged` or `ServerChanged` | needs a person: two owners, or a connection that moved | 15m |
| `Ready=False` with `PermissionDenied` or `ReconcileError` | a policy gap or a refused value; retried with backoff | 30m |
| `Ready=False` with `OutputNotDelivered` or `SecretNotOwned` | a one-time value did not reach its Secret | 15m |
| `Synced=False` with `WriteBudgetExceeded` for a long time | something keeps rewriting objects | 1h |
| `Synced=False` with `CleanupBlocked` | a finalizer blocks a deletion | 30m |

`Paused`, `CleanupBlocked` and `WriteBudgetExceeded` set only `Synced`, so they are not visible in
`openbao_resource_ready` (see [observability](../operate/observability.md#what-the-metrics-do-not-show)).
