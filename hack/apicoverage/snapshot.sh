#!/usr/bin/env bash
# Writes the OpenAPI document of a `bao server -dev` with every builtin auth method and secrets
# engine mounted at its default path. Usage: snapshot.sh <bao-binary> <output.json> [port]
set -euo pipefail

err() { printf '%s: %s\n' "${0##*/}" "$*" >&2; }

if [ "$#" -lt 2 ]; then
	err "usage: snapshot.sh <bao-binary> <output.json> [port]"
	exit 2
fi
bao_bin=$1
out=$2
port=${3:-28417}
addr="http://127.0.0.1:${port}"
token=root

bao_pid=""
tmp=""
cleanup() {
	if [ -n "$bao_pid" ]; then
		kill "$bao_pid" 2>/dev/null || true
		wait "$bao_pid" 2>/dev/null || true
	fi
	if [ -n "$tmp" ]; then
		rm -f -- "$tmp"
	fi
	return 0
}
trap cleanup EXIT

api() {
	curl -fsS --max-time 30 -H "X-Vault-Token: ${token}" "$@"
}

"$bao_bin" server -dev -dev-root-token-id="$token" -dev-listen-address="127.0.0.1:${port}" >/dev/null 2>&1 &
bao_pid=$!

ready=""
for _ in $(seq 1 50); do
	if curl -fsS --max-time 2 "${addr}/v1/sys/health" >/dev/null 2>&1; then
		ready=yes
		break
	fi
	sleep 0.2
done
if [ -z "$ready" ]; then
	err "bao dev server did not become ready on ${addr}"
	exit 1
fi

catalog=$(api "${addr}/v1/sys/plugins/catalog")
if ! jq -e '(.data.auth | type == "array") and (.data.secret | type == "array")' >/dev/null <<<"$catalog"; then
	err "unexpected plugin catalog response"
	exit 1
fi

# the database engine is builtin but absent from the catalog's secret list; dev mode already
# mounts kv v2 at secret/, so kv/ below is kv v1 and both versions are in the document.
auth_types=$(jq -r '.data.auth[]' <<<"$catalog")
secret_types=$(jq -r '.data.secret[]' <<<"$catalog"; printf 'database\n')

for t in $auth_types; do
	api -X POST -d "$(jq -nc --arg t "$t" '{type: $t}')" "${addr}/v1/sys/auth/${t}" >/dev/null
done
for t in $secret_types; do
	api -X POST -d "$(jq -nc --arg t "$t" '{type: $t}')" "${addr}/v1/sys/mounts/${t}" >/dev/null
done

tmp=$(mktemp "${out}.XXXXXX")
api "${addr}/v1/sys/internal/specs/openapi?generic_mount_paths=true" | jq -S . >"$tmp"
chmod 0644 "$tmp"
mv -- "$tmp" "$out"
tmp=""
