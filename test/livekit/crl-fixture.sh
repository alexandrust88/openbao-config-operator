#!/usr/bin/env bash
# Serves the rendered PEM CRL inside kind and points URL CRLs at that controlled fixture.
set -euo pipefail

rendered=${1:?usage: crl-fixture.sh RENDERED_YAML NAMESPACE}
namespace=${2:?usage: crl-fixture.sh RENDERED_YAML NAMESPACE}
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
yq=${YQ:-$repo_root/bin/yq}

urls=$("$yq" -r 'select(.kind == "CertAuthCRL" and .spec.url != null) | .metadata.name' "$rendered")
[ -n "$urls" ] || exit 0
work_dir=$(mktemp -d)
trap 'rm -rf -- "$work_dir"' EXIT
"$yq" ea -r '[select(.kind == "CertAuthCRL" and .spec.crl != null) | .spec.crl] | .[0]' "$rendered" >"$work_dir/ca.crl"
if ! grep -q '^-----BEGIN X509 CRL-----$' "$work_dir/ca.crl"; then
  printf '%s: URL CRLs require a rendered PEM CRL fixture\n' "${0##*/}" >&2
  exit 1
fi
kubectl -n "$namespace" create configmap livekit-crl --from-file=ca.crl="$work_dir/ca.crl" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$namespace" apply -f "$repo_root/hack/e2e/crl.yaml" >/dev/null
kubectl -n "$namespace" rollout status deployment/livekit-crl --timeout=2m
CRL_URL="http://livekit-crl.$namespace.svc:8080/ca.crl" "$yq" -i \
  '(select(.kind == "CertAuthCRL" and .spec.url != null) | .spec.url) = strenv(CRL_URL)' "$rendered"
