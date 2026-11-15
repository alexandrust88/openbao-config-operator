# Tech-debt register

This register lists material limitations of the current implementation and its verification.
Planned product outcomes belong in the [backlog](backlog.md); accepted design choices belong in
the [ADRs](../adr/). Close an entry by meeting its exit criteria, then delete it or mark it
complete with a pointer to the evidence.

## TD-001: Live coverage uses OpenBao dev mode

Status: open

Integration tests run each kind against `bao server -dev`: in-memory storage, a root token, one
node, no TLS. The e2e test (`test/e2e`) runs the manager in kind, where a namespaced connection logs
in with Kubernetes auth through TokenRequest as the tenant ServiceAccount, under a non-root tenant
policy, and reconciles a `Policy`, a `KVSecret` and an `OpenBaoEndpoint` through create, drift
repair, steady state and delete; a two-tenant e2e test (`test/e2e/isolation_test.go`) proves that a second tenant's writes outside its OpenBao namespace and policy, its references to another tenant's objects and its use of a server that excludes it are refused, and that none of the objects it tried to create exists in OpenBao.

Exit criteria: the per-kind tests run against a server with persistent storage, TLS and a
non-root token. The second-tenant scenario is done (`test/e2e/isolation_test.go`). Storage, HA,
TLS and backup behaviour of the server stay out of scope.

## TD-002: Compatibility with other servers was claimed but not tested

Status: closed by [ADR 0009](../adr/0009-openbao-only-target.md). The project targets OpenBao only
and makes no compatibility claim for other servers.

## TD-003: No OpenAPI field coverage check

Status: open

Path-level coverage is checked: `api-coverage/` holds the OpenAPI snapshots for OpenBao 2.6 and 2.7,
and `make api-coverage` (CI job `api-coverage`) and `test/apicoverage` fail when a writable path is
unclassified. Field-level coverage is not checked, so a request field added or renamed upstream on a
typed path is noticed only by hand.

Exit criteria: a check that lists the request fields of each typed path that map to no CRD field
and have no recorded exclusion ([BL-014](backlog.md#bl-014-detect-openbao-api-changes-automatically)).

## TD-004: Unconfirmed paths and external-plugin kinds

Status: open

Rows marked `(u)` in the [kind catalogue](../kinds.md) take their path from documentation, not from
OpenBao source. External-plugin kinds cannot run against
`bao server -dev`, so they are verified by golden payload tests only.

Exit criteria: before an external-plugin kind is un-gated ([phase 6](../maintainers/roadmap.md#phases)), confirm its path in the plugin source and run
it once against a server with the plugin registered through `Plugin`; remove the `(u)` mark.

## TD-005: The default install can read Secrets cluster-wide

Status: open; mitigated by `watchNamespaces`

Without `watchNamespaces`, the manager ClusterRole can get, list and watch Secrets and
ConfigMaps and request ServiceAccount tokens in every namespace, because any namespace may hold
a connection or a referenced Secret. The tenant boundary is enforced by the OpenBao identity of
each connection, not by Kubernetes RBAC. Setting `watchNamespaces` in the Helm chart replaces the
cluster-wide grants with a Role per listed namespace.

Exit criteria: keep the scoped install covered by `make validate-manifests`, document which
install mode fits which tenancy model, and revisit only if a shared installation needs
controller-enforced Secret scoping.

## TD-006: The docs site depends on Material for MkDocs, which reaches end of life

Status: open

The documentation site is built with MkDocs and the Material theme (`mkdocs.yml`,
`hack/docs/requirements.txt`). Material for MkDocs is in maintenance mode and reaches end of life on
2027-05-05; its authors point users to Zensical as the successor.

Exit criteria: before 2027-05-05, build the site with Zensical (or another maintained generator),
keep `mkdocs build --strict` equivalent link checking in CI, and drop the Material pin.

## TD-007: Some adapters read OpenBao and Kubernetes outside the engine's resolver

Status: open

`Desired` gets no OpenBao client, so seven adapters resolve their connection themselves inside `Desired`
through `refs.For` and an adapter-held `engine.Connections` (GroupMembership, EntityAlias, GroupAlias,
OIDCAssignment, OIDCProvider, MFALoginEnforcement, Policy with `accessorRefs`), and AuthMount and SecretsMount
read Plugins through an adapter-held `client.Reader` instead of `Resolver.Object`. These reads bypass the
client of the pass (request attribution, priority, the namespace and server checks done in `connect`) and the
read-only resolver of `managementPolicy: Observe`, and a zero-value adapter fails with "needs a reader".

Two ways out, both a contract change for the maintainers:

- pass the pass's namespace-scoped `openbao.Client` to `Desired` (or to a new optional `DesiredWithClient`), and
  route every Kubernetes read through `Resolver.Object`;
- move the OpenBao name lookups into `PathResolver` or `Observer`, which already get the client, and keep
  `Desired` pure.

Exit criteria: no adapter holds `engine.Connections` or a `client.Reader` for use in `Desired`.

## TD-008: A KV history purge can destroy a version written right before it

Status: open

A destructive delete of a KVSecret or RandomSecret that created its KV version 2 path reads
`<mount>/data/<path>`, and when no live key remains it sends `DELETE <mount>/metadata/<path>`. A version
another writer creates between that read and the DELETE (one request apart) is destroyed with the history.
OpenBao cannot close the window: `pathMetadataDelete` takes only the path, with no check-and-set or version
condition, and deletes every version. The race-free alternative, `POST <mount>/destroy/<path>` for the
versions up to the one read empty, keeps newer versions but leaves the metadata in place and needs `update` on
`<kv-mount>/destroy/*` in the operator policy.

Exit criteria: OpenBao offers a conditional metadata delete and the purge uses it, or the maintainers accept
the destroy-versions semantics and policy.
