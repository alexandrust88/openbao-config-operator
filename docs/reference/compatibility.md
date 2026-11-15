# Compatibility

The versions and platforms each release supports and the ones CI tests. Sources: the chart's
`Chart.yaml`, the `Makefile` tool pins, `.github/workflows/release.yml`,
[ADR 0009](../adr/0009-openbao-only-target.md), [ADR 0023](../adr/0023-openbao-api-changes-and-crd-lifecycle.md)
and the [release process](../maintainers/release-process.md#openbao-versions).

## Summary

| component | supported | tested in CI |
|-----------|-----------|--------------|
| OpenBao | 2.6 and 2.7 (minimum 2.6) | 2.7.2: unit, envtest and integration tests run against `bao server -dev` (`BAO_VERSION` in the `Makefile`); the e2e test runs `ghcr.io/openbao/openbao:2.7.2` in kind (`hack/e2e/openbao.yaml`). 2.6 is covered by its OpenAPI snapshot (`api-coverage/openapi-2.6.json`) only |
| HashiCorp Vault | not supported | not tested |
| Kubernetes | 1.31 or later (chart `kubeVersion: ">=1.31.0-0"`) | envtest 1.35 (from `k8s.io/api` v0.35 in `go.mod`); kind node `kindest/node:v1.35.8` with kind v0.33.0 in the e2e workflow |
| Helm | 3.8 or later, for OCI charts | Helm v4.3.1 (`HELM_VERSION` in the `Makefile`) for lint, unit tests and e2e |
| Image architectures | `linux/amd64`, `linux/arm64` | both are built and checked by `hack/release/verify-release.sh` |

## OpenBao

- OpenBao is the only target ([ADR 0009](../adr/0009-openbao-only-target.md)). The design relies on
  OpenBao features: self-init, namespaces, horizontal read scaling and OCI plugins. There is no
  flavour detection.
- Each operator minor supports the OpenBao minors that have an API snapshot in `api-coverage/` and
  are listed in its release notes; today `openapi-2.6.json` and `openapi-2.7.json`.
- The integration suite runs against the single pinned version, 2.7.2. A scheduled run against every
  supported minor is not in place yet
  ([BL-014](../project/backlog.md#bl-014-detect-openbao-api-changes-automatically)).
- Adapters do not branch on the server version. A field or endpoint the server lacks shows as the
  reason `Unsupported` ([ADR 0023](../adr/0023-openbao-api-changes-and-crd-lifecycle.md)).
- `OpenBaoServer.status.version` reports the version the server returns from `sys/health`.
- A new OpenBao minor is supported in the next operator minor after its snapshot is added and
  classified, with a target of six weeks after the OpenBao release. An OpenBao minor is dropped in
  the first operator minor after upstream stops releasing fixes for it, announced one minor ahead
  ([release process](../maintainers/release-process.md#openbao-versions)).

### OpenBao features the operator depends on

| feature | needed for |
|---------|------------|
| cluster id in `sys/health` | binding acquired objects to one server (`status.serverID`) |
| namespaces | `OpenBaoNamespace`, `spec.namespace`, and the connection `namespace` and `authNamespace` |
| self-init | starting without a root token at runtime ([self-init guide](../guides/self-init.md)) |
| `sys/workflows` | the `Workflow` kind |
| external plugins | the external-plugin kinds; without the plugin they report `Unsupported` |

## HashiCorp Vault

Vault is neither tested nor promised ([ADR 0009](../adr/0009-openbao-only-target.md)). There are no
Vault Enterprise kinds, and `OpenBaoEndpoint` does not support Vault-only paths. Features OpenBao
does not have (Sentinel, KMIP, Transform, managed keys, replication) get no kind.

## Kubernetes

- The chart requires Kubernetes 1.31 or later: CRD validation rules use the CEL IP and CIDR
  library, which the API server accepts in new CRD expressions only from 1.31.
- Tests run on the envtest version that matches `k8s.io/api` in `go.mod` (1.35) and on kind in the
  e2e workflow.
- The project supports the Kubernetes minors that are supported upstream when an operator minor is
  released, and raises the chart's `kubeVersion` only in a minor release, with a release note.
- No admission webhooks are installed, so no cert-manager is needed
  ([ADR 0005](../adr/0005-cel-validation-without-webhooks.md)).
- All CRDs are `v1alpha1`; versions change additively without a conversion webhook
  ([ADR 0020](../adr/0020-crd-versioning-without-webhooks.md)).

## Helm and install methods

| method | artefact |
|--------|----------|
| Helm, OCI | `oci://ghcr.io/alexandrust88/charts/openbao-config-operator`, version `@VERSION@` |
| plain manifests | `install.yaml` and `crds.yaml` attached to the GitHub release `@TAG@` |

The chart ships the CRDs as templates, so `helm upgrade` upgrades them; `crds.keep` (default `true`)
annotates them with `helm.sh/resource-policy: keep`. The [install guide](../get-started/install.md)
has the commands.

## Image

| property | value |
|----------|-------|
| registry | `ghcr.io/alexandrust88/openbao-config-operator` |
| platforms | `linux/amd64`, `linux/arm64` (one multi-arch index) |
| base | `gcr.io/distroless/static:nonroot`, user 65532 |
| reference | by version tag; the released chart pins the image by digest |
| signing | keyless cosign, with an SBOM attestation and build provenance |

## Kind availability classes

Each kind in the [kind catalogue](../kinds.md) has an availability class.

| class | meaning | default | tested |
|-------|---------|---------|--------|
| `builtin` | the object is compiled into OpenBao 2.7 (core or the built-in plugin registry) | enabled | every built-in kind is integration-tested against `bao server -dev` |
| `external-plugin` | needs a plugin from openbao-plugins or a third party, registered with `Plugin` | disabled: the controllers run only with `--enable-plugin-kinds` (chart `pluginKinds.enabled: true`), and the chart installs their CRDs only then | unit tests and recorded payloads only; OpenBao 2.7.2 does not ship the plugins, so they are not run against a server |

An external-plugin kind reports `Unsupported` until its plugin is mounted. Only the LDAP, Kerberos
and RADIUS paths are confirmed by OpenBao documentation; the catalogue marks the others `(u)`.

The 52 external-plugin kinds are experimental: they are not tested against any OpenBao version, so the support
statements on this page do not cover them, and no coverage claim counts on them. The typed share in
[API coverage](../api-coverage.md) for OpenBao 2.7.2 contains no plugin-kind path; the 2.6 figure counts LDAP,
Kerberos and RADIUS paths, which 2.6 still builds in, through plugin kinds that are not run against a server.
