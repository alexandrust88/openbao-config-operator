# 0008. The API group domain openbaoconfig.io is provisional

Status: proposed (open question). Date: 2026-11-13.

## Context

The project was scaffolded with the domain `openbaoconfig.io`, so the groups are
`core.openbaoconfig.io`, `sys.openbaoconfig.io` and so on, and the annotation prefix is
`openbaoconfig.io/`. The project does not own that domain. API group names are part of every
manifest users write and cannot be renamed after release without a migration. An unowned domain
risks collisions with other projects. The `openbao` name belongs to the OpenBao project, so a domain under it needs that
project's agreement.

## Decision

Keep `openbaoconfig.io` during pre-alpha. Before v1alpha1 is published (first tagged release,
chart or CRD bundle), the maintainers must pick a domain they own or are permitted to use, and
rename every group, the annotation prefix, `PROJECT` and the Go module path if needed.

## Consequences

- Manifests written before the rename will break; the README states this.
- Options to evaluate: a domain the maintainers register, a subdomain of an organisation that
  hosts the project, or a subdomain under `openbao.org` if the OpenBao project agrees.
- The rename is a mechanical change across `api/`, `config/`, the chart, docs and samples, and
  should be done in one change before the first release.
