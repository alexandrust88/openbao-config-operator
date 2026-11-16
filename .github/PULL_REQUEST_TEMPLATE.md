<!--
The title becomes the squash-merge commit subject and the basis of the CHANGELOG entry, so it must be a conventional commit:
`feat(secrets): add TransitCacheConfig`, `fix(engine): ...`, `docs: ...`. Add `!` for a breaking change.
This description becomes the commit body: end it with `BREAKING CHANGE: <what users must do>` for a breaking
change, or `Release-As: X.Y.Z` (maintainers only) to force a version.
-->

## What and why

<!-- What does this change do, and which issue does it close? "Closes #123". -->

## How it was tested

<!-- The exact commands you ran and their result, e.g. make check, make test-integration, make test-e2e.
     A skipped test is not a pass. -->

## Checklist

- [ ] The title is a conventional commit, and the change is one coherent change (no unrelated refactoring or reformatting)
- [ ] `make check` passes
- [ ] New or changed behaviour has tests: unit tests for `Desired` and `FieldSpec`, envtest for CEL rules, an integration scenario against `bao server -dev` for a new builtin kind
- [ ] API changes are regenerated (`make manifests generate chart-sync samples`) and `make verify-generated` is clean
- [ ] Docs are updated: [docs/kinds.md](../docs/kinds.md) for a new or changed kind, an example under `examples/`, and user docs for new flags or values
- [ ] No tokens, secret values or OpenBao response bodies in code, logs, events, status, fixtures, examples or docs

## API and upgrade impact

<!-- Delete if none. New or changed fields, defaults, validation, deprecations, required OpenBao permissions.
     Removing or renaming a field follows the deprecation policy in docs/maintainers/release-process.md. -->
