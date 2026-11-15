#!/usr/bin/env bash
# The team onboarding tutorial of docs/tutorials/team-onboarding.md, after hack/docs/tutorial-tenant-onboarding.sh.
# The page includes the sections between the snippet markers; checks the page does not show sit outside them.
#
# Environment:
#   KIND_CLUSTER  kind cluster the quickstart ran on (default openbao-quickstart)
set -euo pipefail

cd "$(dirname "$0")/../.."
trap 'echo "tutorial-team-onboarding: failed at line $LINENO" >&2' ERR

KIND_CLUSTER=${KIND_CLUSTER:-openbao-quickstart}

fail() {
  echo "tutorial-team-onboarding: $*" >&2
  exit 1
}

# Checks capture output before grep: grep -q in a pipe can end the writer early, which pipefail reports.
bao_out() {
  kubectl -n openbao exec deploy/openbao -- bao "$@"
}

# The page shows the commands of these functions as ones that fail; the script runs them here and checks the error.
reach_tenant_b() {
  # --8<-- [start:reach-tenant-b]
  kubectl --as tenant-a-admin apply -f examples/tutorials/team-onboarding/reach-tenant-b.yaml
  # --8<-- [end:reach-tenant-b]
}
team_denied() {
  # --8<-- [start:team-denied]
  kubectl -n openbao exec deploy/openbao -- \
    env BAO_TOKEN="$DEVTEAM_TOKEN" bao kv get -namespace=tenant-a/payments -mount=secret -field=password app
  # --8<-- [end:team-denied]
}

# synced_reason waits up to 2 minutes until the Synced reason of a tenant-a resource is $2.
synced_reason() {
  local reason=""
  for _ in $(seq 1 24); do
    reason=$(kubectl -n tenant-a get "$1" -o jsonpath='{.status.conditions[?(@.type=="Synced")].reason}')
    [ "$reason" = "$2" ] && return 0
    sleep 5
  done
  fail "$1 has the Synced reason '$reason', want $2"
}

kubectl config use-context "kind-$KIND_CLUSTER"

# --8<-- [start:devteam]
kubectl --as tenant-a-admin apply -f examples/tutorials/team-onboarding/devteam.yaml
# --8<-- [end:devteam]

# --8<-- [start:payments]
kubectl --as tenant-a-admin apply -f examples/tutorials/team-onboarding/payments.yaml
# --8<-- [end:payments]

# --8<-- [start:wait]
kubectl --as tenant-a-admin -n tenant-a wait --for=condition=Ready --timeout=3m \
  openbaonamespace.sys.openbaoconfig.io/devteam \
  openbaonamespace.sys.openbaoconfig.io/payments \
  secretsmount.sys.openbaoconfig.io/devteam-secret \
  secretsmount.sys.openbaoconfig.io/payments-secret \
  randomsecret.secrets.openbaoconfig.io/devteam-app \
  randomsecret.secrets.openbaoconfig.io/payments-app \
  policy.sys.openbaoconfig.io/team-devteam \
  policy.sys.openbaoconfig.io/team-payments \
  kubernetesauthrole.auth.openbaoconfig.io/team-devteam \
  kubernetesauthrole.auth.openbaoconfig.io/team-payments
# --8<-- [end:wait]

# --8<-- [start:verify]
kubectl -n openbao exec deploy/openbao -- bao namespace list -namespace=tenant-a
kubectl -n openbao exec deploy/openbao -- bao policy read -namespace=tenant-a team-devteam
# --8<-- [end:verify]

children=$(bao_out namespace list -namespace=tenant-a)
for team in devteam payments; do
  grep -qx "$team/" <<<"$children" || fail "OpenBao does not list the namespace tenant-a/$team"
  [ "$(bao_out read -namespace="tenant-a/$team" -field=type sys/mounts/secret)" = kv ] ||
    fail "secret/ in tenant-a/$team is not a kv mount"
done
tenant_b_children=$(bao_out namespace list -namespace=tenant-b 2>/dev/null || true)
if grep -qx 'devteam/' <<<"$tenant_b_children"; then
  fail "the team namespace devteam exists under tenant-b"
fi

# --8<-- [start:team-login]
DEVTEAM_JWT=$(kubectl -n tenant-a create token devteam --audience openbao)
DEVTEAM_TOKEN=$(kubectl -n openbao exec deploy/openbao -- \
  bao write -namespace=tenant-a -field=token auth/kubernetes/login role=team-devteam jwt="$DEVTEAM_JWT")
kubectl -n openbao exec deploy/openbao -- \
  env BAO_TOKEN="$DEVTEAM_TOKEN" bao kv get -namespace=tenant-a/devteam -mount=secret -field=password app
# --8<-- [end:team-login]

devteam_value=$(kubectl -n openbao exec deploy/openbao -- \
  env BAO_TOKEN="$DEVTEAM_TOKEN" bao kv get -namespace=tenant-a/devteam -mount=secret -field=password app)
[ "${#devteam_value}" -eq 32 ] || fail "devteam did not read a 32-character password from tenant-a/devteam"

if denied=$(team_denied 2>&1); then
  fail "devteam read the secret of payments"
fi
grep -qiE 'permission denied|403' <<<"$denied" || fail "devteam's read of payments failed for another reason: $denied"

# --8<-- [start:reach-root]
kubectl --as tenant-a-admin apply -f examples/tutorials/team-onboarding/reach-root.yaml
# --8<-- [end:reach-root]

synced_reason policy.sys.openbaoconfig.io/reach-root ConnectionDenied

# --8<-- [start:reach-root-status]
kubectl --as tenant-a-admin -n tenant-a get policy.sys.openbaoconfig.io/reach-root \
  -o jsonpath='{.status.conditions[?(@.type=="Synced")].message}{"\n"}'
# --8<-- [end:reach-root-status]

[ "$(kubectl -n tenant-a get policy.sys.openbaoconfig.io/reach-root \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')" = False ] || fail "reach-root is Ready"
message=$(kubectl -n tenant-a get policy.sys.openbaoconfig.io/reach-root \
  -o jsonpath='{.status.conditions[?(@.type=="Synced")].message}')
grep -q 'namespace tenant-a is not in allowedNamespaces' <<<"$message" ||
  fail "reach-root was refused for another reason: $message"

if denied=$(reach_tenant_b 2>&1); then
  fail "tenant-a-admin created a resource in the Kubernetes namespace tenant-b"
fi
grep -qi forbidden <<<"$denied" || fail "the apply in tenant-b failed for another reason: $denied"

# The tenant's own OpenBao identity, the one its connection logs in as, is refused outside tenant-a.
tenant_jwt=$(kubectl -n tenant-a create token openbao-config --audience openbao)
tenant_token=$(bao_out write -namespace=tenant-a -field=token auth/kubernetes/login role=operator jwt="$tenant_jwt")
escape='path "secret/data/*" { capabilities = ["read"] }'
for target in root tenant-b; do
  flag=()
  [ "$target" = root ] || flag=(-namespace="$target")
  if out=$(kubectl -n openbao exec -i deploy/openbao -- env BAO_TOKEN="$tenant_token" \
    bao policy write ${flag[@]+"${flag[@]}"} team-escape - <<<"$escape" 2>&1); then
    fail "the tenant-a identity wrote a policy in $target"
  fi
  grep -qE '403|permission denied' <<<"$out" || fail "the policy write in $target failed for another reason: $out"
done
for ns in root tenant-b; do
  flag=()
  [ "$ns" = root ] || flag=(-namespace="$ns")
  for name in team-escape tenant-a-in-root team-tenant-a; do
    if bao_out policy read ${flag[@]+"${flag[@]}"} "$name" >/dev/null 2>&1; then
      fail "policy $name exists in the OpenBao namespace $ns"
    fi
  done
done

echo "tutorial-team-onboarding: devteam and payments reach only their own namespace, and tenant-a stays in its own"
