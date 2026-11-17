#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=hack/lab/common.sh
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
cd "$repo_root"
arch=$(docker info --format '{{.Architecture}}')
case "$arch" in aarch64) arch=arm64 ;; x86_64) arch=amd64 ;; esac
build=$(mktemp -d)
trap 'rm -rf -- "$build"' EXIT
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -o "$build/transit" ./examples/apps/transit
base=$(awk '$1=="FROM" && $2 ~ /distroless\/static/ {print $2}' Dockerfile)
[[ -n $base ]] || { echo 'Pinned distroless image not found' >&2; exit 1; }
cat > "$build/Dockerfile" <<'DOCKER'
ARG BASE_IMAGE
FROM ${BASE_IMAGE}
COPY transit /transit
USER 65532:65532
ENTRYPOINT ["/transit"]
DOCKER
docker build --build-arg "BASE_IMAGE=$base" -t example.com/transit-app:build "$build"
id=$(docker image inspect example.com/transit-app:build --format '{{.Id}}')
id=${id#sha256:}
export TRANSIT_APP_IMAGE="example.com/transit-app:${id:0:12}"
docker tag example.com/transit-app:build "$TRANSIT_APP_IMAGE"
"${KIND:-$repo_root/bin/kind}" load docker-image "$TRANSIT_APP_IMAGE" --name "${LAB_CONTEXT#kind-}"
python3 hack/lab/transit.py up
python3 hack/lab/transit.py test
