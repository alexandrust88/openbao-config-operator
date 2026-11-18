---
description: "Check supported OpenBao and Kubernetes versions, installation requirements and compatibility limits."
---

# Compatibility

The versions and platforms each release supports. Sources: the chart's `Chart.yaml`,
[ADR 0009](../adr/0009-openbao-only-target.md), [ADR 0023](../adr/0023-openbao-api-changes-and-crd-lifecycle.md)
and the [release process](../maintainers/release-process.md#openbao-versions).

## Summary

| component | declared compatibility | verified with |
|-----------|-----------|--------------|
| OpenBao | 2.6 and 2.7 under the release policy; minimum 2.6, with endpoint-specific limits | 2.7.2 on a running server; 2.6 by its OpenAPI description only |
| Kubernetes | 1.31 or later (chart `kubeVersion: ">=1.31.0-0"`) | Kubernetes 1.35 |
| Helm | 3.8 or later, for OCI charts | Helm 4.3 |
| Image architectures | `linux/amd64`, `linux/arm64` | both architectures in the published multi-arch index |

## OpenBao

**Runtime compatibility is verified on 2.7.2.** The 2.6 OpenAPI snapshot establishes that a path or
field exists in that API description; it does not exercise authentication, defaults, reads,
writes or deletion on a running 2.6 server. Treat 2.6 as a declared compatibility target with
incomplete runtime evidence, and test the kinds you use before upgrading or deploying there.
The [API coverage report](../api-coverage.md) separates the two release inventories.

- OpenBao is the only target ([ADR 0009](../adr/0009-openbao-only-target.md)). The design relies on
  OpenBao features: self-init, namespaces and OCI plugins. Other servers
  are not supported.
- Each operator minor supports the OpenBao minors listed in its release notes; today 2.6 and 2.7
  (see [API coverage](../api-coverage.md)).
- Adapters do not branch on the server version. Recognised unsupported endpoints or features report
  `Unsupported`; other API validation failures may report `ReconcileError` ([ADR 0023](../adr/0023-openbao-api-changes-and-crd-lifecycle.md)).
- `BaoServer.status.version` reports the version the server returns from `sys/health`.
- A new OpenBao minor is supported in the next operator minor after its snapshot is added and
  classified, with a target of six weeks after the OpenBao release. An OpenBao minor is dropped in
  the first operator minor after upstream stops releasing fixes for it, announced one minor ahead
  ([release process](../maintainers/release-process.md#openbao-versions)).

### OpenBao features the operator depends on

| feature | needed for |
|---------|------------|
| cluster id in `sys/health` | binding acquired objects to one server (`status.serverID`) |
| namespaces | `BaoNamespace`, `spec.openbaoNamespace`, and the connection `openbaoNamespace` and `openbaoAuthNamespace` |
| self-init | starting without a root token at runtime ([self-init guide](../guides/self-init.md)) |
| `sys/workflows` | the `BaoWorkflow` kind |
| external plugins | the external-plugin kinds; without the plugin they report `Unsupported` |

## Kubernetes

- The chart requires Kubernetes 1.31 or later: CRD validation rules use the CEL IP and CIDR
  library, which the API server accepts in new CRD expressions only from 1.31.
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
| Helm repository | `https://docs.obco.dev/charts`, for tools without OCI support |
| plain manifests | `install.yaml` and `crds.yaml` attached to the GitHub release `@TAG@` |

The chart ships the CRDs as templates, so `helm upgrade` upgrades them; `crds.keep` (default `true`)
annotates them with `helm.sh/resource-policy: keep`. Every chart is also signed with the project's GPG
key (Helm provenance), so `helm install --verify` works. The
[install guide](../get-started/install.md) has the commands.

## Image

| property | value |
|----------|-------|
| registry | `ghcr.io/alexandrust88/openbao-config-operator` |
| platforms | `linux/amd64`, `linux/arm64` (one multi-arch index) |
| base | `gcr.io/distroless/static:nonroot`, user 65532 |
| reference | by version tag; the released chart pins the image by digest |
| signing | keyless cosign, with an SBOM attestation and build provenance |

## Supported releases

The first release is 1.0.0; the earlier pre-releases (0.1.x to 0.5.0) were withdrawn. The latest two minors are
supported: the latest minor gets every fix, and the previous minor gets security fixes until three months after the
next minor is released. The product version does not make the API stable: it is `v1alpha1`, and fields can still
change in a minor release under the [deprecation policy](#deprecation-policy).

Release history: the [GitHub releases](https://github.com/alexandrust88/openbao-config-operator/releases) and the
[changelog](https://github.com/alexandrust88/openbao-config-operator/blob/main/CHANGELOG.md). Every deprecation and
breaking change is in the release notes with what you must do.

## Deprecation policy

Everything deprecated is listed in the release notes of the release that deprecates it and again in the release
that removes it. The [release process](../maintainers/release-process.md#deprecation-policy) has the full policy.

| what | rule |
|------|------|
| `v1alpha1` CRD fields | a field to be removed or renamed is deprecated first and kept for at least one minor release; a renamed field keeps working under its old name during that release |
| `v1beta1` CRD fields | a deprecated field is kept for at least three minor releases or nine months, whichever is longer |
| `v1` CRD fields | not removed; a removal needs a new API version |
| validation | making a mutable field immutable, narrowing an enum or rejecting objects that were accepted counts as a removal, unless OpenBao already rejects those values |
| defaults | change only in a minor release and are always called out in the release notes |
| API versions | once `v1beta1` exists, `v1alpha1` is served next to it for at least one minor release, then deprecated, and stops being served only after a release note told you to migrate stored objects ([Upgrade](../operate/upgrade.md#moving-to-v1beta1)) |
| manager flags, chart values, metric names and labels | deprecated one minor release ahead: the old flag or value keeps working, the manager logs a warning at start-up, and the chart's `values.schema.json` description names the replacement |

A deprecated field's description in the [API reference](api/index.md) starts with `Deprecated: use <field> instead`.

## Kind availability classes

Each kind in the [kind catalogue](../kinds.md) has an availability class.

| class | meaning | default | verified on a running OpenBao |
|-------|---------|---------|----------|
| `builtin` | the object is compiled into OpenBao 2.7 (core or the built-in plugin registry) | enabled | yes; server prerequisites vary by kind (for example, RaftAutopilot requires raft storage). See the [catalogue](../kinds.md) |
| `external-plugin` | needs a plugin from openbao-plugins or a third party, registered with `Plugin` | disabled: the controllers run only with `--enable-plugin-kinds` (chart `pluginKinds.enabled: true`), and the chart installs their CRDs only then | no; checked against recorded payloads only |

An external-plugin kind reports `Unsupported` until its plugin is mounted. Only the LDAP, Kerberos
and RADIUS paths are confirmed by OpenBao documentation; the catalogue marks the others `(u)`.

The 52 external-plugin kinds are experimental and outside the support statements on this page. The typed
share in [API coverage](../api-coverage.md) for OpenBao 2.7.2 counts no plugin-kind path; the 2.6 figure counts
LDAP, Kerberos and RADIUS paths, which 2.6 still builds in, through plugin kinds that are not verified on a
running server.
