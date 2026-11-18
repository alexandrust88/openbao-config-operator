---
description: "Find a first contribution to OpenBao Config Operator and the checks needed to submit it."
---

# First issues

Well-scoped tasks for a first or second contribution. Each one is self-contained, has a clear
"done", and a source page that explains it. Maintainers open a GitHub issue labelled
`good first issue` or `help wanted` for each; comment on the issue before you start so two people
do not do the same work.

## API and validation

| task | done when | source |
|------|-----------|--------|
| a table of the kubectl short names (`+kubebuilder:resource:shortName`, `bao*`) | short-name table in [kinds.md](../kinds.md) that matches the CRDs | [BL-007](../project/backlog.md#bl-007-short-api-group-names-and-kubectl-short-names) |
| generic test that each kind's `Desired` keys match its CRD schema properties | the test fails for a kind that drops a typed field | [BL-012](../project/backlog.md#bl-012-remaining-v2-rule-gaps) |
| `MaxLength` on duration strings, entity and group metadata values and JWT leeways read by CEL | CRDs regenerated, envtest still passes, CEL cost lower | [BL-012](../project/backlog.md#bl-012-remaining-v2-rule-gaps) |
| e2e step: a `ResourceQuota` object count blocks the next custom resource | runs in the e2e workflow | [BL-012](../project/backlog.md#bl-012-remaining-v2-rule-gaps), [tenant quotas](../operate/tenant-quotas.md) |
| `DatabaseConnection` and LDAP rotated root credentials on `WriteOnce` instead of `CreateOnly` | kinds use the engine support already in place | [ADR 0017](../adr/0017-write-once-fields-for-rotated-root-credentials.md) |

New typed kinds still follow the recipe in [AGENTS.md](../../AGENTS.md#adding-a-kind): types with
CEL rules, an adapter with its `FieldSpec`, unit tests against a read-shaped fixture, an
integration scenario against `bao server -dev`, an example under `examples/`, and a row in
[kinds.md](../kinds.md). Paths that stay on `BaoEndpoint` are listed in
[API coverage](../api-coverage.md#endpoint-only-paths).

## Tests and CI gates

| task | done when | source |
|------|-----------|--------|
| run [examples/self-init/verify.sh](../../examples/self-init/verify.sh) in the e2e workflow | the self-init example is a CI gate | [BL-012](../project/backlog.md#bl-012-remaining-v2-rule-gaps) |
| integration suite against every supported OpenBao minor, not only the pinned one | workflow matrix and green runs recorded | [BL-014](../project/backlog.md#bl-014-detect-openbao-api-changes-automatically) |

## Documentation

| task | done when |
|------|-----------|
| a worked example for [adopting existing configuration](../guides/migration.md): `obc-import`, `Observe`, then `Manage` on a real server | added to `docs/guides/migration.md` and reviewed by a maintainer |
| a variant of the [tenant onboarding tutorial](../tutorials/tenant-onboarding.md), which uses the quickstart's dev server, on a real (non-dev) OpenBao with TLS and an OpenBao namespace | a page under `docs/tutorials/`, tested end to end |

Before your first pull request, read [CONTRIBUTING.md](../../CONTRIBUTING.md) and pick a check from
the [verification guide](../project/verification.md) that matches the change.
