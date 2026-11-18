---
description: "Architecture decisions behind OpenBao Config Operator, including reconciliation, tenant boundaries, ownership and resource lifecycle."
---

# Architecture decisions

The operator keeps the configuration of an existing OpenBao server in the state Kubernetes custom
resources declare. Each record states its status, the context that forced the choice, the decision
and the consequences. Every record is accepted except 0004, which is superseded in part by 0015, and 0008,
which is superseded by 0025.
Records 0017 and 0023 are partly implemented, and 0020 applies from the first CRD version after
`v1alpha1`. Record 0025 is implemented in 1.0.0, the first release.

## Engine: one reconciler serves every kind

| Number | Title | Status | Decision |
|--------|-------|--------|----------|
| [0001](0001-openbao-api-client.md) | [Use the OpenBao api/v2 client](0001-openbao-api-client.md) | accepted; implemented | Every OpenBao call uses `github.com/openbao/openbao/api/v2` through a small logical client, and adapters call generic paths instead of the typed `Sys()` helpers. |
| [0002](0002-generic-engine-with-adapters.md) | [One generic engine with per-kind adapters and FieldSpec](0002-generic-engine-with-adapters.md) | accepted; implemented; the comparison rule (only the keys the spec sets) is superseded by [ADR 0011](0011-full-field-comparison.md) | One reconciler serves every kind, each kind supplies an adapter and a `FieldSpec`, and [0011](0011-full-field-comparison.md) replaces the rule that compared only the keys the spec sets. |
| [0011](0011-full-field-comparison.md) | [Full-field comparison with declared defaults](0011-full-field-comparison.md) | accepted; implemented | The desired payload includes every typed field, an omitted field is sent as the default the adapter declares, and `BaoEndpoint` still compares only its own data keys. |
| [0012](0012-ownership-path-index-and-marker.md) | [Ownership: path index and server-side marker](0012-ownership-path-index-and-marker.md) | accepted; implemented (path index and markers on entities, groups and namespaces, and the KV version 2 metadata marker) | The older resource owns a path, a server-side marker wins where the API stores metadata, and a second resource gets `Conflict` and does not write. |
| [0014](0014-drift-tiers-and-request-budget.md) | [Drift tiers and per-server request budget](0014-drift-tiers-and-request-budget.md) | accepted; implemented | Access-critical kinds are re-read every 15 minutes and other kinds every hour, with jitter, and a spec change is served ahead of drift reads under that server's rate limit. |
| [0017](0017-write-once-fields-for-rotated-root-credentials.md) | [Write-once fields for rotated root credentials](0017-write-once-fields-for-rotated-root-credentials.md) | accepted; partly implemented | A write-once field is sent on create and again only when its rotation nonce changes, and it is never compared. |

## Tenancy: the platform owns the server, and a tenant acts as itself

| Number | Title | Status | Decision |
|--------|-------|--------|----------|
| [0003](0003-connections-and-tenant-boundary.md) | [Connection kinds and the tenant boundary](0003-connections-and-tenant-boundary.md) | accepted; implemented; amended by [ADR 0010](0010-openbaoserver-owns-address-and-audiences.md) (address and audiences move to the server kind, now `BaoServer`) | A namespaced connection logs in as a ServiceAccount in its own namespace, never as the operator, and a cluster-scoped connection is limited to the namespaces it allows. |
| [0010](0010-openbaoserver-owns-address-and-audiences.md) | [OpenBaoServer owns the address and audiences](0010-openbaoserver-owns-address-and-audiences.md) | accepted; implemented | The platform-owned `BaoServer` holds the address, CA bundle, allowed namespaces, audiences, rate limit and request timeout, and a connection names that server and carries no address of its own. |
| [0021](0021-one-writer-cluster-per-namespace.md) | [One writer cluster per OpenBao namespace](0021-one-writer-cluster-per-namespace.md) | accepted; implemented | One OpenBao namespace has one writer cluster, and a marker that names another cluster yields `Conflict`. |

## Safety: changes to OpenBao stay explicit

| Number | Title | Status | Decision |
|--------|-------|--------|----------|
| [0004](0004-creation-and-deletion-policy-defaults.md) | [Creation and deletion policy defaults](0004-creation-and-deletion-policy-defaults.md) | superseded in part by [ADR 0015](0015-create-and-orphan-by-default.md) (both defaults) | This record defaulted creation to `CreateOrAdopt` and deletion by kind; [0015](0015-create-and-orphan-by-default.md) replaces both, with `Create` and `Orphan` for every kind. |
| [0007](0007-no-secret-sync.md) | [No OpenBao-to-Kubernetes secret sync](0007-no-secret-sync.md) | accepted; implemented | Secret values stay in OpenBao; the only Kubernetes Secret the operator writes is a value it generates and OpenBao returns once. |
| [0013](0013-destructive-delete-guard.md) | [Destructive-delete guard](0013-destructive-delete-guard.md) | accepted; implemented, including key material, MFA methods and entities | Mounts, namespaces, KV and random secrets, audit devices, key material, MFA methods, entities and `BaoEndpoint` delete only with `obco.dev/allow-destructive-delete: "true"`, and a terminating Kubernetes namespace orphans them. |
| [0015](0015-create-and-orphan-by-default.md) | [Create by default and orphan by default](0015-create-and-orphan-by-default.md) | accepted; implemented | `creationPolicy` defaults to `Create` and `deletionPolicy` defaults to `Orphan` for every kind. |
| [0016](0016-observe-mode-and-import-tool.md) | [Observe mode and import tool](0016-observe-mode-and-import-tool.md) | accepted; implemented | `Observe` reads and reports drift and does not write, delete or add a finalizer, and `obc-import` emits resources in that mode with `creationPolicy: Adopt`. |
| [0018](0018-write-circuit-breaker.md) | [Write circuit breaker](0018-write-circuit-breaker.md) | accepted; implemented | Rewrites of an owned object whose spec and input secrets are unchanged are capped at 60 per minute per server; creates, adoptions, spec changes and new secret inputs do not spend that budget. |
| [0019](0019-pause-annotation.md) | [Pause annotation](0019-pause-annotation.md) | accepted; implemented | `obco.dev/paused: "true"` on a resource, its Kubernetes namespace or its `BaoServer` stops reads, writes and deletes, and a deletion keeps the finalizer. |
| [0022](0022-audit-attribution-header.md) | [Audit attribution header](0022-audit-attribution-header.md) | accepted; implemented | Every request sends `X-Openbao-Config-Resource` set to the custom resource's group, kind, namespace and name. |

## API lifecycle: CRDs stay stable as OpenBao changes

| Number | Title | Status | Decision |
|--------|-------|--------|----------|
| [0005](0005-cel-validation-without-webhooks.md) | [CEL validation without admission webhooks](0005-cel-validation-without-webhooks.md) | accepted; implemented | OpenAPI and CEL in the CRD schema validate each object, and v1alpha1 does not ship an admission webhook. |
| [0006](0006-endpoint-escape-hatch.md) | [Endpoint escape hatch](0006-endpoint-escape-hatch.md) | accepted; implemented; only OpenBao paths are in scope per [ADR 0009](0009-openbao-only-target.md) | `BaoEndpoint` writes any payload to any path through the same engine, and `deletionPolicy: Delete` also requires `obco.dev/allow-destructive-delete: "true"`. |
| [0008](0008-provisional-api-group-domain.md) | [Keep the API group domain openbaoconfig.io](0008-provisional-api-group-domain.md) | superseded by [ADR 0025](0025-move-the-api-to-obco-dev.md) | This record kept `openbaoconfig.io` for every API group and for the annotation prefix; [0025](0025-move-the-api-to-obco-dev.md) moves both to `obco.dev`. |
| [0009](0009-openbao-only-target.md) | [OpenBao-only target](0009-openbao-only-target.md) | accepted; implemented | The operator targets OpenBao 2.6 and later only, with no Vault kinds and no flavour detection. |
| [0020](0020-crd-versioning-without-webhooks.md) | [CRD versioning without webhooks](0020-crd-versioning-without-webhooks.md) | accepted; not yet applied, because every CRD still serves only `v1alpha1` | A new CRD version only adds fields, Kubernetes converts it with `conversion: None`, and a conversion webhook is reserved for a break that cannot be avoided. |
| [0023](0023-openbao-api-changes-and-crd-lifecycle.md) | [OpenBao API changes and the CRD lifecycle](0023-openbao-api-changes-and-crd-lifecycle.md) | accepted; partly implemented, with the open detection and runtime items tracked in [BL-014](../project/backlog.md#bl-014-detect-openbao-api-changes-automatically) | Adapters absorb OpenBao's wire changes, and a CRD changes only when users gain a field or lose one that OpenBao removed, so an OpenBao release does not bump the CRD version. |
| [0025](0025-move-the-api-to-obco-dev.md) | [Move the API to obco.dev](0025-move-the-api-to-obco-dev.md) | accepted; implemented in 1.0.0, the first release | Every group moves from `openbaoconfig.io` to `obco.dev`, kinds that clash in kubectl are renamed, `openbaoNamespace` names an OpenBao namespace, and operator metrics take the `obco_` prefix, in the first release, 1.0.0, with no migration from the withdrawn pre-releases. |

## KV cleanup

| Number | Title | Status | Decision |
|--------|-------|--------|----------|
| [0024](0024-bounded-kv-history-cleanup.md) | [Bounded KV history cleanup](0024-bounded-kv-history-cleanup.md) | accepted; implemented | `KVSecret` and `RandomSecret` record `status.purgeThroughVersion` before destroying history, then destroy only versions up to that cutoff in batches of at most 64, and keep the metadata. |

## Related

- [Architecture](../architecture.md)
- [Concepts](../concepts/index.md)
