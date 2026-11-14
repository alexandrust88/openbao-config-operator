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
| `make manifests generate chart-sync` | CRDs, RBAC, deepcopy code and the chart copies can be regenerated | Does not prove the generated output was committed |
| `make verify-generated` | Regenerating changes nothing tracked under `api`, `config` and `charts`, and leaves no untracked generated file | Requires a Git checkout; regenerates every package, so run it only when you own the whole tree |
| `make govulncheck` | No known vulnerability is reachable from the code | Only covers vulnerabilities in the Go vulnerability database |
| `make helm-lint` | The chart lints in strict mode and renders with default values | Does not install the chart |
| `make validate-manifests` | The kustomize and Helm output validates against the Kubernetes schemas | Custom resources without a published schema are skipped |
| `make check` | `fmt-check`, `vet`, `lint`, `test` and `verify-generated`: the pull request gate without integration and e2e | Starts no OpenBao and no cluster |
| `make api-coverage` | Every writable path of each OpenAPI snapshot in `api-coverage/` (2.6 and 2.7) has exactly one class with a reason, every typed kind named there has a CRD, and `docs/api-coverage.md` is regenerated; CI fails when the report changes | Path level only: a new request field on a typed path is not detected ([BL-014](backlog.md#bl-014-detect-openbao-api-changes-automatically)) |
| `make test-samples` | Every generated sample in `examples-generated/` passes server-side validation, CEL included, against envtest with the real CRDs | No OpenBao; not run by a CI workflow |
| `bin/helm unittest charts/openbao-config-operator` | The chart's unit tests in `charts/openbao-config-operator/tests` pass: CRDs, deployment, RBAC, plugin kinds and resources render as asserted | Rendering only; no install. CI installs the helm-unittest plugin before this step; locally install it yourself |
| `make test-namespaces` | Every built-in kind through each connection mode (root, child through `spec.namespace`, connection based in a child, AppRole login inside a child) against one `bao server -dev` per mode and the manager built from `./cmd`; logins pointed outside their namespace are refused without a write, and the audit log shows where each object was written | Dev mode only; skips locally without `bao` (fails in CI) |
| `make test-conformance` | Every built-in kind passes the no-write test (examples applied, a further drift pass writes nothing) and the default test (minimal spec, then each optional field set and removed) against `bao server -dev` and the manager built from `./cmd` | Runs on push to `main`, nightly and on demand, not on pull requests; kinds in `test/conformance/exceptions.yaml` are `n/a` for the named test |
| `make test-livekit` | The live kit in `examples/live` renders with kustomize, every built-in kind has a minimal and a full example that sets every spec field, every rendered object passes server-side validation, and the root and child overlays reach `Ready` against `bao server -dev` | Logins are rewritten to AppRole, so the kit's Kubernetes auth is not exercised; not run by a CI workflow |
| `make test-e2e` | In a kind cluster, the suite deploys an OpenBao dev server and installs the Helm chart; the manager runs with two ready replicas and a leader lease; a namespaced `OpenBaoConnection` logs in with Kubernetes auth as the tenant ServiceAccount; a `Policy`, a `KVSecret` and an `OpenBaoEndpoint` are written to OpenBao; a policy deleted in OpenBao is restored; a rotated source Secret is rewritten; the KV version stays unchanged for 40 seconds in steady state; deleting the resources removes the objects from OpenBao | Dev mode OpenBao; no second tenant, so it does not prove that a tenant cannot write outside its policy; needs Docker and kind, and deletes its kind cluster afterwards |
| `go test ./<package> -run '^$' -fuzz '^<FuzzTest>$' -fuzztime 1m` | The fuzz targets hold for generated input: OpenBao paths, login paths and namespaces (`internal/openbao`), JWT audience parsing (`internal/connection`), the ownership path, field comparison and write-only hashing (`internal/engine`), and import names (`internal/importer`) | Only the seed corpora run, as part of `make test`; no CI job fuzzes |

## Scoped checks

When several changes are in progress in one tree, check only your packages:

```sh
gofmt -l api/<group> internal/controller/<group>
go vet ./api/<group>/... ./internal/controller/<group>/...
go test ./internal/controller/<group>/... -count=1
make bao && BAO_BIN="$PWD/bin/bao" go test -tags integration ./test/integration/... -run '<Test>' -count=1
git status --porcelain -- api config/crd/bases   # only your types, deepcopy and CRD files changed
```

Generate with controller-gen scoped to your group, as described in [AGENTS.md](../AGENTS.md).

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
  `hack/e2e/openbao-dev.yaml`): run `make test-integration` and record any changed wire shape in
  the affected adapter tests.
