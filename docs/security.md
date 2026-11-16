# Security

This page describes what the operator protects, from whom, and how. It also explains how to
verify a release. To report a vulnerability, see [SECURITY.md](../SECURITY.md).

The full threat model, with data flows, trust boundaries, STRIDE threats per boundary and the
open gaps, is [security/threat-model.md](security/threat-model.md). The deployment checklist that
follows from it is [security/hardening.md](security/hardening.md).

## Threat model

### Assets

| asset | where it lives |
|-------|----------------|
| OpenBao configuration: policies, auth roles, mounts, identities; together they decide who can read which secret | OpenBao, declared by custom resources |
| connection credentials: tokens, AppRole secret IDs, ServiceAccount tokens | Kubernetes Secrets, the TokenRequest API, files in the operator pod |
| secret inputs: passwords, JWTs, client secrets | Kubernetes Secrets referenced by `<field>SecretRef` |
| one-time outputs: secret IDs, OIDC client secrets, exported private keys | Kubernetes Secrets written through `writeSecretRef` |
| the hash key of `status.writeOnlyHash` | the hash-key Secret in the operator namespace |

### Actors

| actor | can | cannot |
|-------|-----|--------|
| platform admin | create connections of both kinds, install the operator, write OpenBao policies | - |
| tenant (namespace admin or editor) | create and edit resources in its namespace through the `<release>-tenant-editor` role, aggregated into `admin` and `edit` | create or edit connections, write resource status, act with any OpenBao identity but the one of its namespace's connection |
| compromised tenant | everything the tenant's OpenBao policy allows, inside the tenant's OpenBao namespace | reach the root or a sibling OpenBao namespace, borrow the operator's identity, read Secrets of other Kubernetes namespaces through the operator |
| compromised operator pod | everything its Kubernetes RBAC and every connection's OpenBao policy allow | - |

The OpenBao policy behind each connection identity is the real limit. The operator adds no
authority of its own on top of it.

## The tenant boundary

- A namespaced `OpenBaoConnection` serves only resources in its own namespace.
- Its Kubernetes and JWT auth must name a `serviceAccountRef` in its own namespace. The CRD
  rejects a connection without one, and the resolver refuses it again
  (`serviceAccountRef is required for a namespaced connection`) if the CRD validation was
  bypassed. The operator requests a 10-minute token for that ServiceAccount through the
  TokenRequest API and uses it for one login. A namespaced connection can never log in as the
  operator.
- `audiences` is required on those connections, so the token sent to OpenBao is never valid
  against the Kubernetes API server. Connections have no address: only the platform's
  `OpenBaoServer` sets where the token goes, and it must be https unless `allowInsecureHTTP` is set.
- Injected identity and token files exist only on `ClusterOpenBaoConnection`, which only platform
  admins create. `spec.allowedNamespaces` limits which namespaces may use it; without it, no
  namespace can, and `{}` admits every namespace.
- The `<release>-tenant-editor` role grants every namespaced kind except `OpenBaoConnection`, and
  only `get` on status subresources. Tenants therefore cannot choose an identity, and cannot forge
  `status.path` to steer a deletion.
- Secret references in a resource resolve in the resource's own namespace. A tenant cannot make
  the operator read another namespace's Secret.
- `writeSecretRef` never takes over an existing Secret that the resource does not own: the
  operator fails with "the Secret exists and is not controlled by this resource".

See [ADR 0003](adr/0003-connections-and-tenant-boundary.md) for the decision.

## OpenBao namespace escape

A resource's `spec.namespace` is relative to its connection's `namespace`. Four checks keep it
inside:

1. The CRD pattern allows only segments that start with a letter, digit, `_` or `-`, separated by
   single slashes. A leading slash, an empty segment, `.` and `..` are rejected at admission. The
   connection's `namespace` and `authNamespace` have the same pattern.
2. The engine rejects a namespace with an empty, `.` or `..` segment, or a first segment `root`,
   with the reason `InvalidSpec`.
3. The client joins the relative namespace to the base without resolving `.` or `..`. A `..`
   segment that reached it anyway becomes an invalid segment, which OpenBao refuses, so the
   request fails instead of climbing out of the base namespace.
4. On deletion, the namespace recorded in `status.namespace` must lie inside the connection's
   current namespace. Otherwise the operator refuses to delete and asks for
   `deletionPolicy: Orphan`.

`spec.namespace` and `connectionRef` are immutable. If the connection's own `namespace` changes
after a resource acquired its object, the resource stops with `NamespaceChanged` instead of
writing a second object elsewhere.

## Write-only inputs

OpenBao does not return passwords, JWTs and client secrets on read, so the operator cannot compare
them. It records `status.writeOnlyHash` instead and writes again when the hash changes.

The hash is an HMAC-SHA256 of the canonical JSON of the write-only values, keyed by a random
32-byte key in the hash-key Secret (`openbao-config-operator-hash-key` by default, set with
`--hash-key-secret`). Anyone who can read the status therefore cannot test guesses of a
low-entropy password offline. The manager creates the key when the Secret is absent, as an
immutable Secret. Protect it like any other Secret in the operator namespace.

## Logs, events and status

- Logs never contain tokens, secret values or OpenBao response bodies.
- OpenBao errors keep only OpenBao's error strings, never a raw body. Write-only values that
  OpenBao echoes in an error message are replaced with `<redacted>` before the error reaches the
  status or the logs.
- `Updated` events name the keys that differed, never their values.
- Status holds paths, IDs, accessors and the HMAC, never secret values.
- The operator does not copy secret values from OpenBao into Kubernetes. It writes a Secret only
  for a value OpenBao returns once ([ADR 0007](adr/0007-no-secret-sync.md)).

## Environment isolation

- Run one operator installation per environment, and give each connection only the OpenBao it
  belongs to. A development cluster must not hold a connection to production OpenBao.
- Use a separate OpenBao identity, and policy, per connection. Do not share a token between
  connections or environments.
- Select only platform namespaces in a `ClusterOpenBaoConnection`'s `allowedNamespaces`; never use `{}`.
- Secret values stay in OpenBao. Workloads read them with External Secrets Operator or an OpenBao
  agent, with their own identity, not through this operator.

## Kubernetes RBAC

| scope | grants | when |
|-------|--------|------|
| ClusterRole `<fullname>-manager` | `OpenBaoServer` and `ClusterOpenBaoConnection` with their status and finalizers; `get`, `list`, `watch` on Namespaces; `GET /.well-known/openid-configuration` | always |
| same ClusterRole | the namespaced kinds with their status and finalizers; Secrets (`get`, `list`, `watch`, `create`, `update`, `patch`), ConfigMaps (read), `serviceaccounts/token` (`create`), Events | `watchNamespaces` empty |
| Role `<fullname>-manager` per listed namespace | the namespaced rules of the previous row | `watchNamespaces` set |
| Role `<fullname>-manager` in the release namespace | `get` on the hash-key Secret, `create` on Secrets | `watchNamespaces` set without the release namespace |
| Role `<fullname>-leader-election` | Leases and Events in the release namespace | `leaderElection.enabled` |
| ClusterRoles `<fullname>-metrics-auth`, `<fullname>-metrics-reader` | TokenReviews and SubjectAccessReviews for the metrics endpoint; `GET /metrics` for scrapers | `metrics.enabled` |

`create` on `serviceaccounts/token` lets the manager mint a token for any ServiceAccount in the
namespaces it covers. Set `watchNamespaces` to the namespaces that hold connections and their
references, so a compromised manager cannot impersonate ServiceAccounts elsewhere. The default install is
cluster-wide, which the threat model lists as residual risk
[RR-01](security/threat-model.md#residual-risks-and-recommendations).

The manager pod runs as non-root user 65532 on a distroless image, with a read-only root
filesystem, no capabilities, no privilege escalation and the `RuntimeDefault` seccomp profile.
The metrics endpoint requires an authenticated and authorised client. HTTP/2 is off by default on
the metrics server (`--enable-http2`).

## Supply chain

- Images are built for amd64 and arm64 from base images pinned by digest; the runtime image is
  distroless and runs as 65532. Builds are reproducible: `SOURCE_DATE_EPOCH` from the commit,
  `-trimpath`, no VCS stamping, rewritten layer timestamps, OCI labels from the build arguments.
- The release workflow runs only on a `v*` tag, after the full CI suite, and refuses to overwrite an
  existing release, image tag or chart version. It pushes the image by digest, gates on high and
  critical vulnerabilities (`hack/release/scan.sh`, dated and justified exceptions only), then
  signs and attests before it tags.
- The image index, each platform image and the chart are signed with keyless cosign, using the
  workflow's GitHub OIDC identity, so no cosign key exists to leak.
- From v0.3.0 the chart also carries a Helm provenance file (`.tgz.prov`) signed with a GPG key
  that is stored as a secret of the `release` environment. Artifact Hub shows the chart as signed
  because of it. The GPG signature proves who packaged the chart; the cosign signature also proves
  which workflow run built it.
- SPDX and CycloneDX SBOMs and build provenance (its two forms and their SLSA levels are in
  [verify the release](security/hardening.md#verify-the-release)) are attached as signed attestations; the chart carries a
  CycloneDX SBOM of itself and the image it pins. Release assets are covered by a signed
  `sha256sums.txt`.
- The released chart pins the image index by digest.
- Workflow actions are pinned by commit SHA, workflow and Makefile container images by digest,
  downloaded tools by version and checksum. Renovate updates all of them after a minimum release
  age of three days.
- `make release-dry-run` (and a manual run of the release workflow) performs every step locally
  with a throwaway key and verifies the result, without publishing.

### Verifying releases

The commands, and a script that runs all of them, are in
[hardening.md](security/hardening.md#verify-the-release).
