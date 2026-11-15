# Architecture v2 (OpenBao-only)

openbao-config-operator keeps the configuration of an existing OpenBao server in the state that
Kubernetes custom resources declare. One custom resource is one object at one API path, such as a
policy, an auth role, a mount or an entity. The operator does not deploy, initialise, unseal or
upgrade the server.

v2 targets OpenBao only. It replaces the v1 design (OpenBao plus Vault where the APIs match); the
reasons are in [ADR 0009](adr/0009-openbao-only-target.md) and the [changes table](#changes-from-v1).

Status markers in this document: **in progress** means the target is being implemented and the
code does not have it yet; **planned** means a later [roadmap](#roadmap) phase. Everything else
is implemented.

## Scope

- In scope: the common configuration objects (sys, auth, secrets engines, identity, OIDC), one
  raw `OpenBaoEndpoint` kind for every other path, and multiple tenants on one server.
- OpenBao only, minimum 2.6, current 2.7.2. No flavour detection; features OpenBao does not have
  get no kind.
- Out of scope: server lifecycle (OpenBao Helm chart, KubeBao operator, self-init), syncing secret
  values into Kubernetes Secrets (External Secrets Operator, [ADR 0007](adr/0007-no-secret-sync.md)),
  and one-off actions such as a login, a PKI sign or creating a token.

## Changes from v1

| area | v1 | v2 | status |
|------|----|----|--------|
| target | OpenBao + Vault where APIs match | OpenBao only ([ADR 0009](adr/0009-openbao-only-target.md)) | docs and catalogue done; `status.flavor` still reported |
| field comparison | only keys the spec sets | every typed field, omitted ones at their declared default ([ADR 0011](adr/0011-full-field-comparison.md)) | in progress (nested maps compared exactly, `Partial` opts out; top-level keys still desired-only) |
| re-login | on any 401/403 | on 401, or when lookup-self fails after a 403 | done |
| creationPolicy default | `CreateOrAdopt` | `Create` ([ADR 0015](adr/0015-create-and-orphan-by-default.md)) | done |
| ownership | `status.acquired` | status + path index + server-side marker ([ADR 0012](adr/0012-ownership-path-index-and-marker.md)) | done (marker on entities, groups, namespaces) |
| address | tenant sets it on the connection | platform sets it on cluster-scoped `OpenBaoServer` ([ADR 0010](adr/0010-openbaoserver-owns-address-and-audiences.md)) | done |
| token audiences | required | required and listed in the server's `allowedAudiences` | done |
| write-only tracking | plain hash | HMAC-SHA256 with an operator-held key | done |
| `writeSecretRef` | writes and owns the Secret | refuses a Secret it does not own | done |
| destructive deletes | guard on human access only | guard on mounts, namespaces, KV data and audit too ([ADR 0013](adr/0013-destructive-delete-guard.md)) | done |
| drift | one interval + jitter | two tiers + per-server request budget ([ADR 0014](adr/0014-drift-tiers-and-request-budget.md)) | done |
| catalogue | union incl. cloud methods | built-ins first; plugin kinds gated until plugin support; the rest via `OpenBaoEndpoint` | done (`--enable-plugin-kinds`) |

## Design rules

The engine enforces these rules; an adapter cannot break them.

1. **Steady-state reconcile reads and does not write.** `openbao_writes_total` is zero at rest.
   Proven on a live OpenBao 2.7.2 cluster (81 objects of 55 kinds, zero rewrites across drift
   intervals and a manager restart). The per-kind no-write CI test is in progress.
2. **One cached client per connection.** The operator logs in once and renews. Logging in on every
   reconcile would grow entities, aliases and leases without bound; `openbao_logins_total` makes
   this visible. A new login only after a 401 or a failed lookup-self (see
   [client cache](#client-cache)).
3. **The spec is the whole desired state.** Every typed field is compared, including omitted ones
   at their declared default, so removing a field from the spec unsets it. In progress: nested maps
   are compared exactly; top-level keys the spec omits are not compared yet.
4. **One path has one owner.** A second CR on the same server, namespace and path gets `Conflict`
   and never writes.
5. **Create by default, adopt on request.** `creationPolicy` defaults to `Create`.
6. **Retain by default.** `deletionPolicy` defaults to `Orphan`; `Delete` is opt-in and only then
   is a finalizer added. Destructive kinds need a second explicit guard (mounts, namespaces, KV data,
   audit and `AccessLayout` human access).
7. **Secrets only by reference.** Secret material comes from `SecretKeySelector` references. Nothing
   secret appears in a spec, status, event or log.
8. **A tenant acts only as itself.** A namespaced connection never uses the operator's identity,
   files or network position. Done: the address comes only from the platform's `OpenBaoServer`.
9. **No root token at runtime.** OpenBao self-init creates the operator's auth role and policy at
   first boot and revokes the transient root token. [operations](operations/connections.md) has a
   tested example.
10. **`Ready` and `Synced` conditions with typed reasons and `observedGeneration` on every kind**,
    so Argo CD and Flux health checks and sync waves work. [operations](operations/) ships an Argo
    CD health check.

Typed kinds give validation, references and accurate status; `OpenBaoEndpoint` covers any path without a
typed kind ([ADR 0006](adr/0006-endpoint-escape-hatch.md)).

## API groups

All groups are version `v1alpha1`. All kinds except `OpenBaoServer` and
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

The `auth` and `secrets` groups also hold external-plugin kinds (LDAP, Kerberos, RADIUS, the cloud
methods and engines, and others). They stay in the code but are disabled by default behind
`--enable-plugin-kinds` (chart `pluginKinds.enabled`) until [plugin support](#plugin-support)
works end to end. [kinds.md](kinds.md) lists every kind with its status.

Kinds are not prefixed. The exceptions are the connection kinds, `OpenBaoServer`,
`OpenBaoNamespace` and `OpenBaoEndpoint`, whose bare names would collide with core Kubernetes kinds. The API group
domain is provisional ([ADR 0008](adr/0008-provisional-api-group-domain.md)).

### Coverage policy

- **Any path:** `OpenBaoEndpoint` reaches every OpenBao API.
- **Built-in kinds first:** every object compiled into OpenBao gets a typed kind, tested against
  `bao server -dev` and listed in [kinds.md](kinds.md).
- **Plugin kinds after plugin support:** kinds for external plugins are gated until a plugin can be
  registered and tested in CI (phase 5); phase 6 then enables them one backend at a time. Until
  the plugin is mounted, a plugin kind reports `Unsupported` and re-checks at the drift interval.
- **Not typed:** features OpenBao does not have (Sentinel, KMIP, Transform, managed keys,
  replication) and one-off actions. Values OpenBao generates once, such as a secret-id or a client secret, go to
  a Kubernetes Secret through `writeSecretRef`.
- **Root only:** nine sys kinds exist only in the root namespace and are refused elsewhere with
  `RootOnlyKind` (see [namespace scope](#namespace-scope)).
- **No generated types:** CRD types are written by hand with an explicit `Desired` per kind. The
  OpenBao OpenAPI spec has no enums, almost no `required` fields, no write-only or create-only
  markers, and duplicate operation IDs that overwrite schemas (`JwtWriteRoleRequest` holds the CEL
  role fields). A pinned snapshot is still used in CI to check field coverage.

## Components

```text
cmd/main.go, cmd/controllers.go  manager wiring, flags, metrics, health; table of kind Setup funcs
api/common/v1alpha1              shared spec/status types (connectionRef, policies, conditions, secret refs)
api/<group>/v1alpha1             CRD types per group: schema only, no client code
internal/openbao                 client construction, auth, token renewal, per-connection pool, rate limit, errors
internal/connection              resolves a connectionRef into a client (Secrets, files, TokenRequest, allowedNamespaces)
internal/engine                  generic reconciler, Adapter contract, compare, HMAC key, Secret and reference indexes
internal/refs                    flexible references between kinds
internal/controller/<group>      one small Adapter per kind, plus the connection controller
test/integration                 adapters against envtest + a real `bao server -dev`
test/e2e                         kind + Helm chart + OpenBao
charts/openbao-config-operator   Helm chart (CRDs as templates)
```

The API packages import nothing from `internal/`, so other projects can import the types.

## Connections and the tenant boundary

| kind | scope | owner | holds | status |
|------|-------|-------|-------|--------|
| `OpenBaoServer` | cluster | platform | `address`, CA bundle, `allowedNamespaces`, `allowedAudiences`, `rateLimit`, `requestTimeout` | done |
| `OpenBaoConnection` | namespaced | tenant | `serverRef`, `namespace`, `authNamespace`, `auth` | done |
| `ClusterOpenBaoConnection` | cluster | platform | same, plus `allowedNamespaces`; may use the operator's own identity | done |

Resources reference a connection with `spec.connectionRef: {kind, name}`; `kind` defaults to
`OpenBaoConnection`, which must be in the same namespace. Both fields are immutable (CEL).

Connection fields (`address`, CA bundle, timeout and rate limit are on the `OpenBaoServer`):

| field | meaning |
|-------|---------|
| `serverRef.name` | the `OpenBaoServer`; for an `OpenBaoConnection` its `allowedNamespaces` must select the namespace |
| `namespace` | base OpenBao namespace for every request through this connection |
| `authNamespace` | namespace of the auth mount used to log in; unset means `namespace`, `""` means root |
| `auth.token` | `secretRef`; cluster kind only: or `filesystem.path` |
| `auth.kubernetes`, `auth.jwt` | `mountPath`, `role`, `serviceAccountRef`, `audiences`; cluster kind only: or `injectedIdentity` |
| `auth.appRole` | `mountPath`, `roleIDSecretRef`, `secretIDSecretRef` |

Credential sources, read at each login so rotation needs no restart:

| source | where | kinds |
|--------|-------|-------|
| Secret | `auth.token.secretRef`, `auth.appRole.*SecretRef` | both |
| TokenRequest | `serviceAccountRef` + `audiences`: a short-lived token for a named ServiceAccount | both; namespaced: same namespace, `audiences` required |
| injected identity | `injectedIdentity.tokenPath`: the operator pod's own ServiceAccount token | cluster only |
| filesystem | `auth.token.filesystem.path`: a token file in the operator pod, such as an agent sink | cluster only |

`injectedIdentity.tokenPath` defaults to `/var/run/secrets/openbao/token`, where the chart's
`injectedIdentity.projectedToken` volume (on by default) mounts a token for the audience
`openbao-config-operator`. The automounted token, whose audience is the API server, is refused by
CEL and by the resolver. CEL allows at most one of `serviceAccountRef` and `injectedIdentity`,
exactly one of `secretRef` and `filesystem`, and `audiences` only with, and required by,
`serviceAccountRef`. A cluster `kubernetes` or `jwt` block with neither keeps
its v1 meaning: injected identity with the default path. A missing file sets `Ready=False` with
reason `CredentialsNotFound`.

What a tenant cannot do:

- **choose the address:** v2 has no address field on connections; `serverRef` must name an
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

`ClusterOpenBaoConnection` may use the operator's identity and files because only platform admins
create it; that right is as strong as the operator's own OpenBao identity.

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
against `bao server -dev` and writes `.tmp/night/ns-matrix.md`.

### Client cache

`internal/openbao` keeps one authenticated client per connection, keyed by connection UID,
connection generation, server generation and a hash of the credential inputs. Each client:

- renews a renewable token once a third of its TTL has passed and logs in again before a
  non-renewable token expires;
- on a 401 logs in once and retries the request;
- on a 403 today logs in again at most once per 5 s; target: call lookup-self, log in again only if
  that fails, otherwise report `PermissionDenied` without a login;
- retries 429 and 5xx with backoff, inside one token bucket per server shared by every connection
  to it (`OpenBaoServer.spec.rateLimit`); every HTTP attempt takes a token, retries, logins and token
  renewals included; drift reads go without priority, so spec changes go first;
- serves another OpenBao namespace from a clone (`WithNamespace`) without changing the shared
  client;
- shares one login between concurrent cache misses; the login runs detached from any single
  caller's context (with its own timeout), and each caller stops waiting when its own context ends;
- revokes the token it logged in for, best effort and in the background, when the connection is
  deleted, denied or its credentials change; a static token (`auth.token`) is never revoked.

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
| `driftInterval` | tier default | per-object drift interval |
| `managementPolicy` | `Manage` | `Observe`: read and report drift, never write or delete (see [operational safety](#operational-safety)) |

Kinds whose endpoint has no delete reject `Delete` with CEL; for singletons, `Delete` resets the
setting to its default. Config paths that always read back with defaults (for example
`CertAuthConfig`, `KVConfig`, `PKIConfig`, `OIDCConfig`) can never be created, so use `Adopt` or
`CreateOrAdopt` for them.

### Common status

| field | meaning |
|-------|---------|
| `conditions` | `Ready` (the object matches the spec) and `Synced` (the last reconcile succeeded), each with a typed reason and `observedGeneration` |
| `observedGeneration` | generation of the last reconcile |
| `path` | full OpenBao path including the namespace, so deletion works after the spec changes |
| `namespace` | absolute OpenBao namespace the object was written in; a change stalls with `NamespaceChanged` |
| `serverID` | cluster id (from `sys/health`) of the OpenBao server the object was claimed or acquired on; a change stalls with `ServerChanged` |
| `acquired` | `Created` or `Adopted`; set once the object is ours |
| `writeOnlyHash` | HMAC-SHA256 of the write-only inputs under a key derived from the operator's hash key and the resource's UID, group and kind, so equal inputs of two resources hash differently; a change forces a write (an operator built before the per-resource key rewrites each resource with write-only inputs once) |
| `id` | server-generated ID (entity, group, alias, accessor) |

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

`DefaultDeletionPolicy` is still in the interface but ignored and deprecated. v2 adds
`Destructive()` (done) and the full-payload contract for `Desired` (every typed field, defaults
filled; in progress).

Optional interfaces cover the kinds that do not fit "write a payload to a path": `Observer`
(custom read, such as lookup by name or list and filter), `Writer` (custom write, such as enable
then tune a mount), `Deleter`, `StatusWriter` (copy IDs and accessors to status), `OutputWriter`
(values returned once, to an owned Secret), `PathResolver` (path depends on a referenced object),
`FieldsProvider` (per-object comparison rules) and `ReferenceWatcher` (re-queue when a referent
changes). v2 adds `Marker`, which writes and reads the ownership marker where the API has
metadata (done), and `DriftTier`, `Singleton`, `SharedPath`, `OutputSecretNamer` and
`ReadBackSkipper`. A `Writer` that finds nothing left to write (the server caught up between the
engine's read and the write) returns `engine.ErrNothingWritten`: the engine then counts no write in
`openbao_writes_total`, records no `Updated` event and gives the write budget slot back.

Field classes ([ADR 0011](adr/0011-full-field-comparison.md)):

| class | compared | sent | today |
|-------|----------|------|-------|
| normal | after normalising (durations in seconds, numbers numerically) | yes | yes |
| set | order ignored | yes | `Sets` |
| alias | read name mapped to write name, for example `token_period` to `period` | yes | `Aliases` |
| write-only | no; HMAC into `writeOnlyHash` | yes, every write, because several config endpoints replace the whole object | `WriteOnly` |
| write-once | no | on create, and again only after a rotation nonce changes ([ADR 0017](adr/0017-write-once-fields-for-rotated-root-credentials.md)) | `WriteOnce` + `RotationNonce` |
| create-only | no | on create only | `CreateOnly` |
| server-owned | no | no (accessor, `creation_time`) | `ServerOwned` |

Today `Diff` compares only the keys `Desired` returns. Each adapter declaring the server default of
every optional field, and an integration test per kind that writes an empty spec and asserts that
a second reconcile makes no write, are in progress. `OpenBaoEndpoint` compares only its data keys; a key
removed from `data` is not unset on the server.

### Ownership

In order ([ADR 0012](adr/0012-ownership-path-index-and-marker.md)):

1. server-side marker (managed-by + cluster id + CR UID) where the object has metadata: entities,
   groups, namespaces (done); KV v2 metadata (planned);
2. status: `acquired` set and `status.path` matching (done);
3. a field index on server + absolute namespace + path finds duplicates; the older CR keeps the
   path, the newer gets `Conflict` and never writes (done).

An existing object that passes neither check: `Create` reports `Conflict`; `Adopt` and
`CreateOrAdopt` take it over. After status loss, kinds without a marker need a one-time `Adopt`.
The children that `AccessGrant` and `AccessLayout` created are in the path index too, and a planned child that
another resource owns is never written, whatever the `creationPolicy`: before acquisition the resource reports
`Conflict`, afterwards a reconcile error that keeps its own claim.

Before a create the engine records the path and server in the status (a claim) and the path index
shows it to every other resource; within the operator, creates of one path on one server run one at
a time and read the object again first, so of two `Create` resources only the first creates it and
the second gets `Conflict`. When that second read finds the object, the claims are read again from
the API server rather than the cache, so a `CreateOrAdopt` resource never adopts what an older
resource has just created; an object found on the first read is checked the same way before a resource that does
not hold it yet adopts it. That live read lists the resource's own kind and only the other kinds
whose cached path index shows a claim, page by page, and only in the watched namespaces
(`--watch-namespaces`), so a create costs one paginated LIST per watched namespace, not one per kind. A create that wrote nothing leaves no claim unless the object exists
afterwards, so a failed create never lets the resource take over an object somebody else creates
later. Once a create succeeded, `acquired: Created` is recorded at once, before outputs are
delivered, so a later error in the same pass (an undelivered one-time output, a create that takes
several steps) leaves the object owned and deletable. Kinds created by a POST to a collection
(aliases, MFA methods) make no claim.

Acquisition is bound to the server: `status.serverID` holds the server's cluster id, which
`sys/health` reports without a token and which survives restarts. Each client caches it for one
minute and reads it again, past the cache, right before a create, a recreate and a delete, so a
different server behind the same address (a DNS or load balancer change, a re-initialised server)
gets none of them. If a connection or `OpenBaoServer` change makes the resource reach another
cluster, reconciling stops with `ServerChanged` and nothing is written or deleted there. This holds
under `managementPolicy: Observe` too: Observe never rebinds a recorded `serverID` while the
resource holds a path, so switching to Observe and back cannot take over the other server's
object. To move a resource to the new server on
purpose, release it: clear its status (`kubectl patch <kind> <name> --subresource=status --type=merge
-p '{"status":null}'`); the next reconcile treats the new server like a first apply, so set
`creationPolicy: Adopt` or `CreateOrAdopt` if the object already exists there. A resource acquired
before `serverID` existed records the server it reaches on its next reconcile.

### Reconcile sequence

1. Resolve the connection. Not `Ready`: `Synced=False` with reason `ConnectionNotReady`, requeue.
   Not allowed from this namespace: `ConnectionDenied`.
2. Path index: duplicate means `Conflict`, stop.
3. Add the finalizer if `deletionPolicy=Delete`.
4. Read. A 404 means absent; a 404 "namespace not found" and a missing referent stall with
   `ReferenceNotReady` and a short growing retry; 403 is `PermissionDenied`; anything
   else is an error. A 400 is never treated as absent.
5. Absent: create. Present and not ours: apply `creationPolicy`.
6. Compare by field class plus the HMAC; write only on a difference. After a write, read back and
   confirm; a mismatch gives `OutOfSync` and a backoff retry (pinning the read to the active node
   is in progress).
7. Write the status only if it changed. Requeue at the drift interval with ±20% jitter.

Adapter errors map to reasons: `ErrInvalidSpec` is terminal (`InvalidSpec`, no retry until the
spec or a Secret changes), `ErrUnsupported` retries at the drift interval, `ErrInProgress` requeues
without backoff. Write errors redact any write-only value OpenBao echoes.

### Deletion

- `Orphan` (default): no finalizer, nothing removed.
- `Delete`: finalizer; the engine deletes `status.path`. A 404 counts as success, and a child whose
  parent mount or namespace is gone counts as deleted. If the connection or a credential is gone,
  the finalizer stays with `Synced=False` reason `CleanupBlocked` and the engine retries; the
  object is never silently orphaned.
- Before deleting, the engine reads the cluster id again, past the client's cache, to check that the
  connection still reaches the server in `status.serverID` and, for kinds with an ownership marker, re-reads the object: another server
  (`ServerChanged`) or a marker naming another cluster or resource (`Conflict`) leaves the object in
  OpenBao, removes the finalizer and records a Warning event. The same applies when an older
  resource owns the path.
- Destructive kinds (`SecretsMount`, `AuthMount`, `OpenBaoNamespace`, `KVSecret`, `Audit`,
  `OpenBaoEndpoint`) delete only with the annotation
  `openbaoconfig.io/allow-destructive-delete: "true"`; otherwise the finalizer holds with reason
  `DeleteGuarded`. The same annotation gates an update that would
  recreate a mount ([ADR 0013](adr/0013-destructive-delete-guard.md)).
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
  | `PKIKey`, `PKIIssuer` (exported), `TOTPKey` (generated) | no | `Ready=False`, reason `OutputNotDelivered`, naming the Secret and how to rotate: delete the object in OpenBao and the resource generates a new one, or set `deletionPolicy: Delete`, delete the resource (which deletes the object) and create it again |

  The last row checks on each read that the owned Secret holds the value; PKI outputs carry
  `key_id`, so a Secret left from an earlier key does not count. A generated `TOTPKey` stores an HMAC
  fingerprint of its URL, keyed with the engine hash key, in `status.id` and in the output Secret's
  `fingerprint` key, so a Secret left over after the key was regenerated out of band is detected as not
  delivered ([BL-015](backlog.md#bl-015-totp-output-delivery-after-an-out-of-band-key-rotation)).
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

A typed kind adds validation, references and drift behaviour on top.

## Access models

The `access` group is built on the primitives and writes policies, roles, groups and aliases
through the same engine.

- `AccessLevel` is a named set of capabilities on data and on metadata, cited by grants and
  layouts.
- `AccessGrant` is the machine side. ServiceAccount subjects get one Kubernetes auth role each and
  one shared policy, rendered from rules of the form `mount + path + level`. Paths can use
  `{sa.namespace}` and `{sa.name}`, which OpenBao identity templating renders. CEL requires a broad
  subject (a glob or a selector) to scope every path with `{sa.namespace}`. See
  [templated-policies.md](templated-policies.md).
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

| threat | control | status |
|--------|---------|--------|
| tenant makes the operator call any URL | only the platform sets the address, on `OpenBaoServer` | done |
| tenant borrows the operator identity | no injected or filesystem sources on namespaced connections; resolver backstop | done |
| API server token minted for a tenant SA | audiences required and in `allowedAudiences`; the API server audience always rejected | done |
| another namespace's Secret read | every `*SecretRef` is same-namespace | done |
| unrelated Secret overwritten via `writeSecretRef` | only absent or CR-owned Secrets are written | done |
| weak write-only secret guessed from status | HMAC-SHA256 keyed by the operator | done |
| two tenants fight over one path | path index | done |
| CR deleted from Git removes data | `Orphan` default + destructive guard | done |
| leaks | events name keys only; logs carry no tokens or bodies | done |
| root token left behind | self-init creates the role and revokes root | done (example) |

The HMAC key is the immutable Secret `openbao-config-operator-hash-key` (flag
`--hash-key-secret`) in the operator namespace (`POD_NAMESPACE`), created with 32 random bytes on
first start.

Operator permissions: Secrets get/list/watch cluster-wide (references; the cache keeps metadata
only), Secrets create/update (outputs and the HMAC key), `serviceaccounts/token` create
(TokenRequest). In OpenBao: its own role, used only by `ClusterOpenBaoConnection`. A
namespace-scoped install mode (`--watch-namespaces`) exists. Residual risks: one-time outputs live
in Kubernetes Secrets (etcd encryption and RBAC apply), and drift in write-only fields is
invisible. Details in [security.md](security.md).

## Scale and drift

Reads per second = objects / interval ([ADR 0014](adr/0014-drift-tiers-and-request-budget.md)).
Every interval has ±20% jitter. Done:

- two tiers, both operator flags: access-critical every 15m (`Policy`, `AuthMount`, every kind of
  the `auth` group, `Audit`, `Group`, `GroupMembership`, access groups) and standard every 1h
  (everything else); `spec.driftDetection: Disabled` and `spec.driftInterval` per object;
- `OpenBaoServer.spec.rateLimit`: one token bucket shared by every connection to that server; drift
  reads wait, spec-change reconciles go first; `openbao_drift_lag_seconds` shows the lag.

Planned: drift reads on standbys, with the read after the operator's own write on the active node.

`--max-concurrent-reconciles` (default 4) applies per kind; watches are field-indexed; one active
replica with leader election, on by default because creates are serialised only within one process
(`--leader-elect=false` for a single replica without the lease RBAC; sharding by `OpenBaoServer`
label later).

## OpenBao-native features

- self-init (2.4): the operator's mount, role and policy at first boot, transient root revoked;
- namespaces: the tenant boundary; `OpenBaoNamespace` handles the asynchronous delete;
- horizontal read scaling (2.5): drift reads on standbys (eventually consistent; planned);
- OCI plugins (2.5): `Plugin` registers what the server configuration downloads;
- namespace sealing (2.6): `OpenBaoNamespace` may request a Shamir seal, with shares through
  `writeSecretRef` (planned);
- 2.6: `sys/health` dropped `performance_standby` and `last_wal`; self-init gained CEL, templates
  and `when`, and fails on unknown keys; `sys/workflows` (`Workflow` kind); built-in LDAP, Kerberos
  and RADIUS deprecated in favour of openbao-plugins;
- OpenBao-only kinds: `JWTCELRole`, `Workflow`.

## Operational safety

Items 1 to 3 are phase 1 engine work.

| # | addition | status |
|---|----------|--------|
| 1 | **Observe mode and import** ([ADR 0016](adr/0016-observe-mode-and-import-tool.md)). `spec.managementPolicy: Manage` (default) or `Observe`. `Observe` reads and reports drift (`Synced` reason `Drifted` with the differing keys, or `Observed` when equal) and never writes or deletes; no finalizer. An import CLI (`cmd/obc-import`) reads a live server and emits CR YAML with `managementPolicy: Observe` and `creationPolicy: Adopt`, so a running OpenBao is adopted incrementally. | done |
| 2 | **Write-once fields for rotated root credentials** ([ADR 0017](adr/0017-write-once-fields-for-rotated-root-credentials.md)). Sent on create and when a rotation nonce changes, never re-sent otherwise, so a stale Secret does not undo a rotate-root inside OpenBao. For `DatabaseConnection` `password`/`private_key` and LDAP `bindpass` with rotation. | engine done (`FieldSpec.WriteOnce`); kinds still use `CreateOnly` |
| 3 | **Write circuit breaker** ([ADR 0018](adr/0018-write-circuit-breaker.md)). Rewrites of objects the operator already owns, with an unchanged spec and unchanged input Secrets, are capped per `OpenBaoServer` (operator flag, default 60 per minute). Creates, adoptions, spec changes and changed input Secrets (a new `status.writeOnlyHash`) are user-intended and exempt, so onboarding and Secret rotation are not throttled; a regenerated one-time output (a new secret ID while the last one is undelivered) is not. At the cap the engine holds further rewrites to that server, sets `Synced=False` with reason `WriteBudgetExceeded` (a stable message, so retries do not patch status) and an event, and spreads the retries over the next minute. | done (`--write-budget-per-minute`) |
| 4 | **Pause** ([ADR 0019](adr/0019-pause-annotation.md)). `openbaoconfig.io/paused: "true"` on an object, its Kubernetes Namespace or its `OpenBaoServer` stops reconciling (no reads, writes or deletes; a deletion keeps the finalizer) with reason `Paused`. | done |
| 5 | **CRD versioning without webhooks** ([ADR 0020](adr/0020-crd-versioning-without-webhooks.md)). Version bumps stay schema-compatible (additive; storage version migration with kube-storage-version-migrator). A conversion webhook only if a breaking change is unavoidable. | decided |
| 6 | **One writer cluster per OpenBao namespace** ([ADR 0021](adr/0021-one-writer-cluster-per-namespace.md)). The server-side marker carries a cluster id (`--cluster-id`) and the CR UID; a marker from another cluster gives `Conflict`. | done (`--cluster-id`) |
| 7 | **Audit attribution** ([ADR 0022](adr/0022-audit-attribution-header.md)). Every request carries `X-Openbao-Config-Resource: <group>/<kind>/<k8s-namespace>/<name>`; an `AuditRequestHeader` example makes OpenBao log it, so each audit entry traces to a Git object. | done (`examples/sys/auditrequestheader-attribution.yaml`) |
| 8 | **Tenant object limits.** Kubernetes `ResourceQuota` object counts (`count/policies.sys.openbaoconfig.io` and so on) stop one tenant from exhausting the drift budget; see [tenant-quotas.md](operations/tenant-quotas.md). | documented |

Smaller items: a guide for adopting existing configuration
([operations/migration.md](operations/migration.md)), and optional `policyRefs` on auth roles so a role waits for its `Policy` CRs to be `Ready`
(planned).

## Plugin support

Phase 5 (planned). The server configuration downloads the binary; that is lifecycle, outside the
operator. `Plugin` owns the catalog entry (name, type, version, sha256; adopts an auto-registered
one) and reports the version in status. `AuthMount` and `SecretsMount` take `pluginRef` and wait
for the `Plugin` to be `Ready`. A missing plugin gives `Ready=False` with reason
`PluginNotRegistered`, re-checked at the drift interval without a loop. A version change on
`Plugin` re-registers and reloads; mounts are not recreated.

Phase 6 enables plugin-backed kinds, each with its backend container in CI: `LDAPAuthConfig`,
`LDAPAuthGroup`, `KerberosAuthConfig`, `KerberosAuthGroup`, `RADIUSAuthConfig`,
`RADIUSAuthUser`, `LDAPSecretsConfig`, `LDAPSecretsStaticRole`, `LDAPSecretsDynamicRole`. The
other plugin kinds in the code (cloud, GitHub, Okta, OCI, CF, Consul, Nomad, Terraform Cloud,
MongoDB Atlas, Quay) stay gated until each has a plugin and backend in CI. `RabbitMQConfig` and
`RabbitMQRole` are built into 2.7.2 and integration-tested; the v2 spec lists them under phase 6
pending the open question below.

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

Lint, unit tests, envtest, integration of every adapter against `bao server -dev`, a
`verify-generated` job, govulncheck and a kind e2e test with the chart. In progress: run the
integration suite on each pinned OpenBao minor; the no-write test (second reconcile makes zero
writes, every kind); the default test (empty spec, then each optional field set and removed).

## Delivery

- Images are multi-arch (amd64, arm64) and distroless, published to ghcr.io and tagged by version
  and digest.
- A `v*` tag triggers a GitHub release with the image, the Helm chart (OCI) and `install.yaml`,
  signed with keyless cosign and carrying an SBOM attestation and build provenance (GitHub artifact
  attestations: SLSA Build L2; cosign `slsaprovenance1` statement written by the release job: no SLSA level).
- Workflow actions are pinned by SHA; Renovate keeps those pins and Go modules current.

## Roadmap

A phase ends when its exit test passes in CI.

1. Engine and tenant boundary: `OpenBaoServer`, connections, engine field classes, ownership and
   path index, observe mode, write-once fields, write circuit breaker, `Policy`, `OpenBaoEndpoint`, chart,
   CI. Exit: no-write and default tests for `Policy`; a
   namespaced connection cannot reach the operator identity (e2e); the self-init example boots
   with no root token left. In progress.
2. Tenant and cluster onboarding: `OpenBaoNamespace`, mounts, Kubernetes, JWT and AppRole auth,
   `TokenRole`, `KubernetesSecrets*`. Exit: one commit onboards a tenant namespace and a remote
   cluster, including dynamic tokens. Kinds implemented; exit test planned.
3. Identity with marker. Exit: wiping status then reconciling makes zero writes for marked kinds.
   Kinds implemented; marker done; exit test planned.
4. Secrets engines, the rest of sys, OIDC and MFA. Exit: 10k objects hold a 1h drift interval
   inside the budget. Kinds implemented; scale test planned.
5. Plugin support (LDAP plugin from OCI and OpenLDAP in CI). Planned.
6. Plugin-backed kinds against real backends. Kinds in code, gated.
7. Access models, `CertAuthRole`, `UserpassUser`. Kinds implemented and integration-tested.

Phases describe exit criteria, not build order: the kinds of phases 2, 3, 4 and 7 were built
before the v2 engine and are retrofitted to its rules as the engine lands.

## Open questions

- LDAP, Kerberos and RADIUS still built in on 2.7? RabbitMQ still built in (it is on 2.7.2)?
- First OpenBao release with namespaces; sealed-namespace API (create parameters, key share
  return)?
- Which kinds have metadata for the marker (entities, groups, namespaces, KV v2: test each)?
- How to force a read to the active node after a write?
- HMAC key rotation without rewriting every write-only field?
- Namespace-scoped install mode as the chart default?
