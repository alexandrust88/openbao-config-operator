---
description: "Planned OpenBao Config Operator work and priorities for contributors."
---

# Backlog

These are real outcomes that are planned and not implemented yet. The order of work, by release, and the phases
are only in the [roadmap](../maintainers/roadmap.md); this page holds the outcomes behind them and the work that is
not a single kind. Known
limitations of what already exists belong in the [tech-debt register](tech-debt.md).

## BL-001: Pick an owned API group domain before the first release

Status: closed on 2026-11-15. The maintainers keep `openbaoconfig.io`; it has shipped in every release since v0.1.0 and
[ADR 0008](../adr/0008-provisional-api-group-domain.md) records the decision.

Goal: publish v1alpha1 under an API group domain that will not be renamed.

Acceptance criteria:

- ADR 0008 is accepted with the chosen domain.
- No rename: every group, the annotation prefix, `PROJECT`, the chart, the docs and the samples keep
  `openbaoconfig.io`.

## BL-002: Core configuration kinds

Status: done (test/apicoverage, docs/api-coverage.md)

Goal: a platform team can configure namespaces, mounts, the Kubernetes, JWT/OIDC, AppRole and
token auth methods, the core identity objects, password policies, rate-limit quotas and KV and
random secrets with typed kinds instead of `OpenBaoEndpoint`.

Constraints: cross-object fields use flexible references (exactly one of a literal, an OpenBao
name or path, a same-namespace Kubernetes ref or a label selector); KV values come only from
Kubernetes Secrets and are write-only; values OpenBao returns once go to `writeSecretRef`.

Acceptance criteria:

- Every [phase 2](../maintainers/roadmap.md#phases) row in the catalogue, except `Audit` (BL-003), has types, an adapter, unit and
  envtest tests, an integration scenario against `bao server -dev` and a sample that sets every
  spec field.
- No kind writes in steady state.

## BL-003: Audit devices

Status: open; deferred from [phase 2](../maintainers/roadmap.md#phases)

Goal: manage `sys/audit/<path>` declaratively.

Rationale: OpenBao refuses to create audit devices through the API unless the server sets
`unsafe_allow_api_audit_creation`, and declarative audit configuration in the server config is
the recommended path. `OpenBaoEndpoint` covers the API path meanwhile.

Acceptance criteria: a design, recorded as an ADR, that states when the kind applies, how it
reports a server that refuses API creation, and why it does not weaken the server's default.

## BL-004: Secrets engines, OIDC, MFA, plugins and sys singletons

Status: done for the kinds (`api/secrets`, `api/identity`, `api/sys`, [kind catalogue](../kinds.md)); the phase 4
scale test is open in the [roadmap](../maintainers/roadmap.md#phases)

Goal: typed kinds for the database, PKI, transit, SSH, Kubernetes, RabbitMQ and TOTP engines,
the OIDC provider kinds, MFA, the plugin catalogue and the sys singletons.

Acceptance criteria: as BL-002; singletons default to `Orphan` or reset through a `Deleter`, and
no reconcile triggers a rotation, issue or other one-shot action.

## BL-005: External-plugin kinds

Status: open

Goal: typed kinds for the cloud auth methods and secrets engines, LDAP, Kerberos and RADIUS,
which OpenBao ships as external plugins.

Acceptance criteria: each kind reports `Unsupported` until its plugin is registered and mounted,
and is verified as described in TD-004 before release.

## BL-006: Access models

Status: done (`api/access/v1alpha1`, `internal/controller/access`; roadmap phase 7)

Goal: `AccessLevel`, `AccessGrant` and `AccessLayout` render policies, roles, groups and aliases
from a machine-side and a human-side access description, as the architecture describes.

Acceptance criteria: a level cannot be deleted while cited; removing human access requires the
disruptive-update annotation; the rendered objects go through the same engine and show no
steady-state writes.

Kinds for features OpenBao does not have are not planned.

## BL-007: Short API group names and kubectl short names

Status: open for kubectl short names. The group rename this item proposed is rejected: the domain stays
`openbaoconfig.io` (BL-001, [ADR 0008](../adr/0008-provisional-api-group-domain.md)).

Goal: shorter names to type. `openbaoconfig.io` makes long CRD and RBAC names, and generic short names such as
`policy` collide with other operators.

Proposal: add a unique `+kubebuilder:resource:shortName` of 2 to 8 characters to every kind (for example `obpol`
for Policy and `obsm` for SecretsMount).

Acceptance criteria:
- a short-name table in docs/kinds.md, unique across the catalogue and against core Kubernetes resources;
- regenerated CRDs, chart and docs.

## BL-008: Threat model and hardening guide

Status: done (docs/security/threat-model.md, hardening.md; review N1-N6 fixed; GAP-07 is BL-013)

Goal: a written threat model and hardening guide for the operator, following the method of
cert-manager's [threat modelling](https://cert-manager.io/docs/devops-tips/threat-modelling/) page and
the ControlPlane 2026 cert-manager hardening guide.

Acceptance criteria:
- data-flow diagram with trust boundaries (tenant namespace, operator pod, kube-apiserver, OpenBao,
  etcd Secrets) and the assets on each side (operator OpenBao identity, Secret read, TokenRequest, HMAC key);
- threat list per boundary (STRIDE), each threat mapped to a control in code or deployment, and residual risks;
- a hardening guide: least-privilege RBAC and namespace-scoped install, network policy, pod security, image
  verification, OpenBao-side policy for the operator, audit settings, upgrade and key rotation;
- an independent review of the finished implementation against the threat model, with findings filed and fixed.

## BL-009: Remaining typed-kind gaps

Status: done

The listed gaps are implemented, including CertAuthCRL, TransitCacheConfig, TransitKeyConfig,
KVSecretMetadata, PKICELRole and PKIKeyConfig. Paths that stay on OpenBaoEndpoint are classified in
[API coverage](../api-coverage.md): cubbyhole, sys/raw, sys/loggers, SSH issuer and zero-address
configuration, the LDAP library set, and external keys (those need a KMS plugin).

## BL-010: Provable API coverage in CI

Status: done (test/apicoverage, docs/api-coverage.md)

Goal: "full coverage" is a number per OpenBao release, not a claim. Every writable path in the pinned OpenAPI
snapshot of each supported OpenBao minor is in exactly one list: typed kind (with the kind name), Endpoint only
(with a reason), or action (not configuration). CI fails when a path is in none, so a new release that adds
API surface is caught automatically. The report shows covered, Endpoint-only and action counts per release.

## BL-011: Restore without status, chart attestation

Status: open

- GroupMembership and AccessGrant/AccessLayout keep which members and children they own only in status. A
  restore of the CRs without status cannot rebuild ownership. Use the server-side ownership marker (or a
  deterministic naming scheme that can be recomputed) so a status-less restore adopts exactly its own objects.
- Done: the Helm chart OCI artifact now gets a CycloneDX SBOM attestation in release.yml.

## BL-012: Remaining v2 rule gaps

Status: open

- Design rule 3: the engine compares every key Desired returns; it does not itself enforce that Desired covers every typed
  field. Add a generic test that compares each kind's Desired keys with its CRD schema properties.
- Design rule 9: the self-init example is verified by examples/self-init/verify.sh (docker). Run it in the e2e workflow
  on GitHub runners so it is a CI gate.
- Safety 8: tenant quotas are documented only; add an e2e step that a ResourceQuota count blocks the 11th object.
- Unbounded strings read by CEL (about 55 duration strings, Entity/Group metadata values, JWT leeways): add
  MaxLength for cost headroom.

## BL-013: NetworkPolicy on by default

Status: done (chart: on by default, metrics from the release and monitoring namespaces, NOTES warning for open
API server or OpenBao egress, `networkPolicy.flavor: cilium` for a CiliumNetworkPolicy)

The NetworkPolicy is enabled by default, but empty `apiServer.cidrs` or `openbao.to` leaves egress
unrestricted by destination on the configured ports. The chart notes warn about this; operators
should set explicit peers as described in
[hardening.md](../security/hardening.md#r-network-01-enable-the-networkpolicy).

## BL-014: Detect OpenBao API changes automatically

Status: open

[ADR 0023](../adr/0023-openbao-api-changes-and-crd-lifecycle.md) turns every OpenBao minor into a failing pull
request. Done:

- Renovate tracks `BAO_VERSION` in the Makefile (`# renovate: datasource=github-releases depName=openbao/openbao`,
  matched by the Makefile custom manager in `renovate.json`); its pull request runs integration and API coverage.
- Server version from `sys/health` on `OpenBaoServer` status (`status.version` in
  `api/core/v1alpha1/openbaoserver_types.go`), and acquired objects bound to their server by cluster id with reason
  `ServerChanged` (`internal/engine/reconciler.go`).

Missing pieces:

- The `BAO_VERSION` pull request refreshes the API snapshot and runs conformance.
- Field-level API coverage: every request parameter of a typed path maps to a CRD field or is excluded with a
  reason (path-level coverage exists).
- Nightly integration against every supported OpenBao minor (2.6 is snapshot-only today).
- An integration run logged in with the least-privilege operator policy from [openbao-policy.md](../operate/openbao-policy.md)
  instead of the dev root token.
- `api-coverage diff <old> <new>` listing added, removed and changed paths and fields for release notes.
- The server version named in `Unsupported` and `PermissionDenied` messages.

## BL-015: TOTP output delivery after an out-of-band key rotation

Status: done (internal/controller/secrets/totpkey.go, TestTOTPKeyRegeneratedKeyNotDeliveredIsNotReady)

PKI outputs carry `key_id` in the output Secret, so a Secret left over from an earlier key is detected as not
delivered. A TOTP key has no readable identifier: if a delivered key is deleted outside the operator, regenerated,
and the Secret write then fails, the old URL in the Secret still counts as delivered.

Fix: when OpenBao generates a key, the adapter records an HMAC-SHA256 of the URL, keyed with the operator's hash
key, in `status.id` and writes the same value to the output Secret under `fingerprint`. The delivery check compares
them, so a Secret holding an earlier key's URL keeps the resource at `Ready=False`, reason `OutputNotDelivered`.
The hash is keyed because `keySize` may be as small as one byte and status is readable more widely than the
Secret. Secrets written before the fingerprint existed stay accepted, as for PKI outputs without `key_id`.

## BL-016: Per-namespace fairness for the write budget

Status: open

The write budget (ADR 0018) is one sliding window per `OpenBaoServer`, shared by every namespace. Changed input
Secrets no longer spend it, but one namespace with many objects that drift (or a kind that flaps) can still use
the whole window and delay other namespaces' drift repairs on the same server. Give each namespace a fair share
of the window (for example a per-namespace sub-budget, or round-robin between namespaces with held-back writes),
with a metric per namespace.

## BL-017: Refuse a literal password in DatabaseConnection pluginConfig.connection_url

Status: open

`connectionURL` refuses a literal password with CEL, but `pluginConfig` can carry `connection_url` too. The same
rule on `pluginConfig` is rejected at CRD install: its map values have no `maxLength`, so the estimated CEL cost is
over 100 times the budget. Fix: bound the values (a named string type with `MaxLength`, which gives
`additionalProperties.maxLength`, plus the existing `MaxProperties=64`), then add
`!has(self.pluginConfig) || !('connection_url' in self.pluginConfig) || <the connectionURL check>` with an envtest
case for the rejection and a CRD-install test for the cost budget. It changes the Go type of `pluginConfig` and its
adapter, so it waits for the owner of `databaseconnection_types.go`.

## BL-018: Durable ownership of retained transit keys

Status: design needed; not implemented

`TransitKey` defaults to `Orphan`. Deleting its Kubernetes object retains the key
but removes the live resource's ownership claim. Transit keys do not carry the
operator's server-side ownership marker. A replacement resource with `Create`
therefore conflicts with the existing key; an authorised explicit `Adopt` can
manage it. This is retention, not a permanent reservation of the key name for a
former tenant.

Before adding durable reservations, define a record keyed by server identity,
canonical OpenBao namespace and mount/key path, with an immutable tenant
identity rather than a reusable namespace name. Decide where the record survives
namespace deletion, how backups restore it, and how an administrator transfers or
releases it. A Kubernetes-only record does not protect against another writer
cluster or direct OpenBao API access. A record inside OpenBao needs a reserved
storage path and tightly scoped privileges, without pretending transit supports
arbitrary ownership metadata.

Acceptance tests must cover namespace deletion and name reuse, CR recreation,
controller restart, backup restore, concurrent adoption, connection retargeting
and an audited explicit transfer. Keep this a separate shared-engine design
review under [ADR 0012](../adr/0012-ownership-path-index-and-marker.md); adding a
second `KMSKey` wrapper would not solve the ownership problem.

## BL-019: Cancellable delayed destruction of key material

Status: design needed; not implemented

The current destructive-delete guard requires `deletionPolicy: Delete` plus the
annotation; it does not introduce a waiting period. Retaining a terminating CR
for a timer alone is not cancellable deletion: Kubernetes cannot undo its
`deletionTimestamp`. A future design needs an explicit scheduling state while
the CR still exists, durable deadlines, cancellation authorisation and a defined
response to Kubernetes namespace deletion. Neither a controller restart nor a
GitOps prune may shorten the waiting period or silently skip the guard.

Specify whether scheduled keys remain usable, how new writes are stopped, how
ciphertext migration is proved complete, and how deadlines behave during
outages and clock changes. Require tests for schedule/cancel/reschedule,
restart, namespace deletion, stale credentials, ownership changes, repeated
execution and failed OpenBao deletes. Do not change the shared deletion contract
in an application-example change; review it against
[ADR 0013](../adr/0013-destructive-delete-guard.md) first.
