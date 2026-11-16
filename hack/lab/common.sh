#!/usr/bin/env bash
# Shared defaults for the disposable kind lab; never target a shared Kubernetes context.
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
export repo_root
export LAB_CONTEXT=${LAB_CONTEXT:-kind-${KIND_CLUSTER:-openbao-config-operator-lab}}
export LAB_NAMESPACE=${LAB_NAMESPACE:-obco-lab}
export LAB_CONNECTION=${LAB_CONNECTION:-lab-openbao}
case "$LAB_CONTEXT" in
  kind-*) ;;
  *) echo "The lab requires a kind context, got $LAB_CONTEXT" >&2; exit 1 ;;
esac
lab_kubectl() { kubectl --context "$LAB_CONTEXT" "$@"; }

if [[ -n ${LAB_KUBECONFIG:-} ]]; then
  export KUBECONFIG=$LAB_KUBECONFIG
elif [[ -f "$repo_root/.tmp/lab/${LAB_CONTEXT#kind-}/kubeconfig" ]]; then
  export KUBECONFIG="$repo_root/.tmp/lab/${LAB_CONTEXT#kind-}/kubeconfig"
fi
