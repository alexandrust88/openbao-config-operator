# 0023. OpenBao API changes and the CRD lifecycle

Status: accepted. Date: 2026-11-14.

## Context

OpenBao ships a minor release every few months and has no long-term-support line. A minor can add, rename,
deprecate or remove request fields, change a default, change a read shape, add or remove an endpoint, or need a
new capability in a policy. The operator has 68 built-in kinds and 52 plugin kinds whose CRDs users write into
Git, so every OpenBao change must reach users without breaking their manifests and without an API version bump
on OpenBao's schedule.

Already decided elsewhere and assumed here:

- [ADR 0006](0006-endpoint-escape-hatch.md): `OpenBaoEndpoint` covers any path without a typed kind.
- [ADR 0009](0009-openbao-only-target.md): OpenBao only.
- [ADR 0020](0020-crd-versioning-without-webhooks.md): CRD versions change additively with `conversion: None`.
- [Support window](../maintainers/release-process.md#openbao-versions): each operator minor supports the OpenBao
  minors that have an API snapshot in `api-coverage/`; no version-branching shims in adapters.

## Decision

### 1. The CRDs are our API, the adapter is the translation layer

CRD field names, types and defaults are chosen for users and stay stable. OpenBao's wire shape lives in the
adapter (`Desired`, `FieldSpec`, optional `Observer`). OpenBao changes are absorbed there first; a CRD changes
only when users gain something (a new field) or lose something upstream removed. CRDs are not generated from
OpenBao's OpenAPI document: generated types leak wire names and churn on every upstream release.

### 2. How each upstream change maps to the CRD

| OpenBao change | CRD change | Mechanism |
|---|---|---|
| new optional request field | new optional field in the served version; unset is not sent | additive; a server that rejects it gives reason `Unsupported` |
| field renamed on the wire | none | `FieldSpec.Aliases` |
| type or format change (string to list, duration format) | none when possible | adapter normalisation, `FieldSpec.Durations`/`Sets` |
| default changed | none | the no-write conformance gate fails; update the declared default |
| read shape changed | none | read fixtures and the no-write gate fail; fix the adapter |
| field deprecated upstream | `Deprecated:` doc comment, still read | [deprecation policy](../maintainers/release-process.md#deprecation-policy) |
| field removed upstream | kept while any supported minor has it, then deprecated and removed per policy | support window |
| new endpoint or engine | `OpenBaoEndpoint` at once; a typed kind later as a new CRD | additive |
| endpoint removed | the kind reports `Unsupported` on newer servers; removed after the window | support window |
| new capability needed in a policy | none | the least-privilege integration run fails (section 4); update `docs/operate/openbao-policy.md` |

OpenBao's schedule never forces a CRD version bump. Only our own breaking changes do, and ADR 0020 keeps those
additive with a deprecation period.

### 3. CRD version lifecycle

- `v1alpha1` today; `v1beta1` as the storage version at operator 1.0; `v1` when the API has been stable for a
  year of releases. Rules per level are in the deprecation policy.
- A new version is served alongside the old one with `conversion: None`; both schemas stay identical except
  for additions. Stored objects move with kube-storage-version-migrator; a version leaves `served` only after
  `status.storedVersions` no longer lists it, announced one minor ahead.
- The chart ships CRDs as templates with `helm.sh/resource-policy: keep`, so `helm upgrade` upgrades them and
  uninstall keeps them; CRDs are applied before the manager rolls.
- Every CRD change passes the install test (CEL cost) and `make verify-generated` (no hand edits).

### 4. Detection: every OpenBao minor becomes a failing pull request

| Check | Catches | Status |
|---|---|---|
| per-minor OpenAPI snapshot, every writable path classified (`go test ./test/apicoverage/...`) | new or removed endpoints | in place |
| no-write conformance gate and adapter read fixtures | changed defaults and read shapes | in place |
| integration suite against the pinned `BAO_VERSION` | behaviour changes on typed kinds | in place |
| field-level coverage: every request parameter of a typed path maps to a CRD field or is excluded with a reason | new fields on typed kinds | to do (BL-014) |
| Renovate bumps `BAO_VERSION`; that pull request refreshes the snapshot and runs every check above | starts the work without a person watching releases | partly in place: Renovate tracks `BAO_VERSION` (`Makefile`, `renovate.json`) and the pull request runs integration and API coverage; snapshot refresh and conformance on that pull request to do (BL-014) |
| nightly integration run against every supported minor (today 2.6 and 2.7) | regressions on the older minor | to do (BL-014) |
| integration run logged in with the documented least-privilege operator policy, not a root token | new capabilities or `sudo` requirements | to do (BL-014) |
| `api-coverage diff <old> <new>`: paths and fields added, removed, changed | release-note input and triage list | to do (BL-014) |

### 5. Runtime

In place: `OpenBaoServer` status records the server version from `sys/health` (`status.version` in
`api/core/v1alpha1/openbaoserver_types.go`), and acquired objects are bound to one server by its cluster id
(`status.serverID`, reason `ServerChanged` emitted by `internal/engine/reconciler.go`). To do (BL-014):
`Unsupported` and `PermissionDenied` messages name the version. Adapters do not branch on the version: a field or
endpoint the server lacks surfaces as `Unsupported`.

## Consequences

- An OpenBao minor costs a maintainer one Renovate pull request: classify new paths and fields, fix what fails,
  merge. The six-week support target in the release process stays realistic.
- CRDs stay stable across OpenBao minors; users rewrite manifests only on our deprecations.
- The adapter carries the complexity of wire changes, kept small by `FieldSpec` and covered by fixtures.
- Until BL-014 lands, new fields on typed kinds and new capability requirements are found by users or by reading
  OpenBao release notes.
