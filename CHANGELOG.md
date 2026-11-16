# Changelog

All notable changes to this project are listed here. Versions follow [Semantic Versioning](https://semver.org);
before 1.0.0 a minor release may contain breaking changes, and the release notes say so. Each version links to its
GitHub release, which lists the published artefacts, their digests and the merged pull requests.

The releases up to v0.3.1 were tagged by hand and their entries were written by hand. Once the release-please GitHub
App is configured, [release-please](https://github.com/googleapis/release-please) writes new entries from the
conventional-commit titles of merged pull requests. Do not edit released entries. The process is in
[docs/maintainers/release-process.md](docs/maintainers/release-process.md).

## [0.3.1](https://github.com/alexandrust88/openbao-config-operator/releases/tag/v0.3.1) (2026-11-16)

No change to the CRDs. The chart refuses values that weaken the pod or inject YAML: check your values before you
upgrade ([upgrading to 0.3.1](docs/operate/upgrade.md#upgrading-to-031)).

### Security

- The chart quotes string values in its templates, and the values schema requires plain names, durations, CIDRs
  and audiences, so a value that contains a newline can no longer add YAML keys past the chart's own guards ([#11](https://github.com/alexandrust88/openbao-config-operator/pull/11)).
- The values schema refuses `privileged`, privilege escalation, `runAsNonRoot: false`, added capabilities, hostPath
  volumes, `serviceAccount.name: default`, `POD_NAMESPACE` in `extraEnv` and, in `extraArgs`, every flag the chart
  already sets ([#11](https://github.com/alexandrust88/openbao-config-operator/pull/11)).
- The aggregated viewer role no longer grants read on `ClusterOpenBaoConnection`, and the manager no longer has
  `patch` on Secrets ([#11](https://github.com/alexandrust88/openbao-config-operator/pull/11)).

### Features

- `rbac.tenantEditor.excludeKinds` takes kinds, such as `openbaoendpoints`, out of the aggregated tenant-editor
  role ([#11](https://github.com/alexandrust88/openbao-config-operator/pull/11)).

### Fixes

- The alerts' `runbook_url` points at the documentation site ([#11](https://github.com/alexandrust88/openbao-config-operator/pull/11)).
- `hack/release/verify-release.sh` stops with "needs Helm v4" instead of a key parse error under Helm 3, and the
  release notes link the verification guide ([#11](https://github.com/alexandrust88/openbao-config-operator/pull/11)).

### Documentation

- Every page was checked against the code; wrong statements about backup, observe mode, RBAC with
  `watchNamespaces` and the release process are corrected ([#11](https://github.com/alexandrust88/openbao-config-operator/pull/11)).

## [0.3.0](https://github.com/alexandrust88/openbao-config-operator/releases/tag/v0.3.0) (2026-11-16)

No change to the operator, the CRDs or the chart values.

### Features

- The Helm chart is signed with a GPG Helm provenance file: `openbao-config-operator-0.3.0.tgz.prov` is attached to
  the release and pushed as a layer of the OCI chart, so `helm install --verify` works and Artifact Hub shows the
  chart as signed. The public key is served at `/charts/pgp-public-key.asc` on the project's GitHub Pages site
  ([#9](https://github.com/alexandrust88/openbao-config-operator/pull/9)).

### Documentation

- Role-based tutorials (platform owner, several OpenBao servers, tenant onboarding, team onboarding) whose commands
  run in the e2e workflow, a tested uninstall procedure, and a page that compares the operator with other projects
  ([#10](https://github.com/alexandrust88/openbao-config-operator/pull/10)).
- The documented tenant policy for `bound_service_account_namespaces` is corrected: the earlier form refused every
  write on OpenBao ([#10](https://github.com/alexandrust88/openbao-config-operator/pull/10)).

## [0.2.0](https://github.com/alexandrust88/openbao-config-operator/releases/tag/v0.2.0) (2026-11-15)

### Breaking changes

- `TransitKey`, `PKIIssuer`, `PKIKey`, `SSHCA`, `TOTPKey`, `MFAMethod` and `Entity` are destructive kinds. With
  `deletionPolicy: Delete`, the operator deletes the OpenBao object only when the resource has the annotation
  `openbaoconfig.io/allow-destructive-delete: "true"`; without it the resource reports `DeleteGuarded`. When the
  resource's Kubernetes Namespace is being deleted, the object stays in OpenBao and the operator records a Warning
  `Orphaned` event. `TransitKey` sets `deletion_allowed` only after the annotation check. To find affected resources
  and migrate, see [upgrading to 0.2.0](docs/operate/upgrade.md#upgrading-to-020)
  ([#8](https://github.com/alexandrust88/openbao-config-operator/pull/8)).

### Features

- `AuthMount` and `SecretsMount` share one read of `sys/auth` or `sys/mounts` per connection, OpenBao namespace and
  table for 2 seconds, instead of one read per mount
  ([#8](https://github.com/alexandrust88/openbao-config-operator/pull/8)).
- The adoption check lists each kind at most once every 10 seconds instead of once per object
  ([#8](https://github.com/alexandrust88/openbao-config-operator/pull/8)).
- A label or annotation change on a credential Secret no longer forces a new login and token revocation; a value
  change still does ([#8](https://github.com/alexandrust88/openbao-config-operator/pull/8)).
- Chart: `crds.annotations` and `crds.labels` add metadata to every CRD, and the notes point at the release's
  `crds.yaml` when the release does not manage the CRDs
  ([#7](https://github.com/alexandrust88/openbao-config-operator/pull/7)).
- Each release attaches a signed `examples-<version>.tar.gz` with the curated examples and generated samples of that
  release ([#7](https://github.com/alexandrust88/openbao-config-operator/pull/7)).

### Documentation

- A documentation site in which every example is dry-run against the CRDs in CI
  ([#7](https://github.com/alexandrust88/openbao-config-operator/pull/7)).

## [0.1.1](https://github.com/alexandrust88/openbao-config-operator/releases/tag/v0.1.1) (2026-11-15)

The first version with a GitHub release, release assets (`install.yaml`, `crds.yaml`, the chart archive, SBOMs,
provenance and signed checksums) and an OCM component version.

### Bug fixes

- The release workflow resolves the OCM input paths from an absolute directory, which made the v0.1.0 release fail
  ([#5](https://github.com/alexandrust88/openbao-config-operator/pull/5)).

### Chart

- Search keywords for Artifact Hub ([#4](https://github.com/alexandrust88/openbao-config-operator/pull/4)).

## 0.1.0 (2026-11-15)

The first tagged version. The release workflow pushed the image and the Helm chart to `ghcr.io`, then failed in the
OCM step, so v0.1.0 has no GitHub release, release assets or OCM component. Use v0.1.1 or later.

- 68 built-in kinds and 52 experimental external-plugin kinds (behind `--enable-plugin-kinds`) in the API group
  `openbaoconfig.io`, version `v1alpha1`.
- One generic engine with per-kind adapters: path-index ownership, `Create` and `Orphan` defaults, the
  destructive-delete guard, drift tiers with a per-server request budget, observe mode with the `obc-import` tool,
  the write circuit breaker and the pause annotation.
- A Helm chart and kustomize manifests, with a multi-arch image signed with keyless cosign.
