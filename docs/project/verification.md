# Verification guide

This page says what each check proves and what it does not. Run the narrowest check that proves
a change first, then widen. Report the commands you ran and their results; a skipped test is not
a pass.

## Canonical checks

| Command | What it proves | Limits |
| --- | --- | --- |
| `make fmt-check` | Go sources are gofmt-formatted | Does not assess behaviour |
| `make vet` | `go vet` finds no suspicious constructs | Not a substitute for tests |
| `make lint-config lint` | The golangci-lint configuration is valid and the sources pass it, including the module plugins from `.custom-gcl.yml` | Linter findings are not runtime evidence |
| `make test-unit` | Unit tests that need no control plane pass: engine compare and reconcile logic, adapter `Desired` and `FieldSpec` rules, client behaviour | No Kubernetes API server, no CRD schema, no OpenBao |
| `make test` | Unit and envtest tests pass, including CEL and OpenAPI validation of the generated CRDs against a real API server | No OpenBao; no controllers running in a cluster |
| `make test-integration` | Each builtin kind against a real `bao server -dev` and envtest: create, steady state without a write, drift repair, delete per policy | Dev mode only: in-memory storage, root token, one node, no TLS. Skips when `bao` or the envtest binaries are absent, so check the output for `SKIP` |
| `make manifests generate chart-sync samples` | CRDs, RBAC, deepcopy code, the chart copies and `examples-generated/` can be regenerated | Does not prove the generated output was committed |
| `make verify-generated` | Regenerating changes nothing tracked under `api`, `config`, `charts` and `examples-generated`, and leaves no untracked generated file | Requires a Git checkout; regenerates every package, so run it only when you own the whole tree |
| `make govulncheck` | No known vulnerability is reachable from the code | Only covers vulnerabilities in the Go vulnerability database |
| `make helm-lint` | The chart lints in strict mode and renders with default values | Does not install the chart |
| `make validate-manifests` | The kustomize and Helm output validates against the Kubernetes schemas | Custom resources without a published schema are skipped |
| `make mod-check` | `go.mod` and `go.sum` are tidy, and every downloaded module matches `go.sum` | Does not check for known vulnerabilities |
| `make test-docs` | Every manifest in `examples/`, `examples-generated/`, the docs includes and complete YAML blocks on docs pages passes a server-side dry run with strict field validation against envtest with the CRDs, and every snippet include exists | No OpenBao; the commands on the tutorial pages run only in the e2e workflow |
| `make check` | `fmt-check`, `vet`, `mod-check`, `lint`, `test`, `test-docs`, `verify-generated`, `api-coverage`, `helm-lint` and `validate-manifests`: the fast subset of the pull request gate | Starts no cluster; `test` starts `bao server -dev` for a few packages when `bao` is available; leaves out integration, namespaces, live kit, conformance, chart unit tests and e2e |
| `make api-coverage` | Every writable path of each OpenAPI snapshot in `api-coverage/` (2.6 and 2.7) has exactly one class with a reason, every typed kind named there has a CRD, and `docs/api-coverage.md` is regenerated; CI fails when the report changes | Path level only: a new request field on a typed path is not detected ([BL-014](backlog.md#bl-014-detect-openbao-api-changes-automatically)) |
| `make test-samples` | Every generated sample in `examples-generated/` passes server-side validation, CEL included, against envtest with the real CRDs | No OpenBao; not run by a CI workflow, but `make test-docs` (CI job `docs-examples`) dry-runs the same samples |
| `bin/helm unittest charts/openbao-config-operator` | The chart's unit tests in `charts/openbao-config-operator/tests` pass: CRDs, deployment, RBAC, plugin kinds and resources render as asserted | Rendering only; no install. CI installs the helm-unittest plugin before this step; locally install it yourself |
| `make test-namespaces` | Every built-in kind through each connection mode (root, child through `spec.namespace`, connection based in a child, AppRole login inside a child) against one `bao server -dev` per mode and the manager built from `./cmd`; logins pointed outside their namespace are refused without a write, and the audit log shows where each object was written | Dev mode only; skips locally without `bao` (fails in CI) |
| `make test-conformance` | Every built-in kind passes the no-write test (examples applied, a further drift pass writes nothing) and the default test (minimal spec, then each optional field set and removed) against `bao server -dev` and the manager built from `./cmd` | A pull request measures only the kinds it touches (`hack/ci/conformance-kinds.sh`); push to `main`, the weekly run and a manual run measure every kind. Kinds in `test/conformance/exceptions.yaml` are `n/a` for the named test |
| `make test-livekit` | The live kit in `examples/live` renders with kustomize, every built-in kind has a minimal and a full example that sets every spec field, every rendered object passes server-side validation, and the root and child overlays reach `Ready` against `bao server -dev` | Logins are rewritten to AppRole, so this job does not exercise Kubernetes auth; runs in the pull request CI. Explicit prerequisite exceptions are not live passes |
| `make test-e2e` | In a disposable kind cluster, installs the Helm chart and a three-node self-initialising OpenBao Raft cluster with TLS and PVCs; exercises Kubernetes authentication, configuration writes, drift repair, source Secret rotation, steady state and deletion; checks TLS rejection, active-Service routing, OpenBao pod replacement with retained PVC/data, and manager leader failover | Needs Docker, kubectl and Helm on the `PATH`; the Makefile downloads kind. This command runs the focused Go suite, including the two-tenant isolation test; the CI workflow also runs `test/livekit/kind.sh` across connection modes and the quickstart, tutorial and uninstall scripts (`hack/docs/`). A compiled suite is not evidence of a successful cluster run |
| `go test ./<package> -run '^$' -fuzz '^<FuzzTest>$' -fuzztime 1m` | The fuzz targets hold for generated input: OpenBao paths, login paths and namespaces (`internal/openbao`), JWT audience parsing (`internal/connection`), the ownership path, field comparison and write-only hashing (`internal/engine`), and import names (`internal/importer`) | Seed corpora run in `make test`; the weekly and on-demand fuzz workflow explores new inputs |

## Live CRD coverage

`go test -tags integration ./test/livekit -run '^TestLiveInventory$' -count=1 -v`
checks the rendered overlays that the default live run actually executes against the CRD catalogue.
An example file alone does not count. The inventory currently classifies all 120 CRDs:

- 61 kinds must have an executable overlay scenario without a prerequisite exception.
- Three kinds, `DatabaseConnection`, `DatabaseRole` and `DatabaseStaticRole`, need PostgreSQL, so only the kind
  e2e run (`test/livekit/kind.sh`, which starts PostgreSQL) applies their overlays.
- Four built-in kinds use dedicated integration fixtures. `RabbitMQConfig` and `RabbitMQRole` have integration
  scenarios outside the default overlays, `Plugin` needs a plugin executable and checksum for registration, and
  `RaftAutopilot` needs Raft storage. `test/livekit/coverage-exclusions.yaml` names the test functions and their
  prerequisites; the inventory check verifies that those functions exist. Registration does not prove an external
  plugin can run.
- 52 external-plugin kinds require plugins and backends absent from the pinned OpenBao binary.
  Schema and payload checks cover these kinds, but do not constitute live coverage.

The full `make test-livekit` run requires at least one current Ready object for every overlay-covered
kind. Managed objects also need current `Synced=True`, except in Observe mode, where drift
may legitimately produce `Synced=False`. Readiness checks reject stale conditions from an earlier generation. Restricting the overlays
is useful for diagnosis, but does not prove complete coverage. Per-object prerequisite exceptions
remain visible in the reports; they are not successful reconciliations.

The live-kit CI job retains its runtime coverage in `e2e.md.coverage.json`, alongside the
per-object summary and logs. Missing reports fail the job.

The kind workflow exercises the full-field overlays through Kubernetes authentication against the
TLS active-node Service. Its result artifacts retain test logs, JUnit results and live-kit reports.
CI rejects an empty or skipped focused e2e suite. The conformance workflow retains its matrix and
logs and rejects a skipped matrix suite. Live evidence still requires the corresponding jobs to
finish successfully; inventory, compilation and workflow linting cannot substitute for that run.

## Scoped checks

When several changes are in progress in one tree, check only your packages:

```sh
gofmt -l api/<group> internal/controller/<group>
go vet ./api/<group>/... ./internal/controller/<group>/...
go test ./internal/controller/<group>/... -count=1
make bao && BAO_BIN="$PWD/bin/bao" go test -tags integration ./test/integration/... -run '<Test>' -count=1
git status --porcelain -- api config/crd/bases
```

The last command must list only your types, deepcopy and CRD files.

Generate with controller-gen scoped to your group, as described in [AGENTS.md](../../AGENTS.md).

## Evidence rules

- Changing an OpenBao endpoint or payload: add or update an adapter unit test with a fixture
  shaped like a real read, not derived from the code under test. Cover the custom rules with
  negative cases: write-only keys are not compared, aliases map read names to write names, sets
  ignore order, durations compare in seconds.
- Changing API markers or CEL: regenerate, inspect the CRD and RBAC diff, and add an envtest case
  that a valid object is accepted and an invalid one is rejected with the expected message.
- Steady state: an integration scenario lets an unchanged object pass several drift intervals and
  asserts that no write happened, for example through `openbao_writes_total{kind}` or a value a
  rewrite would reset.
- Delete behaviour: assert that `Delete` removes the object and that `Orphan` leaves it in OpenBao
  after the custom resource is gone.
- External-plugin kinds cannot run against `bao server -dev`; they get a golden payload test, and
  the gap is recorded in the [tech-debt register](tech-debt.md).
- Live tests use a local `bao server -dev` or the disposable kind cluster. Never point them at a
  shared or production OpenBao or Kubernetes cluster.
- Bumping the pinned OpenBao release (`BAO_VERSION` in the Makefile and the image in
  `hack/e2e/openbao.yaml`): run `make test-integration` and record any changed wire shape in
  the affected adapter tests.
