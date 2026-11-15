#!/usr/bin/env bash
# The multiple OpenBao instances tutorial of docs/tutorials/multiple-instances.md, after
# hack/docs/tutorial-platform-owner.sh. The page includes the sections between the snippet markers; checks the page
# does not show sit outside them.
#
# Environment:
#   KIND_CLUSTER  kind cluster the quickstart ran on (default openbao-quickstart)
set -euo pipefail

cd "$(dirname "$0")/../.."
trap 'echo "tutorial-multiple-instances: failed at line $LINENO" >&2' ERR

KIND_CLUSTER=${KIND_CLUSTER:-openbao-quickstart}

fail() {
  echo "tutorial-multiple-instances: $*" >&2
  exit 1
}

# Checks capture output before grep: grep -q in a pipe can end the writer early, which pipefail reports.
bao_1() {
  kubectl -n openbao exec deploy/openbao -- bao "$@"
}
bao_2() {
  kubectl -n openbao-2 exec deploy/openbao -- bao "$@"
}
server_id() {
  kubectl -n platform get "$1" -o jsonpath='{.status.serverID}'
}

kubectl config use-context "kind-$KIND_CLUSTER"

# --8<-- [start:openbao-2]
kubectl apply -f examples/tutorials/multiple-instances/openbao-2.yaml
kubectl -n openbao-2 rollout status deployment/openbao --timeout=3m
# --8<-- [end:openbao-2]

auth_methods=$(bao_2 auth list)
if ! grep -q '^kubernetes/' <<<"$auth_methods"; then
  # --8<-- [start:openbao-2-auth-enable]
  kubectl -n openbao-2 exec deploy/openbao -- bao auth enable kubernetes
  # --8<-- [end:openbao-2-auth-enable]
fi
# --8<-- [start:openbao-2-auth]
kubectl -n openbao-2 exec deploy/openbao -- bao write auth/kubernetes/config \
  kubernetes_host=https://kubernetes.default.svc:443
kubectl -n openbao-2 exec -i deploy/openbao -- bao policy write openbao-config-operator - \
  < examples/tutorials/platform-owner/openbao-config-operator.hcl
kubectl -n openbao-2 exec deploy/openbao -- bao write auth/kubernetes/role/openbao-config-operator \
  bound_service_account_names=openbao-config-operator \
  bound_service_account_namespaces=openbao-config-operator \
  audience=openbao-config-operator \
  token_policies=openbao-config-operator \
  token_ttl=1h token_max_ttl=4h
# --8<-- [end:openbao-2-auth]

# --8<-- [start:connect]
kubectl apply -f examples/tutorials/multiple-instances/openbaoserver.yaml \
  -f examples/tutorials/multiple-instances/clusteropenbaoconnection.yaml
kubectl wait openbaoserver/openbao-2 clusteropenbaoconnection/platform-2 --for=condition=Ready --timeout=2m
# --8<-- [end:connect]

# --8<-- [start:configure]
kubectl apply -f examples/tutorials/multiple-instances/instance-1.yaml \
  -f examples/tutorials/multiple-instances/instance-2.yaml \
  -f examples/tutorials/multiple-instances/shared-policy.yaml
kubectl -n platform wait --for=condition=Ready --timeout=3m \
  policy.sys.openbaoconfig.io/instance-1-app \
  kubernetesauthrole.auth.openbaoconfig.io/instance-1-app \
  secretsmount.sys.openbaoconfig.io/instance-2-kv \
  authmount.sys.openbaoconfig.io/instance-2-userpass \
  policy.sys.openbaoconfig.io/ops-read-instance-1 \
  policy.sys.openbaoconfig.io/ops-read-instance-2
# --8<-- [end:configure]

# --8<-- [start:servers]
kubectl -n platform get policies.sys.openbaoconfig.io,kubernetesauthroles,secretsmounts,authmounts \
  -o custom-columns=KIND:.kind,NAME:.metadata.name,CONNECTION:.spec.connectionRef.name,SERVER:.status.serverID
# --8<-- [end:servers]

server_1=$(server_id policy.sys.openbaoconfig.io/ops-read-instance-1)
server_2=$(server_id policy.sys.openbaoconfig.io/ops-read-instance-2)
[ -n "$server_1" ] && [ -n "$server_2" ] || fail "a shared policy has no status.serverID"
[ "$server_1" != "$server_2" ] || fail "both shared policies report the same OpenBao cluster id $server_1"
for obj in policy.sys.openbaoconfig.io/instance-1-app kubernetesauthrole.auth.openbaoconfig.io/instance-1-app \
  policy.sys.openbaoconfig.io/platform-auditor; do
  [ "$(server_id "$obj")" = "$server_1" ] || fail "$obj is not bound to the first OpenBao"
done
for obj in secretsmount.sys.openbaoconfig.io/instance-2-kv authmount.sys.openbaoconfig.io/instance-2-userpass; do
  [ "$(server_id "$obj")" = "$server_2" ] || fail "$obj is not bound to the second OpenBao"
done

# --8<-- [start:verify-1]
kubectl -n openbao exec deploy/openbao -- bao policy list
kubectl -n openbao exec deploy/openbao -- bao read -field=token_policies auth/kubernetes/role/instance-1-app
# --8<-- [end:verify-1]

# --8<-- [start:verify-2]
kubectl -n openbao-2 exec deploy/openbao -- bao policy list
kubectl -n openbao-2 exec deploy/openbao -- bao secrets list
kubectl -n openbao-2 exec deploy/openbao -- bao auth list
# --8<-- [end:verify-2]

policies_1=$(bao_1 policy list)
policies_2=$(bao_2 policy list)
for name in instance-1-app ops-read platform-auditor; do
  grep -qx "$name" <<<"$policies_1" || fail "policy $name is missing in the first OpenBao"
done
grep -qx ops-read <<<"$policies_2" || fail "policy ops-read is missing in the second OpenBao"
for name in instance-1-app platform-auditor; do
  if grep -qx "$name" <<<"$policies_2"; then
    fail "policy $name of the first OpenBao was written to the second"
  fi
done
role_policies=$(bao_1 read -field=token_policies auth/kubernetes/role/instance-1-app)
grep -q instance-1-app <<<"$role_policies" || fail "role instance-1-app does not attach the policy instance-1-app"
if bao_2 read auth/kubernetes/role/instance-1-app >/dev/null 2>&1; then
  fail "role instance-1-app of the first OpenBao was written to the second"
fi
[ "$(bao_1 read -field=policy sys/policies/acl/ops-read)" = "$(bao_2 read -field=policy sys/policies/acl/ops-read)" ] ||
  fail "the shared policy ops-read differs between the two OpenBao servers"

[ "$(bao_2 read -field=type sys/mounts/instance-2-kv)" = kv ] || fail "instance-2-kv is not a kv mount in the second OpenBao"
[ "$(bao_2 read -field=type sys/auth/userpass)" = userpass ] ||
  fail "userpass/ is not a userpass auth method in the second OpenBao"
if bao_1 read sys/mounts/instance-2-kv >/dev/null 2>&1; then
  fail "instance-2-kv of the second OpenBao exists in the first"
fi
if bao_1 read sys/auth/userpass >/dev/null 2>&1; then
  fail "userpass/ of the second OpenBao exists in the first"
fi

echo "tutorial-multiple-instances: each OpenBao holds exactly its own configuration and the shared policy"
