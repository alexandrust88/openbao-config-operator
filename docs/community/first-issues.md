# First issues

Well-scoped tasks for a first or second contribution. Each one is self-contained, has a clear "done", and a source
page that explains it. Maintainers open a GitHub issue labelled `good first issue` or `help wanted` for each; comment
on the issue before you start so two people do not do the same work.

## New typed kinds

Each follows the recipe in [AGENTS.md](../../AGENTS.md#adding-a-kind): types with CEL rules, an adapter with its
`FieldSpec`, unit tests against a read-shaped fixture, an integration scenario against `bao server -dev`, an
example under `examples/`, and a row in [kinds.md](../kinds.md). `OpenBaoEndpoint` covers these paths today
([API coverage](../api-coverage.md#endpoint-only-paths)).

| kind | path | notes |
|------|------|-------|
| `TransitCacheConfig` | `<m>/cache-config` | singleton per transit mount; one integer field ([BL-009](../project/backlog.md#bl-009-remaining-typed-kind-gaps)) |
| `CertAuthCRL` | `auth/<b>/crls/<name>` | the CRL is PEM input; decide whether it comes from a Secret or a ConfigMap ([BL-009](../project/backlog.md#bl-009-remaining-typed-kind-gaps)) |
| `KVSecretMetadata` | `<m>/metadata/<path>` | `max_versions`, `cas_required`, `delete_version_after`, `custom_metadata`; or a metadata section on `KVSecret` (discuss first, [BL-009](../project/backlog.md#bl-009-remaining-typed-kind-gaps)) |
| SSH zero-address config | `<m>/config/zeroaddress` | list of role names, an unordered set |
| transit mount key config | `<m>/config/keys` | mount-wide transit setting |

## Tests and validation

| task | done when | source |
|------|-----------|--------|
| generic test that each kind's `Desired` keys match its CRD schema properties | the test fails for a kind that drops a typed field | [BL-012](../project/backlog.md#bl-012-remaining-v2-rule-gaps) |
| `MaxLength` on duration strings, entity and group metadata values and JWT leeways read by CEL | CRDs regenerated, envtest still passes, CEL cost lower | [BL-012](../project/backlog.md#bl-012-remaining-v2-rule-gaps) |
| e2e step: a `ResourceQuota` object count blocks the next custom resource | runs in the e2e workflow | [BL-012](../project/backlog.md#bl-012-remaining-v2-rule-gaps), [tenant quotas](../operate/tenant-quotas.md) |

## Documentation

| task | done when |
|------|-----------|
| a worked example for [adopting existing configuration](../guides/migration.md): `obc-import`, `Observe`, then `Manage` on a real server | added to `docs/guides/migration.md` and reviewed by a maintainer |
| a Flux example next to the Argo CD one: health checks and dependency ordering for connections before resources | a page under `docs/guides/` with tested manifests |
| a "first tenant in ten minutes" tutorial on a real (non-dev) OpenBao with TLS and an OpenBao namespace | a page under `docs/guides/`, tested end to end |
