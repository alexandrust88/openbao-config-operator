#!/usr/bin/env bash
# The kind quickstart of docs/get-started/quickstart.md, end to end. The page includes the sections between the
# snippet markers, so each section stays a command a reader can paste; checks the page does not show sit outside
# the markers. CI runs this script against the locally built image and chart.
#
# Environment:
#   VERSION           chart version to install; required unless CHART_REF is a local chart directory
#   CHART_REF         chart to install (default oci://ghcr.io/alexandrust88/charts/openbao-config-operator)
#   IMAGE             manager image to install instead of the chart's, as repository:tag or repository@sha256:digest;
#                     the image must already be in the cluster (kind load docker-image)
#   KIND_CLUSTER      kind cluster name (default openbao-quickstart)
#   SKIP_KIND_CREATE  1 to use the existing kind cluster KIND_CLUSTER instead of creating it
#   CLEANUP           1 to delete the kind cluster at the end
set -euo pipefail

cd "$(dirname "$0")/../.."
trap 'echo "quickstart: failed at line $LINENO" >&2' ERR

VERSION=${VERSION:-}
IMAGE=${IMAGE:-}
SKIP_KIND_CREATE=${SKIP_KIND_CREATE:-0}
CLEANUP=${CLEANUP:-0}

fail() {
  echo "quickstart: $*" >&2
  exit 1
}

# Checks capture output before grep: grep -q in a pipe can end the writer early, which pipefail reports.
bao_out() {
  kubectl -n openbao exec deploy/openbao -- bao "$@"
}

# The page shows plain exports; overrides from the environment are restored after them.
kind_cluster_override=${KIND_CLUSTER:-}
chart_ref_override=${CHART_REF:-}
# --8<-- [start:env]
export KIND_CLUSTER=openbao-quickstart
export CHART_REF=oci://ghcr.io/alexandrust88/charts/openbao-config-operator
# --8<-- [end:env]
KIND_CLUSTER=${kind_cluster_override:-$KIND_CLUSTER}
CHART_REF=${chart_ref_override:-$CHART_REF}

if [ -z "$VERSION" ] && [ ! -d "$CHART_REF" ]; then
  fail "set VERSION to the chart version to install, for example VERSION=0.1.1"
fi

if [ -n "$IMAGE" ]; then
  case $IMAGE in
    *@sha256:*) image_args=(--set "image.repository=${IMAGE%@*}" --set "image.digest=${IMAGE#*@}") ;;
    *:*) image_args=(--set "image.repository=${IMAGE%:*}" --set "image.tag=${IMAGE##*:}" --set image.digest=) ;;
    *) fail "IMAGE must be repository:tag or repository@sha256:digest" ;;
  esac
  # CI installs its own image; wrapping helm keeps the operator section exactly what readers paste.
  helm() { command helm "$@" "${image_args[@]}"; }
fi

clusters=$(kind get clusters 2>/dev/null || true)
if [ "$SKIP_KIND_CREATE" != 1 ] && ! grep -qx "$KIND_CLUSTER" <<<"$clusters"; then
  # --8<-- [start:cluster]
  kind create cluster --name "$KIND_CLUSTER" --wait 2m
  # --8<-- [end:cluster]
fi
kubectl config use-context "kind-$KIND_CLUSTER"

# --8<-- [start:openbao]
kubectl apply -f examples/quickstart/openbao-dev.yaml
kubectl -n openbao rollout status deployment/openbao --timeout=3m
# --8<-- [end:openbao]

auth_methods=$(bao_out auth list)
if ! grep -q '^kubernetes/' <<<"$auth_methods"; then
  # --8<-- [start:openbao-auth-enable]
  kubectl -n openbao exec deploy/openbao -- bao auth enable kubernetes
  # --8<-- [end:openbao-auth-enable]
fi
# --8<-- [start:openbao-auth]
kubectl -n openbao exec deploy/openbao -- bao write auth/kubernetes/config \
  kubernetes_host=https://kubernetes.default.svc:443
kubectl -n openbao exec -i deploy/openbao -- bao policy write operator-quickstart - \
  < examples/quickstart/operator-quickstart.hcl
kubectl -n openbao exec deploy/openbao -- bao write auth/kubernetes/role/operator-quickstart \
  bound_service_account_names=openbao-config \
  bound_service_account_namespaces=quickstart \
  audience=openbao \
  token_policies=operator-quickstart \
  token_ttl=1h token_max_ttl=4h
# --8<-- [end:openbao-auth]

# --8<-- [start:operator]
helm upgrade --install openbao-config-operator "$CHART_REF" \
  --version "$VERSION" \
  --namespace openbao-config-operator --create-namespace \
  --set replicas=1 \
  --wait --timeout 5m
# --8<-- [end:operator]

# --8<-- [start:connect]
kubectl apply -f examples/quickstart/namespace.yaml \
  -f examples/quickstart/openbaoserver.yaml \
  -f examples/quickstart/openbaoconnection.yaml
kubectl wait openbaoserver/quickstart --for=condition=Ready --timeout=2m
kubectl -n quickstart wait openbaoconnection/openbao --for=condition=Ready --timeout=2m
# --8<-- [end:connect]

# --8<-- [start:resources]
kubectl apply -f examples/quickstart/policy.yaml \
  -f examples/quickstart/secretsmount.yaml \
  -f examples/quickstart/kubernetesauthrole.yaml \
  -f examples/quickstart/randomsecret.yaml
kubectl -n quickstart wait --for=condition=Ready --timeout=3m \
  policy.sys.openbaoconfig.io/quickstart-app-read \
  secretsmount.sys.openbaoconfig.io/quickstart-kv \
  kubernetesauthrole.auth.openbaoconfig.io/quickstart-app \
  randomsecret.secrets.openbaoconfig.io/quickstart-app-password
# --8<-- [end:resources]

# --8<-- [start:status]
kubectl -n quickstart get openbao
# --8<-- [end:status]

# --8<-- [start:verify]
kubectl -n openbao exec deploy/openbao -- bao policy read quickstart-app-read
kubectl -n openbao exec deploy/openbao -- bao read -field=type sys/mounts/quickstart-kv
kubectl -n openbao exec deploy/openbao -- bao read -field=token_policies auth/kubernetes/role/quickstart-app
# --8<-- [end:verify]

policy=$(bao_out policy read quickstart-app-read)
grep -q '"quickstart-kv/data/app"' <<<"$policy" ||
  fail "policy quickstart-app-read in OpenBao does not match examples/quickstart/policy.yaml"
[ "$(bao_out read -field=type sys/mounts/quickstart-kv)" = kv ] || fail "quickstart-kv is not a kv mount"
role_policies=$(bao_out read -field=token_policies auth/kubernetes/role/quickstart-app)
grep -q quickstart-app-read <<<"$role_policies" || fail "role quickstart-app does not attach the policy quickstart-app-read"

# --8<-- [start:app-login]
APP_JWT=$(kubectl -n quickstart create token app --audience openbao)
APP_TOKEN=$(kubectl -n openbao exec deploy/openbao -- \
  bao write -field=token auth/kubernetes/login role=quickstart-app jwt="$APP_JWT")
kubectl -n openbao exec deploy/openbao -- \
  env BAO_TOKEN="$APP_TOKEN" bao kv get -field=password quickstart-kv/app
# --8<-- [end:app-login]

# --8<-- [start:secret]
kubectl -n quickstart get secret quickstart-app-password -o jsonpath='{.data.password}' | base64 -d
# --8<-- [end:secret]

app_value=$(kubectl -n openbao exec deploy/openbao -- \
  env BAO_TOKEN="$APP_TOKEN" bao kv get -field=password quickstart-kv/app)
secret_value=$(kubectl -n quickstart get secret quickstart-app-password -o jsonpath='{.data.password}' | base64 -d)
[ "${#app_value}" -eq 32 ] || fail "the application did not read a 32-character password from quickstart-kv/app"
[ "$app_value" = "$secret_value" ] || fail "Secret quickstart-app-password differs from quickstart-kv/app in OpenBao"

# --8<-- [start:drift]
kubectl -n openbao exec -i deploy/openbao -- bao policy write quickstart-app-read - <<'EOF'
path "quickstart-kv/data/*" {
  capabilities = ["read"]
}
EOF
# --8<-- [end:drift]

restored=0
for _ in $(seq 1 24); do
  sleep 5
  policy=$(bao_out policy read quickstart-app-read)
  if grep -q '"quickstart-kv/data/app"' <<<"$policy"; then
    restored=1
    break
  fi
done
[ "$restored" = 1 ] || fail "the operator did not restore policy quickstart-app-read within 2 minutes"

# --8<-- [start:drift-check]
kubectl -n openbao exec deploy/openbao -- bao policy read quickstart-app-read
kubectl -n quickstart get events --field-selector involvedObject.name=quickstart-app-read,reason=Updated
# --8<-- [end:drift-check]

echo "quickstart: every resource is Ready and verified in OpenBao"

if [ "$CLEANUP" = 1 ]; then
  # --8<-- [start:cleanup]
  kind delete cluster --name "$KIND_CLUSTER"
  # --8<-- [end:cleanup]
fi
