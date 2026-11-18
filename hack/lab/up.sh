#!/usr/bin/env bash
# Start a disposable kind lab. PostgreSQL is a secrets-engine target; OpenBao always uses raft.
set -euo pipefail
# shellcheck source=hack/lab/common.sh
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
profile=${1:-raft}
if (($#)); then shift; fi
case "$profile" in raft|postgres) ;; *) echo "usage: $0 [raft|postgres] [-f OPERATOR_VALUES.yaml ...]" >&2; exit 2 ;; esac
values=(-f "$repo_root/hack/lab/profiles/operator-ha.yaml")
while (($#)); do
  case "$1" in
    -f|--values)
      [[ $# -ge 2 && -f $2 ]] || { echo "Expected an existing operator values file" >&2; exit 2; }
      values+=(-f "$(cd "$(dirname "$2")" && pwd)/$(basename "$2")"); shift 2 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done
cd "$repo_root"
cluster=${LAB_CONTEXT#kind-}
[[ $cluster =~ ^[a-z0-9][a-z0-9-]*$ ]] || { echo "Invalid kind cluster name" >&2; exit 2; }
out="$repo_root/.tmp/lab/$cluster"
mkdir -p "$out"
export KUBECONFIG=${LAB_KUBECONFIG:-$out/kubeconfig}
export LAB_KUBECONFIG=$KUBECONFIG
kind=${KIND:-$repo_root/bin/kind}
helm=${HELM:-$repo_root/bin/helm}
[[ -x $helm ]] || make helm
[[ -x $kind ]] || make kind
# Reuse the centrally maintained Kubernetes version/digest instead of adding a second pin.
node_image=${KIND_NODE_IMAGE:-$(awk '$1 == "KIND_NODE_IMAGE" && $2 == "?=" { print $3 }' Makefile)}
[[ -n $node_image ]] || { echo "KIND_NODE_IMAGE is missing" >&2; exit 1; }
clusters=$("$kind" get clusters)
if grep -Fxq -- "$cluster" <<<"$clusters"; then
  "$kind" get kubeconfig --name "$cluster" >"$KUBECONFIG"
else
  "$kind" create cluster --name "$cluster" --image "$node_image" --kubeconfig "$KUBECONFIG" --wait 180s
fi
chmod 600 "$KUBECONFIG"
[[ $(kubectl config current-context) == "$LAB_CONTEXT" ]] || { echo "Unexpected lab kubeconfig context" >&2; exit 1; }
# These existing helpers use only the isolated kubeconfig exported above.
statefulset=$(lab_kubectl -n openbao get statefulset openbao --ignore-not-found -o name)
configmap=$(lab_kubectl -n openbao get configmap openbao --ignore-not-found -o name)
if [[ -z $statefulset && -z $configmap ]]; then
  hack/e2e/tls.sh
  lab_kubectl apply -f hack/e2e/openbao.yaml -f test/e2e/testdata/openbao-auth.yaml
  lab_kubectl -n openbao label statefulset/openbao configmap/openbao "lab.obco.dev/cluster=$cluster"
elif [[ -n $statefulset && -n $configmap ]]; then
  # Add-ons can extend the config and image; never replace them with the base fixture on reuse.
  for resource in statefulset/openbao configmap/openbao; do
    owner=$(lab_kubectl -n openbao get "$resource" -o 'jsonpath={.metadata.labels.lab\.obco\.dev/cluster}')
    [[ $owner == "$cluster" ]] || { echo "$resource is not owned by lab $cluster; refusing to replace it" >&2; exit 1; }
  done
  echo "Reusing the lab's OpenBao configuration and image, including installed add-ons"
else
  echo "Incomplete lab: OpenBao StatefulSet and ConfigMap must both exist or both be absent" >&2
  exit 1
fi
lab_kubectl -n openbao rollout status statefulset/openbao --timeout=300s
lab_kubectl -n openbao wait pod -l openbao-active=true --for=condition=Ready --timeout=120s
image=${LAB_OPERATOR_IMAGE:-example.com/openbao-config-operator:lab}
if [[ -z ${LAB_OPERATOR_IMAGE:-} ]]; then
  make docker-build-host IMG="$image"
  id=$(docker image inspect "$image" --format '{{.Id}}')
  id=${id#sha256:}
  immutable="${image%:*}:${id:0:12}"
  docker tag "$image" "$immutable"
  image=$immutable
fi
"$kind" load docker-image "$image" --name "$cluster"
"$helm" upgrade --install openbao-config-operator charts/openbao-config-operator \
  --kube-context "$LAB_CONTEXT" --namespace openbao-config-operator-system --create-namespace \
  "${values[@]}" --set-string "image.repository=${image%:*}" --set-string "image.tag=${image##*:}" \
  --set-string image.digest= --wait --timeout 5m
hack/lab/bootstrap.sh
if [[ $profile == postgres ]]; then
  if ! lab_kubectl -n "$LAB_NAMESPACE" get secret live-db-root >/dev/null 2>&1; then
    password_file=$(mktemp)
    trap 'rm -f -- "$password_file"' EXIT
    chmod 600 "$password_file"
    openssl rand -hex 24 | tr -d '\n' >"$password_file"
    lab_kubectl -n "$LAB_NAMESPACE" create secret generic live-db-root --from-file=password="$password_file"
    rm -f -- "$password_file"
    trap - EXIT
  fi
  hack/e2e/postgres.sh "$LAB_NAMESPACE"
fi
printf 'Lab ready: context=%s profile=%s kubeconfig=%s\n' "$LAB_CONTEXT" "$profile" "$KUBECONFIG"
printf 'OpenBao uses TLS and three raft replicas. Operator Helm values: %s\n' "${values[*]}"
