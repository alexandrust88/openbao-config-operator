#!/usr/bin/env bash
# Builds a test-only OpenBao image so external plugins remain available after a raft pod is replaced.
set -euo pipefail

: "${KIND_CLUSTER:=openbao-config-operator-test-e2e}"
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
out=${1:?output directory required}
mkdir -p "$out"
out=$(cd "$out" && pwd)
yq=${YQ:-$repo_root/bin/yq}
kind=${KIND:-$repo_root/bin/kind}
arch=$(docker info --format '{{.Architecture}}')
case $arch in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo "unsupported Docker architecture: $arch" >&2; exit 1 ;; esac

CGO_ENABLED=0 GOOS=linux GOARCH=$arch go -C "$repo_root/test/livekit/pluginfixture" build -mod=readonly -trimpath -o "$out/plugin" .
shasum -a 256 "$out/plugin" | awk '{print $1}' >"$out/sha256"
base_image=$("$yq" -r 'select(.kind == "StatefulSet" and .metadata.name == "openbao") | .spec.template.spec.containers[] | select(.name == "openbao") | .image' "$repo_root/hack/e2e/openbao.yaml")
cat >"$out/Dockerfile" <<DOCKERFILE
FROM $base_image
COPY --chmod=0555 plugin /plugins/openbao-plugin-secrets-live-kit
COPY --chmod=0555 plugin /plugins/openbao-plugin-auth-live-kit
DOCKERFILE
image=example.com/openbao-livekit-plugins:$(printf '%s\n%s\n' "$base_image" "$(cat "$out/sha256")" | shasum -a 256 | cut -c1-12)
docker build -t "$image" "$out"
"$kind" load docker-image "$image" --name "$KIND_CLUSTER"
printf '%s\n' "$image" >"$out/image"
rm -f "$out/plugin" "$out/Dockerfile"
PLUGIN_IMAGE=$image "$yq" '
  (select(.kind == "ConfigMap" and .metadata.name == "openbao") | .data."server.hcl") += "\nplugin_directory = \"/plugins\"\n" |
  (select(.kind == "StatefulSet" and .metadata.name == "openbao") | .spec.template.spec.containers[] | select(.name == "openbao") | .image) = strenv(PLUGIN_IMAGE)
' "$repo_root/hack/e2e/openbao.yaml" >"$out/openbao.yaml"
