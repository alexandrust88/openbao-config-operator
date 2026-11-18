# AGENTS.md

Guide for coding agents in this repository. Humans follow the same rules
([CONTRIBUTING.md](CONTRIBUTING.md)). Read [docs/architecture.md](docs/architecture.md) and
[docs/adr/](docs/adr/) before changing behaviour; a change that contradicts an ADR needs a new ADR.

## Orientation

A Go operator that manages the configuration of an existing OpenBao server through namespaced
CRDs. One custom resource is one object at one API path. OpenBao is the only target
([ADR 0009](docs/adr/0009-openbao-only-target.md)).

- One generic reconciler in `internal/engine` plus one small adapter per kind
  ([ADR 0002](docs/adr/0002-generic-engine-with-adapters.md)). The engine owns connections,
  finalizers, creation and deletion policy, conditions, events, metrics and requeues. Adapters own
  only the path, the payload and the comparison rules.
- `BaoEndpoint` writes any payload to any path ([ADR 0006](docs/adr/0006-endpoint-escape-hatch.md)).
  One-off actions and rarely used paths get no typed kind; `BaoEndpoint` covers them. Paths
  that OpenBao does not have are not supported.
- Out of scope: server lifecycle (deploy, init, unseal, raft, upgrade) and syncing secret values
  into Kubernetes ([ADR 0007](docs/adr/0007-no-secret-sync.md)). The only Secrets the operator
  writes hold values OpenBao returns once, through `writeSecretRef`.

```text
cmd/main.go                    manager wiring, flags, metrics, health
cmd/controllers.go             API schemes and the Setup<Kind> call of every kind
cmd/obc-import                 import CLI: builds Observe-mode resources from a live server
api/common/v1alpha1            ResourceSpec, ResourceStatus, refs, condition reasons
api/<group>/v1alpha1           CRD types per group (core, sys, auth, secrets, identity, access)
internal/openbao               client, auth, token renewal, per-connection cache, retries, metrics
internal/connection            connectionRef -> authenticated client, tenant boundary
internal/engine                generic reconciler, adapter interfaces, FieldSpec compare, Secret index
internal/refs                  flexible reference resolution (literal, OpenBao name, Kubernetes ref, selector)
internal/importer              reads a live server for obc-import
internal/controller/<group>    one adapter file and Setup<Kind> func per kind
test/integration               envtest + real `bao server -dev` (build tag integration)
test/e2e                       kind cluster + Helm chart + OpenBao (build tag e2e)
config/, charts/               kustomize and Helm; CRDs and RBAC in both are generated
docs/                          architecture, kind catalogue, ADRs, backlog, tech debt
```

`api/` imports nothing from `internal/`; other projects import the types as a public API.

## Sources of truth and boundaries

- Contracts shared by every kind; change them only on purpose, in a separate change, together
  with docs/architecture.md:
  - `api/common/v1alpha1/types.go`: `ResourceSpec`, `ResourceStatus`, condition types, reasons.
    Adapters never invent a reason string.
  - `internal/engine/contract.go`: `Adapter`, `FieldSpec`, `FieldsProvider`, `Observer`,
    `Writer`, `OutputWriter`, `Deleter`, `StatusWriter`, `Resolver`, `Connections`,
    `ErrInProgress`, `ErrReferenceNotReady`.
  - `internal/openbao/contract.go`: the narrow `Client`, `ErrNotFound`, `Auth`.
- Generated, never edited by hand: `api/**/zz_generated.deepcopy.go`, `config/crd/bases/`,
  `config/rbac/role.yaml`, `charts/openbao-config-operator/templates/crds/` and
  `charts/openbao-config-operator/templates/rbac/_manager-rules.tpl`. Change the markers or the
  source, then regenerate.
- `PROJECT` is kubebuilder metadata; change it only when the API inventory changes.
- Preserve unrelated work in a dirty tree; other contributors may be editing other packages.

## Adding a kind

1. Find the row in [docs/kinds.md](docs/kinds.md): name, group, path, availability, phase.
   `not-in-openbao` and `action-not-config` rows get no typed kind.
2. Read the real wire shape in the OpenBao source and with `bao read -format=json` against
   `bao server -dev`. List write-only, create-only, renamed-on-read, unordered and duration fields
   before writing types.
3. Types in `api/<group>/v1alpha1/<kind>_types.go`, written from
   `api/sys/v1alpha1/aclpolicy_types.go`:
   - embed `commonv1alpha1.ResourceSpec` and `ResourceStatus` with `json:",inline"`, implement
     `GetResourceSpec` and `GetResourceStatus`;
   - camelCase fields named after the API keys; secret input only as `<field>SecretRef`
     (`SecretKeySelector`), never a plain-text field;
   - cross-object fields are flexible references: exactly one of a literal (`mountAccessor`,
     `entityID`), an OpenBao name or path (`mountPath`, `entityName`), a same-namespace
     Kubernetes ref (`mountRef`, `entityRef`) or a label selector (`mountSelector`), enforced
     by CEL;
   - markers (`MinLength`, `MaxItems`, `Enum`, `Pattern`, `listType=set`), CEL immutability
     (`self == oldSelf`) for the name, mount paths and type, and a bound (`maxItems`,
     `maxLength`) on every list or string a CEL rule touches;
   - `+kubebuilder:resource:categories=obco;obco-<group>`, status subresource, `Ready`, `Synced` and `Age`
     print columns;
   - doc comments become CRD descriptions: write them for the user ("the OpenBao role").
4. Adapter in `internal/controller/<group>/<kind>.go`: RBAC markers, `Setup<Kind>`, `Path`,
   `Desired` and `Fields`. Every kind defaults to `deletionPolicy: Orphan`; users opt into
   `Delete` on each resource. Add an optional interface only when the default read, write or delete is wrong. Every write-only,
   create-only, aliased, unordered or duration field goes in `FieldSpec`. Resolve references
   through the `Resolver` and wrap `engine.ErrReferenceNotReady` when a referent is missing or not
   `Ready`.
5. Register the scheme and `Setup<Kind>` in `cmd/controllers.go`.
6. Generate (below), add `examples/<group>/<kind>.yaml` setting every spec field (validated by a
   server-side dry-run test in `test/examples/`), add a `minimal` and a `full` example of a built-in
   kind to the live kit (`examples/live/components/`, listed in `examples/live/coverage.md`;
   `make test-livekit` fails without them), run `make samples`, and update the docs/kinds.md row if
   the path, availability or notes changed.
7. Tests: unit tests for `Desired` and `FieldSpec` against a read-shaped fixture, envtest for the
   CEL rules, and an integration scenario for builtin kinds (create, steady state without a write,
   drift repair, delete per policy). External-plugin kinds get a golden payload test instead.

## Generation

Scope controller-gen to the group you changed. Never pass `paths=./...`: it loads every package
and directory under the root and can write unrelated CRDs into `config/crd/bases`.

```sh
./bin/controller-gen object:headerFile=hack/boilerplate.go.txt paths=./api/<group>/...
./bin/controller-gen crd paths=./api/<group>/... output:crd:artifacts:config=config/crd/bases
```

`make manifests generate chart-sync` regenerates CRDs, RBAC, deepcopy code and the chart copies
for the whole tree; use it when you own the whole change. CI runs `make verify-generated` and
fails on any diff or untracked generated file.

## Verification

Run the narrowest check that proves the change first, then widen. Report the exact commands and
their results; a skipped test is not a pass. What each check proves, and what it does not, is in
[docs/project/verification.md](docs/project/verification.md).

```sh
gofmt -l api/<group> internal/controller/<group>
go build ./...
go vet ./internal/controller/<group>/...
go test ./internal/controller/<group>/... -count=1
make setup-envtest
KUBEBUILDER_ASSETS="$(./bin/setup-envtest use 1.35.0 --bin-dir "$PWD/bin" -p path)" \
  go test ./internal/controller/<group>/... -count=1
make bao && BAO_BIN="$PWD/bin/bao" \
  go test -tags integration ./test/integration/... -run '<Test>' -count=1
make check
make test-integration
make test-e2e
```

- `make setup-envtest` downloads envtest binaries for the Kubernetes minor of `k8s.io/api` in `go.mod`
  (1.35 today); the next command runs the envtest and CEL tests with them.
- The integration run finds the envtest binaries under `bin/k8s`.
- `make check` is the fast subset of the pull request gate: `fmt-check`, `vet`, `mod-check`, `lint`,
  `test`, `test-docs`, `verify-generated`, `api-coverage`, `helm-lint` and `validate-manifests`. It
  starts no cluster; `test` starts `bao server -dev` for a few packages when
  `bao` is available and skips them otherwise.
- `make test-integration` runs every scenario against the pinned `bao` binary.
- `make test-e2e` needs Docker, kind, kubectl and Helm; it creates and deletes a local kind cluster.

## Hard rules

- No secrets anywhere visible: never tokens, secret values or OpenBao response bodies in status,
  conditions, events, logs, metrics, error messages, samples, fixtures or docs. Events name the
  keys that differ, never their values. `status.writeOnlyHash` is the only trace of a secret
  input. Samples and fixtures use obviously fake values in separate Secret documents.
- Write-only inputs are hashed, never compared and never read back to check them.
- Steady state writes nothing: an unchanged object must give an empty diff against the real read.
  `obco_writes_total{kind}` stays flat at rest. A kind that rewrites on every drift check is a bug.
- A 404 is absent; every other status is an error. Never treat 400 as not found globally; a kind
  that needs it handles it in its own `Observer`.
- Validation is OpenAPI markers and CEL in the CRD. No admission webhooks
  ([ADR 0005](docs/adr/0005-cel-validation-without-webhooks.md)).
- Adapters are stateless: no I/O in `Path`, `Desired` or `Fields` except through the `Resolver`,
  never mutate `obj.Spec`, never set conditions, finalizers or requeues.
- Never call one-shot actions from a reconcile (rotate, generate, sign, issue, login, tidy);
  model the configuration endpoint only.
- A namespaced connection never logs in with the operator's own ServiceAccount.
- Never `RequeueAfter` together with a non-nil error.
- Pinned OpenBao: `BAO_VERSION` in the Makefile and the image tag and digest in
  `hack/e2e/openbao.yaml` move together, never to a mutable tag. No version-branching shims
  in adapters; a missing feature reports `Unsupported`.
- Keep live tests narrow: each test starts its own `bao server -dev` on a free local port, skips
  cleanly when `bao` or the envtest binaries are absent, and stops the server on cleanup. Add an
  e2e scenario only for wiring that envtest and `bao -dev` cannot show. Never point a test at a
  shared or production server.
- Comments: none for a localized change unless they explain a non-obvious why; never more than a
  few lines; no change history. Exported identifiers get a one-line doc comment.
- Smallest coherent diff: no unrelated reformatting, renames or refactors, no speculative options,
  no new production dependency without a stated reason.
- Log messages start with a capital letter, have no trailing period and use key/value pairs.
- Lowercase file names, no emojis, no new root-level files without a reason.

## Project knowledge

| file | holds |
|------|-------|
| [docs/architecture.md](docs/architecture.md) | scope, design rules, components, reconcile flow, CI gates |
| [docs/maintainers/roadmap.md](docs/maintainers/roadmap.md) | phases and the order of planned work |
| [docs/kinds.md](docs/kinds.md) | the catalogue of kinds: path, availability, phase |
| [docs/adr/](docs/adr/) | accepted decisions and their rationale |
| [docs/project/verification.md](docs/project/verification.md) | what each check proves and its limits |
| [docs/project/backlog.md](docs/project/backlog.md) | planned outcomes not implemented yet |
| [docs/project/tech-debt.md](docs/project/tech-debt.md) | known current limitations and their exit criteria |

Keep these categories separate: product promises and design in the architecture, rationale in
ADRs, planned outcomes in the backlog, unresolved shortcomings in
the tech-debt register, and implementation detail in code and tests. Update or remove stale
guidance instead of appending exceptions.

## References

Read these before changing the engine, an adapter or an API type; this file and `docs/architecture.md` win where
they are stricter.

- [Kubebuilder book](https://book.kubebuilder.io), and its
  [good practices](https://book.kubebuilder.io/reference/good-practices.html) (idempotent reconciliation, status
  conditions) and [markers reference](https://book.kubebuilder.io/reference/markers.html).
- [controller-runtime FAQ](https://github.com/kubernetes-sigs/controller-runtime/blob/main/FAQ.md): common
  patterns and questions.
- [Kubernetes API conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md)
  and the [operator pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/).
- [Logging conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-instrumentation/logging.md#message-style-guidelines):
  message style and verbosity levels.
- Source: [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime),
  [controller-tools](https://github.com/kubernetes-sigs/controller-tools),
  [Kubebuilder](https://github.com/kubernetes-sigs/kubebuilder).
