---
description: Track released OpenBao Config Operator capabilities, outstanding test gates and planned work without promised release dates.
---

# Roadmap

Where the project is going, by release. This page is the only place that orders work and names phases; the
[backlog](../project/backlog.md) holds the outcomes and acceptance criteria, the
[tech-debt register](../project/tech-debt.md) the known gaps, and the [architecture](../architecture.md) the design.
Dates are not promised.

Want to help? Items marked **help wanted** are good places to start; ask in the linked issue or in Discussions
before you begin a large one.

## Where we are

- 1.0.0 is the first release: a multi-arch image, the Helm chart, `install.yaml`, `crds.yaml` and an examples
  bundle, signed with keyless cosign, and the chart also with a GPG Helm provenance file, from the
  [GitHub releases](https://github.com/alexandrust88/openbao-config-operator/releases). The earlier pre-releases
  (0.1.x to 0.5.0) were withdrawn. The [changelog](../../CHANGELOG.md) lists the changes of each release.
- The API groups are `<area>.obco.dev/v1alpha1`, under a domain the project owns
  ([ADR 0025](../adr/0025-move-the-api-to-obco-dev.md)).
- 74 built-in kinds implemented and integration-tested against `bao server -dev`, and 52 experimental plugin
  kinds behind `--enable-plugin-kinds` ([kind catalogue](../kinds.md)).
- Engine: path-index ownership with a server-side marker, `Create`/`Orphan` defaults, the destructive-delete
  guard, drift tiers with a per-server request budget, observe mode with the `obc-import` CLI, the write circuit
  breaker and pause ([architecture](../architecture.md#operational-safety)).
- Evidence: [API coverage](../api-coverage.md) per OpenBao release (92.2% of configuration paths typed on 2.7.2,
  built-in kinds only), the conformance run (no-write and default tests per kind: on pull requests for the kinds a
  change touches, every kind weekly and on `main`), the two-tenant isolation e2e test, and a
  [load test](../operate/load-testing.md) up to 5,000 objects with zero writes at rest.
- The chart enables NetworkPolicy by default. Set explicit API server and OpenBao peers; empty peer
  lists leave egress unrestricted by destination on the configured ports
  ([hardening guide](../security/hardening.md#r-network-01-enable-the-networkpolicy)).

## Phases

The [kind catalogue](../kinds.md) tags each kind with one of these phases. A phase ends when its exit test passes
in CI. Phases describe exit criteria, not build order: the kinds of phases 2, 3, 4 and 7 were built before the
current engine and are retrofitted to its rules as the engine lands.

| phase | scope | exit test | release | state |
|-------|-------|-----------|---------|-------|
| 1 | engine and tenant boundary: `BaoServer`, connections, field classes, ownership and path index, observe mode, write-once fields, write circuit breaker, `ACLPolicy`, `BaoEndpoint`, chart, CI | no-write and default tests for built-in kinds; a namespaced connection cannot reach the operator identity (e2e); the self-init example boots with no root token left | next | conformance done (no-write and default tests on pull requests); operator-identity e2e done (`test/e2e/isolation_test.go`); self-init CI gate open |
| 2 | tenant and cluster onboarding: `BaoNamespace`, mounts, Kubernetes, JWT and AppRole auth, `TokenRole`, `KubernetesSecrets*` | one commit onboards a tenant namespace and a remote cluster, including dynamic tokens | next | kinds done; exit test open |
| 3 | identity with the ownership marker | wiping status then reconciling makes zero writes for marked kinds | next | kinds and marker done; exit test open |
| 4 | secrets engines, the rest of sys, OIDC and MFA | 10,000 objects hold a 1 h drift interval inside the request budget | after next | kinds done; scale test open |
| 5 | plugin support: `Plugin` registers OCI plugins, mounts wait for it | the LDAP plugin from OCI and OpenLDAP in CI | later | open |
| 6 | plugin-backed kinds against real backends, LDAP, Kerberos and RADIUS first | each kind runs against its backend in CI before it is un-gated | later | kinds in code, experimental and gated |
| 7 | access models, `CertAuthRole`, `UserpassUser` | kinds integration-tested | done | done |

`after 6` in the catalogue marks plugin kinds outside the core plan (cloud, GitHub, Okta, OCI, CF, Consul,
Nomad, Terraform Cloud, MongoDB Atlas, Quay); they stay gated until each has a plugin and backend in CI.

## Open from the first plan

The API group domain is decided ([BL-001](../project/backlog.md#bl-001-pick-an-owned-api-group-domain-before-the-first-release)).
Still open:

| item | source | status |
|------|--------|--------|
| kubectl short names for every kind, unique against core resources | [BL-007](../project/backlog.md#bl-007-short-api-group-names-and-kubectl-short-names) | decision pending |

## Next: tenant onboarding you can trust

Goal: the tenant boundary is proven, one commit onboards a tenant namespace and a remote cluster, and a restore
from Git rebuilds ownership.

| item | source | help wanted |
|------|--------|-------------|
| phase 1 exit test: the self-init example as a CI gate (the operator-identity e2e test is done: `test/e2e/isolation_test.go`) | [BL-012](../project/backlog.md#bl-012-remaining-v2-rule-gaps) | |
| phase 2 exit test: one commit onboards a tenant namespace and a remote cluster, including dynamic tokens | [phases](#phases) | |
| phase 3 exit test: wiping status then reconciling makes zero writes for marked kinds | [phases](#phases) | |
| restore without status: ownership of memberships and access children rebuilt from the server-side marker | [BL-011](../project/backlog.md#bl-011-restore-without-status-chart-attestation) | |
| ownership marker on KV version 2 metadata | [ADR 0012](../adr/0012-ownership-path-index-and-marker.md) | |
| typed kinds `CertAuthCRL`, `TransitCacheConfig`, `TransitKeyConfig`, `KVSecretMetadata`, `PKICELRole` and `PKIKeyConfig` | [BL-009](../project/backlog.md#bl-009-remaining-typed-kind-gaps) | done |
| generic test that each kind's `Desired` covers its CRD schema (full-field comparison itself is done, [ADR 0011](../adr/0011-full-field-comparison.md)); `MaxLength` on strings read by CEL | [BL-012](../project/backlog.md#bl-012-remaining-v2-rule-gaps) | yes |
| `DatabaseConnection` and LDAP rotated root credentials on `WriteOnce` instead of `CreateOnly` (the engine supports it; the kinds do not use it yet) | [ADR 0017](../adr/0017-write-once-fields-for-rotated-root-credentials.md) | yes |

<a id="v03-scale-and-operations"></a>

## After that: scale and operations

Goal: large shared servers with many tenants.

| item | source | help wanted |
|------|--------|-------------|
| phase 4 exit test: 10,000 objects hold a 1 h drift interval inside the request budget, on CI hardware | [phases](#phases), [load testing](../operate/load-testing.md) | |
| pin the read after a write to the active node | [architecture](../architecture.md#reconcile-sequence) | |
| drift reads on standby nodes (OpenBao horizontal read scaling) | [architecture](../architecture.md#scale-and-drift) | |
| per-namespace fairness for the write budget | [BL-016](../project/backlog.md#bl-016-per-namespace-fairness-for-the-write-budget) | |
| integration suite on every supported OpenBao minor, not only the pinned one | [architecture](../architecture.md#ci-gates), [BL-014](../project/backlog.md#bl-014-detect-openbao-api-changes-automatically) | yes |
| audit devices: an ADR on when the kind applies and how it reports a server that refuses API creation | [BL-003](../project/backlog.md#bl-003-audit-devices) | |
| guide for adopting existing configuration: extend [migration.md](../guides/migration.md) with worked examples | [architecture](../architecture.md#operational-safety) | yes |
| tenant quota e2e: a `ResourceQuota` count blocks the next object | [BL-012](../project/backlog.md#bl-012-remaining-v2-rule-gaps) | yes |
| namespace-scoped install as the chart default | [TD-005](../project/tech-debt.md#td-005-the-default-install-can-read-secrets-cluster-wide) | |

## Later: plugins and the road to v1beta1

| item | source |
|------|--------|
| phase 5 plugin support | [phases](#phases), [architecture](../architecture.md#plugin-support), [BL-005](../project/backlog.md#bl-005-external-plugin-kinds) |
| phase 6 plugin-backed kinds against real backends, each un-gated when its backend runs in CI | [phases](#phases), [TD-004](../project/tech-debt.md#td-004-unconfirmed-paths-and-external-plugin-kinds) |
| hash key rotation without rewriting every write-only field | [known limitations](../reference/limitations.md#ownership-and-restore) |
| sharding by `BaoServer` label for very large fleets | [architecture](../architecture.md#scale-and-drift) |
| namespace sealing support in `BaoNamespace`, with shares through `writeSecretRef` | [architecture](../architecture.md#openbao-native-features) |
| `v1beta1`, when the conditions in the [release process](release-process.md#versioning) hold | [ADR 0020](../adr/0020-crd-versioning-without-webhooks.md) |

## Open questions

- Are LDAP, Kerberos and RADIUS still built in on 2.7? RabbitMQ is built into 2.7.2; does it stay?
- What is the sealed-namespace API (create parameters, key share return)?
- Which other kinds have metadata for the ownership marker (`IdentityEntity`, `IdentityGroup`, `BaoNamespace` and `KVSecretMetadata` carry it)?
- How can a read be forced to the active node after a write?

## Not planned

- Kinds for features OpenBao does not have (Sentinel, replication, KMIP, Transform)
  ([ADR 0009](../adr/0009-openbao-only-target.md)).
- Syncing secret values into Kubernetes Secrets: use External Secrets Operator ([ADR 0007](../adr/0007-no-secret-sync.md)).
- Server lifecycle (deploy, initialise, unseal, upgrade): use the OpenBao Helm chart or an operator for that.
- One-off actions as resources (login, issue, sign, rotate, tidy).
- Admission webhooks ([ADR 0005](../adr/0005-cel-validation-without-webhooks.md)).

## Changing the roadmap

Open a pull request against this page. New outcomes go into the [backlog](../project/backlog.md) first with acceptance
criteria; this page only orders them. Maintainers review the roadmap at each minor release.

## Hash-key rotation design constraints

Rewriting-free rotation is deferred. A future implementation must verify a recorded HMAC against
an explicitly bounded previous-key set before moving status to the new key, without writing
unchanged remote inputs. It must still write a changed Secret, never accept an unmatched old
hash as equal, and distinguish key retirement from restoring lost status. Multi-replica rollout,
status patch failure and rollback need tests before enabling this behaviour.

The current key also participates in adapter credential fingerprints, beyond the generic
`writeOnlyHash` check. A keyring cannot safely be introduced only in that generic comparison.
Compromise recovery still requires retiring the old key; keeping it indefinitely to avoid writes
is not a security fix. Until these contracts are designed and tested, use the existing
[controlled replacement procedure](../security/hardening.md#r-keys-01-hash-key), which deliberately
rewrites current write-only inputs. Out-of-band drift in values OpenBao never returns remains
unobservable, regardless of key rotation.
