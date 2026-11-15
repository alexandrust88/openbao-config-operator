#!/usr/bin/env bash
# Chart lifecycle checks against a throwaway kind cluster: release packaging, fresh install, upgrade from the newest
# released chart, rollback, uninstall with and without crds.keep, reinstall with crds.install=false and the
# crds.install/crds.keep guard. Every step asserts cluster or release state, not only exit codes. The last step deletes
# the operator's CRDs, so the script only runs against a kind-* context.
#
# Environment:
#   IMAGE             manager image already loaded into the kind cluster, as repository:tag (required)
#   KIND_CLUSTER      kind cluster name (default obco-helm); the script uses the context kind-$KIND_CLUSTER
#   CHART_DIR         chart under test (default charts/openbao-config-operator)
#   PREVIOUS_CHART    released chart to upgrade from (default oci://ghcr.io/alexandrust88/charts/openbao-config-operator)
#   PREVIOUS_VERSION  released chart version (default: the newest version of PREVIOUS_CHART)
#   NAMESPACE         release namespace (default openbao-config-operator)
#   ARTIFACTS_DIR     where diagnostics are written on failure (default .tmp/helm-lifecycle)
#   HELM, KUBECTL, YQ tool overrides (default helm, kubectl, yq from PATH); jq and git come from PATH
set -Eeuo pipefail

cd "$(dirname "$0")/../.."

IMAGE=${IMAGE:-}
KIND_CLUSTER=${KIND_CLUSTER:-obco-helm}
CHART_DIR=${CHART_DIR:-charts/openbao-config-operator}
PREVIOUS_CHART=${PREVIOUS_CHART:-oci://ghcr.io/alexandrust88/charts/openbao-config-operator}
PREVIOUS_VERSION=${PREVIOUS_VERSION:-}
NAMESPACE=${NAMESPACE:-openbao-config-operator}
ARTIFACTS_DIR=${ARTIFACTS_DIR:-.tmp/helm-lifecycle}
helm_bin=${HELM:-helm}
kubectl_bin=${KUBECTL:-kubectl}
yq_bin=${YQ:-yq}

# 0.1.0 was published before the OCM release fix (v0.1.1); upgrades are only checked from 0.1.1 on.
min_previous_version=0.1.1
release=openbao-config-operator
nokeep_release=obco-nokeep
nokeep_namespace=$NAMESPACE-nokeep
cr_namespace=obco-lifecycle
lease=openbao-config-operator.openbaoconfig.io
helm_timeout=5m
guard_message='crds.install=false with crds.keep=false'
context=kind-$KIND_CLUSTER
ctx_args=(--context "$context")
helm_ctx_args=(--kube-context "$context")

fail() {
  echo "lifecycle: FAIL: $*" >&2
  exit 1
}

step() {
  printf '\n==== [%3ss] %s ====\n' "$SECONDS" "$*"
}

ok() {
  echo "lifecycle: ok: $*"
}

k() { "$kubectl_bin" "${ctx_args[@]}" "$@"; }
h() { "$helm_bin" "${helm_ctx_args[@]}" "$@"; }

work=$(mktemp -d)

diagnostics() {
  local ns
  h list -A -a || true
  for ns in "$NAMESPACE" "$nokeep_namespace"; do
    echo "--- namespace $ns"
    h -n "$ns" history "$release" || true
    h -n "$ns" history "$nokeep_release" || true
    k -n "$ns" get deploy,rs,pods,lease -o wide || true
    k -n "$ns" describe deploy || true
    k -n "$ns" logs -l app.kubernetes.io/name=openbao-config-operator --all-containers --prefix --tail=300 || true
    k -n "$ns" logs -l app.kubernetes.io/name=openbao-config-operator --all-containers --prefix --previous --tail=100 || true
  done
  k get pods -A -o wide || true
  k get events -A --sort-by=.lastTimestamp | tail -n 150 || true
  k get crd -o custom-columns='NAME:.metadata.name,RELEASE:.metadata.annotations.meta\.helm\.sh/release-name,POLICY:.metadata.annotations.helm\.sh/resource-policy,CHART:.metadata.labels.helm\.sh/chart' |
    grep -E 'NAME|openbaoconfig\.io' || true
  k -n "$cr_namespace" get policies.sys.openbaoconfig.io -o yaml || true
}

on_exit() {
  local status=$?
  if [ "$status" -ne 0 ]; then
    step "failed (exit $status): diagnostics in $ARTIFACTS_DIR/diagnostics.log"
    mkdir -p "$ARTIFACTS_DIR"
    diagnostics >"$ARTIFACTS_DIR/diagnostics.log" 2>&1 || true
    cat "$ARTIFACTS_DIR/diagnostics.log"
  fi
  rm -rf -- "$work"
  exit "$status"
}
trap on_exit EXIT
trap 'echo "lifecycle: failed at line $LINENO: $BASH_COMMAND" >&2' ERR

# CRD names the chart installs with default values: config/crd/bases minus the plugin kinds, named
# <group>_<plural>.yaml as in package-chart.sh.
expected_crds() {
  local file base
  for file in config/crd/bases/*.yaml; do
    base=$(basename "$file")
    grep -qxF "$base" hack/chart/plugin-crds.txt && continue
    base=${base%.yaml}
    echo "${base#*_}.${base%%_*}"
  done
}

live_crds_json() {
  k get crd -o json | jq '[.items[] | select(.spec.group | endswith(".openbaoconfig.io"))]'
}

# Names, keep policy and chart label of the live CRDs, as a file: the full CRDs exceed the argument size limit.
live_crds_meta() {
  live_crds_json | jq '[.[] | {name: .metadata.name, policy: .metadata.annotations["helm.sh/resource-policy"],
    chart: .metadata.labels["helm.sh/chart"]}]' >"$work/live-meta.json"
  echo "$work/live-meta.json"
}

deployment() {
  local ns=$1 rel=$2 name
  name=$(k -n "$ns" get deploy -l "app.kubernetes.io/instance=$rel" -o name)
  [ -n "$name" ] || fail "no Deployment of release $rel in $ns"
  echo "$name"
}

deployment_image() {
  k -n "$1" get "$(deployment "$1" "$2")" -o jsonpath='{.spec.template.spec.containers[?(@.name=="manager")].image}'
}

assert_available() {
  local ns=$1 rel=$2 deploy want ready
  deploy=$(deployment "$ns" "$rel")
  k -n "$ns" rollout status "$deploy" --timeout=180s
  k -n "$ns" wait --for=condition=Available "$deploy" --timeout=60s >/dev/null
  want=$(k -n "$ns" get "$deploy" -o jsonpath='{.spec.replicas}')
  ready=$(k -n "$ns" get "$deploy" -o jsonpath='{.status.readyReplicas}')
  [ "$ready" = "$want" ] || fail "$deploy: $ready of $want replicas ready"
  ok "$deploy Available, $ready/$want ready"
}

# The lease holder must be a live, Ready pod of the release: readiness means /readyz answered ok.
assert_leader() {
  local ns=$1 rel=$2 deadline=$((SECONDS + 180)) holder pods
  while :; do
    holder=$(k -n "$ns" get lease "$lease" -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || true)
    pods=$(k -n "$ns" get pods -l "app.kubernetes.io/instance=$rel" -o json | jq -r '.items[]
      | select(.metadata.deletionTimestamp == null)
      | select(any(.status.conditions[]?; .type == "Ready" and .status == "True")) | .metadata.name')
    if [ -n "$holder" ] && grep -qxF "${holder%%_*}" <<<"$pods"; then
      break
    fi
    [ "$SECONDS" -lt "$deadline" ] || fail "lease $ns/$lease held by '$holder', not by a Ready pod of $rel: $pods"
    sleep 5
  done
  ok "leader ${holder%%_*} holds $ns/$lease"
  k -n "$ns" logs "${holder%%_*}" -c manager | grep -m1 -i 'acquired lease' || true
}

# expected CRDs present; with keep=true they carry helm.sh/resource-policy=keep.
assert_crds() {
  local keep=$1 meta missing unkept
  meta=$(live_crds_meta)
  missing=$(expected_crds | jq -Rr --slurpfile live "$meta" 'select(. as $n | $live[0] | map(.name) | index($n) | not)')
  [ -z "$missing" ] || fail "CRDs missing: $missing"
  if [ "$keep" = keep ]; then
    unkept=$(expected_crds | jq -Rr --slurpfile live "$meta" '. as $n | select($live[0][] | select(.name == $n) | .policy != "keep")')
    [ -z "$unkept" ] || fail "CRDs without helm.sh/resource-policy=keep: $unkept"
  fi
  ok "$(expected_crds | wc -l | tr -d ' ') CRDs present${keep:+ ($keep)}"
}

# The live CRDs named in a release revision have its versions and schemas. CRDs the revision does not name are
# left alone: helm.sh/resource-policy=keep retains a CRD that a later chart drops.
assert_crds_match_revision() {
  local ns=$1 rel=$2 rev=$3 diff
  h -n "$ns" get manifest "$rel" --revision "$rev" |
    "$yq_bin" ea -o json -I0 'select(.kind == "CustomResourceDefinition")' |
    jq -sS 'map({key: .metadata.name, value: [.spec.versions[] | {name, served, storage, schema: .schema.openAPIV3Schema}]})
      | from_entries' >"$work/want.json"
  live_crds_json |
    jq -S 'map({key: .metadata.name, value: [.spec.versions[] | {name, served, storage, schema: .schema.openAPIV3Schema}]})
      | from_entries' >"$work/live.json"
  [ "$(jq length "$work/want.json")" -gt 0 ] || fail "revision $rev of $rel has no CRDs"
  diff=$(jq -rn --slurpfile want "$work/want.json" --slurpfile live "$work/live.json" '
    $want[0] | keys[] | select($want[0][.] != $live[0][.])')
  [ -z "$diff" ] || fail "live CRDs differ from revision $rev of $rel: $diff"
  ok "$(jq length "$work/want.json") live CRDs match revision $rev of $rel"
}

assert_revision() {
  local ns=$1 rel=$2 rev=$3 chart=$4 history
  history=$(h -n "$ns" history "$rel" -o json)
  jq -e --argjson rev "$rev" --arg chart "$chart" \
    'max_by(.revision) | .revision == $rev and .status == "deployed" and .chart == $chart' <<<"$history" >/dev/null ||
    fail "$rel: expected revision $rev of $chart deployed, history: $history"
  ok "$rel revision $rev deployed ($chart)"
}

assert_step_annotation() {
  local ns=$1 rel=$2 want=$3 got
  got=$(k -n "$ns" get "$(deployment "$ns" "$rel")" -o json | jq -r '.spec.template.metadata.annotations["obco-lifecycle/step"] // ""')
  [ "$got" = "$want" ] || fail "pod template obco-lifecycle/step is '$got', want '$want'"
}

cr_uid() {
  k -n "$cr_namespace" get policies.sys.openbaoconfig.io lifecycle-probe -o jsonpath='{.metadata.uid}'
}

assert_cr() {
  local got
  got=$(cr_uid) || fail "Policy $cr_namespace/lifecycle-probe is gone"
  [ "$got" = "$probe_uid" ] || fail "Policy $cr_namespace/lifecycle-probe was recreated (uid $got, want $probe_uid)"
  ok "Policy $cr_namespace/lifecycle-probe kept (uid $got)"
}

assert_no_release() {
  local ns=$1 rel=$2 releases
  releases=$(h -n "$ns" list -a -q)
  ! grep -qxF "$rel" <<<"$releases" || fail "release $rel still listed in $ns"
  [ -z "$(k -n "$ns" get deploy -l "app.kubernetes.io/instance=$rel" -o name)" ] || fail "Deployment of $rel still in $ns"
  ok "release $rel and its Deployment are gone from $ns"
}

[ -n "$IMAGE" ] || fail "IMAGE is required: the operator image loaded into kind cluster $KIND_CLUSTER (repository:tag)"
case ${IMAGE##*/} in
  *:*) ;;
  *) fail "IMAGE must be repository:tag, got $IMAGE" ;;
esac
pr_image_args=(--set "image.repository=${IMAGE%:*}" --set-string "image.tag=${IMAGE##*:}" --set-string image.digest=
  --set image.pullPolicy=IfNotPresent)
k config get-contexts "$context" >/dev/null 2>&1 || fail "no kube context $context; create the cluster with make setup-test-e2e KIND_CLUSTER=$KIND_CLUSTER"
chart_version=$("$yq_bin" '.version' "$CHART_DIR/Chart.yaml")
chart_name=$("$yq_bin" '.name' "$CHART_DIR/Chart.yaml")
pr_chart=$chart_name-$chart_version
mkdir -p "$ARTIFACTS_DIR"

step "a. package the chart like release.yml (no push, no signing), lint and render the package"
package_repo=$("$yq_bin" '.image.repository' "$CHART_DIR/values.yaml")
package_digest="sha256:$(printf '0%.0s' {1..64})"
tgz=$(HELM="$helm_bin" YQ="$yq_bin" hack/release/package-chart.sh "v$chart_version" "$package_repo" "$package_digest" "$work/package")
[ -f "$tgz" ] || fail "package-chart.sh did not write $tgz"
tar -xzOf "$tgz" "$chart_name/Chart.yaml" >"$work/Chart.yaml"
[ "$("$yq_bin" '.version + " " + .appVersion' "$work/Chart.yaml")" = "$chart_version $chart_version" ] ||
  fail "packaged version/appVersion is not $chart_version"
grep -qF "$package_repo@$package_digest" <<<"$("$yq_bin" '.annotations["artifacthub.io/images"]' "$work/Chart.yaml")" ||
  fail "artifacthub.io/images does not pin $package_repo@$package_digest"
"$helm_bin" lint --strict "$tgz"
"$helm_bin" template "$release" "$tgz" --namespace "$NAMESPACE" >"$work/package.yaml"
rendered_image=$("$yq_bin" ea 'select(.kind == "Deployment") | .spec.template.spec.containers[] | select(.name == "manager") | .image' "$work/package.yaml")
[ "$rendered_image" = "$package_repo@$package_digest" ] || fail "packaged chart renders image $rendered_image"
rendered_crds=$("$yq_bin" ea '[select(.kind == "CustomResourceDefinition")] | length' "$work/package.yaml")
[ "$rendered_crds" = "$(expected_crds | wc -l | tr -d ' ')" ] || fail "packaged chart renders $rendered_crds CRDs"
ok "$(basename "$tgz"): version $chart_version, image pinned by digest, $rendered_crds CRDs"

step "b. fresh install of $pr_chart with $IMAGE"
h install "$release" "$CHART_DIR" -n "$NAMESPACE" --create-namespace "${pr_image_args[@]}" --wait --timeout "$helm_timeout"
assert_available "$NAMESPACE" "$release"
[ "$(deployment_image "$NAMESPACE" "$release")" = "$IMAGE" ] || fail "Deployment does not run $IMAGE"
assert_crds keep
plugin_installed=$(live_crds_json | jq -r --rawfile plugins hack/chart/plugin-crds.txt '
  ($plugins | split("\n") | map(select(. != "") | sub("\\.yaml$"; "") | (split("_") | .[1] + "." + .[0]))) as $p
  | .[].metadata.name | select(. as $n | $p | index($n))')
[ -z "$plugin_installed" ] || fail "plugin CRDs installed without pluginKinds.enabled: $plugin_installed"
assert_leader "$NAMESPACE" "$release"

step "c. upgrade from the newest released chart to $pr_chart"
h uninstall "$release" -n "$NAMESPACE" --wait --timeout "$helm_timeout"
assert_no_release "$NAMESPACE" "$release"
assert_crds keep
if [ -z "$PREVIOUS_VERSION" ]; then
  if PREVIOUS_VERSION=$("$helm_bin" show chart "$PREVIOUS_CHART" 2>"$work/show.err" | "$yq_bin" '.version'); then
    echo "lifecycle: newest released chart: $PREVIOUS_CHART $PREVIOUS_VERSION"
  else
    echo "lifecycle: SKIP upgrade from a release: no chart at $PREVIOUS_CHART ($(cat "$work/show.err"))"
    PREVIOUS_VERSION=
  fi
fi
if [ -n "$PREVIOUS_VERSION" ] && [ "$(printf '%s\n%s\n' "$min_previous_version" "$PREVIOUS_VERSION" | sort -V | head -n1)" != "$min_previous_version" ]; then
  echo "lifecycle: SKIP upgrade from $PREVIOUS_VERSION: released before the OCM release fix ($min_previous_version)"
  PREVIOUS_VERSION=
fi
if [ -n "$PREVIOUS_VERSION" ]; then
  h install "$release" "$PREVIOUS_CHART" --version "$PREVIOUS_VERSION" -n "$NAMESPACE" \
    --set-string podAnnotations.obco-lifecycle/step=previous --wait --timeout "$helm_timeout"
  previous_chart=$chart_name-$PREVIOUS_VERSION
else
  echo "lifecycle: no usable release; revision 1 is $pr_chart, so the upgrade and rollback cover $pr_chart -> $pr_chart only"
  h install "$release" "$CHART_DIR" -n "$NAMESPACE" "${pr_image_args[@]}" \
    --set-string podAnnotations.obco-lifecycle/step=previous --wait --timeout "$helm_timeout"
  previous_chart=$pr_chart
fi
assert_revision "$NAMESPACE" "$release" 1 "$previous_chart"
assert_available "$NAMESPACE" "$release"
previous_image=$(deployment_image "$NAMESPACE" "$release")
k create namespace "$cr_namespace" --dry-run=client -o yaml | k apply -f -
k apply -f - <<EOF
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: Policy
metadata:
  name: lifecycle-probe
  namespace: $cr_namespace
spec:
  connectionRef:
    name: does-not-exist
  deletionPolicy: Orphan
  name: lifecycle-probe
  policy: |
    path "secret/data/lifecycle/*" {
      capabilities = ["read"]
    }
EOF
probe_uid=$(cr_uid)
h upgrade "$release" "$CHART_DIR" -n "$NAMESPACE" "${pr_image_args[@]}" \
  --set-string podAnnotations.obco-lifecycle/step=upgrade --wait --timeout "$helm_timeout"
assert_revision "$NAMESPACE" "$release" 2 "$pr_chart"
assert_available "$NAMESPACE" "$release"
[ "$(deployment_image "$NAMESPACE" "$release")" = "$IMAGE" ] || fail "upgraded Deployment does not run $IMAGE"
assert_crds_match_revision "$NAMESPACE" "$release" 2
meta=$(live_crds_meta)
bad_labels=$(expected_crds | jq -Rr --slurpfile live "$meta" --arg chart "$pr_chart" '. as $n
  | select($live[0][] | select(.name == $n) | .chart != $chart)')
[ -z "$bad_labels" ] || fail "CRDs not relabelled helm.sh/chart=$pr_chart: $bad_labels"
assert_crds keep
assert_cr
assert_leader "$NAMESPACE" "$release"

step "d. rollback to revision 1, then forward to revision 2"
h rollback "$release" 1 -n "$NAMESPACE" --wait --timeout "$helm_timeout"
assert_revision "$NAMESPACE" "$release" 3 "$previous_chart"
assert_available "$NAMESPACE" "$release"
assert_step_annotation "$NAMESPACE" "$release" previous
[ "$(deployment_image "$NAMESPACE" "$release")" = "$previous_image" ] || fail "rolled back Deployment does not run $previous_image"
assert_crds_match_revision "$NAMESPACE" "$release" 1
assert_cr
h rollback "$release" 2 -n "$NAMESPACE" --wait --timeout "$helm_timeout"
assert_revision "$NAMESPACE" "$release" 4 "$pr_chart"
assert_available "$NAMESPACE" "$release"
assert_step_annotation "$NAMESPACE" "$release" upgrade
[ "$(deployment_image "$NAMESPACE" "$release")" = "$IMAGE" ] || fail "rolled forward Deployment does not run $IMAGE"
assert_crds_match_revision "$NAMESPACE" "$release" 2
assert_cr
assert_leader "$NAMESPACE" "$release"

step "e. uninstall with crds.keep=true keeps the CRDs and the custom resources"
h uninstall "$release" -n "$NAMESPACE" --wait --timeout "$helm_timeout"
assert_no_release "$NAMESPACE" "$release"
assert_crds keep
assert_cr

step "f. reinstall with crds.install=false over the kept CRDs"
h install "$release" "$CHART_DIR" -n "$NAMESPACE" "${pr_image_args[@]}" --set crds.install=false \
  --wait --timeout "$helm_timeout"
assert_revision "$NAMESPACE" "$release" 1 "$pr_chart"
assert_available "$NAMESPACE" "$release"
release_crds=$(h -n "$NAMESPACE" get manifest "$release" | "$yq_bin" ea '[select(.kind == "CustomResourceDefinition")] | length')
[ "$release_crds" = 0 ] || fail "release manifest holds $release_crds CRDs with crds.install=false"
assert_crds keep
assert_cr
assert_leader "$NAMESPACE" "$release"

step "g. crds.install=false with crds.keep=false is refused"
if out=$("$helm_bin" template "$release" "$CHART_DIR" --namespace "$NAMESPACE" --set crds.install=false --set crds.keep=false 2>&1); then
  fail "helm template rendered crds.install=false,crds.keep=false"
fi
grep -qF "$guard_message" <<<"$out" || fail "helm template failed without the guard message: $out"
ok "refused: $(grep -F "$guard_message" <<<"$out" | head -n1)"

step "h. uninstall a release installed with crds.keep=false deletes the CRDs"
h uninstall "$release" -n "$NAMESPACE" --wait --timeout "$helm_timeout"
assert_no_release "$NAMESPACE" "$release"
assert_crds keep
# The kept CRDs still name the first release as owner; --take-ownership moves them to this one.
h install "$nokeep_release" "$CHART_DIR" -n "$nokeep_namespace" --create-namespace "${pr_image_args[@]}" \
  --set crds.keep=false --take-ownership --wait --timeout "$helm_timeout"
assert_available "$nokeep_namespace" "$nokeep_release"
assert_crds ""
still_kept=$(live_crds_json | jq -r '.[] | select(.metadata.annotations["helm.sh/resource-policy"] == "keep") | .metadata.name')
[ -z "$still_kept" ] || fail "CRDs still annotated helm.sh/resource-policy=keep: $still_kept"
h uninstall "$nokeep_release" -n "$nokeep_namespace" --wait --timeout "$helm_timeout"
assert_no_release "$nokeep_namespace" "$nokeep_release"
deadline=$((SECONDS + 180))
until [ "$(live_crds_json | jq length)" = 0 ]; do
  [ "$SECONDS" -lt "$deadline" ] || fail "CRDs left after uninstall with crds.keep=false: $(live_crds_json | jq -r '.[].metadata.name')"
  sleep 5
done
! k -n "$cr_namespace" get policies.sys.openbaoconfig.io lifecycle-probe >/dev/null 2>&1 ||
  fail "Policy $cr_namespace/lifecycle-probe survived the CRD deletion"
ok "every openbaoconfig.io CRD and the Policy are deleted"

step "all chart lifecycle scenarios passed"
