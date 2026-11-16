#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=hack/lab/common.sh
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
# renovate: datasource=helm depName=external-secrets registryUrl=https://charts.external-secrets.io
ESO_CHART_VERSION=2.11.0
helm=${HELM:-$repo_root/bin/helm}
"$helm" upgrade --install lab-external-secrets external-secrets \
  --repo https://charts.external-secrets.io --version "$ESO_CHART_VERSION" \
  --kube-context "$LAB_CONTEXT" --namespace obco-lab-eso --create-namespace \
  --values "$repo_root/hack/lab/eso-values.yaml" --wait --timeout 5m
python3 "$repo_root/hack/lab/eso.py" up
