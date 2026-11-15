#!/usr/bin/env bash
# The uninstall procedure of docs/get-started/install.md, run last by hack/docs/tutorials.sh on the cluster that the
# quickstart and the four tutorials left. It removes the OpenBao configuration of tenant-b (deletionPolicy Delete and
# the destructive-delete annotation) and keeps everything else (Orphan), then checks both in OpenBao after the
# manager and the CRDs are gone. The page includes the sections between the snippet markers; the scoping to tenant-b
# and the checks sit outside them.
#
# Environment:
#   KIND_CLUSTER  kind cluster the quickstart ran on (default openbao-quickstart)
set -euo pipefail

cd "$(dirname "$0")/../.."
trap 'echo "uninstall: failed at line $LINENO" >&2' ERR

KIND_CLUSTER=${KIND_CLUSTER:-openbao-quickstart}

fail() {
  echo "uninstall: $*" >&2
  exit 1
}

# Checks capture output before grep: grep -q in a pipe can end the writer early, which pipefail reports.
bao_1() {
  kubectl -n openbao exec deploy/openbao -- bao "$@"
}
bao_2() {
  kubectl -n openbao-2 exec deploy/openbao -- bao "$@"
}

# count_managed prints how many resources match the jq condition $1.
count_managed() {
  kubectl get openbao --all-namespaces -o json | jq "[.items[] | select($1)] | length"
}

# wait_finalizer waits up to 2 minutes until the platform resource $1 carries the engine finalizer.
wait_finalizer() {
  local finalizers=""
  for _ in $(seq 1 24); do
    finalizers=$(kubectl -n platform get "$1" -o jsonpath='{.metadata.finalizers}')
    grep -q 'openbaoconfig.io/finalizer' <<<"$finalizers" && return 0
    sleep 5
  done
  fail "$1 has no finalizer openbaoconfig.io/finalizer after 2 minutes: $finalizers"
}

# The resources of tenant-b that accept deletionPolicy Delete. Its KubernetesAuthConfig rejects Delete; it goes with
# the auth method.
tenant_b=(
  openbaonamespace.sys.openbaoconfig.io/tenant-b
  authmount.sys.openbaoconfig.io/tenant-b-kubernetes
  policy.sys.openbaoconfig.io/tenant-b-admin
  kubernetesauthrole.auth.openbaoconfig.io/tenant-b-operator
)

kubectl config use-context "kind-$KIND_CLUSTER"

# The quickstart's policy and role use deletionPolicy Delete, so the keep step has something to change.
[ "$(count_managed '.spec.deletionPolicy == "Delete"')" -ge 2 ] || fail "the tutorials left fewer than two Delete resources"

# --8<-- [start:orphan]
kubectl get openbao --all-namespaces -o json \
  | jq -r '.items[] | select(.spec.deletionPolicy == "Delete")
      | "\(.metadata.namespace) \(.kind | ascii_downcase).\(.apiVersion | split("/")[0])/\(.metadata.name)"' \
  | while read -r namespace resource; do
      kubectl -n "$namespace" patch "$resource" --type merge -p '{"spec":{"deletionPolicy":"Orphan"}}'
    done
# --8<-- [end:orphan]

[ "$(count_managed '.spec.deletionPolicy == "Delete"')" = 0 ] || fail "a resource still has deletionPolicy Delete"

for resource in "${tenant_b[@]}"; do
  kubectl -n platform patch "$resource" --type merge -p '{"spec":{"deletionPolicy":"Delete"}}'
done
for resource in "${tenant_b[@]}"; do
  wait_finalizer "$resource"
done

# --8<-- [start:destructive]
kubectl get openbao --all-namespaces -o json \
  | jq -r '.items[] | select(.spec.deletionPolicy == "Delete")
      | "\(.metadata.namespace) \(.kind | ascii_downcase).\(.apiVersion | split("/")[0])/\(.metadata.name)"' \
  | while read -r namespace resource; do
      kubectl -n "$namespace" annotate "$resource" openbaoconfig.io/allow-destructive-delete=true --overwrite
    done
# --8<-- [end:destructive]

annotated=$(count_managed '.metadata.annotations["openbaoconfig.io/allow-destructive-delete"] == "true"')
[ "$annotated" = "${#tenant_b[@]}" ] || fail "$annotated resources carry the destructive-delete annotation, want ${#tenant_b[@]}"

# --8<-- [start:delete-resources]
resources=$(kubectl get crd -o name | cut -d/ -f2 | grep '\.openbaoconfig\.io$' \
  | grep -vxE '(openbaoservers|openbaoconnections|clusteropenbaoconnections)\.core\.openbaoconfig\.io' \
  | paste -sd, -)
kubectl delete "$resources" --all --all-namespaces --timeout=10m
# --8<-- [end:delete-resources]

if bao_1 read sys/namespaces/tenant-b >/dev/null 2>&1; then
  fail "the OpenBao namespace tenant-b still exists after its resources were deleted with deletionPolicy Delete"
fi
namespaces=$(bao_1 namespace list)
grep -qx 'tenant-a/' <<<"$namespaces" || fail "the OpenBao namespace tenant-a went with deletionPolicy Orphan"

# --8<-- [start:delete-connections]
kubectl delete openbaoconnections.core.openbaoconfig.io,clusteropenbaoconnections.core.openbaoconfig.io \
  --all --all-namespaces --timeout=5m
# --8<-- [end:delete-connections]

# --8<-- [start:delete-servers]
kubectl delete openbaoservers.core.openbaoconfig.io --all --timeout=5m
# --8<-- [end:delete-servers]

# --8<-- [start:check]
kubectl get openbao --all-namespaces
# --8<-- [end:check]

left=$(kubectl get openbao --all-namespaces -o name)
[ -z "$left" ] || fail "objects of the operator are left before the uninstall: $left"

# --8<-- [start:helm-uninstall]
helm uninstall openbao-config-operator --namespace openbao-config-operator
# --8<-- [end:helm-uninstall]

manager=""
for _ in $(seq 1 24); do
  manager=$(kubectl -n openbao-config-operator get deployments,pods -o name)
  [ -z "$manager" ] && break
  sleep 5
done
[ -z "$manager" ] || fail "the manager is still running 2 minutes after helm uninstall: $manager"

# --8<-- [start:crds]
kubectl get crd -o name | grep '\.openbaoconfig\.io$' | xargs kubectl delete --timeout=5m
# --8<-- [end:crds]

crds=$(kubectl get crd -o name)
if grep '\.openbaoconfig\.io$' <<<"$crds"; then
  fail "CRDs of the operator are left, Terminating or not"
fi

# What deletionPolicy Orphan kept, in both servers, with the manager and the CRDs gone.
namespaces=$(bao_1 namespace list)
for name in platform tenant-a; do
  grep -qx "$name/" <<<"$namespaces" || fail "the OpenBao namespace $name is gone"
done
if grep -qx 'tenant-b/' <<<"$namespaces"; then
  fail "the OpenBao namespace tenant-b is back"
fi
children=$(bao_1 namespace list -namespace=tenant-a)
grep -qx 'devteam/' <<<"$children" || fail "the OpenBao namespace tenant-a/devteam is gone"
bao_1 policy read quickstart-app-read >/dev/null || fail "policy quickstart-app-read is gone"
bao_1 policy read platform-auditor >/dev/null || fail "policy platform-auditor is gone"
bao_1 read auth/kubernetes/role/quickstart-app >/dev/null || fail "role quickstart-app is gone"
bao_2 policy read ops-read >/dev/null || fail "policy ops-read is gone from the second OpenBao"
[ "$(bao_2 read -field=type sys/mounts/instance-2-kv)" = kv ] || fail "instance-2-kv is gone from the second OpenBao"

echo "uninstall: tenant-b is removed from OpenBao, everything else is kept, and no object or CRD of the operator is left"
