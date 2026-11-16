#!/usr/bin/env bash
# Creates a throwaway CA and the server certificate of the kind e2e OpenBao (hack/e2e/openbao.yaml): the Secret
# openbao/openbao-tls (tls.crt, tls.key, ca.crt) and the ConfigMap openbao/openbao-ca (ca.crt). An existing Secret is
# kept, so running pods and the clients that already trust its CA keep working. The CA key is discarded.
set -euo pipefail

ns=openbao

kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
if kubectl -n "$ns" get secret openbao-tls >/dev/null 2>&1; then
  echo "reusing Secret $ns/openbao-tls"
  exit 0
fi

dir=$(mktemp -d)
trap 'rm -rf -- "$dir"' EXIT
sans=DNS:localhost,IP:127.0.0.1
for name in openbao openbao-active openbao-internal '*.openbao-internal'; do
  sans+=",DNS:$name,DNS:$name.$ns.svc,DNS:$name.$ns.svc.cluster.local"
done

for key in ca tls; do openssl ecparam -genkey -name prime256v1 -noout -out "$dir/$key.key"; done
openssl req -new -key "$dir/ca.key" -subj /CN=openbao-e2e-ca -out "$dir/ca.csr"
openssl x509 -req -sha256 -in "$dir/ca.csr" -signkey "$dir/ca.key" -days 365 -out "$dir/ca.crt" \
  -extfile <(printf 'basicConstraints=critical,CA:TRUE\nsubjectKeyIdentifier=hash\nkeyUsage=critical,keyCertSign,cRLSign\n')
openssl req -new -key "$dir/tls.key" -subj /CN=openbao -out "$dir/tls.csr"
openssl x509 -req -sha256 -in "$dir/tls.csr" -CA "$dir/ca.crt" -CAkey "$dir/ca.key" -CAcreateserial -days 365 \
  -out "$dir/tls.crt" \
  -extfile <(printf 'subjectAltName=%s\nsubjectKeyIdentifier=hash\nauthorityKeyIdentifier=keyid:always\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=serverAuth\n' "$sans")

# The ConfigMap goes first: the Secret marks a complete run, so a failed run is redone from scratch.
kubectl -n "$ns" create configmap openbao-ca --from-file=ca.crt="$dir/ca.crt" --dry-run=client -o yaml |
  kubectl apply -f - >/dev/null
kubectl -n "$ns" create secret generic openbao-tls --type=kubernetes.io/tls --from-file=tls.crt="$dir/tls.crt" \
  --from-file=tls.key="$dir/tls.key" --from-file=ca.crt="$dir/ca.crt" >/dev/null
echo "created Secret $ns/openbao-tls and ConfigMap $ns/openbao-ca"
