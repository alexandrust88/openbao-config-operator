# CI security

How the GitHub Actions workflows in `.github/workflows` are protected, what each security job checks, and what to do
when a change adds an action or a tool. The repository settings that back this page (rulesets, allowed actions,
environments) are in [GitHub settings](../maintainers/github-settings.md).

## Trust model

| Workflow trigger | Who can start it | Token and secrets |
|------------------|------------------|-------------------|
| `pull_request` | anyone with a fork; runs for outside contributors wait for a maintainer's approval | read-only token, no secrets |
| `push` to `main` or `release-*`, `schedule`, `workflow_dispatch` | maintainers (merged pull requests) | per job, see below |
| `push` of a `v*` tag | only the owner (tag ruleset, admin role bypass) | the publish jobs, which start only after a maintainer approves the `release` environment |
| `workflow_run` (`pages.yml` only) | a successful tag-push run of `release` | runs the default branch, reads no artifact of the triggering run |

Rules every workflow follows:

- `permissions: {}` at the top; each job asks for the scopes it needs. Only the jobs that publish, sign or attest get
  `packages: write`, `id-token: write` or `attestations: write`, and none of them runs on a pull request.
- No `pull_request_target`, `issue_comment` or `issues` trigger, and no job checks out pull request code with a
  privileged token.
- Every `actions/checkout` sets `persist-credentials: false`.
- Values a contributor controls (pull request title, branch, tag, inputs) reach shell scripts only through `env:`,
  never as `${{ }}` inside `run:`.
- Every action is pinned to a full commit SHA with a `# vX.Y.Z` comment; every container image is pinned by digest;
  every binary the Makefile or a workflow downloads itself has its version and SHA-256 recorded in the repository
  (bao, actionlint and ocm per OS and architecture in the Makefile, which refuses a platform without a pin;
  helm-unittest, pinact and poutine in the workflow `env:`), or is built with `go install` at an exact version (the
  Go checksum database verifies it). The envtest control-plane binaries are fetched by `setup-envtest`, which checks
  them against its release index, and cosign by the SHA-pinned `sigstore/cosign-installer` action.
- Signing is keyless (OIDC to Sigstore), so the repository holds no signing key. The only repository secret is the
  release-please GitHub App key, in the `release-please` environment.
- GitHub-hosted runners only. No AI agent runs in CI.

## Security jobs

| Job (workflow) | Checks | Blocks the merge |
|----------------|--------|------------------|
| `actionlint` (`workflows.yml`) | workflow syntax, expressions and shellcheck on `run:`; shellcheck on `hack/release/*.sh`, `hack/github/*.sh` and `hack/docs/*.sh` | yes |
| `zizmor` (`workflows.yml`) | template injection, token permissions, dangerous triggers, cache poisoning, unpinned actions; uploads the full audit to code scanning | yes, on medium or higher |
| `pinact` (`workflows.yml`) | every `uses:` is a full SHA, its version comment resolves to that SHA upstream (no impostor commits from forks), and the release is at least 7 days old | yes |
| `poutine` (`workflows.yml`) | untrusted checkout plus execution, injection, known-vulnerable actions, self-hosted runners on pull requests | yes |
| `analyze (go)`, `analyze (actions)` (`codeql.yml`) | CodeQL for the Go code and for the workflows | yes, high alerts |
| `gitleaks` (`secret-scan.yml`) | secrets in the new commits; the weekly run rescans the history | yes |
| `osv-scanner` (`vulnerabilities.yml`), `govulncheck` (`ci.yml`) | known vulnerabilities in dependencies | yes |
| `dependency-review` (`dependency-review.yml`) | new dependencies with high-severity advisories or a denied licence | yes |
| `scorecard` (`scorecard.yml`) | OpenSSF Scorecard of the repository settings and workflows | no, reported to code scanning |

Accepted findings are suppressed one by one with a reason: `.github/zizmor.yml` or a `# zizmor: ignore[...]` comment on
the line, and `.github/poutine.yml`. Both files are owned by the maintainers (`.github/CODEOWNERS`).

## Adding an action or a tool

1. Prefer a `run:` step with `gh`, `curl` or a tool the runner already has over a new third-party action.
2. Pick a release that is at least 7 days old and pin the full commit SHA with the exact version as a comment. Resolve
   the SHA from the tag yourself (`git ls-remote https://github.com/<owner>/<repo> refs/tags/<version>`) or run
   `pinact run` locally, which reads `.github/pinact.yaml`.
3. Read the action's `action.yml` at that commit: nested `uses:` must be SHA-pinned too, and inputs that pick a tool
   version must not default to `latest` (set them, as the workflows do for `cosign-release`).
4. Give the job only the permissions the action documents, and keep secrets in the step that needs them.
5. Add a third-party action to the allowed-actions list in `hack/github/apply-settings.sh` and
   [GitHub settings](../maintainers/github-settings.md), including any action it calls.
6. For a binary, record the version and SHA-256 in the step's `env:` with a `# renovate:` comment, like the pinact and
   poutine installs in `workflows.yml`; for an image, pin `tag@sha256:digest`. Renovate then proposes updates after
   the same 7-day wait.
7. If you add, rename or remove a job that should block merges, update the required-check list in both
   `hack/github/apply-settings.sh` and [GitHub settings](../maintainers/github-settings.md).

## Not adopted

- **Workflow lockfiles** (`.github/workflows/actions.lock`): a GitHub Enterprise Cloud feature, not available to this
  repository. They also do not cover reusable workflows or images; SHA pins plus the `pinact` check do.
- **harden-runner egress blocking**: a block list can only be written from an audit run of each job, and the agent
  is downloaded at run time and reports to a third party. Adding it to the signing jobs would add code with access to
  the OIDC token. The release jobs instead pull only digest-pinned images and checksum-verified tools, hold no
  long-lived secret and wait for a maintainer's approval in the `release` environment.
