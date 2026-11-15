#!/usr/bin/env bash
# Deploys the PostgreSQL of the live kit's database component on the kind e2e cluster (hack/e2e/postgres.yaml) with
# the root password that examples/live/create-secrets.sh generated into live-db-root of the kit namespace, and waits
# until it accepts connections. Run after create-secrets.sh.
set -euo pipefail

kit_ns=${1:?usage: postgres.sh KIT_NAMESPACE}
ns=postgres
dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

work_dir=$(mktemp -d)
trap 'rm -rf -- "$work_dir"' EXIT
umask 077
kubectl -n "$kit_ns" get secret live-db-root -o 'jsonpath={.data.password}' | base64 -d >"$work_dir/password"
if [ ! -s "$work_dir/password" ]; then
  printf '%s: secret %s/live-db-root has no password; run create-secrets.sh first\n' "${0##*/}" "$kit_ns" >&2
  exit 1
fi

kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$ns" create secret generic postgres-auth --from-file=password="$work_dir/password" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl apply -f "$dir/postgres.yaml" >/dev/null
kubectl -n "$ns" rollout status deployment/postgres --timeout=3m
