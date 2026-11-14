#!/usr/bin/env bash
# Prints a SLSA v1 provenance predicate for a release artifact, in the shape actions/attest-build-provenance uses,
# with the Dockerfile base images as resolved dependencies. Outside GitHub Actions it describes a local build.
# Usage: hack/release/provenance.sh > predicate.json
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/../.." && pwd)

server=${GITHUB_SERVER_URL:-https://github.com}
repository=${GITHUB_REPOSITORY:-alexandrust88/openbao-config-operator}
ref=${GITHUB_REF:-refs/heads/local}
sha=${GITHUB_SHA:-$(git -C "$repo_root" rev-parse --verify -q HEAD || echo unknown)}
workflow_ref=${GITHUB_WORKFLOW_REF:-$repository/.github/workflows/release.yml@$ref}
started=$(date -u +%Y-%m-%dT%H:%M:%SZ)

if [ -n "${GITHUB_RUN_ID:-}" ]; then
  builder_id="$server/$workflow_ref"
  invocation_id="$server/$repository/actions/runs/$GITHUB_RUN_ID/attempts/${GITHUB_RUN_ATTEMPT:-1}"
else
  builder_id="local://hack/release/dry-run.sh"
  invocation_id="local://$(hostname -s 2>/dev/null || echo host)/$started"
fi

base_images=$(awk 'toupper($1) == "FROM" { for (i = 2; i <= NF; i++) if ($i ~ /@sha256:/) print $i }' "$repo_root/Dockerfile")

jq -n \
  --arg server "$server" \
  --arg repository "$repository" \
  --arg ref "$ref" \
  --arg sha "$sha" \
  --arg workflow_path "${workflow_ref#"$repository"/}" \
  --arg event_name "${GITHUB_EVENT_NAME:-local}" \
  --arg repository_id "${GITHUB_REPOSITORY_ID:-}" \
  --arg repository_owner_id "${GITHUB_REPOSITORY_OWNER_ID:-}" \
  --arg runner_environment "${RUNNER_ENVIRONMENT:-local}" \
  --arg builder_id "$builder_id" \
  --arg invocation_id "$invocation_id" \
  --arg started "$started" \
  --arg base_images "$base_images" \
  '{
    buildDefinition: {
      buildType: "https://actions.github.io/buildtypes/workflow/v1",
      externalParameters: {
        workflow: {ref: $ref, repository: ($server + "/" + $repository), path: ($workflow_path | sub("@.*$"; ""))}
      },
      internalParameters: {
        github: {
          event_name: $event_name,
          repository_id: $repository_id,
          repository_owner_id: $repository_owner_id,
          runner_environment: $runner_environment
        }
      },
      resolvedDependencies: (
        [{uri: ("git+" + $server + "/" + $repository + "@" + $ref), digest: {gitCommit: $sha}}] +
        [$base_images | split("\n")[] | select(length > 0) | split("@") |
          {uri: ("pkg:docker/" + .[0]), digest: {sha256: (.[1] | ltrimstr("sha256:"))}}]
      )
    },
    runDetails: {
      builder: {id: $builder_id},
      metadata: {invocationId: $invocation_id, startedOn: $started}
    }
  }'
