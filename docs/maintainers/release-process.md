# Release process

This page is for maintainers. It covers versioning, how to cut a release, backports, the support window and the
deprecation policy. The release workflow itself (build, scan, sign, attest, publish) is
[`.github/workflows/release.yml`](../../.github/workflows/release.yml); how users verify a release is in
[SECURITY.md](../../SECURITY.md#verifying-releases).

## Versioning

One version number covers the image, the Helm chart, `install.yaml` and `crds.yaml` of a release. Tags are
`vX.Y.Z` or `vX.Y.Z-prerelease` and follow [Semantic Versioning 2.0](https://semver.org): no leading zeros in
numeric identifiers (`v1.2.3-rc.1`, not `v1.2.3-rc.01`) and no build metadata (`+...`), which OCI tags cannot
carry. The release workflow checks the tag (`hack/release/check-version.sh`) and packages the chart with it before
anything is pushed.

The project version and the API version are separate:

| project version | API versions | compatibility promise |
|-----------------|--------------|-----------------------|
| `0.y.z` | `v1alpha1` | a minor release may break the API; breaking changes are called out in the release notes and follow the [deprecation policy](#deprecation-policy) for alpha |
| `0.y.z`, late | `v1alpha1` and `v1beta1` served | `v1beta1` added as a schema-compatible version ([ADR 0020](../adr/0020-crd-versioning-without-webhooks.md)); `v1alpha1` stays the storage version for at least one minor |
| `1.0.0` and later | `v1beta1` (then `v1`) as storage version | breaking changes only in a major release; beta deprecation rules apply |

Conditions for adding `v1beta1` (tracked in the [roadmap](roadmap.md)):

- the API group domain is final ([BL-001](../project/backlog.md#bl-001-pick-an-owned-api-group-domain-before-the-first-release));
- two consecutive minor releases without a breaking change to `v1alpha1`;
- the no-write and default conformance gates pass for every built-in kind;
- a migration guide for `v1alpha1` objects exists and the storage version migration is tested.

### How the version is chosen

Pull requests are squash-merged: their title becomes the commit subject and their description the commit body. The
title is a [conventional commit](https://www.conventionalcommits.org) (enforced by
[`pr-title.yml`](../../.github/workflows/pr-title.yml)), and footers such as `BREAKING CHANGE:` or `Release-As:` go
at the end of the pull request description. [release-please](https://github.com/googleapis/release-please) reads
those commits:

| commit | before 1.0.0 | from 1.0.0 |
|--------|--------------|------------|
| `fix:`, `perf:` | patch | patch |
| `feat:` | minor | minor |
| `feat!:`, `fix!:` or a `BREAKING CHANGE:` footer | minor | major |
| `docs:`, `chore:`, `ci:`, `test:`, `refactor:`, `build:`, `style:` | no release on their own | no release on their own |

The configuration is in [`.github/release-please-config.json`](../../.github/release-please-config.json) and the
current version in [`.github/.release-please-manifest.json`](../../.github/.release-please-manifest.json). To force
a version, for example a release candidate, end the description of the next pull request with a
`Release-As: 0.2.0-rc.1` footer; edit the description before you merge, because the squash commit copies it.

## Repository settings this process relies on

Set these once when the repository is created:

- Pull requests: allow squash merging only; default squash commit message "Pull request title and description".
- A ruleset on `main` and `release-X.Y`: require pull requests, the `ci` jobs and `Conventional commit title`, and
  block force pushes. Approvals are required only once there are two maintainers
  ([solo and strict mode](github-settings.md#solo-and-strict-mode)). Only maintainers may create `release-X.Y`
  branches.
- Release immutability on: a published release cannot be changed.
- A tag ruleset on `v*`: only maintainers may create tags; tags cannot be updated or deleted.
- Environment `release`, with the owner as required reviewer, for every publishing job, and the environment
  secrets `HELM_SIGNING_KEY` and `HELM_SIGNING_PASSPHRASE` ([chart signing key](#chart-signing-key)).
- Environment `release-please`, limited to `main` and `release-X.Y`. Optional, recommended: a GitHub App with
  contents and pull-requests write, its client id in the environment variable `RELEASE_APP_CLIENT_ID` and its key
  in the environment secret `RELEASE_APP_PRIVATE_KEY`. Without the app, the release pull request is opened with
  `GITHUB_TOKEN`, which starts no workflows, so required checks never report on it; a maintainer then has to push
  an empty commit to the release branch or merge with admin rights.
- Discussions on, with the categories Announcements, Q&A, Ideas and Show and tell.
- Private vulnerability reporting on.

## Before the first release

Do these once, before `v0.1.0`:

1. **Rehearse in a fork.** Fork the repository into a throwaway account, enable Actions in the fork, run `hack/github/apply-settings.sh` against
   it, push a throwaway tag such as `v0.0.1-rehearsal.1` and let the release workflow run to the end. Check the
   release page and the packages, then run
   `REPOSITORY=<owner>/openbao-config-operator hack/release/verify-release.sh v0.0.1-rehearsal.1`. Delete the fork and
   its packages afterwards; nothing it published is used.
2. **Make the packages public after the first release.** New packages are private, so the
   `v0.1.0` image and chart cannot be pulled anonymously. When the release workflow has finished, open the
   owner's Packages tab, set the `openbao-config-operator` image package and the
   `charts/openbao-config-operator` chart package to public (Package settings > Change visibility), and connect
   both to this repository. Then run `hack/release/verify-release.sh v0.1.0` from a machine that is not logged in to
   `ghcr.io`.

## Chart signing key

The `chart` job signs the Helm chart with a dedicated GPG key: `helm package --sign` writes
`openbao-config-operator-<version>.tgz.prov`, which the release uploads next to the chart and pushes as a layer of
the OCI chart, and `artifacthub.io/signKey` names the key's fingerprint and its public key on GitHub Pages
(`/charts/pgp-public-key.asc`, served from `.github/pgp-public-key.asc`). Artifact Hub shows the chart as signed
when that `.prov` file exists. The job fails when either secret is missing or when the signature does not verify
with the committed public key, so set the key up before the next tag:

```sh
gpg --quick-gen-key "openbao-config-operator chart signing key" rsa4096 sign never
FPR=$(gpg --with-colons --list-secret-keys "openbao-config-operator chart signing key" | awk -F: '$1 == "fpr" {print $10; exit}')
echo "$FPR"
gpg --armor --export-secret-keys "$FPR" | gh secret set HELM_SIGNING_KEY --env release --repo alexandrust88/openbao-config-operator
gh secret set HELM_SIGNING_PASSPHRASE --env release --repo alexandrust88/openbao-config-operator
gpg --armor --export "$FPR" > .github/pgp-public-key.asc
```

`gpg` asks for a passphrase when it creates the key and again when it exports it; `gh secret set
HELM_SIGNING_PASSPHRASE` prompts for the same passphrase, so it stays out of the shell history. Merge
`.github/pgp-public-key.asc` through a pull request before you tag: the release checks out the tagged commit and
verifies against that file, and `pages.yml` publishes it. Keep an offline backup of the private key and of the
revocation certificate that `gpg` writes to `~/.gnupg/openpgp-revocs.d/`.

The key has no expiry on purpose: Helm rejects a signature whose key has expired by the time it verifies, so an
expiry date would break `helm install --verify` for every published chart. To rotate, generate a new key the same
way, set both secrets again, and write both public keys into the committed file
(`gpg --armor --export "$OLD_FPR" "$FPR" > .github/pgp-public-key.asc`), so charts signed with the old key still
verify. If the old key leaked, revoke it, drop it from the file and say in the release notes which versions it
signed; their cosign signatures still verify.

## Cutting a release

1. **Check main.** The latest `ci`, `e2e` and weekly `conformance` runs on `main` are green, and no
   `priority/critical-urgent` issue is open for the release.
2. **Review the release pull request.** release-please keeps one open, titled `chore(main): release X.Y.Z`. Check
   that the version is what you expect and that `CHANGELOG.md` reads well. Edit the entries in the pull request if
   needed; do not edit released entries later. Every breaking change and deprecation must be in the entry with what
   users must do.
3. **Label merged pull requests.** The GitHub release notes are generated from pull request labels
   ([`.github/release.yml`](../../.github/release.yml)). Make sure the merged pull requests have a `kind/*` label.
4. **Dry-run when the release pipeline changed.** Run the `release` workflow by hand (`workflow_dispatch`) with
   version `vX.Y.Z-dryrun`. It builds, scans, signs with a throwaway key and verifies everything, and publishes
   nothing. Locally, `make release-dry-run` does the same.
5. **Merge the release pull request** (squash).
6. **Tag the merge commit** with a signed, annotated tag and push it:

   ```sh
   git fetch origin
   git switch --detach origin/main
   git log -1 --format='%H %s'
   git tag -s vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```

   The commit printed by `git log` must be the release pull request's merge commit. Signing the tag is a
   convention: nothing checks the tag signature (see [github-settings.md](github-settings.md#release-tags-signed-tag-ruleset-optional)).
7. **Watch the release workflow and approve its jobs.** It runs CI and e2e, checks the version, refuses to
   overwrite an existing release (and stops when it cannot tell), builds the multi-arch image, scans it, signs the
   image, chart and checksums with keyless cosign, signs the chart with the GPG chart signing key, attests the SBOMs and the provenance (GitHub artifact
   attestations, SLSA Build L2, and a cosign `slsaprovenance1` statement written by the release job, which has no
   SLSA level; nothing reaches Build L3), and creates the GitHub release with the generated notes. The `image`,
   `chart`, `ocm` and `assets` jobs each wait for an approval in the `release` environment; before the first
   approval, check that the run's commit is the one from step 6. The tag push also runs `release-please` once more,
   which marks the release pull request `autorelease: tagged` so the next release pull request can open.

   Releases run one at a time (concurrency group `release`). GitHub keeps only one waiting run per group, so a
   third tag pushed while one release runs and another waits cancels the waiting one: push the next tag after the
   previous release has finished.
8. **Verify** the published release from a clean machine:

   ```sh
   hack/release/verify-release.sh vX.Y.Z
   ```

9. **Announce** in Discussions (Announcements) with a link to the release, and note breaking changes and
   deprecations in a separate paragraph.

The `ocm` job of the release workflow publishes the OCM component version of the release to `ghcr.io/<owner>/ocm`
once the image and the chart are signed: `hack/ocm/component.sh` fills `release/component-constructor.yaml` with the
pushed digests, checks it with `ocm`, refuses to overwrite a published version and pushes it, and the job signs and
attests the component descriptor with keyless cosign. `hack/ocm/verify.sh vX.Y.Z` verifies it, and
`make release-dry-run` (or `make ocm-dry-run` on its outputs) proves the whole step, including an
`ocm transfer --copy-resources` round trip, against local registries.

The `assets` job also builds the examples bundle, `examples-<version>.tar.gz`, with
`hack/release/examples-bundle.sh`: the curated examples (`examples/`) and the generated samples
(`examples-generated/`) of the tagged commit, at their repository paths, so users get examples that match the CRDs
of the release without cloning the repository. Files that git ignores are never packed, so local files such as
`examples/live/params/params.env` do not ship, and the version placeholders that the docs use are replaced with the
version and tag of the release. The archive is reproducible (sorted entries, the commit time as modification time,
owner 0:0, `gzip -n`) and is built before the checksums, so `sha256sums.txt`, its signature and the provenance cover it. Users
download it from `https://github.com/alexandrust88/openbao-config-operator/releases/download/<tag>/examples-<version>.tar.gz`.
`hack/release/verify-release.sh` with `ASSETS_DIR` checks that the bundle is listed in `sha256sums.txt`, is covered by
the provenance and holds only those two directories (releases up to v0.1.1 have no bundle), and `make release-dry-run`
builds it twice and fails unless both archives are identical.

If the release workflow fails after the tag is pushed, fix the cause on `main` and release the next patch
version. Never move or reuse a tag: published artefacts are immutable, and users may have pinned the digest.

### Residual risk: the release gate lives in the tagged workflow

A tag push runs `release.yml` as it is in the tagged commit, and the `environment: release` lines that make the jobs
wait for approval are part of that file. A maintainer, or anyone holding a maintainer's push credentials, can tag a
commit that is not on `main` and whose `release.yml` drops the environment; that run signs with the same identity
as a reviewed release (`release.yml@refs/tags/vX.Y.Z`), because keyless signing needs no environment secret. The
tag ruleset limits who can push `v*` tags; it does not check that the tagged commit was reviewed.

Until that changes, the approver checks before approving: the run's commit is on `main` (or on the `release-X.Y`
branch for a backport) and is the release pull request's merge commit, and the run shows the jobs waiting for the
`release` environment. In solo mode the same maintainer tags and approves, so this check is the only control.

Backlog: release through `workflow_dispatch` on `main`. A dispatched workflow on `main` (reviewed under the `main`
ruleset) would check the version, create the tag itself and publish, and verifiers would pin the signer identity to
`release.yml@refs/heads/main` with the `workflow_dispatch` trigger, so a workflow file outside `main` could no longer
produce a trusted signature. This changes the identity every verifier and admission policy pins, so it waits for a
release with a migration note.

## Backports

- Release branches are named `release-X.Y` and are created from the release tag when the first backport is needed:

  ```sh
  git switch -c release-X.Y vX.Y.0
  git push origin release-X.Y
  ```

- Only fixes are backported: bugs, security fixes and documentation corrections. Features, new kinds, new fields and
  dependency upgrades that are not security fixes are not backported. A CRD change is backported only to fix a
  validation rule that rejects valid specs.
- Mark a merged fix with `backport/candidate`. Cherry-pick it with `git cherry-pick -x` into a pull request against
  `release-X.Y`, keeping the original conventional-commit title.
- release-please also runs on `release-X.Y` branches and opens a `chore(release-X.Y): release X.Y.Z` pull request.
  Release it as above, tagging the merge commit on the release branch.
- Backports go only to minors inside the [support window](#support-window).

## Support window

### Operator releases

| phase | supported releases |
|-------|--------------------|
| before the first release | `main` only |
| `0.y` | the latest minor; critical security fixes only for the previous minor, for 30 days after the next minor is released, at the maintainers' discretion |
| from `1.0.0` | the latest two minors; security fixes for the previous minor until three months after the next minor is released |

This matches [SECURITY.md](../../SECURITY.md#supported-versions); change both together.

### OpenBao versions

OpenBao has no long-term-support releases, so the operator follows the upstream minors:

- Each operator minor supports the OpenBao minors that have an API snapshot in [`api-coverage/`](../../api-coverage/)
  and are listed in its release notes. Today that is 2.6 and 2.7 (minimum 2.6, see
  [architecture](../architecture.md#scope)); the integration suite runs against the pinned 2.7.2 (`BAO_VERSION` in
  the Makefile), and 2.6 is covered by its API snapshot only.
- When OpenBao releases a new minor: add its snapshot with `hack/apicoverage/snapshot.sh`, classify the new paths,
  run `make test-integration` against it, move the pin, and ship support in the next operator minor. Target: within
  six weeks of the OpenBao release.
- An OpenBao minor is dropped in the first operator minor after upstream stops releasing fixes for it, announced in
  the release notes of the operator minor before.
- No version-branching shims in adapters: a feature an older OpenBao lacks reports `Unsupported`.

### Kubernetes versions

The chart requires Kubernetes 1.31 or later (`kubeVersion`): CRD rules use the CEL IP/CIDR library, which the API server accepts in new CRD expressions only from 1.31. Tests run on the
envtest version that matches `k8s.io/api` in `go.mod` (1.35 today) and on kind in the e2e workflow. The project
supports the Kubernetes minors that are supported upstream when the operator minor is released, and raises the
chart's `kubeVersion` only in a minor release, with a release note.

## Deprecation policy

The goal is that no user is surprised by a removal. Everything deprecated is listed in the release notes of the
release that deprecates it and again in the release that removes it.

### CRD fields

| API version | rule |
|-------------|------|
| `v1alpha1` | a field to be removed or renamed is deprecated first and kept for at least one minor release ([upgrade.md](../operate/upgrade.md#api-compatibility)); a renamed field keeps working under its old name during that release |
| `v1beta1` | a deprecated field is kept for at least three minor releases or nine months, whichever is longer |
| `v1` | fields are not removed; a removal needs a new API version |

How to deprecate a field:

1. Start its doc comment with `Deprecated: use <field> instead; removed in <version>.` so `kubectl explain` and the
   API reference show it.
2. Keep reading it: the adapter maps the old field to the new one when only the old one is set. A CEL rule may
   reject setting both.
3. Add the deprecation to the release pull request's changelog entry with the migration step.

Making a mutable field immutable, narrowing an enum or tightening validation so that accepted objects are rejected
counts as a removal and follows the same rule, unless OpenBao already rejects those values.

### Defaults

A default changes only in a minor release and is always called out. Changing `creationPolicy`, a kind's default
`deletionPolicy` or the destructive-delete guard needs an ADR ([ADR 0004](../adr/0004-creation-and-deletion-policy-defaults.md),
[ADR 0015](../adr/0015-create-and-orphan-by-default.md)).

### API versions

Following the Kubernetes deprecation policy, once `v1beta1` exists: `v1alpha1` is served next to it for at least one
minor release, then deprecated (the CRD version gets `deprecated: true` and a `deprecationWarning`), and stops being
served only after a release note told users to run the storage version migration in [upgrade.md](../operate/upgrade.md).
A deprecated beta version is served for at least three minor releases or nine months.

### Manager flags, chart values and metrics

Deprecated one minor release ahead: the old flag or value keeps working, the manager logs a warning at start-up,
and the chart's `values.schema.json` description says what replaces it. Metric names and labels are treated like
API fields, because users alert on them.

## Security releases

Vulnerabilities are handled as described in [SECURITY.md](../../SECURITY.md). The fix is prepared in a GitHub
security advisory's private fork, released as a patch on every supported minor at the agreed disclosure time, and
the advisory is published with the release. Do not mention the vulnerability in public issues, pull request titles
or commit messages before then.

## Cadence

Minor releases roughly every six to eight weeks when there are features to ship; patch releases whenever a fix is
ready. A release is never cut to meet a date with failing gates.
