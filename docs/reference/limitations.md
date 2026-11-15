# Known limitations

Limitations of the current release that affect users, with the workaround where one exists. The
sources are the [tech-debt register](../project/tech-debt.md), the [backlog](../project/backlog.md),
the [ADRs](../adr/index.md) and [architecture v2](../architecture.md); each entry links to its source. An
entry leaves this page when its source is closed.

!!! warning "Pre-release API"
    All CRDs are `v1alpha1`, and the API group domain `openbaoconfig.io` is provisional
    ([ADR 0008](../adr/0008-provisional-api-group-domain.md),
    [BL-001](../project/backlog.md#bl-001-pick-an-owned-api-group-domain-before-the-first-release)).
    Kind names and groups can change before the first published release.

## Verification

| limitation | effect | source |
|------------|--------|--------|
| Integration tests use `bao server -dev`: in-memory storage, a root token, one node, no TLS | storage, HA, TLS and least-privilege behaviour of a production server are not exercised by the per-kind tests. The e2e test in kind adds a tenant login under a non-root policy for three kinds | [TD-001](../project/tech-debt.md#td-001-live-coverage-uses-openbao-dev-mode) |
| No test yet proves that a second tenant cannot write outside its policy | tenant isolation rests on the OpenBao policy of each connection identity, and that is not tested end to end | [TD-001](../project/tech-debt.md#td-001-live-coverage-uses-openbao-dev-mode) |
| Only OpenBao 2.7.2 runs in CI; 2.6 is covered by its OpenAPI snapshot | a behaviour difference on 2.6 is not caught | [BL-014](../project/backlog.md#bl-014-detect-openbao-api-changes-automatically), [compatibility](compatibility.md) |
| Field-level API coverage is not checked | a request field added or renamed upstream on a typed path is noticed only by hand; use `OpenBaoEndpoint` for it meanwhile | [TD-003](../project/tech-debt.md#td-003-no-openapi-field-coverage-check) |
| External-plugin kinds are not run against a server | they are verified by golden payload tests only, and paths marked `(u)` in the [kind catalogue](../kinds.md) come from documentation, not OpenBao source; they are disabled by default | [TD-004](../project/tech-debt.md#td-004-unconfirmed-paths-and-external-plugin-kinds) |
| The per-kind no-write and default conformance tests run on pushes to `main`, weekly and on demand, not on pull requests | a change that makes a kind rewrite on every pass can merge and is caught afterwards | [architecture](../architecture.md#ci-gates) |

## Drift detection and comparison

| limitation | effect | source |
|------------|--------|--------|
| Top-level keys the spec omits are not compared yet | removing an optional field from the spec does not always unset it in OpenBao; set the field to its default explicitly | [ADR 0011](../adr/0011-full-field-comparison.md), [architecture](../architecture.md#the-engine) |
| `OpenBaoEndpoint` compares only its `data` keys | a key removed from `data` is not unset on the server | [architecture](../architecture.md#the-engine) |
| Write-only fields cannot be compared | drift in a password or other value OpenBao does not return is invisible; the operator writes it again only when the input Secret or spec changes | [architecture](../architecture.md#security-model) |
| The read after a write is not pinned to the active node | on a cluster whose standbys serve reads, a read-back can return stale values and report `OutOfSync`, retried with backoff | [architecture](../architecture.md#reconcile-sequence) |
| Drift reads always go to the configured address | reading from standbys (horizontal read scaling) is planned | [architecture](../architecture.md#scale-and-drift) |
| The write budget is one window per `OpenBaoServer`, shared by every namespace | one namespace whose objects keep drifting can delay other namespaces' drift repairs on the same server | [BL-016](../project/backlog.md#bl-016-per-namespace-fairness-for-the-write-budget) |

## Ownership and restore

| limitation | effect | source |
|------------|--------|--------|
| Only entities, groups and namespaces carry a server-side ownership marker | after status loss, other kinds need a one-time `creationPolicy: Adopt` to be managed again | [ADR 0012](../adr/0012-ownership-path-index-and-marker.md) |
| `GroupMembership`, `AccessGrant` and `AccessLayout` keep the members and children they own only in status | a restore without status cannot remove members or children that the spec no longer lists; remove them by hand | [BL-011](../project/backlog.md#bl-011-restore-without-status-chart-attestation), [backup and restore](../operate/backup-restore.md) |
| Creates are serialised per process | with several replicas, leader election must stay on (the chart default) | [architecture](../architecture.md#scale-and-drift) |
| The hash key of `status.writeOnlyHash` cannot be rotated without rewriting every write-only field | if the hash-key Secret is deleted, the next manager start creates a new key and every object with write-only inputs is rewritten once | [architecture](../architecture.md#open-questions) |

## OpenBao behaviour

| limitation | effect | workaround | source |
|------------|--------|------------|--------|
| A standby that serves requests itself (the OpenBao 2.7 default) answers `failed to update audit table` (400) to an audit enable and does not forward it | `Audit` reports `ReconcileError` | point the `OpenBaoServer` address at the active node only, or set `disable_standby_reads = true` in the OpenBao server configuration | [troubleshooting](../operate/troubleshooting.md#common-openbao-errors) |
| A proxy or a standby without request forwarding answers with a redirect | the operator never follows redirects: resource `ReconcileError`, connection `AuthenticationFailed` | point the address at the active node or a service that forwards | [troubleshooting](../operate/troubleshooting.md#common-openbao-errors) |
| Nine sys kinds exist only in the root namespace | `Audit`, `AuditRequestHeader`, `RateLimitQuota`, `QuotaConfig`, `Plugin`, `CORSConfig`, `UIHeader`, `Logger` and `RaftAutopilot` in a child namespace stop with `RootOnlyKind` | use a connection based in the root namespace | [architecture](../architecture.md#namespace-scope) |
| `KeyRotationConfig` needs the root or a sealable namespace | `Unsupported` elsewhere | | [architecture](../architecture.md#namespace-scope) |
| A KV version 2 history purge cannot be conditional | a destructive delete of a `KVSecret` or `RandomSecret` that created its path can destroy a version another writer created in the instant between the operator's read and its delete | | [TD-008](../project/tech-debt.md#td-008-a-kv-history-purge-can-destroy-a-version-written-right-before-it) |

## Kinds and features

| limitation | effect | source |
|------------|--------|--------|
| External-plugin kinds are disabled by default | enable them with `pluginKinds.enabled`; each reports `Unsupported` until its plugin is registered and mounted | [kind catalogue](../kinds.md), [BL-005](../project/backlog.md#bl-005-external-plugin-kinds-roadmap-phase-3b) |
| No typed kind yet for `CertAuthCRL`, `TransitCacheConfig` and KV version 2 metadata | use `OpenBaoEndpoint` | [BL-009](../project/backlog.md#bl-009-remaining-typed-kind-gaps) |
| No kind for features OpenBao does not have (Sentinel, KMIP, Transform, managed keys, replication) or for one-off actions (a login, a PKI sign, creating a token) | out of scope | [architecture](../architecture.md#coverage-policy) |
| `DatabaseConnection.pluginConfig.connection_url` is not checked for a literal password | `connectionURL` refuses one; the same value in `pluginConfig` is accepted. | [BL-017](../project/backlog.md#bl-017-refuse-a-literal-password-in-databaseconnection-pluginconfigconnection_url) |
| Namespace sealing on `OpenBaoNamespace` is planned | not available | [architecture](../architecture.md#openbao-native-features) |
| Plugin support end to end (roadmap phase 5) is planned | registering a plugin and mounting it through `pluginRef` is not verified against a server with the plugin in CI | [architecture](../architecture.md#plugin-support) |

## Security and install

| limitation | effect | workaround | source |
|------------|--------|------------|--------|
| The default install can read Secrets and ConfigMaps and request ServiceAccount tokens cluster-wide | the tenant boundary relies on each connection's OpenBao identity, not on Kubernetes RBAC | set `watchNamespaces` for a Role per namespace | [TD-005](../project/tech-debt.md#td-005-the-default-install-can-read-secrets-cluster-wide) |
| One-time outputs (secret IDs, client secrets, generated keys) live in Kubernetes Secrets | etcd encryption and RBAC protect them, not OpenBao | | [architecture](../architecture.md#security-model) |
| The NetworkPolicy is on by default but leaves egress open by destination while `apiServer.cidrs` and `openbao.to` are empty | the operator can reach any host on the API server and OpenBao ports | name the peers | [hardening](../security/hardening.md#r-network-01-enable-the-networkpolicy) |
| The documentation site is built with Material for MkDocs, which reaches end of life on 2027-05-05 | no user effect today | | [TD-006](../project/tech-debt.md#td-006-the-docs-site-depends-on-material-for-mkdocs-which-reaches-end-of-life) |
