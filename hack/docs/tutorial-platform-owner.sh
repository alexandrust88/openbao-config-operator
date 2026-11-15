#!/usr/bin/env bash
# The platform owner tutorial of docs/tutorials/platform-owner.md, on the cluster that hack/docs/quickstart.sh left.
# The page includes the sections between the snippet markers; checks the page does not show sit outside them.
#
# Environment:
#   KIND_CLUSTER  kind cluster the quickstart ran on (default openbao-quickstart)
set -euo pipefail

cd "$(dirname "$0")/../.."
trap 'echo "tutorial-platform-owner: failed at line $LINENO" >&2' ERR

KIND_CLUSTER=${KIND_CLUSTER:-openbao-quickstart}

fail() {
  echo "tutorial-platform-owner: $*" >&2
  exit 1
}

# Checks capture output before grep: grep -q in a pipe can end the writer early, which pipefail reports.
bao_out() {
  kubectl -n openbao exec deploy/openbao -- bao "$@"
}

kubectl config use-context "kind-$KIND_CLUSTER"

# --8<-- [start:operator-identity]
kubectl -n openbao exec -i deploy/openbao -- bao policy write openbao-config-operator - \
  < examples/tutorials/platform-owner/openbao-config-operator.hcl
kubectl -n openbao exec deploy/openbao -- bao write auth/kubernetes/role/openbao-config-operator \
  bound_service_account_names=openbao-config-operator \
  bound_service_account_namespaces=openbao-config-operator \
  audience=openbao-config-operator \
  token_policies=openbao-config-operator \
  token_ttl=1h token_max_ttl=4h
# --8<-- [end:operator-identity]

# --8<-- [start:connect]
kubectl apply -f examples/tutorials/platform-owner/namespace.yaml \
  -f examples/tutorials/platform-owner/openbaoserver.yaml \
  -f examples/tutorials/platform-owner/clusteropenbaoconnection.yaml
kubectl wait openbaoserver/openbao clusteropenbaoconnection/platform --for=condition=Ready --timeout=2m
# --8<-- [end:connect]

[ "$(kubectl get clusteropenbaoconnection platform -o jsonpath='{.status.authenticated}')" = true ] ||
  fail "the ClusterOpenBaoConnection platform is not authenticated"

# --8<-- [start:configure]
kubectl apply -f examples/tutorials/platform-owner/root-config.yaml \
  -f examples/tutorials/platform-owner/platform-namespace.yaml
kubectl -n platform wait --for=condition=Ready --timeout=3m \
  policy.sys.openbaoconfig.io/platform-auditor \
  authmount.sys.openbaoconfig.io/approle \
  openbaonamespace.sys.openbaoconfig.io/platform \
  secretsmount.sys.openbaoconfig.io/platform-secret
# --8<-- [end:configure]

# --8<-- [start:status]
kubectl -n platform get openbao
# --8<-- [end:status]

# --8<-- [start:verify]
kubectl -n openbao exec deploy/openbao -- bao policy read platform-auditor
kubectl -n openbao exec deploy/openbao -- bao auth list
kubectl -n openbao exec deploy/openbao -- bao namespace list
kubectl -n openbao exec deploy/openbao -- bao secrets list -namespace=platform
# --8<-- [end:verify]

policy=$(bao_out policy read platform-auditor)
grep -q 'path "sys/namespaces"' <<<"$policy" ||
  fail "policy platform-auditor in OpenBao does not match examples/tutorials/platform-owner/root-config.yaml"
[ "$(bao_out read -field=type sys/auth/approle)" = approle ] || fail "approle/ is not an AppRole auth method"
namespaces=$(bao_out namespace list)
grep -qx 'platform/' <<<"$namespaces" || fail "OpenBao does not list the namespace platform"
[ "$(bao_out read -namespace=platform -field=type sys/mounts/secret)" = kv ] ||
  fail "secret/ in the OpenBao namespace platform is not a kv mount"
options=$(bao_out read -namespace=platform -field=options sys/mounts/secret)
grep -q 'version:2' <<<"$options" || fail "secret/ in the OpenBao namespace platform is not KV version 2"

echo "tutorial-platform-owner: the platform connection and the root and platform namespace configuration are Ready"
