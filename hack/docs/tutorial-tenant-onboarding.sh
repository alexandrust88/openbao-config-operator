#!/usr/bin/env bash
# The day-1 tenant onboarding tutorial of docs/tutorials/tenant-onboarding.md, after
# hack/docs/tutorial-platform-owner.sh. The page includes the sections between the snippet markers; checks the page
# does not show sit outside them.
#
# Environment:
#   KIND_CLUSTER  kind cluster the quickstart ran on (default openbao-quickstart)
set -euo pipefail

cd "$(dirname "$0")/../.."
trap 'echo "tutorial-tenant-onboarding: failed at line $LINENO" >&2' ERR

KIND_CLUSTER=${KIND_CLUSTER:-openbao-quickstart}

fail() {
  echo "tutorial-tenant-onboarding: $*" >&2
  exit 1
}

# Checks capture output before grep: grep -q in a pipe can end the writer early, which pipefail reports.
bao_out() {
  kubectl -n openbao exec deploy/openbao -- bao "$@"
}

# The page shows these as commands that print "no"; kubectl auth can-i exits 1 then, so the script runs them here
# and checks the answers.
rbac_denied() {
  # --8<-- [start:rbac-denied]
  kubectl auth can-i create openbaoconnections.core.openbaoconfig.io -n tenant-a --as tenant-a-admin
  kubectl auth can-i create policies.sys.openbaoconfig.io -n tenant-b --as tenant-a-admin
  kubectl auth can-i create clusteropenbaoconnections.core.openbaoconfig.io --as tenant-a-admin
  kubectl auth can-i create namespaces --as tenant-a-admin
  # --8<-- [end:rbac-denied]
}

kubectl config use-context "kind-$KIND_CLUSTER"

# --8<-- [start:kubernetes]
kubectl apply -f examples/tutorials/tenant-onboarding/namespaces.yaml
# --8<-- [end:kubernetes]

# --8<-- [start:rbac-allowed]
kubectl auth can-i create openbaonamespaces.sys.openbaoconfig.io -n tenant-a --as tenant-a-admin
kubectl auth can-i create kubernetesauthroles.auth.openbaoconfig.io -n tenant-a --as tenant-a-admin
# --8<-- [end:rbac-allowed]

answers=$(rbac_denied 2>&1) || true
[ "$(grep -cx no <<<"$answers")" = 4 ] || fail "tenant-a-admin has rights beyond its namespace: $answers"

# --8<-- [start:openbao]
kubectl apply -f examples/tutorials/tenant-onboarding/openbao-tenant-a.yaml \
  -f examples/tutorials/tenant-onboarding/openbao-tenant-b.yaml
kubectl -n platform wait --for=condition=Ready --timeout=3m \
  openbaonamespace.sys.openbaoconfig.io/tenant-a \
  openbaonamespace.sys.openbaoconfig.io/tenant-b \
  authmount.sys.openbaoconfig.io/tenant-a-kubernetes \
  authmount.sys.openbaoconfig.io/tenant-b-kubernetes \
  kubernetesauthconfig.auth.openbaoconfig.io/tenant-a-kubernetes \
  kubernetesauthconfig.auth.openbaoconfig.io/tenant-b-kubernetes \
  policy.sys.openbaoconfig.io/tenant-a-admin \
  policy.sys.openbaoconfig.io/tenant-b-admin \
  kubernetesauthrole.auth.openbaoconfig.io/tenant-a-operator \
  kubernetesauthrole.auth.openbaoconfig.io/tenant-b-operator
# --8<-- [end:openbao]

# --8<-- [start:connections]
kubectl apply -f examples/tutorials/tenant-onboarding/connections.yaml
kubectl -n tenant-a wait openbaoconnection/openbao --for=condition=Ready --timeout=2m
kubectl -n tenant-b wait openbaoconnection/openbao --for=condition=Ready --timeout=2m
# --8<-- [end:connections]

for tenant in tenant-a tenant-b; do
  [ "$(kubectl -n "$tenant" get openbaoconnection openbao -o jsonpath='{.status.authenticated}')" = true ] ||
    fail "the connection of $tenant is not authenticated"
done

# --8<-- [start:verify]
kubectl -n openbao exec deploy/openbao -- bao namespace list
kubectl -n openbao exec deploy/openbao -- bao read -namespace=tenant-a auth/kubernetes/role/operator
# --8<-- [end:verify]

namespaces=$(bao_out namespace list)
for tenant in tenant-a tenant-b; do
  grep -qx "$tenant/" <<<"$namespaces" || fail "OpenBao does not list the namespace $tenant"
  policy=$(bao_out policy read -namespace="$tenant" tenant-admin)
  grep -q 'path "auth/kubernetes/role/team-\*"' <<<"$policy" ||
    fail "policy tenant-admin in $tenant does not match examples/tutorials/tenant-onboarding/openbao-$tenant.yaml"
  [ "$(bao_out read -namespace="$tenant" -field=bound_service_account_namespaces auth/kubernetes/role/operator)" = "[$tenant]" ] ||
    fail "role operator in $tenant does not bind only the Kubernetes namespace $tenant"
done
if bao_out policy read tenant-admin >/dev/null 2>&1; then
  fail "policy tenant-admin exists in the root namespace; it belongs in the tenant namespaces only"
fi

echo "tutorial-tenant-onboarding: tenant-a and tenant-b have their Kubernetes and OpenBao namespaces and a Ready connection"
