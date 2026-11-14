# GitHub repository settings

The security workflows in `.github/workflows` only protect `main` if the repository settings below are in place.
Apply them once when the repository is created, and again after a workflow job is added, renamed or removed (the
required-check list must match the job names). The workflows themselves and their trust model are described in
[CI security](../security/ci-security.md).

`hack/github/apply-settings.sh` applies everything that has an API. Run it as a repository admin:

```sh
gh auth login --scopes repo,read:user
hack/github/apply-settings.sh alexandrust88/openbao-config-operator
```

It is idempotent: it updates the rulesets and environments it finds by name and creates the missing ones. The
steps it cannot do are marked "manual" below.

## Solo and strict mode

The project starts with one maintainer ([GOVERNANCE.md](../../GOVERNANCE.md#single-maintainer-phase)). GitHub never
lets the author of a pull request approve it, so a rule that requires an approval would block every change of a sole
maintainer. The script therefore has two modes:

| | solo (`SOLO_MAINTAINER=true`, the default) | strict (`SOLO_MAINTAINER=false`) |
|-|-|-|
| required approvals on `main` and `release-X.Y` | 0 | 1 |
| review from code owners | off | on |
| approval of the most recent push | off | on |
| `release` environment | manual approval by the owner; the maintainer who started the run may approve it | manual approval by the owner; prevent self-review on (see the [residual risk](release-process.md#residual-risk-the-release-gate-lives-in-the-tagged-workflow)) |

Everything else is the same in both modes: pull requests, required status checks, code scanning, linear history,
no force pushes or deletions, the tag ruleset, and the rest of this page.

Switch to strict mode in the pull request that adds the second maintainer to [MAINTAINERS.md](../../MAINTAINERS.md):
add them as collaborators and code owners, merge, then run

```sh
SOLO_MAINTAINER=false hack/github/apply-settings.sh alexandrust88/openbao-config-operator
```

and update the decision rules in [GOVERNANCE.md](../../GOVERNANCE.md#decision-making) in the same pull request.

## Prerequisites

- A repository owned by a personal account, which has no teams. The owner is the code owner in
  `.github/CODEOWNERS` and the reviewer of the `release` environment (`RELEASE_REVIEWER` names another user). The
  bypass actor of the `release-tags` and `release-branches` rulesets is the admin repository role, which on a
  personal repository only the owner holds.
- The Renovate GitHub App installed on the repository (manual, <https://github.com/apps/renovate>), with read access
  to Dependabot alerts so `vulnerabilityAlerts` in `renovate.json` works.

## General

Settings > General.

| Setting | Value | Why |
|---------|-------|-----|
| Allow merge commits | off | |
| Allow squash merging | on, default message "Pull request title and description" | release-please builds the changelog from the squashed title and reads footers (`BREAKING CHANGE:`, `Release-As:`) from the body, which is the pull request description |
| Allow rebase merging | off | |
| Always suggest updating pull request branches | on | the main ruleset requires up-to-date branches |
| Allow auto-merge | off | Renovate does not automerge; every update gets a human review |
| Automatically delete head branches | on | |
| Discussions | on | categories Announcements, Q&A, Ideas, Show and tell (manual) |
| Wiki | off | the docs live in `docs/` |
| Releases: enable release immutability | on | a published release's assets and tag cannot change; `gh release create` in `release.yml` uploads every asset before it publishes |

## Rulesets

Settings > Rules > Rulesets. Rulesets, not classic branch protection: they cover tags too, and Scorecard reads them.

### `main` (branch ruleset)

Target: the default branch and `refs/heads/release-[0-9]*` (the `release-X.Y` backport branches; the pattern leaves
out the `release-please--branches--*` branches release-please creates and force-pushes). Enforcement: active. Bypass
list: empty.

| Rule | Value |
|------|-------|
| Restrict deletions | on |
| Block force pushes | on |
| Require linear history | on |
| Require a pull request before merging | solo mode: 0 approvals; strict mode: 1 approval, require review from code owners, require approval of the most recent push. Both: dismiss stale approvals on push, require conversation resolution, allowed merge method: squash |
| Require status checks to pass | require branches to be up to date; the checks below, source "GitHub Actions" |
| Require code scanning results | CodeQL: security alerts "High or higher", alerts "Errors" |
| Require signed commits | optional (`REQUIRE_SIGNED_COMMITS=true` in the script). Squash merges made in the GitHub UI are signed by GitHub, so this only constrains direct pushes to release branches |

Required status checks (the job names, or the job `name:` where one is set):

| Workflow | Check |
|----------|-------|
| `ci.yml` | `lint`, `test`, `integration`, `namespaces`, `livekit`, `verify-generated`, `api-coverage`, `govulncheck`, `manifests` |
| `e2e.yml` | `e2e` |
| `codeql.yml` | `analyze (go)`, `analyze (actions)` |
| `dependency-review.yml` | `dependency-review` |
| `secret-scan.yml` | `gitleaks` |
| `vulnerabilities.yml` | `osv-scanner` |
| `workflows.yml` | `actionlint`, `zizmor`, `pinact`, `poutine` |
| `pr-title.yml` | `Conventional commit title` |
| `pages.yml` | `pages` (runs on every pull request and builds the site only when a docs path changed) |

Not required, on purpose: `labels` runs only when its paths change, and a required check that never starts
blocks the merge. `conformance` and `govulncheck-nightly` do not run on pull
requests.

### `release-branches` (branch ruleset)

Target: `refs/heads/release-[0-9]*`. Enforcement: active. Bypass list: the admin repository role, on a personal repository only the owner (always).

| Rule | Value |
|------|-------|
| Restrict creations | on: only maintainers create a backport branch. A new `release-X.Y` branch can deploy to the `release-please` environment, which holds the GitHub App key, so a writer who could create one with an unreviewed workflow could read that key |

### `release-tags` (tag ruleset)

Target: `refs/tags/v*`. Enforcement: active. Bypass list: the admin repository role, on a personal repository only the owner (always).

| Rule | Value |
|------|-------|
| Restrict creations | on: only the bypass list can push a `v*` tag, and that push starts the release |
| Restrict updates | on: a published tag never moves |
| Restrict deletions | on |

### `release-tags-signed` (tag ruleset, optional)

Created only with `REQUIRE_SIGNED_COMMITS=true` (and deleted again without it). Target: `refs/tags/v*`. Enforcement:
active. Bypass list: empty, because a bypass actor skips every rule of its ruleset, so a signature rule inside
`release-tags` would never apply to the maintainers who push the tags.

| Rule | Value |
|------|-------|
| Require signed commits | on: the commit a `v*` tag points at must carry a signature GitHub verifies |

The signature of the tag object itself (`git tag -s` in [release-process.md](release-process.md)) is not enforced:
neither GitHub nor the release workflow checks it, so it is a convention, not a control.

## Security

Settings > Advanced Security (called "Code security" on older plans).

| Setting | Value | Why |
|---------|-------|-----|
| Private vulnerability reporting | on | the intake channel in `SECURITY.md` |
| Dependency graph | on | needed by Dependabot alerts and `dependency-review` |
| Dependabot alerts | on | Renovate reads them (`vulnerabilityAlerts`) |
| Dependabot security updates | on | a fallback when Renovate is not running; Renovate usually opens the fix first |
| Dependabot version updates | off (no `.github/dependabot.yml`) | Renovate owns version updates; both would open duplicate pull requests |
| Secret scanning | on | |
| Push protection | on | blocks a push that contains a known secret format; `secret-scan.yml` covers the history |
| Code scanning default setup | off | `codeql.yml` is the advanced setup; default setup and a CodeQL workflow cannot both run |

Code scanning receives SARIF from three workflows: CodeQL (`codeql.yml`), zizmor (`workflows.yml`) and Scorecard
(`scorecard.yml`).

## Actions

Settings > Actions > General.

| Setting | Value | Why |
|---------|-------|-----|
| Actions permissions | allow the organization's and select non-organization actions | |
| Allow actions created by GitHub | on | `actions/*`, `github/codeql-action/*` |
| Allow actions by verified creators | off | only the explicit list below |
| Allowed actions and reusable workflows | see list | every third-party action in the workflows, including nested ones |
| Require actions to be pinned to a full-length commit SHA | on | every `uses:` is SHA-pinned; Renovate keeps the pins current |
| Fork pull request workflows from outside collaborators | require approval for all external contributors | |
| Workflow permissions | read repository contents and packages | each job asks for what it needs; workflows start from `permissions: {}` |
| Allow GitHub Actions to create and approve pull requests | off | release-please uses a GitHub App token, see [release-process.md](release-process.md) |
| `pull_request_target` event policy | leave it blocked (the public-repository default from 2026-12-02) | no workflow uses `pull_request_target`; allowing it would only open a path for one added later |
| Require workflow lockfiles | not available | an Enterprise Cloud feature; the SHA pins plus the `pinact` check cover the same ground, see [CI security](../security/ci-security.md) |

Allowed third-party actions (update when a workflow adds one):

```text
crazy-max/ghaction-github-labeler@*
docker/build-push-action@*
docker/login-action@*
docker/setup-buildx-action@*
googleapis/release-please-action@*
ossf/scorecard-action@*
sigstore/cosign-installer@*
```

## Environments

Settings > Environments.

| Environment | Deployment branches and tags | Protection |
|-------------|------------------------------|------------|
| `release` | tags `v*` only | required reviewers: the owner (`RELEASE_REVIEWER`); prevent self-review off in solo mode, on in strict mode (see [solo and strict mode](#solo-and-strict-mode)); holds no secrets (signing is keyless). Every publishing job of `release.yml` (`image`, `chart`, `ocm`, `assets`) uses it, so each waits for its own approval |
| `release-please` | branches `main` and `release-[0-9]*` | none; holds `RELEASE_APP_CLIENT_ID` (variable) and `RELEASE_APP_PRIVATE_KEY` (secret), set by hand; new `release-*` branches are restricted by the `release-branches` ruleset |
| `github-pages` | branch `main` only (the script creates it if the first Pages deployment has not) | none |

## Pages

Settings > Pages: source "GitHub Actions". The site is built and deployed by `pages.yml`.

## Private repository

The workflows also run while the repository is private on a free plan. The features that need a public repository
(or GitHub Advanced Security) are skipped, and they switch on by themselves once the repository is public:

| Workflow | Skipped while private | Still runs |
|----------|-----------------------|------------|
| `codeql.yml` | the CodeQL analysis and upload; `analyze (go)` and `analyze (actions)` pass without analysing | |
| `dependency-review.yml` | the whole job; the `dependency-review` check passes as skipped | `osv-scanner` (`vulnerabilities.yml`) covers known vulnerabilities |
| `workflows.yml` | the zizmor SARIF report and its upload | the zizmor gate (`--min-severity=medium`), `actionlint`, `pinact`, `poutine` |
| `scorecard.yml` | the Scorecard run, its publication and upload | |
| `pages.yml` | `configure-pages`, the Pages artifact and the `deploy` job | the docs and Helm index build, so the `pages` check still fails on a broken site |

`codeql.yml` and `scorecard.yml` read the visibility from the API because schedule events carry no repository
object; the others use `github.event.repository.private`. `gitleaks`, `osv-scanner`, `govulncheck` and the tests do not
depend on the visibility.

The repository settings have the same limit. On a private repository of the free plan,
`hack/github/apply-settings.sh` prints one warning for each setting it skips and applies the rest: the general
settings, Dependabot alerts and security updates, the Actions policy (allowed actions, SHA pinning, read-only default
token, fork pull request approval), immutable releases and the three environments without protection. It reads the
visibility and the plan from the API, and skips any other setting GitHub refuses with "Upgrade to GitHub Pro or make
this repository public" the same way. Unenforced while private on the free plan:

| Setting | While it is missing |
|---------|---------------------|
| rulesets `main`, `release-branches`, `release-tags` (and `release-tags-signed`) | no required pull request, status checks, code scanning results or linear history, and no force-push block, on `main` and `release-X.Y`; anyone with write access can create, move or delete a `v*` tag and start the release |
| `release` environment: required reviewer, deployment tags `v*` | the publishing jobs start without an approval |
| `release-please` and `github-pages` environments: deployment branches | any branch can deploy to them |
| secret scanning and push protection | `secret-scan.yml` (gitleaks) is the only secret check |
| private vulnerability reporting | the intake channel in `SECURITY.md` is closed |
| Pages source | `pages.yml` builds the site and the Helm index but does not deploy them |

When the repository becomes public (or moves to a paid plan):

1. Run `hack/github/apply-settings.sh` again: it applies everything it skipped and the rest of this page. A setting
   it still warns about is not available on that plan.
2. Make the `ghcr.io` packages public (Package settings > Change visibility), see
   [release-process.md](release-process.md).
3. Run `scorecard.yml` once (`push` to `main` or wait for the schedule) and check that the results appear in
   Security > Code scanning and on the OpenSSF Scorecard API.
4. Register the project for the OpenSSF Best Practices badge (<https://www.bestpractices.dev>).

## Check the result

```sh
gh api repos/alexandrust88/openbao-config-operator/rulesets --jq '.[] | [.name, .target, .enforcement] | @tsv'
gh api repos/alexandrust88/openbao-config-operator/actions/permissions
gh api repos/alexandrust88/openbao-config-operator --jq .security_and_analysis
gh api repos/alexandrust88/openbao-config-operator/immutable-releases
```

The Scorecard run on `main` (Security > Code scanning, tool "Scorecard") then reports Branch-Protection,
Token-Permissions, Pinned-Dependencies and SAST from these settings.
