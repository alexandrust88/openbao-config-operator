---
description: "Design decision for OpenBao Config Operator: keep the API group domain openbaoconfig.io. Rationale, constraints and consequences."
---

# 0008. Keep the API group domain openbaoconfig.io

Status: accepted (final, released in v0.1.0). Date: 2026-11-15. Proposed on 2026-11-13 as a provisional domain.

## Context

The project was scaffolded with the domain `openbaoconfig.io`, so the groups are
`core.openbaoconfig.io`, `sys.openbaoconfig.io` and so on, and the annotation prefix is
`openbaoconfig.io/`. The project does not own that domain. API group names are part of every
manifest users write and cannot be renamed after release without a migration. An unowned domain
risks collisions with other projects. The `openbao` name belongs to the OpenBao project, so a domain under it needs that
project's agreement.

The first proposal (2026-11-13) kept `openbaoconfig.io` during pre-alpha and asked the maintainers to pick a
domain they own, and rename every group, the annotation prefix, `PROJECT` and the Go module path, before the first
tagged release. Options were a domain the maintainers register, a subdomain of an organisation that hosts the
project, or a subdomain under `openbao.org` if the OpenBao project agrees.

v0.1.0 and v0.1.1 were released with `openbaoconfig.io`. From then on a rename breaks every manifest, RBAC rule and
GitOps repository of real users, and needs a migration (delete the custom resources and CRDs, install the new
CRDs, re-apply).

## Decision

Keep `openbaoconfig.io` as the final API group domain for every group and for the annotation prefix. No rename is
planned. [BL-001](../project/backlog.md#bl-001-pick-an-owned-api-group-domain-before-the-first-release) is closed
by this decision.

## Consequences

- Manifests written for v0.1.0 and later stay valid; no group migration is needed before `v1beta1`.
- The collision risk of an unowned domain remains; a collision would surface as a CRD name clash at install
  time.
- Short names to type are tracked separately in
  [BL-007](../project/backlog.md#bl-007-short-api-group-names-and-kubectl-short-names), without a group rename.
- The file name of this ADR keeps the word "provisional" so that existing links resolve.
