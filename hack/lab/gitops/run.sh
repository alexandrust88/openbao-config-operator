#!/usr/bin/env bash
set -euo pipefail
profile=${1:-}
case "$profile" in argocd|flux) ;; *) echo 'Usage: hack/lab/gitops/run.sh argocd|flux' >&2; exit 2 ;; esac
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
cd "$repo_root"
export LAB_CONTEXT=${LAB_CONTEXT:-kind-obco-gitops-$profile}
cluster=${LAB_CONTEXT#kind-}
[[ $LAB_CONTEXT == kind-* && $cluster =~ ^[a-z0-9][a-z0-9-]*$ ]] || exit 2
out="$repo_root/.tmp/lab/$cluster"
mkdir -p "$out"
export KUBECONFIG="$out/kubeconfig"
export LAB_NAMESPACE=obco-gitops LAB_CONNECTION=gitops-openbao
kind=${KIND:-$repo_root/bin/kind}
[[ -x $kind ]] || make kind
[[ -x "$repo_root/bin/helm" ]] || make helm
clusters=$("$kind" get clusters)
if grep -Fxq "$cluster" <<<"$clusters"; then
 echo 'GitOps regression requires a fresh cluster name; refusing to reuse an existing cluster' >&2; exit 1
fi
node_image=$(awk '$1=="KIND_NODE_IMAGE" && $2=="?=" {print $3}' Makefile)
cleanup() {
  result=$?
  trap - EXIT
  if ((result != 0)); then
    python3 hack/lab/gitops/diagnostics.py || true
  fi
  if [[ ${KEEP_GITOPS_LAB:-0} != 1 ]]; then
    if ! "$kind" delete cluster --name "$cluster"; then
      if ((result == 0)); then result=1; fi
    fi
  fi
  exit "$result"
}
trap cleanup EXIT
"$kind" create cluster --name "$cluster" --image "$node_image" --kubeconfig "$KUBECONFIG" --wait 180s
chmod 600 "$KUBECONFIG"
image=example.com/openbao-config-operator:gitops-build
make docker-build-host IMG="$image"
id=$(docker image inspect "$image" --format '{{.Id}}'); id=${id#sha256:}
export LAB_OPERATOR_IMAGE="${image%:*}:${id:0:12}"
docker tag "$image" "$LAB_OPERATOR_IMAGE"
"$kind" load docker-image "$LAB_OPERATOR_IMAGE" --name "$cluster"
arch=$(docker info --format '{{.Architecture}}'); case "$arch" in aarch64) arch=arm64 ;; x86_64) arch=amd64 ;; esac
mkdir -p "$out/git-server-build"
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -o "$out/git-server-build/server" hack/lab/gitops/server.go
cp hack/lab/gitops/Dockerfile "$out/git-server-build/Dockerfile"
docker build -t example.com/obco-gitops-server:lab "$out/git-server-build"
"$kind" load docker-image example.com/obco-gitops-server:lab --name "$cluster"
hack/e2e/tls.sh
python3 hack/lab/gitops/common.py "$profile"
