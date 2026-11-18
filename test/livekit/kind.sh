#!/usr/bin/env bash
# Applies the live kit to the kind e2e cluster against the in-cluster three-node OpenBao (hack/e2e/openbao.yaml), with
# the operator installed from the chart: test/livekit/kind holds every connection mode. Once they are Ready it checks
# that a child login cannot leave its OpenBao namespace, deletes the active OpenBao node and switches root/full to
# Observe (observe/full): every object must be Ready again and the operator must not write to OpenBao meanwhile. An
# object that is not Ready fails the run unless test/livekit/exceptions excuses it without env: dev, which limits an
# excuse to bao server -dev (outside Kubernetes, inmem storage). Run after `make setup-test-e2e` and loading E2E_IMG
# into kind; it reuses the OpenBao and chart that the e2e suite leaves with E2E_KEEP=true.
set -euo pipefail

: "${E2E_IMG:?E2E_IMG must name the operator image loaded into kind}"
kind_cluster=${KIND_CLUSTER:-openbao-config-operator-test-e2e}
timeout=${LIVEKIT_TIMEOUT:-10m}
stall_polls=60
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
helm=${HELM:-$repo_root/bin/helm}
kustomize=${KUSTOMIZE:-$repo_root/bin/kustomize}
yq=${YQ:-$repo_root/bin/yq}
out_dir=${LIVEKIT_OUT:-$repo_root/.tmp/livekit-kind}
mkdir -p "$out_dir"
operator_ns=openbao-config-operator
sibling_ns=team-b
param() { sed -n "s/^ *- $1=//p" "$repo_root/examples/live/params/kustomization.yaml"; }
namespace=$(param kubernetesNamespace)
audience=$(param audience)
tenant_ns=$(param tenantNamespace)
root_sa=$(param rootServiceAccount)
tenant_role=$(param tenantAuthRole)
tenant_sa=$(param tenantServiceAccount)
child_role=$(param childAuthRole)
child_sa=$(param childServiceAccount)
labels=(root-full r2c-full opt-full child-full ccr-full root-min child-min)

err() { printf '%s: %s\n' "${0##*/}" "$*" >&2; }

if [ "$(kubectl config current-context)" != "kind-$kind_cluster" ]; then
  err "kubectl context is not kind-$kind_cluster"
  exit 1
fi
cd "$repo_root"

# The kit overlay of a live.obco.dev/overlay label, which the overlay globs of the exceptions match.
overlay_dir() {
  case $1 in
    root-full) echo root/full ;;
    r2c-full) echo root-to-child/full ;;
    opt-full) echo optional/full ;;
    child-full) echo child/full ;;
    ccr-full) echo child-connection-root-login/full ;;
    root-min) echo root/minimal ;;
    child-min) echo child/minimal ;;
    *) echo "$1" ;;
  esac
}

# Waits until every OpenBao pod is ready, exactly one is the raft leader and only that pod carries the openbao-active
# label of the openbao-active Service, and sets active to that pod.
wait_openbao() {
  local deadline=$(($(date +%s) + 300)) i ready labelled
  while :; do
    ready=$(kubectl -n openbao get statefulset openbao -o 'jsonpath={.status.readyReplicas}')
    labelled=$(kubectl -n openbao get pods -l openbao-active=true -o 'jsonpath={.items[*].metadata.name}')
    active=()
    for i in 0 1 2; do
      if [ "$(kubectl -n openbao exec "openbao-$i" -- bao read -field=is_self sys/leader 2>/dev/null)" = true ]; then
        active+=("openbao-$i")
      fi
    done
    if [ "$ready" = 3 ] && [ "${#active[@]}" -eq 1 ] && [ "$labelled" = "${active[0]}" ]; then return 0; fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      err "OpenBao has ${ready:-0} of 3 pods ready, ${#active[@]} active and '$labelled' labelled active"
      return 1
    fi
    sleep 3
  done
}

fixture() { kubectl -n openbao get secret openbao-e2e -o "jsonpath={.data.$1}" | base64 -d; }

rendered=$(mktemp)
observed=$(mktemp)
helm_log=$(mktemp)
writes_before=$(mktemp)
writes_after=$(mktemp)
trap 'rm -f -- "$rendered" "$observed" "$helm_log" "$writes_before" "$writes_after"' EXIT

hack/e2e/tls.sh
KIND_CLUSTER=$kind_cluster test/livekit/kind-plugins.sh "$out_dir/plugins"
plugin_sha=$(cat "$out_dir/plugins/sha256")
kubectl apply -f "$out_dir/plugins/openbao.yaml" -f test/e2e/testdata/openbao-auth.yaml
kubectl -n openbao rollout status statefulset/openbao --timeout=300s
kubectl create namespace "$operator_ns" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$operator_ns" create secret generic livekit-token-file --from-file=token=<(fixture token) \
  --dry-run=client -o yaml | kubectl apply -f -

# The chart rolls out while OpenBao starts and the Secrets are created; conn-cluster-token-file reads the token file.
"$helm" upgrade --install openbao-config-operator charts/openbao-config-operator \
  --namespace "$operator_ns" --create-namespace \
  --set "image.repository=${E2E_IMG%:*}" --set "image.tag=${E2E_IMG##*:}" --set-string image.digest= \
  --set "extraArgs={--drift-interval-critical=1m,--drift-interval-standard=1m}" \
  --set-json 'extraVolumes=[{"name":"livekit-token-file","secret":{"secretName":"livekit-token-file"}}]' \
  --set-json 'extraVolumeMounts=[{"name":"livekit-token-file","mountPath":"/var/run/openbao","readOnly":true}]' \
  --wait --timeout 5m >"$helm_log" 2>&1 &
helm_pid=$!

wait_openbao
echo "OpenBao ready, active node ${active[0]}"
kubectl create namespace "$namespace" --dry-run=client -o yaml | kubectl apply -f -
for sa in "$root_sa" "$tenant_sa" "$child_sa"; do
  kubectl -n "$namespace" create serviceaccount "$sa" --dry-run=client -o yaml | kubectl apply -f -
done
# The AppRole and token logins of the optional overlay use the AppRole and token that OpenBao initializes itself with.
kubectl -n "$namespace" create secret generic live-connection-auth-approle --from-file=role-id=<(fixture role-id) \
  --from-file=secret-id=<(fixture secret-id) --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$namespace" create secret generic live-connection-auth-token --from-file=token=<(fixture token) \
  --dry-run=client -o yaml | kubectl apply -f -
# The kit's servers trust the OpenBao CA through the ConfigMap and Secret that they reference.
ca=$(kubectl -n openbao get configmap openbao-ca -o 'jsonpath={.data.ca\.crt}')
kubectl -n "$namespace" create configmap openbao-ca --from-literal=ca.crt="$ca" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$namespace" create secret generic live-connection-auth-ca --from-literal=ca.crt="$ca" \
  --dry-run=client -o yaml | kubectl apply -f -
examples/live/create-secrets.sh --namespace "$namespace"
hack/e2e/postgres.sh "$namespace"
helm_status=0
wait "$helm_pid" || helm_status=$?
cat "$helm_log"
if [ "$helm_status" -ne 0 ]; then
  err "helm upgrade --install failed"
  exit 1
fi

exceptions=()
for f in test/livekit/exceptions/*.yaml; do
  "$yq" -N '.notReady[] | select(.env != "dev") | .kind + "|" + (.name // "*") + "|" + ((.overlays // ["*"]) | join(",")) + "|" + (.reason | sub("\n"; " "))' "$f"
done >"$out_dir/exceptions.txt"
while IFS= read -r line; do exceptions+=("$line"); done <"$out_dir/exceptions.txt"

# Prints the reason when an exception excuses an object, given its kind, its name without the overlay prefix and
# its kit overlay.
excuse() {
  local kind=$1 name=$2 overlay=$3 e ekind ename eoverlays ereason glob globs
  for e in "${exceptions[@]}"; do
    IFS='|' read -r ekind ename eoverlays ereason <<<"$e"
    [ "$ekind" = "$kind" ] || continue
    # shellcheck disable=SC2053 # globs from the exceptions files
    [[ $name == $ename ]] || continue
    IFS=, read -ra globs <<<"$eoverlays"
    for glob in "${globs[@]}"; do
      # shellcheck disable=SC2053 # globs from the exceptions files
      if [[ $overlay == $glob ]]; then printf '%s' "$ereason"; return 0; fi
    done
  done
  return 1
}

"$kustomize" build test/livekit/kind >"$rendered"
test/livekit/crl-fixture.sh "$rendered" "$namespace"
PLUGIN_SHA=$plugin_sha "$yq" -i '(select(.kind == "Plugin") | .spec.sha256) = strenv(PLUGIN_SHA)' "$rendered"
"$yq" -N 'select(.apiVersion | test("obco\.dev/")) | .kind + "|" + .metadata.name + "|" + .metadata.labels["live.obco.dev/overlay"]' "$rendered" >"$out_dir/inventory.txt"
[ -s "$out_dir/inventory.txt" ] || { err "empty live kit inventory"; exit 1; }

# Objects that cannot work on kind (the exceptions above) are not applied, so every applied object must get Ready.
skipped=()
while IFS='|' read -r kind name label; do
  if reason=$(excuse "$kind" "${name#"$label"-}" "$(overlay_dir "$label")"); then skipped+=("$label|$kind|$name|$reason"); fi
done <"$out_dir/inventory.txt"

drop_skipped() {
  local drop kind name e
  drop=$(for e in "${skipped[@]}"; do IFS='|' read -r _ kind name _ <<<"$e"; printf '%s/%s\n' "$kind" "$name"; done)
  # shellcheck disable=SC2016 # $k is a yq variable
  DROP=$drop "$yq" 'select((.kind + "/" + .metadata.name) as $k | (strenv(DROP) | split("\n") | any_c(. == $k)) | not)' "$1" >"$1.apply"
  mv "$1.apply" "$1"
}

drop_skipped "$rendered"
# credentialConfig.ca_cert must match the CA key that create-secrets.sh generates into live-db-client-ca.
db_ca=$(kubectl -n "$namespace" get secret live-db-client-ca -o 'jsonpath={.data.ca\.crt}' | base64 -d)
DB_CA=$db_ca "$yq" -i '(select(.spec.caPrivateKeySecretRef.name == "live-db-client-ca") | .spec.credentialConfig.ca_cert) = strenv(DB_CA)' "$rendered"
printf '%s\n' "${skipped[@]}" >"$out_dir/exclusions.txt"
echo "not applied on kind: ${#skipped[@]} objects"
"$yq" -N 'select(.apiVersion | test("obco\.dev/")) | .kind + "." + (.apiVersion | split("/") | .[0]) + "/" + .metadata.name' "$rendered" >"$out_dir/applied.txt"
[ -s "$out_dir/applied.txt" ] || { err "no executable live kit objects"; exit 1; }
for label in "${labels[@]}"; do
  grep -q "/$label-" "$out_dir/applied.txt" || { err "no executable objects for $label"; exit 1; }
done
kubectl apply -f "$rendered" >/dev/null
names=()
while IFS= read -r ref; do names+=("$ref"); done <"$out_dir/applied.txt"
types=$(cut -d/ -f1 "$out_dir/applied.txt" | sort -u | paste -sd, -)

# Prints the applied objects that are not Ready for their current generation, one kind.group/name per line, from one
# list call per applied kind (every kit object carries the overlay label).
not_ready() {
  {
    printf 'want %s\n' "${names[@]}"
    kubectl -n "$namespace" get "$types" -l live.obco.dev/overlay \
      -o 'jsonpath={range .items[*]}{.status.conditions[?(@.type=="Ready")].status}|{.metadata.generation}/{.status.observedGeneration}/{.status.conditions[?(@.type=="Ready")].observedGeneration}|{.spec.connectionRef.name}|{.spec.managementPolicy}|{.status.conditions[?(@.type=="Synced")].status}|{.status.conditions[?(@.type=="Synced")].observedGeneration}|{.kind}.{.apiVersion}/{.metadata.name}{"\n"}{end}' |
      awk -F'|' '$1 == "True" {
        split($2, g, "/")
        if (g[1] == g[2] && g[1] == g[3] && ($3 == "" || $4 == "Observe" || ($5 == "True" && $6 == g[1]))) {
          ref = $7; sub(/\/[^\/]*\//, "/", ref); print "ready " ref
        }
      }' || true
  } | awk '$1 == "want" { want[++n] = $2; next } { ok[$2] } END { for (i = 1; i <= n; i++) if (!(want[i] in ok)) print want[i] }'
}

# Waits until every applied object is Ready, sets failed to those that are not, and prints a table per mode, also to
# the job summary. LIVEKIT_TIMEOUT is only the upper bound; observe names the mode of the root/full objects.
settle() {
  local title=$1 observe=$2 deadline best=-1 stalled=0 pending now ref kind name state label mode applied failed_n skipped_n
  local report=apply
  if $observe; then report=failover-observe; fi
  local -a waiting
  case $timeout in *m) deadline=$((${timeout%m} * 60)) ;; *s) deadline=${timeout%s} ;; *) deadline=$timeout ;; esac
  deadline=$(($(date +%s) + deadline))
  # Polls sleep 5s each, so the stall window is at least 5m however slow the API server answers.
  echo "waiting for ${#names[@]} objects (at most $timeout, or until $stall_polls polls pass without progress)"
  while :; do
    waiting=()
    while IFS= read -r ref; do waiting+=("$ref"); done < <(not_ready)
    pending=${#waiting[@]}
    [ "$pending" -eq 0 ] && break
    now=$(date +%s)
    if [ "$best" -lt 0 ] || [ "$pending" -lt "$best" ]; then best=$pending stalled=0; else stalled=$((stalled + 1)); fi
    if [ "$now" -ge "$deadline" ]; then echo "timed out with $pending objects not Ready"; break; fi
    if [ "$stalled" -ge "$stall_polls" ]; then echo "no progress for $stalled polls, $pending objects not Ready"; break; fi
    echo "$pending objects not Ready yet"
    sleep 5
  done

  failed=()
  printf 'kind/name\tresult\n' >"$out_dir/${report}-results.tsv"
  printf '\n%-26s %-52s %-22s %s\n' KIND NAME REASON NOTE
  for ref in "${waiting[@]}"; do
    kind=${ref%%.*} name=${ref##*/}
    state=$(kubectl -n "$namespace" get "$ref" -o 'jsonpath={.status.conditions[?(@.type=="Ready")].status}|{.status.conditions[?(@.type=="Ready")].reason}|{.status.conditions[?(@.type=="Ready")].message}' 2>/dev/null) || state="False|ReadFailed|object absent or API unavailable"
    failed+=("$ref")
    printf '%-26s %-52s %-22s %.140s\n' "$kind" "$name" "$(cut -d'|' -f2 <<<"$state")" "$(cut -d'|' -f3 <<<"$state")"
  done
  for ref in "${names[@]}"; do
    state=Ready
    for pending in "${failed[@]}"; do [ "$pending" != "$ref" ] || state=Failed; done
    printf '%s\t%s\n' "$ref" "$state" >>"$out_dir/${report}-results.tsv"
  done
  for e in "${skipped[@]}"; do
    IFS='|' read -r _ kind name reason <<<"$e"
    printf '%-26s %-52s %-22s %.140s\n' "$kind" "$name" "not applied" "$reason"
  done

  printf '\n%s\n%-34s %8s %6s %7s %12s\n' "$title" MODE APPLIED READY FAILED "NOT APPLIED"
  printf '\n### live kit on kind: %s\n\n| mode | applied | Ready | failed | not applied |\n|---|---|---|---|---|\n' "$title" >>"${GITHUB_STEP_SUMMARY:-/dev/null}"
  for label in "${labels[@]}"; do
    mode=$(overlay_dir "$label")
    if [ "$label" = root-full ] && $observe; then mode=observe/full; fi
    applied=$(printf '%s\n' "${names[@]}" | grep -c "/$label-" || true)
    failed_n=$(printf '%s\n' "${failed[@]}" | grep -c "/$label-" || true)
    skipped_n=$(printf '%s\n' "${skipped[@]}" | grep -c "^$label|" || true)
    printf '%-34s %8d %6d %7d %12d\n' "$mode" "$applied" $((applied - failed_n)) "$failed_n" "$skipped_n"
    printf '| %s | %d | %d | %d | %d |\n' "$mode" "$applied" $((applied - failed_n)) "$failed_n" "$skipped_n" >>"${GITHUB_STEP_SUMMARY:-/dev/null}"
  done
}

# Reads the actual mounted subprocess, beyond successful catalog registration and mount configuration.
plugin_health() {
  local path expected actual
  for path in root-full-sys-plugin-mount auth/root-full-sys-plugin-auth-mount; do
    expected=secret
    case $path in auth/*) expected=auth ;; esac
    # shellcheck disable=SC2016 # the test token is expanded only inside the OpenBao pod
    actual=$(kubectl -n openbao exec "${active[0]}" -- sh -ec 'BAO_TOKEN=$E2E_TOKEN bao read -field=type "$1/health"' sh "$path")
    [ "$actual" = "$expected" ] || { err "$path plugin health returned $actual, want $expected"; return 1; }
  done
}

settle "after apply" false
[ "${#failed[@]}" -eq 0 ] || exit 1
plugin_health

# A child login reaches its own OpenBao namespace only: the child role logs in inside it, the tenant role at the root
# with a policy limited to it. Each must write there and be refused at the root and in a sibling namespace.
boundary_failed=0
for login in "$tenant_ns|$child_role|$child_sa" "root|$tenant_role|$tenant_sa"; do
  IFS='|' read -r login_ns role sa <<<"$login"
  # shellcheck disable=SC2016 # expanded by the shell in the OpenBao pod
  got=$(kubectl -n "$namespace" create token "$sa" --audience "$audience" |
    kubectl -n openbao exec -i openbao-0 -- env BAO_ADDR=https://openbao-active.openbao.svc:8200 \
      BAO_CACERT=/openbao/tls/ca.crt BAO_SKIP_VERIFY=false sh -c '
      read -r jwt
      ns() { if [ "$1" = root ]; then echo ""; else echo "$1"; fi; }
      BAO_TOKEN=$(bao write -namespace="$(ns "$1")" -field=token auth/kubernetes/login role="$2" jwt="$jwt") || exit 1
      export BAO_TOKEN
      shift 2
      for target in "$@"; do
        if echo "path \"livekit-boundary\" { capabilities = [\"read\"] }" |
          bao policy write -namespace="$(ns "$target")" livekit-boundary - >/dev/null 2>&1; then
          echo "$target=allowed"
        else
          echo "$target=denied"
        fi
      done' sh "$login_ns" "$role" "$tenant_ns" root "$sibling_ns" | tr '\n' ' ') || got="login failed"
  echo "login $login_ns/$role: $got"
  [ "$got" = "$tenant_ns=allowed root=denied $sibling_ns=denied " ] || boundary_failed=1
done

# Prints the operator leader pod and its obco_writes_total by kind, one "<kind> <count>" line each.
operator_writes() {
  local holder pod pf body=""
  holder=$(kubectl -n "$operator_ns" get lease openbao-config-operator.openbaoconfig.io -o 'jsonpath={.spec.holderIdentity}')
  pod=${holder%%_*}
  kubectl -n "$operator_ns" port-forward "pod/$pod" 18443:8443 >/dev/null 2>&1 &
  pf=$!
  for _ in $(seq 50); do
    if body=$(curl -fsSk -H "Authorization: Bearer $metrics_token" https://127.0.0.1:18443/metrics 2>/dev/null); then break; fi
    sleep 0.2
  done
  kill "$pf" 2>/dev/null || true
  wait "$pf" 2>/dev/null || true
  if [ -z "$body" ]; then
    err "cannot read the metrics of $pod"
    return 1
  fi
  echo "leader $pod"
  awk -F'"' '/^obco_writes_total\{/ { n = split($3, v, " "); s[$2] += v[n] } END { for (k in s) print k, s[k] }' <<<"$body"
}

kubectl -n "$operator_ns" create serviceaccount livekit-metrics --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl create clusterrolebinding livekit-metrics --clusterrole=openbao-config-operator-metrics-reader \
  --serviceaccount="$operator_ns:livekit-metrics" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
metrics_token=$(kubectl -n "$operator_ns" create token livekit-metrics)
echo "switching root/full to Observe before the failover write baseline"
"$kustomize" build examples/live/overlays/observe/full | "$yq" 'select(.spec.managementPolicy == "Observe")' >"$observed"
test/livekit/crl-fixture.sh "$observed" "$namespace"
PLUGIN_SHA=$plugin_sha "$yq" -i '(select(.kind == "Plugin") | .spec.sha256) = strenv(PLUGIN_SHA)' "$observed"
drop_skipped "$observed"
"$yq" -i '(select(.spec.connectionRef != null) | .spec.driftInterval) = "31s"' "$observed"
kubectl apply -f "$observed" >/dev/null
settle "after the Observe transition" true
[ "${#failed[@]}" -eq 0 ] || exit 1
operator_writes >"$writes_before"
wait_openbao
echo "deleting the active OpenBao node ${active[0]} after root/full entered Observe"
kubectl -n openbao delete pod "${active[0]}" --wait=false
# Refresh every generation without briefly reverting the observed objects to Manage.
"$yq" -i 'select(.metadata.labels["live.obco.dev/overlay"] != "root-full")' "$rendered"
"$yq" -i '(select(.spec.connectionRef != null) | .spec.driftInterval) = "31s"' "$rendered"
kubectl apply -f "$rendered" >/dev/null
"$yq" -i '(select(.spec.connectionRef != null) | .spec.driftInterval) = "32s"' "$observed"
kubectl apply -f "$observed" >/dev/null
wait_openbao
settle "after the failover and Observe" true
wait_openbao
echo "OpenBao ready again, active node ${active[0]}"
plugin_health

operator_writes >"$writes_after"
if [ "$(head -n1 "$writes_before")" != "$(head -n1 "$writes_after")" ]; then
  err "the operator leader changed ($(head -n1 "$writes_before") -> $(head -n1 "$writes_after")), cannot compare its writes"
  exit 1
fi
all_writes=$(awk 'NR == FNR { before[$1] = $2; next } $1 != "leader" && $2 > before[$1] { print $1 "=" $2 - before[$1] }' \
  "$writes_before" "$writes_after" | sort | tr '\n' ' ')
echo "OpenBao writes during the failover and Observe: ${all_writes:-none}"
printf '\nOpenBao writes during the failover and Observe: %s\n' "${all_writes:-none}" >>"${GITHUB_STEP_SUMMARY:-/dev/null}"
[ "${#failed[@]}" -eq 0 ] && [ "$boundary_failed" -eq 0 ] && [ -z "$all_writes" ]
