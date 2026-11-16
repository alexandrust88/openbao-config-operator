#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=hack/lab/common.sh
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
"$repo_root/hack/lab/bootstrap.sh"
helm=${HELM:-$repo_root/bin/helm}
# renovate: datasource=helm depName=cert-manager registryUrl=https://charts.jetstack.io
CERT_MANAGER_CHART_VERSION=v1.21.3
"$helm" upgrade --install cert-manager oci://quay.io/jetstack/charts/cert-manager \
  --version "$CERT_MANAGER_CHART_VERSION" --namespace cert-manager --create-namespace --kube-context "$LAB_CONTEXT" \
  --set crds.enabled=true --wait --timeout 5m
python3 "$repo_root/hack/lab/pki.py" up
