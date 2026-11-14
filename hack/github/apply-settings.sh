#!/usr/bin/env bash
# Applies the repository settings in docs/maintainers/github-settings.md with the GitHub API. Idempotent: rulesets
# and environments are updated by name or created. Needs gh authenticated as a repository admin, and jq.
# A private repository on the free plan has no rulesets, environment protection rules, code scanning, secret
# scanning or Pages: the script skips each with a warning and applies the rest; run it again once the repository is
# public (or on GitHub Pro) to apply them.
# Usage: hack/github/apply-settings.sh <owner/repo>
# Env:   RELEASE_REVIEWER (default the repository owner), REQUIRE_SIGNED_COMMITS (default false),
#        SOLO_MAINTAINER (default true; set false once MAINTAINERS.md lists a second maintainer)
set -euo pipefail

[ $# -eq 1 ] || { echo "usage: $0 <owner/repo>" >&2; exit 2; }
repo=$1
owner=${repo%%/*}
reviewer=${RELEASE_REVIEWER:-$owner}
require_signed=${REQUIRE_SIGNED_COMMITS:-false}
solo=${SOLO_MAINTAINER:-true}
case $solo in true | false) ;; *) echo "SOLO_MAINTAINER must be true or false" >&2; exit 2 ;; esac

# GitHub Actions app id: pins each required check to checks reported by Actions, not by any app with that name.
actions_app_id=15368
# Built-in admin repository role. A personal repository has no teams and only its owner holds admin, so this bypass
# actor is the owner.
admin_role_id=5

required_checks=(
  lint test integration namespaces livekit verify-generated api-coverage govulncheck manifests
  e2e
  "analyze (go)" "analyze (actions)"
  dependency-review
  gitleaks
  osv-scanner
  actionlint zizmor pinact poutine
  "Conventional commit title"
  pages
)

allowed_actions=(
  "astral-sh/setup-uv@*"
  "crazy-max/ghaction-github-labeler@*"
  "docker/build-push-action@*"
  "docker/login-action@*"
  "docker/setup-buildx-action@*"
  "googleapis/release-please-action@*"
  "helm/chart-testing-action@*"
  "ossf/scorecard-action@*"
  "sigstore/cosign-installer@*"
)

api() { gh api -H "X-GitHub-Api-Version: 2022-11-28" "$@"; }
put_json() { api -X "$1" "$2" --input - >/dev/null; }
warn() { echo "WARNING: $1: skipped, needs a public repository or GitHub Pro/Team" >&2; }

# unsupported matches the answer GitHub gives for a feature the repository's plan lacks.
unsupported() {
  grep -qiE 'upgrade to github pro|make this repository public|plan does not support|advanced security' <<<"$1"
}

# try runs an API call; a plan error becomes a warning naming the setting, any other error fails the script, so it
# must not run in a pipeline or a subshell.
try() {
  local setting=$1 out
  shift
  if out=$("$@" 2>&1); then return 0; fi
  if unsupported "$out"; then warn "$setting"; return 1; fi
  echo "$out" >&2
  exit 1
}

private=$(api "repos/$repo" --jq .private)
plan=$(api user --jq '.plan.name // ""')
limited=false
if [ "$private" = true ] && [ "$plan" = free ]; then
  limited=true
  echo "private repository on the free plan: rulesets, environment protection, code scanning and Pages are skipped"
fi

reviewer_id=$(api "users/$reviewer" --jq .id)

echo "general settings (solo maintainer: $solo)"
put_json PATCH "repos/$repo" <<'JSON'
{
  "allow_merge_commit": false,
  "allow_squash_merge": true,
  "allow_rebase_merge": false,
  "allow_auto_merge": false,
  "allow_update_branch": true,
  "delete_branch_on_merge": true,
  "squash_merge_commit_title": "PR_TITLE",
  "squash_merge_commit_message": "PR_BODY",
  "has_discussions": true,
  "has_wiki": false
}
JSON

echo "security features"
try "secret scanning and push protection" put_json PATCH "repos/$repo" <<'JSON' || true
{
  "security_and_analysis": {
    "secret_scanning": {"status": "enabled"},
    "secret_scanning_push_protection": {"status": "enabled"}
  }
}
JSON
try "private vulnerability reporting" api -X PUT "repos/$repo/private-vulnerability-reporting" || true
api -X PUT "repos/$repo/vulnerability-alerts" >/dev/null
api -X PUT "repos/$repo/automated-security-fixes" >/dev/null
try "code scanning default setup" put_json PATCH "repos/$repo/code-scanning/default-setup" \
  <<<'{"state": "not-configured"}' || true
try "immutable releases" api -X PUT "repos/$repo/immutable-releases" || true

echo "actions policy"
echo '{"enabled": true, "allowed_actions": "selected", "sha_pinning_required": true}' |
  put_json PUT "repos/$repo/actions/permissions"
jq -n '{github_owned_allowed: true, verified_allowed: false, patterns_allowed: $ARGS.positional}' \
  --args "${allowed_actions[@]}" | put_json PUT "repos/$repo/actions/permissions/selected-actions"
echo '{"default_workflow_permissions": "read", "can_approve_pull_request_reviews": false}' |
  put_json PUT "repos/$repo/actions/permissions/workflow"
try "fork pull request approval policy" put_json PUT "repos/$repo/actions/permissions/fork-pr-contributor-approval" \
  <<<'{"approval_policy": "all_external_contributors"}' || true

signature_rule='[]'
if [ "$require_signed" = true ]; then
  signature_rule='[{"type": "required_signatures"}]'
fi

rulesets=true
if [ "$limited" = true ]; then
  rulesets=false
elif ! out=$(api "repos/$repo/rulesets" 2>&1); then
  unsupported "$out" || { echo "$out" >&2; exit 1; }
  rulesets=false
fi

ruleset_id() { api "repos/$repo/rulesets" --jq ".[] | select(.name == \"$1\") | .id"; }

upsert_ruleset() {
  local name=$1 body=$2 id
  if [ "$rulesets" = false ]; then
    warn "ruleset $name"
    return
  fi
  id=$(ruleset_id "$name")
  if [ -n "$id" ]; then
    echo "update ruleset $name"
    put_json PUT "repos/$repo/rulesets/$id" <<<"$body"
  else
    echo "create ruleset $name"
    put_json POST "repos/$repo/rulesets" <<<"$body"
  fi
}

# Release branches are release-X.Y; the pattern leaves out release-please--branches--*, which release-please creates
# and force-pushes.
release_branches="refs/heads/release-[0-9]*"

main_ruleset=$(jq -n \
  --arg release_branches "$release_branches" \
  --argjson app "$actions_app_id" \
  --argjson signatures "$signature_rule" \
  --argjson solo "$solo" \
  '{
    name: "main",
    target: "branch",
    enforcement: "active",
    bypass_actors: [],
    conditions: {ref_name: {include: ["~DEFAULT_BRANCH", $release_branches], exclude: []}},
    rules: ([
      {type: "deletion"},
      {type: "non_fast_forward"},
      {type: "required_linear_history"},
      {type: "pull_request", parameters: {
        required_approving_review_count: (if $solo then 0 else 1 end),
        dismiss_stale_reviews_on_push: true,
        require_code_owner_review: ($solo | not),
        require_last_push_approval: ($solo | not),
        required_review_thread_resolution: true,
        allowed_merge_methods: ["squash"]
      }},
      {type: "required_status_checks", parameters: {
        strict_required_status_checks_policy: true,
        do_not_enforce_on_create: false,
        required_status_checks: [$ARGS.positional[] | {context: ., integration_id: $app}]
      }},
      {type: "code_scanning", parameters: {code_scanning_tools: [
        {tool: "CodeQL", security_alerts_threshold: "high_or_higher", alerts_threshold: "errors"}
      ]}}
    ] + $signatures)
  }' --args "${required_checks[@]}")
upsert_ruleset main "$main_ruleset"

# A new release-* branch is a deployment target of the release-please environment, which holds the App key.
release_branch_ruleset=$(jq -n --arg release_branches "$release_branches" --argjson admin "$admin_role_id" '{
  name: "release-branches",
  target: "branch",
  enforcement: "active",
  bypass_actors: [{actor_id: $admin, actor_type: "RepositoryRole", bypass_mode: "always"}],
  conditions: {ref_name: {include: [$release_branches], exclude: []}},
  rules: [{type: "creation"}]
}')
upsert_ruleset release-branches "$release_branch_ruleset"

tag_ruleset=$(jq -n \
  --argjson admin "$admin_role_id" \
  '{
    name: "release-tags",
    target: "tag",
    enforcement: "active",
    bypass_actors: [{actor_id: $admin, actor_type: "RepositoryRole", bypass_mode: "always"}],
    conditions: {ref_name: {include: ["refs/tags/v*"], exclude: []}},
    rules: [
      {type: "creation"},
      {type: "update", parameters: {update_allows_fetch_and_merge: false}},
      {type: "deletion"}
    ]
  }')
upsert_ruleset release-tags "$tag_ruleset"

# Bypass actors skip every rule of their ruleset, so the signature rule lives in its own ruleset without any.
if [ "$require_signed" = true ]; then
  upsert_ruleset release-tags-signed "$(jq -n '{
    name: "release-tags-signed",
    target: "tag",
    enforcement: "active",
    bypass_actors: [],
    conditions: {ref_name: {include: ["refs/tags/v*"], exclude: []}},
    rules: [{type: "required_signatures"}]
  }')"
elif [ "$rulesets" = true ] && id=$(ruleset_id release-tags-signed) && [ -n "$id" ]; then
  echo "delete ruleset release-tags-signed (REQUIRE_SIGNED_COMMITS=false)"
  api -X DELETE "repos/$repo/rulesets/$id" >/dev/null
fi

add_deployment_policy() {
  local env=$1 name=$2 type=$3 existing
  existing=$(api "repos/$repo/environments/$env/deployment-branch-policies" \
    --jq ".branch_policies[] | select(.name == \"$name\" and .type == \"$type\") | .id")
  if [ -z "$existing" ]; then
    jq -n --arg name "$name" --arg type "$type" '{name: $name, type: $type}' |
      put_json POST "repos/$repo/environments/$env/deployment-branch-policies"
  fi
}

# upsert_environment creates an environment with its protection and deployment branch policies; where the plan has
# neither, it creates the bare environment and warns.
upsert_environment() {
  local env=$1 body=$2
  shift 2
  echo "environment $env"
  if try "environment $env protection" put_json PUT "repos/$repo/environments/$env" <<<"$body"; then
    for policy in "$@"; do
      add_deployment_policy "$env" "${policy% *}" "${policy##* }"
    done
  else
    echo '{}' | put_json PUT "repos/$repo/environments/$env"
  fi
}

branch_policy='{"deployment_branch_policy": {"protected_branches": false, "custom_branch_policies": true}}'
if [ "$limited" = true ]; then
  warn "environment release required reviewers"
  release_env=$branch_policy
else
  release_env=$(jq -n --argjson reviewer "$reviewer_id" --argjson solo "$solo" '{
    wait_timer: 0,
    prevent_self_review: ($solo | not),
    reviewers: [{type: "User", id: $reviewer}],
    deployment_branch_policy: {protected_branches: false, custom_branch_policies: true}
  }')
fi
upsert_environment release "$release_env" "v* tag"
upsert_environment release-please "$branch_policy" "main branch" "release-[0-9]* branch"
upsert_environment github-pages "$branch_policy" "main branch"

echo "pages source"
if [ "$limited" = true ]; then
  warn "pages source"
elif api "repos/$repo/pages" >/dev/null 2>&1; then
  try "pages source" put_json PUT "repos/$repo/pages" <<<'{"build_type": "workflow"}' || true
else
  try "pages source" put_json POST "repos/$repo/pages" <<<'{"build_type": "workflow"}' || true
fi

echo "done; manual steps: Discussions categories, the Renovate app, the release-please GitHub App (see the doc)"
