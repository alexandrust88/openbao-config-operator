#!/usr/bin/env bash
# Fails unless an OCI tag is absent from its registry: an existing tag would be overwritten, and any error other than
# not-found means absence is unproven, so the release stops instead of retagging.
# Usage: hack/release/check-absent.sh <registry/repository:tag>
# Environment: CRANE.
set -euo pipefail

[ $# -eq 1 ] || { echo "usage: $0 <registry/repository:tag>" >&2; exit 2; }
ref=$1

if out=$("${CRANE:-crane}" digest "$ref" 2>&1); then
  echo "check-absent: $ref already exists ($out); published artifacts are immutable, tag a new version" >&2
  exit 1
fi
grep -qE 'MANIFEST_UNKNOWN|NAME_UNKNOWN|status code 404' <<<"$out" || { echo "check-absent: cannot check whether $ref exists: $out" >&2; exit 1; }
echo "$ref is absent"
