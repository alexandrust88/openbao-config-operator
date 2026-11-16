#!/usr/bin/env bash
# Adds the published LDAP plugin to a lab image without changing its existing plugins.
set -euo pipefail
# shellcheck source=hack/lab/common.sh
source "$(dirname "${BASH_SOURCE[0]}")/../common.sh"
out=${1:-$repo_root/.tmp/lab/plugins}
mkdir -p "$out"
out=$(cd "$out" && pwd)
arch=$(lab_kubectl get nodes -o 'jsonpath={.items[0].status.nodeInfo.architecture}')
# Pinned release assets from https://github.com/openbao/openbao-plugins/releases/tag/auth-ldap-v0.2.0.
case $arch in
  amd64) target=linux_amd64_v1; archive_sha=def2ca548b4a09f0df753ecd19dc8e670a4be4044d57a7c0208528fc340786d8 ;;
  arm64) target=linux_arm64_v8; archive_sha=acc7c8a36cc60bdbbf5c4cd7d82fe397013ee430a8b06f0605fb47359d61f7c3 ;;
  *) echo "Unsupported node architecture: $arch" >&2; exit 1 ;;
esac
base=${LAB_BAO_BASE_IMAGE:-$(lab_kubectl -n openbao get statefulset openbao -o 'jsonpath={.spec.template.spec.containers[?(@.name=="openbao")].image}')}
archive=$out/auth-ldap.tar.gz
curl --fail --location --proto '=https' --tlsv1.2 --retry 3 \
  "https://github.com/openbao/openbao-plugins/releases/download/auth-ldap-v0.2.0/openbao-plugin-auth-ldap_${target}.tar.gz" -o "$archive"
printf '%s  %s\n' "$archive_sha" "$archive" | shasum -a 256 -c -
tar -xOf "$archive" "openbao-plugin-auth-ldap_$target" >"$out/openbao-plugin-auth-ldap"
tar -xOf "$archive" LICENSE >"$out/LICENSE.auth-ldap"
shasum -a 256 "$out/openbao-plugin-auth-ldap" | awk '{print $1}' >"$out/sha256"
plugin_sha=$(cat "$out/sha256")
license_sha=$(shasum -a 256 "$out/LICENSE.auth-ldap" | awk '{print $1}')
# Recover the source image from our own labels, rather than layering on an existing lab image.
# Old unlabelled images migrate once; subsequent runs preserve the same image and pod revision.
label() { docker image inspect "$1" --format "{{index .Config.Labels \"$2\"}}" 2>/dev/null || true; }
image=
original_base=$(label "$base" io.openbaoconfig.lab.ldap.base)
if [[ -n $original_base && $original_base != '<no value>' ]]; then
  if [[ $(label "$base" io.openbaoconfig.lab.ldap.sha256) == "$plugin_sha" &&
        $(label "$base" io.openbaoconfig.lab.ldap.license-sha256) == "$license_sha" &&
        $(label "$base" io.openbaoconfig.lab.ldap.format) == 1 ]]; then
    image=$base
  fi
  base=$original_base
fi
if [[ -z ${image:-} ]]; then
  image="example.com/obco-lab-ldap:$(printf '%s\n%s\n%s\n%s\n' "$base" "$plugin_sha" "$license_sha" labeled-v1 | shasum -a 256 | cut -c1-12)"
  cat >"$out/Dockerfile" <<DOCKERFILE
ARG BASE_IMAGE=$base
FROM \$BASE_IMAGE
COPY --chmod=0555 openbao-plugin-auth-ldap /plugins/openbao-plugin-auth-ldap
COPY --chmod=0444 LICENSE.auth-ldap /plugins/LICENSE.auth-ldap
DOCKERFILE
  docker build --build-arg "BASE_IMAGE=$base" --label "io.openbaoconfig.lab.ldap.base=$base" \
    --label "io.openbaoconfig.lab.ldap.sha256=$plugin_sha" \
    --label "io.openbaoconfig.lab.ldap.license-sha256=$license_sha" \
    --label io.openbaoconfig.lab.ldap.format=1 -t "$image" "$out"
else
  echo "Reusing verified LDAP image $image"
fi
"${KIND:-$repo_root/bin/kind}" load docker-image "$image" --name "${LAB_CONTEXT#kind-}"
printf '%s\n' "$image" >"$out/image"
echo "Image loaded; use $out/image and $out/sha256 for the lab rollout and profile."
