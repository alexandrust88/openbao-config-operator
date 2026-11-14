#!/usr/bin/env bash
# Boots OpenBao in a container with examples/self-init/openbao.hcl, a static seal and fake ServiceAccount files,
# then checks that self-init created the kubernetes auth method, the operator policy and the operator role and
# revoked its root token. A test-only userpass login (verify.hcl below) reads the result.
# Usage: examples/self-init/verify.sh   (needs docker, bao and jq)
set -euo pipefail

image=ghcr.io/openbao/openbao:2.7.2@sha256:5046911075e137e154ce49ab051502f4ec7cf367d484ae4a4dce665d9b90561f
here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
name=obco-selfinit-$$
port=${PORT:-28200}
trap 'docker rm -f "$name" >/dev/null 2>&1 || true; rm -rf "$work"' EXIT

mkdir -p "$work/cfg" "$work/sa" "$work/data"
chmod 777 "$work/data"
openssl rand -out "$work/cfg/unseal.key" 32
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj /CN=fake-kube-ca -days 1 \
  -keyout "$work/sa/key.pem" -out "$work/sa/ca.crt" 2>/dev/null
echo fake-reviewer-token >"$work/sa/token"
cp "$here/openbao.hcl" "$work/cfg/"

cat >"$work/cfg/server.hcl" <<'EOF'
api_addr     = "http://127.0.0.1:8200"
cluster_addr = "http://127.0.0.1:8201"
storage "raft" {
  path    = "/openbao/data"
  node_id = "selfinit-1"
}
listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = true
}
seal "static" {
  current_key_id = "verify-1"
  current_key    = "file:///openbao/cfg/unseal.key"
}
EOF

cat >"$work/cfg/verify.hcl" <<'EOF'
initialize "verify" {
  request "verify-policy" {
    operation = "update"
    path      = "sys/policies/acl/verify"
    data = {
      policy = "path \"sys/policies/acl/*\" { capabilities = [\"read\"] }\npath \"sys/auth\" { capabilities = [\"read\"] }\npath \"auth/kubernetes/*\" { capabilities = [\"read\"] }"
    }
  }
  request "enable-userpass" {
    operation = "update"
    path      = "sys/auth/userpass"
    data      = { type = "userpass" }
  }
  request "verify-user" {
    operation = "update"
    path      = "auth/userpass/users/verify"
    data      = { password = "verify-not-secret", token_policies = ["verify"] }
  }
}
EOF
chmod -R a+r "$work/cfg" "$work/sa"

docker run -d --name "$name" -p "127.0.0.1:$port:8200" -e SKIP_SETCAP=true \
  -v "$work/cfg:/openbao/cfg:ro" -v "$work/data:/openbao/data" \
  -v "$work/sa:/var/run/secrets/kubernetes.io/serviceaccount:ro" \
  "$image" server -config=/openbao/cfg/server.hcl -config=/openbao/cfg/openbao.hcl -config=/openbao/cfg/verify.hcl \
  >/dev/null

export BAO_ADDR=http://127.0.0.1:$port
token=""
for _ in $(seq 1 60); do
  if token=$(bao login -method=userpass -token-only username=verify password=verify-not-secret 2>/dev/null); then
    break
  fi
  sleep 1
done
if [ -z "$token" ]; then
  docker logs "$name" 2>&1 | grep -i error >&2 || true
  echo "verify: self-init did not finish" >&2
  exit 1
fi
export BAO_TOKEN=$token

fail() { echo "verify: $*" >&2; exit 1; }
bao auth list -format=json | jq -e 'has("kubernetes/")' >/dev/null || fail "kubernetes auth is not enabled"
bao read -field=policy sys/policies/acl/openbao-config-operator | grep -q 'sys/policies/acl/\*' ||
  fail "operator policy is missing"
bao read -format=json auth/kubernetes/role/openbao-config-operator |
  jq -e '.data.token_policies == ["openbao-config-operator"] and .data.audience == "openbao-config-operator"' >/dev/null ||
  fail "operator role is missing or wrong"
docker logs "$name" 2>&1 | grep -q 'revoked lease: lease_id=auth/token/root/' || fail "root token was not revoked"
echo "verify: self-init created kubernetes auth, the operator policy and role, and revoked the root token"
