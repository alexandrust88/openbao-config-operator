#!/usr/bin/env bash
# Fails unless the argument is a release tag: v plus a SemVer 2.0 version (no leading zeros in numeric identifiers,
# no empty identifiers). Build metadata (+...) is valid SemVer but rejected, because OCI tags cannot contain "+".
# Usage: hack/release/check-version.sh <vX.Y.Z[-prerelease]>
set -euo pipefail

[ $# -eq 1 ] || { echo "usage: $0 <vX.Y.Z[-prerelease]>" >&2; exit 2; }

num='(0|[1-9][0-9]*)'
pre_id='(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
re="^v$num\.$num\.$num(-$pre_id(\.$pre_id)*)?$"

[[ $1 =~ $re ]] || { echo "check-version: '$1' is not a release tag (vX.Y.Z[-prerelease], SemVer 2.0, no build metadata)" >&2; exit 1; }
