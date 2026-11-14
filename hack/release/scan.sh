#!/usr/bin/env bash
# Release vulnerability gate: fails on any high or critical finding in the given SBOMs that hack/release/grype.yaml does
# not ignore. Every ignore entry needs a reason of the form "until YYYY-MM-DD: <justification>"; an expired entry fails.
# Writes the vulnerability database status next to the first SBOM (grype-db-status.json) and, in GitHub Actions, to the
# step summary.
# Usage: hack/release/scan.sh <sbom.spdx.json>...
set -euo pipefail

[ $# -ge 1 ] || { echo "usage: $0 <sbom>..." >&2; exit 2; }
repo_root=$(cd "$(dirname "$0")/../.." && pwd)
config=$repo_root/hack/release/grype.yaml
grype=${GRYPE:-grype}
yq=${YQ:-yq}

today=$(date -u +%F)
bad=0
while IFS=$'\t' read -r vuln reason; do
  [ -n "$vuln" ] || continue
  if ! [[ $reason =~ ^until\ ([0-9]{4}-[0-9]{2}-[0-9]{2}):\ .+ ]]; then
    echo "scan: ignore for $vuln needs reason \"until YYYY-MM-DD: <justification>\"" >&2
    bad=1
  elif [[ ${BASH_REMATCH[1]} < $today ]]; then
    echo "scan: ignore for $vuln expired on ${BASH_REMATCH[1]}" >&2
    bad=1
  fi
done < <("$yq" -r '.ignore // [] | .[] | [.vulnerability // "", .reason // ""] | @tsv' "$config")
[ "$bad" -eq 0 ] || exit 1

for sbom in "$@"; do
  echo "scan: $sbom"
  "$grype" "sbom:$sbom" --config "$config" --fail-on high --output table
done

status=$(dirname "$1")/grype-db-status.json
"$grype" db status -o json >"$status"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  jq -r '"Vulnerability database: built \(.built), schema \(.schemaVersion), checksum \(.checksum
    // ((.from // "" | capture("checksum=(?<c>[^&]+)") | .c | sub("%3A"; ":"))? ) // "unknown")"' "$status" >>"$GITHUB_STEP_SUMMARY"
fi
