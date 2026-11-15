# Roadmap

Where the project is going, by release. It is built from the [backlog](../backlog.md) (outcomes and acceptance
criteria), the [architecture roadmap](../architecture.md#roadmap) (phases and exit tests) and the
[tech-debt register](../tech-debt.md). Those pages stay the source of truth for each item; this page orders them.
Dates are not promised: a release ships when its exit criteria pass in CI.

Want to help? Items marked **help wanted** are good places to start; ask in the linked issue or in Discussions
before you begin a large one.

## Where we are

- 68 built-in kinds implemented and integration-tested against `bao server -dev`, and 52 plugin-gated kinds behind
  `--enable-plugin-kinds` ([kind catalogue](../kinds.md)).
- Engine v2: path-index ownership with a server-side marker, `Create`/`Orphan` defaults, the destructive-delete
  guard, drift tiers with a per-server request budget, observe mode with the `obc-import` CLI, the write circuit
  breaker and pause ([architecture](../architecture.md#operational-safety)).
- Evidence: [API coverage](../api-coverage.md) per OpenBao release (87.0% of configuration paths typed on 2.7.2),
  the nightly conformance matrix (no-write and default tests per kind), and a [load test](../load-testing.md) up to
  5,000 objects with zero writes at rest.
- The chart enables NetworkPolicy by default. Set explicit API server and OpenBao peers; empty peer
  lists leave egress unrestricted by destination on the configured ports
  ([hardening guide](../security/hardening.md#r-network-01-enable-the-networkpolicy)).
- Not released yet: no tag, image or chart has been published.

## v0.1.0: first public release

Goal: a release people can install, verify and build on without a forced rename later.

| item | source | status |
|------|--------|--------|
| choose the owned API group domain and rename once | [BL-001](../backlog.md#bl-001-pick-an-owned-api-group-domain-before-the-first-release), [ADR 0008](../adr/0008-provisional-api-group-domain.md) | decision pending |
| kubectl short names for every kind, unique against core resources | [BL-007](../backlog.md#bl-007-short-api-group-names-and-kubectl-short-names) | decision pending |
| choose the GitHub organisation; replace the placeholder module path and image names | release | open |
| phase 1 exit tests in CI: no-write and default tests for `Policy`, namespaced connection cannot reach the operator identity (e2e), self-init example boots with no root token left | [architecture](../architecture.md#roadmap), [BL-012](../backlog.md#bl-012-remaining-v2-rule-gaps) | conformance done; e2e and self-init CI gate open |
| release pipeline proven end to end (dry-run, then the real tag), Artifact Hub listing, docs site | [release process](release-process.md) | dry-run done locally |

## v0.2: tenant onboarding you can trust

Goal: one commit onboards a tenant namespace and a remote cluster, and a restore from Git rebuilds ownership.

| item | source | help wanted |
|------|--------|-------------|
| phase 2 exit test: one commit onboards a tenant namespace and a remote cluster, including dynamic tokens | [architecture](../architecture.md#roadmap) | |
| phase 3 exit test: wiping status then reconciling makes zero writes for marked kinds | [architecture](../architecture.md#roadmap) | |
| restore without status: ownership of memberships and access children rebuilt from the server-side marker | [BL-011](../backlog.md#bl-011-restore-without-status-chart-attestation) | |
| full field comparison for top-level keys the spec omits | [ADR 0011](../adr/0011-full-field-comparison.md) | |
| remaining typed kinds: `CertAuthCRL`, `TransitCacheConfig`, `KVSecretMetadata` | [BL-009](../backlog.md#bl-009-remaining-typed-kind-gaps) | yes |
| generic test that each kind's `Desired` covers its CRD schema; `MaxLength` on strings read by CEL | [BL-012](../backlog.md#bl-012-remaining-v2-rule-gaps) | yes |
| `DatabaseConnection` and LDAP rotated root credentials on `WriteOnce` instead of `CreateOnly` | [ADR 0017](../adr/0017-write-once-fields-for-rotated-root-credentials.md) | yes |

## v0.3: scale and operations

Goal: large shared servers with many tenants.

| item | source | help wanted |
|------|--------|-------------|
| phase 4 exit test: 10,000 objects hold a 1 h drift interval inside the request budget | [architecture](../architecture.md#roadmap), [load testing](../load-testing.md) | |
| per-namespace fairness for the write budget | [BL-016](../backlog.md#bl-016-per-namespace-fairness-for-the-write-budget) | |
| integration suite on every supported OpenBao minor, not only the pinned one | [architecture](../architecture.md#ci-gates), [BL-014](../backlog.md#bl-014-detect-openbao-api-changes-automatically) | yes |
| audit devices: an ADR on when the kind applies and how it reports a server that refuses API creation | [BL-003](../backlog.md#bl-003-audit-devices) | |
| drift reads on standby nodes | [architecture](../architecture.md#scale-and-drift) | |
| guide for adopting existing configuration: extend [migration.md](../operations/migration.md) with worked examples | [architecture](../architecture.md#operational-safety) | yes |
| tenant quota e2e: a `ResourceQuota` count blocks the next object | [BL-012](../backlog.md#bl-012-remaining-v2-rule-gaps) | yes |

## Later: plugins and the road to v1beta1

| item | source |
|------|--------|
| phase 5 plugin support: `Plugin` registers OCI plugins, mounts wait for it; LDAP plugin and OpenLDAP in CI | [architecture](../architecture.md#plugin-support), [BL-005](../backlog.md#bl-005-external-plugin-kinds-roadmap-phase-3b) |
| phase 6 plugin-backed kinds against real backends (LDAP, Kerberos, RADIUS first), each un-gated when its backend runs in CI | [architecture](../architecture.md#plugin-support), [TD-004](../tech-debt.md#td-004-unconfirmed-paths-and-external-plugin-kinds) |
| sharding by `OpenBaoServer` label for very large fleets | [architecture](../architecture.md#scale-and-drift) |
| namespace sealing support in `OpenBaoNamespace` | [architecture](../architecture.md#openbao-native-features) |
| `v1beta1`, when the conditions in the [release process](release-process.md#versioning) hold | [ADR 0020](../adr/0020-crd-versioning-without-webhooks.md) |

## Not planned

- Kinds for features OpenBao does not have (Sentinel, replication, KMIP, Transform)
  ([ADR 0009](../adr/0009-openbao-only-target.md)).
- Syncing secret values into Kubernetes Secrets: use External Secrets Operator ([ADR 0007](../adr/0007-no-secret-sync.md)).
- Server lifecycle (deploy, initialise, unseal, upgrade): use the OpenBao Helm chart or an operator for that.
- One-off actions as resources (login, issue, sign, rotate, tidy).
- Admission webhooks ([ADR 0005](../adr/0005-cel-validation-without-webhooks.md)).

## Changing the roadmap

Open a pull request against this page. New outcomes go into the [backlog](../backlog.md) first with acceptance
criteria; this page only orders them. Maintainers review the roadmap at each minor release.
