# Architecture

openbao-config-operator keeps the configuration of an existing OpenBao server in the state that
Kubernetes custom resources declare. One custom resource is one object at one API path, such as a
policy, an auth role, a mount or an entity. The operator does not deploy, initialise, unseal or
upgrade the server.

This page describes the design and why it is shaped this way. What is planned, and in which release, is in the
[roadmap](maintainers/roadmap.md); exact timings and limits are in [constants and timings](reference/constants.md);
what does not work yet is in the [known limitations](reference/limitations.md).

## Scope

- In scope: the common configuration objects (sys, auth, secrets engines, identity, OIDC), one
  raw `OpenBaoEndpoint` kind for every other path, and multiple tenants on one server.
- OpenBao only, minimum 2.6, current 2.7.2 ([ADR 0009](adr/0009-openbao-only-target.md)). No flavour
  detection; features OpenBao does not have get no kind.
- Out of scope: server lifecycle (OpenBao Helm chart, KubeBao operator, self-init), syncing secret
  values into Kubernetes Secrets (External Secrets Operator, [ADR 0007](adr/0007-no-secret-sync.md)),
  and one-off actions such as a login, a PKI sign or creating a token.

## Design rules

The engine enforces these rules; an adapter cannot break them. The numbers are stable, so other pages and tests
cite them.

- **1. Steady-state reconcile reads and does not write.** `openbao_writes_total` is zero at rest.
  Proven on a live OpenBao 2.7.2 cluster (81 objects of 55 kinds, zero rewrites across drift
  intervals and a manager restart). The per-kind no-write conformance test runs on every pull request (for
  the kinds it touches, every kind of a group whose Go code changes, or every kind when shared code changes), on pushes to `main`, weekly and on demand,
  except for cases documented in `test/conformance/exceptions.yaml`.
- **2. One cached client per connection.** The operator logs in once and renews. Logging in on every
  reconcile would grow entities, aliases and leases without bound; `openbao_logins_total` makes
  this visible. A new login follows only a 401 or a 403 whose lookup-self fails (see
  [client cache](#client-cache)).
- **4. One path has one owner.** A second CR on the same server, namespace and path gets `Conflict`
  and never writes.
- **5. Create by default, adopt on request.** `creationPolicy` defaults to `Create`.
- **6. Retain by default.** `deletionPolicy` defaults to `Orphan`; `Delete` is opt-in and only then
  is a finalizer added. Destructive kinds need a second explicit guard, the annotation
  `openbaoconfig.io/allow-destructive-delete`: mounts, namespaces, KV data, audit devices, key material
  (`TransitKey`, `PKIIssuer`, `PKIKey`, `SSHCA`, `TOTPKey`), MFA methods, entities and `OpenBaoEndpoint`. The
  human access of `AccessLayout` has its own guard, the annotation `openbaoconfig.io/allow-disruptive-update`.
- **7. Secrets only by reference.** Secret material comes from `SecretKeySelector` references. Nothing
  secret appears in a spec, status, event or log.
- **8. A tenant acts only as itself.** A namespaced connection never uses the operator's identity,
  files or network position. The address comes only from the platform's `OpenBaoServer`.
- **9. No root token at runtime.** OpenBao self-init creates the operator's auth role and policy at
  first boot and revokes the transient root token. The [self-init guide](guides/self-init.md) has a
  tested example.
- **10. `Ready` and `Synced` conditions with typed reasons and `observedGeneration` on every kind**,
  so Argo CD and Flux health checks and sync waves work. The [Argo CD guide](guides/argocd.md) ships an Argo
  CD health check.

### Not yet enforced

- **3. The spec is the whole desired state.** The target is that every typed field is compared, including
  omitted ones at their declared default, so removing a field from the spec unsets it
  ([ADR 0011](adr/0011-full-field-comparison.md)). Today nested maps are compared exactly, but top-level keys
  the spec omits are not compared, so removing an optional field does not always unset it. The rule moves to the
  list above when ADR 0011 is closed.

Typed kinds give validation, references and accurate status; `OpenBaoEndpoint` covers any path without a
typed kind ([ADR 0006](adr/0006-endpoint-escape-hatch.md)).

## API groups

All groups are version `v1alpha1` under the API group domain `openbaoconfig.io`, which is final
([ADR 0008](adr/0008-provisional-api-group-domain.md)). All kinds except `OpenBaoServer` and
`ClusterOpenBaoConnection` are namespaced. Every kind is in the `openbao` category, so
`kubectl get openbao` lists them all. Short names such as `policy` collide with other operators
(Kyverno); use the full resource name, for example `policies.sys.openbaoconfig.io`, in scripts.

| group | built-in kinds |
|-------|----------------|
| `core.openbaoconfig.io` | `OpenBaoServer`, `OpenBaoConnection`, `ClusterOpenBaoConnection`, `OpenBaoEndpoint` |
| `sys.openbaoconfig.io` | `Policy`, `PasswordPolicy`, `OpenBaoNamespace`, `AuthMount`, `SecretsMount`, `Audit`, `AuditRequestHeader`, `RateLimitQuota`, `QuotaConfig`, `Plugin`, `CORSConfig`, `UIHeader`, `Logger`, `KeyRotationConfig`, `RaftAutopilot`, `Workflow` |
| `auth.openbaoconfig.io` | `KubernetesAuthConfig`, `KubernetesAuthRole`, `JWTAuthConfig`, `JWTAuthRole`, `JWTCELRole`, `AppRole`, `AppRoleSecretID`, `TokenRole`, `CertAuthConfig`, `CertAuthRole`, `UserpassUser` |
| `secrets.openbaoconfig.io` | `KVSecret`, `KVConfig`, `RandomSecret`, `DatabaseConnection`, `DatabaseRole`, `DatabaseStaticRole`, `PKIIssuer`, `PKIKey`, `PKIRole`, `PKIConfig`, `PKIACMEConfig`, `PKIAutoTidy`, `TransitKey`, `SSHCA`, `SSHRole`, `KubernetesSecretsConfig`, `KubernetesSecretsRole`, `RabbitMQConfig`, `RabbitMQRole`, `TOTPKey` |
| `identity.openbaoconfig.io` | `Entity`, `EntityAlias`, `Group`, `GroupAlias`, `GroupMembership`, `OIDCConfig`, `OIDCProvider`, `OIDCClient`, `OIDCKey`, `OIDCRole`, `OIDCScope`, `OIDCAssignment`, `MFAMethod`, `MFALoginEnforcement` |
| `access.openbaoconfig.io` | `AccessLevel`, `AccessGrant`, `AccessLayout` |

The `auth` and `secrets` groups also hold 52 experimental external-plugin kinds (LDAP, Kerberos, RADIUS, the
cloud methods and engines, and others). They stay in the code but are disabled by default behind
`--enable-plugin-kinds` (chart `pluginKinds.enabled`), are not run against a server, and count towards no
coverage figure, until [plugin support](#plugin-support) works end to end. [kinds.md](kinds.md) lists every kind
with its status.

Kinds are not prefixed. The exceptions are the connection kinds, `OpenBaoServer`,
`OpenBaoNamespace` and `OpenBaoEndpoint`, whose bare names would collide with core Kubernetes kinds.

### Coverage policy

- **Any path:** `OpenBaoEndpoint` reaches every OpenBao API.
- **Built-in kinds first:** every object compiled into OpenBao gets a typed kind, tested against
  `bao server -dev` and listed in [kinds.md](kinds.md).
- **Plugin kinds after plugin support:** kinds for external plugins stay gated until a plugin can be
  registered and tested in CI, and are then enabled one backend at a time
  ([roadmap](maintainers/roadmap.md)). Until the plugin is mounted, a plugin kind reports `Unsupported` and
  re-checks at the drift interval.
- **Not typed:** features OpenBao does not have (Sentinel, KMIP, Transform, managed keys,
  replication) and one-off actions. Values OpenBao generates once, such as a secret-id or a client secret, go to
  a Kubernetes Secret through `writeSecretRef`.
- **Root only:** nine sys kinds exist only in the root namespace and are refused elsewhere with
  `RootOnlyKind` (see [namespace scope](#namespace-scope)).
- **No generated types:** CRD types are written by hand with an explicit `Desired` per kind. The
  OpenBao OpenAPI spec has no enums, almost no `required` fields, no write-only or create-only
  markers, and duplicate operation IDs that overwrite schemas (`JwtWriteRoleRequest` holds the CEL
  role fields). A pinned snapshot is still used in CI to check path coverage.

## Components

```text
cmd/main.go, cmd/controllers.go  manager wiring, flags, metrics, health; table of kind Setup funcs
cmd/obc-import, internal/importer  reads a live server into resources with managementPolicy: Observe
api/common/v1alpha1              shared spec/status types (connectionRef, policies, conditions, secret refs)
api/<group>/v1alpha1             CRD types per group: schema only, no client code
internal/openbao                 client construction, auth, token renewal, per-connection pool, rate limit, errors
internal/connection              resolves a connectionRef into a client (Secrets, files, TokenRequest, allowedNamespaces)
internal/engine                  generic reconciler, Adapter contract, compare, HMAC key, Secret and reference indexes
internal/refs                    flexible references between kinds
internal/controller/<group>      one small Adapter per kind, plus the connection controller
test/integration                 adapters against envtest + a real `bao server -dev`
test/conformance                 per-kind no-write and default tests
test/nsmatrix                    every kind in every namespace mode
test/load                        load and scale harness
test/e2e                         kind + Helm chart + OpenBao
charts/openbao-config-operator   Helm chart (CRDs as templates)
```

The API packages import nothing from `internal/`, so other projects can import the types.

### Layers

| layer | packages | owns | depends on |
|-------|----------|------|------------|
| API types | `api/common/v1alpha1`, `api/<group>/v1alpha1` | CRD schemas, defaults, CEL rules, condition and reason names | nothing in `internal/` |
| manager | `cmd/main.go`, `cmd/controllers.go` | flags, metrics and health servers, leader election, the hash key, the table of kind controllers | every layer below |
| connections | `internal/connection`, `internal/controller/core` | resolving a `connectionRef`, the tenant boundary, credential sources, `OpenBaoServer` and connection status | engine contract, OpenBao client |
| OpenBao client | `internal/openbao` | HTTP, TLS, login and renewal, the per-connection client pool, the per-server rate limit, request metrics, the attribution header | OpenBao |
| engine | `internal/engine` | the reconcile sequence, ownership and path index, drift schedule, write budget, conditions, events, engine metrics | OpenBao client |
| adapters | `internal/controller/<group>` | per kind: path, desired payload, comparison rules, optional custom read, write, delete and status | engine contract, `internal/refs` |
| import | `internal/importer`, `cmd/obc-import` | reading a live server into resources with `managementPolicy: Observe` | adapters, engine, OpenBao client |

### Controllers

| controller | kinds | reconciler | name in metrics and logs |
|------------|-------|------------|--------------------------|
| server | `OpenBaoServer` | `internal/controller/core/server_controller.go` | `openbaoserver` |
| connection | `OpenBaoConnection`, `ClusterOpenBaoConnection` | `internal/controller/core/connection_controller.go`, one per kind | `openbaoconnection`, `clusteropenbaoconnection` |
| access level | `AccessLevel` | `internal/controller/access/accesslevel.go`; writes nothing to OpenBao | `accesslevel.access.openbaoconfig.io` |
| engine | every other kind, one controller per kind; external-plugin kinds only with `--enable-plugin-kinds` | `engine.Reconciler` with the kind's adapter | `<kind>.<group>` in lower case, for example `policy.sys.openbaoconfig.io` |

A default install runs 68 controllers, one per built-in kind. Every controller runs only on the leader. Engine
controllers run `--max-concurrent-reconciles` workers each, or `--max-concurrent-reconciles-high-volume` for the
kinds that usually have many objects.

### Status ownership

| status | written by | when | read by |
|--------|------------|------|---------|
| `conditions`, `observedGeneration` of managed kinds | engine | at the end of every pass that changes them | Argo CD and Flux health checks, referrers (a referent must be `Ready` for its generation), `openbao_resource_ready` |
| `path`, `namespace`, `serverID` | engine | as a claim right before a create; again after a successful pass; cleared on `Conflict` and after a create that wrote nothing | the path index (one owner per path), deletion, the `NamespaceChanged` and `ServerChanged` checks |
| `acquired` | engine | once the object is created or adopted; cleared on `Conflict` | ownership checks, deletion (only an acquired object is deleted) |
| `writeOnlyHash`, `rotationNonce` | engine | after a successful write | the next pass, to decide whether write-only or write-once inputs must be sent again |
| `id` and kind-specific fields (`children`, `roles`, `managedKeys`, `latestVersion` and others) | the kind's adapter (`StatusWriter`, or the composite kinds' own bookkeeping), inside the engine's pass | after a successful sync or observe | referrers (`status.id`), the adapter's next pass and deletion |
| `citedBy` and conditions of `AccessLevel` | access level controller | on every change of a citing resource | its `openbaoconfig.io/in-use` finalizer |
| `OpenBaoServer` status | server controller | after a probe that changed something | connection controller (a connection is `Ready` only when its server is) |
| connection status | connection controller | after a check that changed something | the resolver (resources wait while the connection is not `Ready` for its generation), `openbao_connection_ready` |

No status is written by more than one controller. The exact fields and reasons are in
[status and events](reference/status-and-events.md).

## Connections and the tenant boundary

| kind | scope | owner | holds |
|------|-------|-------|-------|
| `OpenBaoServer` | cluster | platform | `address`, CA bundle, `allowedNamespaces`, `allowedAudiences`, `rateLimit`, `requestTimeout` |
| `OpenBaoConnection` | namespaced | tenant | `serverRef`, `namespace`, `authNamespace`, `auth` |
| `ClusterOpenBaoConnection` | cluster | platform | same, plus `allowedNamespaces`; may use the operator's own identity |

Resources reference a connection with `spec.connectionRef: {kind, name}`; `kind` defaults to
`OpenBaoConnection`, which must be in the same namespace. Both fields are immutable (CEL). The fields,
credential sources and their CEL rules are in the [connections reference](reference/connections.md).

Credentials are read at each login, so rotation needs no restart. A namespaced connection logs in with a
ServiceAccount token from TokenRequest, or a token or AppRole from a Secret in its own namespace. Only the
cluster kind may use the operator pod's own projected token (`injectedIdentity`) or a token file, because only
platform admins create it; that right is as strong as the operator's own OpenBao identity.

What a tenant cannot do:

- **choose the address:** connections have no address field; `serverRef` must name an
  `OpenBaoServer` whose `allowedNamespaces` selects the tenant's namespace (resolver check, reason
  `ConnectionDenied`);
- **use the operator identity:** `OpenBaoConnection` has no `injectedIdentity` or `filesystem`
  field, CEL requires `serviceAccountRef` and `audiences`, and the resolver refuses a namespaced
  Kubernetes or JWT login without them when validation was bypassed;
- **mint a general token:** every audience must be in the server's `allowedAudiences`, and the API
  server audiences (the built-in `https://kubernetes.default.svc[.cluster.local]`,
  `kubernetes.default.svc`, `rke2` and `k3s`, the discovered service account issuer,
  `--apiserver-audiences`) are always rejected, before any TokenRequest;
- **exceed its OpenBao policy:** the policy bound to the tenant's ServiceAccount is the limit of
  what it can configure ([ADR 0003](adr/0003-connections-and-tenant-boundary.md)).

These are the controls in the code. What the tests prove today is narrower: the e2e test in `test/e2e` runs a
tenant connection under a non-root policy, and the [known limitations](reference/limitations.md#verification)
list what is not proven end to end; a two-tenant e2e test (`test/e2e/isolation_test.go`) proves that a second tenant's writes outside its OpenBao namespace and policy, its references to another tenant's objects and its use of a server that excludes it are refused, and that none of the objects it tried to create exists in OpenBao. The default install also holds cluster-wide Secret and
TokenRequest rights ([security model](#security-model), RR-01).

`OpenBaoServer` status reports `Ready`, `version`, `sealed` and `standby` from the unauthenticated
`sys/seal-status` and `sys/health` in the root namespace. Connection status copies those and adds
`authenticated`, checked with `auth/token/lookup-self`; a connection is `Ready` only when its server
is. Both statuses are written only when they change.

### Namespace scope

A resource's effective OpenBao namespace is the connection's `namespace` joined with its own
`spec.namespace`, which is relative and cannot climb out: CEL rejects a leading `/` and `.`/`..`
segments, and the engine (`openbao.ValidateNamespace`) refuses them again with `InvalidSpec`. Two
login patterns follow:

| pattern | connection | reaches |
|---------|------------|---------|
| log in at the root | `namespace` unset, or `namespace: team-a` with `authNamespace: ""` | the root and every child namespace the token's policy allows |
| log in in a child | `namespace: team-a`, `authNamespace` unset, auth mount inside `team-a` | `team-a` and its descendants only; a parent or sibling answers 403, reported as `PermissionDenied` with nothing written |

Every built-in kind works in any namespace except the root-only kinds `Audit`, `AuditRequestHeader`,
`RateLimitQuota`, `QuotaConfig`, `Plugin`, `CORSConfig`, `UIHeader`, `Logger` and `RaftAutopilot`:
OpenBao 2.7.2 answers 400 ("operation only available in root namespace", "operation unavailable in
namespaces") or 404 (plugin catalog) for them in a child. Their adapters implement `engine.RootOnly`,
and the engine stops such a resource whose effective namespace is not the root with `Ready=False`
reason `RootOnlyKind`, before any OpenBao call. `KeyRotationConfig` needs the root or a sealable
namespace and reports `Unsupported` elsewhere. `test/nsmatrix` proves each kind in every mode
against `bao server -dev`.

### Client cache

`internal/openbao` keeps one authenticated client per connection, keyed by connection UID,
connection generation, server generation and a hash of the credential inputs. The credential inputs are the
content of the referenced credential Secret and CA object, not their `resourceVersion`, so an edit that changes
only labels or annotations does not force a new login; a token file is keyed on its modification time. A changed
value logs in again and revokes the token of the replaced login. Each client:

- renews a renewable token once a third of its TTL has passed and logs in again before a
  non-renewable token expires;
- on a 401 logs in again and retries the request once;
- on a 403 calls `auth/token/lookup-self`: when the token is still valid, the 403 is a policy denial and the
  request fails with `PermissionDenied` without a login; when lookup-self fails, it logs in again and retries
  once;
- logs in again after a 401 or a failed lookup-self at most once per 5 s, so a dead credential cannot cause a
  login storm;
- retries 429 and 5xx with backoff, inside one token bucket per server shared by every connection
  to it (`OpenBaoServer.spec.rateLimit`); every HTTP attempt takes a token, retries, logins and token
  renewals included; drift reads go without priority, so spec changes go first;
- never follows a redirect;
- serves another OpenBao namespace from a clone (`WithNamespace`) without changing the shared
  client;
- shares one login between concurrent cache misses; the login runs detached from any single
  caller's context (with its own timeout), and each caller stops waiting when its own context ends;
- revokes the token it logged in for, best effort and in the background, when the connection is
  deleted, denied or its credentials change; a static token (`auth.token`) is never revoked.

The renewal points, the re-login floor and the retry counts are in
[constants and timings](reference/constants.md#login-renewal-and-tokens).

## Resources

### Common spec

Every kind embeds `common.ResourceSpec` inline:

| field | default | meaning |
|-------|---------|---------|
| `connectionRef` | required | which connection to use; immutable |
| `namespace` | `""` | OpenBao namespace relative to the connection namespace; immutable |
| `creationPolicy` | `Create` | `Create`: refuse an existing object it does not own. `Adopt`: manage only an existing object. `CreateOrAdopt`: either |
| `deletionPolicy` | `Orphan` | `Orphan`: leave the object. `Delete`: remove it on CR delete |
| `driftDetection` | `Enabled` | `Disabled`: never re-read on a schedule; a pass runs on a spec, Secret or referent change, and once after an operator restart |
| `driftInterval` | tier default | per-object drift interval, at least 30 s |
| `managementPolicy` | `Manage` | `Observe`: read and report drift, never write or delete (see [operational safety](#operational-safety)) |

Kinds whose endpoint has no delete reject `Delete` with CEL. Configuration that always exists with defaults
(adapters implementing `Singleton`, such as `CertAuthConfig`, `KVConfig`, `PKIConfig` and `OIDCConfig`) can never
be created, so the engine adopts the existing object whatever the `creationPolicy`; `Delete` resets such a
setting to its default.

### Common status

Every kind embeds `common.ResourceStatus`: the conditions `Ready` and `Synced`, `observedGeneration`, the full
OpenBao `path`, the absolute `namespace`, the `serverID` the object was acquired on, `acquired`, `writeOnlyHash`,
`id` and `rotationNonce`. Each field is defined in
[status and events](reference/status-and-events.md#common-to-every-managed-kind); the rules below are why they
exist.

Observing never acquires, transfers, relinquishes or preserves ownership: under `managementPolicy:
Observe` the engine leaves `acquired`, `path`, `namespace` and claims as they are, and `serverID`
too while the resource holds a path. A resource that never held a path records in `serverID` the
server its observed `id` came from, which references check. `path` is canonical: leading and
trailing slashes are dropped, as OpenBao ignores them, so `sys/policies/acl/foo/` and
`sys/policies/acl/foo` are one path for the ownership index, the create lock and the request.

A resource counts as settled (the drift check may be skipped) only while `Ready` and `Synced` are
both `True` for its current generation (`engine.Reconciled`); a failed pass leaves an earlier `Ready`
in place, so the retry applies the spec change. Adapters that report fields OpenBao cannot return
(`OpenBaoEndpoint` with `disableRead`, a versioned `Plugin`) use the same check and skip the read
after the write. With `driftDetection: Disabled` the skip also needs the desired payload, values
rendered from referents (an accessor, an id) included, to equal what the last successful pass of this
process applied, so a referent change is written; the digest lives in memory, hence one read per
resource after a restart.

### The engine

`internal/engine` holds one generic reconciler ([ADR 0002](adr/0002-generic-engine-with-adapters.md)).
Each kind supplies an `Adapter` (`internal/engine/contract.go`):

```go
type Adapter[T Object] interface {
    Path(obj T) string                                               // relative to the object's OpenBao namespace
    Desired(ctx context.Context, r Resolver, obj T) (Payload, error) // may resolve Secret and cross-resource refs
    Fields() FieldSpec                                               // comparison rules
}
```

Kinds that do not fit "write a payload to a path" implement optional interfaces. The engine checks for each one
with a type assertion, so a kind pays only for what it uses.

| interface | the adapter |
|-----------|-------------|
| `FieldsProvider` | adds per-object comparison rules, merged over `Fields()` |
| `PathResolver` | computes the path from a referenced object, such as a mount chosen by `mountRef` |
| `Observer` | replaces the default read, for example a lookup by name or a list and filter |
| `Writer` | replaces the default write, for example enable then tune a mount; returns `ErrNothingWritten` when the server already caught up |
| `Deleter` | replaces the default delete of `status.path` |
| `StatusWriter` | copies server-side values (ids, accessors) into the status after a successful sync |
| `OutputWriter` | turns a write response into an owned Kubernetes Secret, for values OpenBao returns once |
| `OutputSecretNamer` | names that Secret, so the engine refuses a Secret it does not own before anything is written to OpenBao |
| `OutputRefresher` | rebuilds the output from a read, for outputs OpenBao can return again, so a lost Secret is rewritten without an OpenBao write |
| `OutputsDeliveredHook` | cleans up in OpenBao once the output Secret holds the current value, such as destroying replaced secret IDs |
| `Destructive` | marks a kind whose delete destroys data; `Delete` and a recreate need the destructive annotation |
| `DriftTier` | puts the kind in the access-critical drift tier |
| `Marker` | writes and reads the server-side ownership marker where the object has metadata |
| `RotationNonce` | supplies the nonce that re-sends `WriteOnce` fields |
| `ReadBackSkipper` | skips the read after a write for objects that cannot be read back |
| `SharedPath` | writes to a path another resource owns on purpose (`GroupMembership`, claim-only edge kinds); left out of the path index and the marker |
| `Singleton` | marks configuration that always exists with defaults; adopted whatever the `creationPolicy` |
| `RootOnly` (`rootonly.go`) | refuses a child namespace with `RootOnlyKind` before any OpenBao call |
| `ReferenceWatcher` (`references.go`) | names the referenced kinds, which the engine watches to re-queue referrers |
| `SecretReferencer` (`secretindex.go`) | names the Secrets the resource reads, for the metadata-only Secret watch |

`ChildOwner` (`ownership.go`) is implemented by the composite resource types, not by adapters: it adds the paths
of the children they created to the path index. On the connection side, `CleanupConnections`, `ServerResolver`
and `ClusterIdentifier` let the engine delete through a terminating connection, key the path index, the write
budget and pause by server, and bind objects to a cluster id.

Adapter errors map to reasons: `ErrInvalidSpec` is terminal (`InvalidSpec`, no retry until the
spec or a Secret changes), `ErrUnsupported` and `ErrPluginNotRegistered` retry at the drift interval,
`ErrInProgress` requeues without backoff, `ErrConflict` reports `Conflict` and `ErrReferenceNotReady` stalls with
a growing retry. Write errors redact any write-only value OpenBao echoes.

### Field classes

The comparison in `internal/engine/compare.go` treats each key by its class
([ADR 0011](adr/0011-full-field-comparison.md)):

| class | `FieldSpec` | compared | sent |
|-------|-------------|----------|------|
| normal | (default) | as JSON, so numbers compare numerically; nested maps exactly, so a nested key removed from the spec is a difference | yes |
| partial | `Partial` | nested maps on the desired keys only, for maps the server merges into or extends; a trailing `*` matches by prefix | yes |
| duration | `Durations` | in seconds, so `"1h"` equals `3600`; only for keys listed, which is opt-in per adapter | yes |
| set | `Sets` | order and duplicates ignored | yes |
| alias | `Aliases` | the read name mapped to the write name, for example `token_period` to `period`; when a read holds both names the read name wins | yes |
| write-only | `WriteOnly` | no; HMAC into `writeOnlyHash` | yes, every write, because several config endpoints replace the whole object |
| write-once | `WriteOnce` + `RotationNonce` | no | on create, and again only after a rotation nonce changes ([ADR 0017](adr/0017-write-once-fields-for-rotated-root-credentials.md)) |
| create-only | `CreateOnly` | no | on create only; on a destructive kind a changed value recreates the object, behind the guard |
| server-owned | `ServerOwned` | no | no (accessor, `creation_time`) |

A nil value, an absent key, an empty list and an empty map are equal to one another; `""`, `0` and `false` are
not equal to absent. Only the keys `Desired` returns are compared (rule 3 is [not yet enforced](#not-yet-enforced)),
and `OpenBaoEndpoint` compares only its data keys, so a key removed from `data` is not unset on the server.

### Reconcile sequence

`engine.Reconciler.Reconcile` (`internal/engine/reconciler.go`) runs these steps in order; any step can end the
pass with a condition and a requeue:

1. **Pause.** The `openbaoconfig.io/paused` annotation on the resource, its Kubernetes Namespace or its
   `OpenBaoServer` stops the pass with `Paused`: no read, write or delete.
2. **Deletion.** A resource being deleted goes to [deletion](#deletion) instead.
3. **Connect.** Resolve the connection (`ConnectionNotReady`, `ConnectionDenied`), validate `spec.namespace`
   (`InvalidSpec`), refuse a root-only kind in a child namespace (`RootOnlyKind`), compare the recorded namespace
   (`NamespaceChanged`), resolve the server and its cluster id, and compare the recorded `serverID`
   (`ServerChanged`).
4. **Recovery ramp.** The first pass after `ConnectionNotReady` waits for the server's recovery ramp, so the
   backlog after an outage reaches the server gradually.
5. **Finalizer.** Add the finalizer when the effective policy is `Delete` (and not `Observe`), or remove it.
6. **Desired.** Build the payload and the comparison rules, through a resolver that reads Secrets and
   referents; add the ownership marker.
7. **Hash.** Compute `writeOnlyHash` and the rotation nonce.
8. **Settled.** Decide whether this is a drift check of an unchanged, `Ready` spec; with `driftDetection:
   Disabled` and an unchanged rendered payload the pass ends here.
9. **Path index.** Resolve and validate the path; an older resource that owns the same absolute path on the same
   server means `Conflict`.
10. **Read.** A 404 means absent; a 404 "namespace not found" and a missing referent stall with
    `ReferenceNotReady`; 403 is `PermissionDenied`; a 400 is never treated as absent. Under `Observe` the pass
    reports `Observed` or `Drifted` here and stops.
11. **Create lock.** Before a create, take the per-server, per-path lock and read again, so of two resources
    only the first creates.
12. **Live owner check.** Before a create or an adoption, read the claims on the path from the API server
    rather than the cache, so a resource never adopts what an older resource has just created.
13. **Marker and acquire.** A marker from another cluster is `Conflict`; a marker naming this resource is
    ours; otherwise apply `creationPolicy` (`Conflict` or `NotFound`, `Adopted` event on adoption).
14. **Destructive guard.** A changed create-only field on a destructive kind needs the destructive annotation
    (`DeleteGuarded`).
15. **Cluster id re-check.** Before a create or a recreate, read the cluster id again past the client's cache; a
    different server means `ServerChanged`. A permitted recreate then deletes the object.
16. **Write.** Compare by field class and hash. When a write is due, refuse an output Secret this resource does
    not own (`SecretNotOwned`); then a rewrite of an owned object with an unchanged spec and unchanged inputs
    takes a slot of the server's write budget, or waits with `WriteBudgetExceeded`; then claim the path in status
    before a create, write, and record `acquired: Created` at once after a create. A 404 "no handler for route"
    on a write is `ReferenceNotReady` (the mount does not exist yet).
17. **Outputs.** Write one-time outputs to the owned Secret, or refresh them from the read.
18. **Read-back.** Read the object again and compare; a mismatch gives `OutOfSync` and a backoff retry.
19. **Status.** Let the `StatusWriter` copy server values, record path, namespace, server, hash and
    `observedGeneration`, set `Ready` and `Synced`, write the status only if it changed, and requeue at the
    jittered drift interval.

The finalizer is added in step 5, before the path index check in step 9, so a resource that ends in `Conflict`
can still hold a finalizer; deletion then removes it without touching the object, because only an acquired path
is deleted.

### Ownership

In order ([ADR 0012](adr/0012-ownership-path-index-and-marker.md)):

1. server-side marker (managed-by, cluster id and CR UID) where the object has metadata: entities,
   groups and namespaces;
2. status: `acquired` set and `status.path` matching;
3. the path index finds other claims on the same path.

The path index is a field index on the absolute OpenBao path only. Claims on another server are filtered out
afterwards, by the recorded `serverID` or, before one is recorded, by the server behind the claimant's
connection; a claimant whose server cannot be resolved keeps its claim. Among claims on one server, the oldest
`creationTimestamp` keeps the path. Timestamps have a resolution of one second, so on a tie a resource that
already holds the path wins over one that does not, and kind, namespace and name decide the rest.

An existing object that passes neither the marker nor the status check: `Create` reports `Conflict`; `Adopt` and
`CreateOrAdopt` take it over, and so does a `Singleton` kind under any policy. After status loss, kinds without a
marker need a one-time `Adopt`. The children that `AccessGrant` and `AccessLayout` created are in the path index
too, and a planned child that another resource owns is never written, whatever the `creationPolicy`: before
acquisition the resource reports `Conflict`, afterwards a reconcile error that keeps its own claim.

Before a create the engine records the path and server in the status (a claim) and the path index
shows it to every other resource; within the operator, creates of one path on one server run one at
a time and read the object again first, so of two `Create` resources only the first creates it and
the second gets `Conflict`. The live claim check before a create or an adoption reads only in the watched
namespaces (`--watch-namespaces`) and does not list every resource of the kind for each object. A create that
wrote nothing leaves no claim unless the object exists afterwards, so a failed create never lets the resource
take over an object somebody else creates later. Once a create succeeded, `acquired: Created` is recorded at
once, before outputs are delivered, so a later error in the same pass (an undelivered one-time output, a create
that takes several steps) leaves the object owned and deletable. Kinds created by a POST to a collection
(aliases, MFA methods) make no claim.

Acquisition is bound to the server: `status.serverID` holds the server's cluster id, which
`sys/health` reports without a token and which survives restarts. Each client caches it briefly and reads it
again, past the cache, right before a create, a recreate and a delete, so a
different server behind the same address (a DNS or load balancer change, a re-initialised server)
gets none of them. If a connection or `OpenBaoServer` change makes the resource reach another
cluster, reconciling stops with `ServerChanged` and nothing is written or deleted there. This holds
under `managementPolicy: Observe` too: Observe never rebinds a recorded `serverID` while the
resource holds a path, so switching to Observe and back cannot take over the other server's
object. To move a resource to the new server on
purpose, release it: clear its status (`kubectl patch <kind> <name> --subresource=status --type=merge
-p '{"status":null}'`); the next reconcile treats the new server like a first apply, so set
`creationPolicy: Adopt` or `CreateOrAdopt` if the object already exists there.

### Deletion

- `Orphan` (default): no finalizer, nothing removed.
- `Delete`: finalizer; the engine deletes `status.path`. A 404 counts as success, and a child whose
  parent mount or namespace is gone counts as deleted. If the connection or a credential is gone,
  the finalizer stays with `Synced=False` reason `CleanupBlocked` and the engine retries.
- A terminating Kubernetes Namespace leaves the object behind in two cases, because holding the finalizer would
  block the Namespace forever: the connection is not ready, or the kind is destructive and the resource lacks
  the destructive annotation. The engine then removes the finalizer, leaves the object in OpenBao and records a
  Warning event with reason `Orphaned`.
- Before deleting, the engine reads the cluster id again, past the client's cache, to check that the
  connection still reaches the server in `status.serverID` and, for kinds with an ownership marker, re-reads the
  object: another server (`ServerChanged`) or a marker naming another cluster or resource (`Conflict`) leaves the
  object in OpenBao, removes the finalizer and records a Warning event. The same applies when an older
  resource owns the path.
- [Destructive kinds](kinds.md) delete only with the annotation `openbaoconfig.io/allow-destructive-delete: "true"`;
  otherwise, in a live Namespace, the finalizer holds with reason `DeleteGuarded`. The same annotation gates an
  update that would recreate the object ([ADR 0013](adr/0013-destructive-delete-guard.md)).
- `AccessLayout` keeps its human-access guard; `AccessLevel` cannot be deleted while cited.

### References

- Secret inputs use `<field>SecretRef: {name, key}` in the same namespace. A metadata-only Secret
  watch with a field index re-queues only the resources that use a rotated Secret; Secret values
  are read live and never cached.
- Cross-resource references are flexible (`internal/refs`): exactly one of a literal OpenBao value
  (`entityID`, `mountAccessor`), an OpenBao name or path resolved through the connection
  (`entityName`, `mountPath`), a same-namespace Kubernetes object (`entityRef`, `mountRef`) or a
  label selector over such objects (`entitySelector`, `mountSelector`). CEL enforces exactly one.
  Kubernetes referents must be `Ready` for their current generation, provide the value in
  `status.id`, and have been reconciled on the referrer's server (`status.serverID`) and OpenBao
  namespace; a referent change re-queues the referrer through a field index.
- One-time outputs use `writeSecretRef: {name}`. The operator writes the Secret only if it is
  absent or already owned by the CR, and sets itself as owner; it refuses any other Secret.
  Every `OutputWriter` adapter also implements `OutputSecretNamer`, so that refusal comes before
  anything is generated in OpenBao (a test in `cmd/` enforces it).
- The engine watches the output Secrets it owns (`Owns`, metadata only), so a deleted or edited Secret
  re-queues its resource.
- A Secret write can still fail after OpenBao generated the value. The value is never kept in
  status or memory, so delivery is modelled per kind:

  | kind | value readable again | after a failed Secret write |
  |------|----------------------|-----------------------------|
  | `RandomSecret`, `OIDCClient` | yes, from KV or the client read | the next pass writes the Secret again (`OutputRefresher`), with no OpenBao write and no reported drift; `Ready=False` reason `OutputNotDelivered` while that fails |
  | `AppRoleSecretID` | no | a new secret ID is generated and delivered; the undelivered one is destroyed. Each new one counts against the write budget, and beyond three undelivered secret IDs the oldest is destroyed before the next is generated (never the one the Secret holds) |
  | `PKIKey`, `PKIIssuer` (exported), `TOTPKey` (generated) | no | `Ready=False`, reason `OutputNotDelivered`, naming the Secret and how to rotate: delete the object in OpenBao and the resource generates a new one, or set `deletionPolicy: Delete`, delete the resource (which deletes the object, with the destructive annotation where the kind needs it) and create it again |

  The last row checks on each read that the owned Secret holds the value; PKI outputs carry
  `key_id`, so a Secret left from an earlier key does not count. A generated `TOTPKey` stores an HMAC
  fingerprint of its URL, keyed with the engine hash key, in `status.id` and in the output Secret's
  `fingerprint` key, so a Secret left over after the key was regenerated out of band is detected as not
  delivered ([BL-015](project/backlog.md#bl-015-totp-output-delivery-after-an-out-of-band-key-rotation)).
- Observe mode makes no remote change: `AppRoleSecretID` destroys retired secret IDs only under
  `managementPolicy: Manage`.

### `OpenBaoEndpoint`, the escape hatch

`OpenBaoEndpoint` writes any payload to any path:

| field | meaning |
|-------|---------|
| `path`, `readPath` | path to write, and an optional different path to read back |
| `data` / `dataSecretRef` | payload as free-form JSON, or from a Secret for secret material |
| `writeOnlyFields`, `ignoreAbsentFields` | drift behaviour |
| `disableRead`, `disableDelete` | for endpoints with no read or no delete |
| `deletePath` | an optional different path to delete |

A typed kind adds validation, references and drift behaviour on top. Because `path` and `deletePath` can name a
mount or a namespace, `OpenBaoEndpoint` is a destructive kind.

## Access models

The `access` group is built on the primitives and writes policies, roles, groups and aliases
through the same engine.

- `AccessLevel` is a named set of capabilities on data and on metadata, cited by grants and
  layouts.
- `AccessGrant` is the machine side. ServiceAccount subjects get one Kubernetes auth role each and
  one shared policy, rendered from rules of the form `mount + path + level`. Paths can use
  `{sa.namespace}` and `{sa.name}`, which OpenBao identity templating renders. CEL requires a broad
  subject (a glob or a selector) to scope every path with `{sa.namespace}`. See
  [templated policies](guides/templated-policies.md).
- `AccessLayout` is the human side. A matrix of role × namespace → level becomes one external group
  per role and OIDC mount, with a group alias and a rendered policy.

An `in-use` finalizer stops a level from being deleted while cited. Removing human access requires
the annotation `openbaoconfig.io/allow-disruptive-update: "true"`.

## Validation

- CRD schemas carry formats, enums, patterns and CEL rules for immutability (`self == oldSelf`),
  one-of choices and cross-field checks. Lists and strings carry `maxItems` and `maxLength` so CEL
  stays inside its cost limits.
- No admission webhooks, so no cert-manager dependency ([ADR 0005](adr/0005-cel-validation-without-webhooks.md)).
  Checks that need other objects run at reconcile time and surface as conditions.

## Security model

| threat | control |
|--------|---------|
| tenant makes the operator call any URL | only the platform sets the address, on `OpenBaoServer`; redirects are never followed |
| tenant borrows the operator identity | no injected or filesystem sources on namespaced connections; resolver backstop |
| API server token minted for a tenant SA | audiences required and in `allowedAudiences`; the API server audience always rejected |
| another namespace's Secret read | every `*SecretRef` is same-namespace |
| unrelated Secret overwritten via `writeSecretRef` | only absent or CR-owned Secrets are written |
| weak write-only secret guessed from status | HMAC-SHA256 keyed by the operator |
| two tenants fight over one path | path index |
| CR deleted from Git removes data | `Orphan` default + destructive guard |
| leaks | events name keys only; logs carry no tokens or bodies |
| root token left behind | self-init creates the role and revokes root (example) |
| compromised manager reads every Secret and mints a token for any ServiceAccount | residual risk in the default cluster-wide install ([RR-01](security/threat-model.md#residual-risks-and-recommendations)); `watchNamespaces` replaces the cluster-wide grants with a Role per namespace |

The HMAC key is the immutable Secret `openbao-config-operator-hash-key` (flag
`--hash-key-secret`) in the operator namespace (`POD_NAMESPACE`), created with 32 random bytes on
first start. It cannot be rotated without rewriting every write-only field once.

Operator permissions: Secrets get/list/watch (references; the cache keeps metadata
only), Secrets create/update (outputs and the HMAC key), `serviceaccounts/token` create
(TokenRequest), cluster-wide by default and per namespace with `--watch-namespaces`. In OpenBao: its own role,
used only by `ClusterOpenBaoConnection`. Other residual risks: one-time outputs live
in Kubernetes Secrets (etcd encryption and RBAC apply), and drift in write-only fields is
invisible. Details in [security.md](security.md) and the [threat model](security/threat-model.md).

## Scale and drift

Reads per second = objects / interval ([ADR 0014](adr/0014-drift-tiers-and-request-budget.md)).
Every interval is jittered.

- Two tiers, both operator flags: access-critical (`Policy`, `AuthMount`, every kind of the `auth` group,
  `Audit`, `Group`, `GroupMembership`, access groups) every 15 minutes and standard (everything else) every
  hour; `spec.driftDetection: Disabled` and `spec.driftInterval` per object.
- `OpenBaoServer.spec.rateLimit`: one token bucket shared by every connection to that server; drift
  reads wait, spec-change reconciles go first; `openbao_drift_lag_seconds` shows the lag.
- Mount reads are shared: kinds that look up a mount share one read of `sys/mounts` or `sys/auth` per connection,
  OpenBao namespace and table for 2 s, not one read per object, so a large mount table is not read N times. An
  out-of-band mount change can show up to 2 s later.
- One active replica with leader election, on by default because creates are serialised only within one
  process (`--leader-elect=false` for a single replica without the lease RBAC). Watches are field-indexed.

The cost model, from the [load test](operate/load-testing.md) on one overloaded laptop (so latencies are
pessimistic; counts are exact):

- **Mount writes are the ceiling.** OpenBao takes one server-wide lock for every mount enable, tune and disable,
  whatever the namespace, so mount creation and deletion run at about 12 to 19 mounts per second however many
  workers the operator runs.
- **OpenBao memory grows with namespaces:** about 1.7 MiB per namespace in the namespace-tree run.
- **The operator idles at about 3,900 to 4,400 goroutines**, from the workers, informers and queues of its
  controllers, flat across every scenario.

The figures have not been repeated on CI hardware.

## OpenBao-native features

The design leans on features only OpenBao has:

- self-init (2.4): the operator's mount, role and policy at first boot, transient root revoked;
- namespaces: the unit of tenancy; `OpenBaoNamespace` handles the asynchronous delete;
- OCI plugins (2.5): `Plugin` registers what the server configuration downloads;
- `sys/workflows` (2.6): the `Workflow` kind;
- `JWTCELRole`: JWT roles with CEL, an OpenBao-only kind.

Horizontal read scaling (2.5) and namespace sealing (2.6) are not used yet; the
[roadmap](maintainers/roadmap.md) tracks them. OpenBao 2.6 dropped `performance_standby` and `last_wal` from
`sys/health` and deprecated the built-in LDAP, Kerberos and RADIUS methods in favour of openbao-plugins.

## Operational safety

- **Observe mode and import** ([ADR 0016](adr/0016-observe-mode-and-import-tool.md)). `spec.managementPolicy:
  Observe` reads and reports drift (`Synced` reason `Drifted` with the differing keys, or `Observed` when equal)
  and never writes or deletes; no finalizer. `cmd/obc-import` reads a live server and emits CR YAML with
  `managementPolicy: Observe` and `creationPolicy: Adopt`, so a running OpenBao is adopted incrementally.
- **Write-once fields for rotated root credentials** ([ADR 0017](adr/0017-write-once-fields-for-rotated-root-credentials.md)).
  Sent on create and when a rotation nonce changes, never re-sent otherwise, so a stale Secret does not undo a
  rotate-root inside OpenBao.
- **Write circuit breaker** ([ADR 0018](adr/0018-write-circuit-breaker.md)). Rewrites of objects the operator
  already owns, with an unchanged spec and unchanged input Secrets, are capped per `OpenBaoServer`
  (`--write-budget-per-minute`). Creates, adoptions, spec changes and changed input Secrets (a new
  `status.writeOnlyHash`) are user-intended and exempt, so onboarding and Secret rotation are not throttled; a
  regenerated one-time output (a new secret ID while the last one is undelivered) is not exempt. At the cap the
  engine holds further rewrites to that server, sets `Synced=False` with reason `WriteBudgetExceeded` (a stable
  message, so retries do not patch status) and an event, and spreads the retries over the next minute. The
  budget lives in process memory, so a restart starts a fresh window.
- **Pause** ([ADR 0019](adr/0019-pause-annotation.md)). `openbaoconfig.io/paused: "true"` on an object, its
  Kubernetes Namespace or its `OpenBaoServer` stops reconciling (no reads, writes or deletes; a deletion keeps
  the finalizer) with reason `Paused`.
- **CRD versioning without webhooks** ([ADR 0020](adr/0020-crd-versioning-without-webhooks.md)). Version bumps
  stay schema-compatible (additive; storage version migration with kube-storage-version-migrator). A conversion
  webhook only if a breaking change is unavoidable.
- **One writer cluster per OpenBao namespace** ([ADR 0021](adr/0021-one-writer-cluster-per-namespace.md)). The
  server-side marker carries a cluster id (`--cluster-id`, by default the UID of `kube-system`) and the CR UID; a
  marker from another cluster gives `Conflict`.
- **Audit attribution** ([ADR 0022](adr/0022-audit-attribution-header.md)). Every request carries
  `X-Openbao-Config-Resource: <group>/<kind>/<k8s-namespace>/<name>`; an `AuditRequestHeader` example
  (`examples/sys/auditrequestheader-attribution.yaml`) makes OpenBao log it, so each audit entry traces to a Git
  object.
- **Tenant object limits.** Kubernetes `ResourceQuota` object counts (`count/policies.sys.openbaoconfig.io` and
  so on) stop one tenant from exhausting the drift budget; see [tenant quotas](operate/tenant-quotas.md).

## Plugin support

The server configuration downloads a plugin binary; that is lifecycle, outside the operator. `Plugin` owns the
catalog entry (name, type, version, sha256; adopts an auto-registered one) and reports the version in status.
`AuthMount` and `SecretsMount` take `pluginRef` and wait for the `Plugin` to be `Ready`. A missing plugin gives
`Ready=False` with reason `PluginNotRegistered`, re-checked at the drift interval without a loop. A version
change on `Plugin` re-registers and reloads; mounts are not recreated.

The plugin-backed kinds are experimental and gated until their plugin and backend run in CI; the
[roadmap](maintainers/roadmap.md) orders them. `RabbitMQConfig` and `RabbitMQRole` are built into 2.7.2 and
integration-tested.

## Observability

| metric | labels | status |
|--------|--------|--------|
| `openbao_requests_total` | connection, method, code | done (target label: server) |
| `openbao_request_duration_seconds` | connection, method | done |
| `openbao_logins_total` | connection, result | done; flat at rest |
| `openbao_token_renewals_total` | connection, result | done |
| `openbao_writes_total` | kind | done; zero at rest |
| `openbao_drift_repairs_total`, `openbao_conflicts_total` | kind | done |
| `openbao_drift_lag_seconds` | connection | done |
| `openbao_resource_ready` | kind, namespace, name, reason | done; proves each resource is Ready, and why not |
| `openbao_write_budget_exhausted_total` | server | done; proves the write budget held writes back |
| `openbao_connection_ready` | kind, namespace, name | done; proves each connection can log in and serve clients |

Events on create, adopt, update (naming the keys that differed, never values), delete, conflict
and guarded delete. Logs never contain tokens, secret values or OpenBao response bodies.

## CI gates

The pull-request gate runs lint, unit tests, envtest, integration of every adapter against
`bao server -dev`, `verify-generated`, govulncheck, a kind e2e test with the chart, and the per-kind
no-write conformance test for the kinds the pull request touches (every kind when shared code changes). The full
no-write and default conformance tests run on pushes to `main`, weekly and on demand, with documented
exceptions. Remaining gaps: integration against every supported OpenBao minor and field-level API coverage.

## Delivery

- Images are multi-arch (amd64, arm64) and distroless, published to ghcr.io and tagged by version
  and digest.
- A `v*` tag triggers a GitHub release with the image, the Helm chart (OCI) and `install.yaml`,
  signed with keyless cosign and carrying an SBOM attestation and build provenance (forms and SLSA levels in
  [verify the release](security/hardening.md#verify-the-release)).
- Workflow actions are pinned by SHA; Renovate keeps those pins and Go modules current.
