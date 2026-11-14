#!/usr/bin/env bash
# Applies the live kit's root/full overlay to the kind e2e cluster against the in-cluster OpenBao dev server, with
# the operator installed from the chart, and fails when an object is not Ready unless test/livekit/exceptions or
# the kind list below excuses it. Run after `make setup-test-e2e` and loading E2E_IMG into kind.
set -euo pipefail

: "${E2E_IMG:?E2E_IMG must name the operator image loaded into kind}"
kind_cluster=${KIND_CLUSTER:-openbao-config-operator-test-e2e}
timeout=${LIVEKIT_TIMEOUT:-10m}
overlay=root/full
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
helm=${HELM:-$repo_root/bin/helm}
kustomize=${KUSTOMIZE:-$repo_root/bin/kustomize}
yq=${YQ:-$repo_root/bin/yq}
namespace=$(sed -n 's/^ *- kubernetesNamespace=//p' "$repo_root/examples/live/params/kustomization.yaml")
root_sa=$(sed -n 's/^ *- rootServiceAccount=//p' "$repo_root/examples/live/params/kustomization.yaml")
root_role=$(sed -n 's/^ *- rootAuthRole=//p' "$repo_root/examples/live/params/kustomization.yaml")
audience=$(sed -n 's/^ *- audience=//p' "$repo_root/examples/live/params/kustomization.yaml")

# Not Ready on this dev server only; the bao -dev harness of TestEndToEnd allows both.
kind_excuses=(
  "Audit|*|the dev server does not set unsafe_allow_api_audit_creation, so it refuses audit devices from the API"
)

err() { printf '%s: %s\n' "${0##*/}" "$*" >&2; }

if [ "$(kubectl config current-context)" != "kind-$kind_cluster" ]; then
  err "kubectl context is not kind-$kind_cluster"
  exit 1
fi
cd "$repo_root"

bao() { kubectl -n openbao exec -i deployment/openbao -- env BAO_ADDR=http://127.0.0.1:8200 BAO_TOKEN=root bao "$@"; }

kubectl apply -f hack/e2e/openbao-dev.yaml -f test/e2e/testdata/openbao-auth.yaml
kubectl -n openbao rollout status deployment/openbao --timeout=180s
bao auth enable kubernetes 2>/dev/null || true
bao write auth/kubernetes/config kubernetes_host=https://kubernetes.default.svc >/dev/null
printf 'path "*" {\n  capabilities = ["create", "read", "update", "patch", "delete", "list", "sudo"]\n}\n' |
  bao policy write livekit-root - >/dev/null
bao write "auth/kubernetes/role/$root_role" "bound_service_account_names=$root_sa" \
  "bound_service_account_namespaces=$namespace" "audience=$audience" token_policies=livekit-root token_ttl=1h >/dev/null

"$helm" upgrade --install openbao-config-operator charts/openbao-config-operator \
  --namespace openbao-config-operator --create-namespace \
  --set "image.repository=${E2E_IMG%:*}" --set "image.tag=${E2E_IMG##*:}" \
  --set "extraArgs={--drift-interval-critical=1m,--drift-interval-standard=1m}" --wait --timeout 5m

kubectl create namespace "$namespace" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$namespace" create serviceaccount "$root_sa" --dry-run=client -o yaml | kubectl apply -f -
examples/live/create-secrets.sh --namespace "$namespace"

rendered=$(mktemp)
trap 'rm -f -- "$rendered"' EXIT
"$kustomize" build test/livekit/kind >"$rendered"
kubectl apply -f "$rendered" >/dev/null
names=()
while IFS= read -r ref; do names+=("$ref"); done < <("$yq" -N 'select(.apiVersion | test("openbaoconfig\.io/")) | .kind + "." + (.apiVersion | split("/") | .[0]) + "/" + .metadata.name' "$rendered")
prefix=$("$yq" -N 'select(.kind == "OpenBaoServer") | .metadata.labels["live.openbaoconfig.io/overlay"]' "$rendered")-
echo "waiting up to $timeout for ${#names[@]} objects"
kubectl wait --for=condition=Ready --timeout="$timeout" -n "$namespace" "${names[@]}" >/dev/null 2>&1 || true

excuses=("${kind_excuses[@]}")
while IFS= read -r line; do excuses+=("$line"); done < <(
  for f in test/livekit/exceptions/*.yaml; do
    "$yq" -N '.notReady[] | .kind + "|" + (.name // "*") + "|" + ((.overlays // ["*"]) | join(",")) + "|" + (.reason | sub("\n"; " "))' "$f"
  done | awk -F'|' '{ n = split($3, g, ","); for (i = 1; i <= n; i++) print $1 "|" $2 "|" g[i] "|" $4 }' |
    while IFS='|' read -r kind name glob reason; do
      # shellcheck disable=SC2053 # globs from the exceptions files
      if [[ $overlay == $glob ]]; then printf '%s|%s|%s\n' "$kind" "$name" "$reason"; fi
    done
)

failed=0 ready=0 excused=0
printf '\n%-26s %-44s %-22s %s\n' KIND NAME REASON NOTE
for ref in "${names[@]}"; do
  kind=${ref%%.*} name=${ref##*/}
  state=$(kubectl -n "$namespace" get "$ref" -o 'jsonpath={.status.conditions[?(@.type=="Ready")].status}|{.status.conditions[?(@.type=="Ready")].reason}|{.status.conditions[?(@.type=="Ready")].message}')
  if [ "${state%%|*}" = True ]; then
    ready=$((ready + 1))
    continue
  fi
  note=""
  for e in "${excuses[@]}"; do
    IFS='|' read -r ekind ename ereason <<<"$e"
    # shellcheck disable=SC2053
    if [ "$ekind" = "$kind" ] && [[ ${name#"$prefix"} == $ename ]]; then note="excused: $ereason"; break; fi
  done
  if [ -n "$note" ]; then excused=$((excused + 1)); else failed=$((failed + 1)); note=$(cut -d'|' -f3 <<<"$state"); fi
  printf '%-26s %-44s %-22s %.140s\n' "$kind" "$name" "$(cut -d'|' -f2 <<<"$state")" "$note"
done
printf '\n%s on kind: %d objects, %d Ready, %d excused, %d failed\n' "$overlay" "${#names[@]}" "$ready" "$excused" "$failed"
[ "$failed" -eq 0 ]
