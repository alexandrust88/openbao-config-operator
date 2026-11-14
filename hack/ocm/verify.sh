#!/usr/bin/env bash
# Verifies a published OCM component version: its component descriptor manifest is signed by the release workflow run
# of exactly this tag (keyless, GitHub OIDC issuer, push trigger) and carries SLSA provenance, the descriptor is this
# component and version, the image and the chart it references are the signed release digests, the manifests match
# the release assets when ASSETS_DIR is set, and ocm re-digests every resource against the descriptor. The descriptor
# is read by the verified manifest digest, not by its tag.
# Usage: hack/ocm/verify.sh <vX.Y.Z>
# Environment: REPOSITORY (owner/name), OCM_REPO, IMAGE, CHART, IMAGE_DIGEST and CHART_DIGEST (resolved from the
# release tags otherwise), ASSETS_DIR, COSIGN_KEY (verify against this public key offline instead, for the dry-run),
# ALLOW_ABSENT=1 (succeed with a message when the version has no component, as releases before the OCM job),
# OCM, CRANE, COSIGN, YQ.
set -euo pipefail

[ $# -eq 1 ] || { echo "usage: $0 <vX.Y.Z>" >&2; exit 2; }
tag=$1
version=${tag#v}
repo_root=$(cd "$(dirname "$0")/../.." && pwd)
repository=${REPOSITORY:-alexandrust88/openbao-config-operator}
owner=$(tr '[:upper:]' '[:lower:]' <<<"${repository%%/*}")
ocm_repo=${OCM_REPO:-ghcr.io/$owner/ocm}
image=${IMAGE:-ghcr.io/$(tr '[:upper:]' '[:lower:]' <<<"$repository")}
chart=${CHART:-ghcr.io/$owner/charts/openbao-config-operator}
ocm=${OCM:-ocm}
crane=${CRANE:-crane}
cosign=${COSIGN:-cosign}
yq=${YQ:-yq}

if [ -n "${COSIGN_KEY:-}" ]; then
  pin=(--key "$COSIGN_KEY" --insecure-ignore-tlog=true)
else
  pin=(--certificate-oidc-issuer https://token.actions.githubusercontent.com
    --certificate-identity "https://github.com/$repository/.github/workflows/release.yml@refs/tags/$tag"
    --certificate-github-workflow-trigger push
    --certificate-github-workflow-repository "$repository"
    --certificate-github-workflow-ref "refs/tags/$tag")
fi

step() { printf '%-64s ' "$1"; }

# check runs a verification quietly and prints its output only when it fails.
check() {
  local out
  if out=$("$@" 2>&1); then echo ok; else echo FAILED; echo "$out" >&2; exit 1; fi
}

# ocm reaches a plain-http registry (the dry-run) only through an http:// prefix, which crane and cosign do not take.
bare() { echo "${1#http://}"; }

name=$(awk '$1 == "name:" {print $2; exit}' "$repo_root/release/component-constructor.yaml")
descriptor_ref="$(bare "$ocm_repo")/component-descriptors/$name"
if ! descriptor_digest=$("$crane" digest "$descriptor_ref:$version" 2>/dev/null); then
  err=$("$crane" digest "$descriptor_ref:$version" 2>&1 || true)
  if [ "${ALLOW_ABSENT:-0}" = 1 ] && grep -qE 'MANIFEST_UNKNOWN|NAME_UNKNOWN|status code 404' <<<"$err"; then
    echo "no OCM component $name:$version published (releases before the OCM job have none); skipped"
    exit 0
  fi
  echo "verify: cannot resolve $descriptor_ref:$version: $err" >&2
  exit 1
fi
image_digest=${IMAGE_DIGEST:-$("$crane" digest "$(bare "$image"):$tag")}
chart_digest=${CHART_DIGEST:-$("$crane" digest "$(bare "$chart"):$version")}

step "signature $name:$version"
check "$cosign" verify "${pin[@]}" "$descriptor_ref@$descriptor_digest"
step "  attestation slsaprovenance1"
check "$cosign" verify-attestation "${pin[@]}" --type slsaprovenance1 "$descriptor_ref@$descriptor_digest"

layer=$("$crane" manifest "$descriptor_ref@$descriptor_digest" |
  jq -r '[.layers[] | select(.mediaType | startswith("application/vnd.ocm.software.component-descriptor."))][0].digest')
descriptor=$("$crane" blob "$descriptor_ref@$layer" | tar -xOf - component-descriptor.yaml | "$yq" -o json)
step "descriptor is $name:$version"
check test "$(jq -r '"\(.component.name):\(.component.version)"' <<<"$descriptor")" = "$name:$version"

# resource prints "<access reference>|<sha256 digest>" of one resource of the descriptor.
resource() {
  jq -r --arg name "$1" '.component.resources[] | select(.name == $name)
    | "\(.access.imageReference // .access.localReference)|sha256:\(.digest.value)"' <<<"$descriptor"
}

for entry in "operator-image|$image|$image_digest" "helm-chart|$chart|$chart_digest"; do
  IFS='|' read -r resource_name repo digest <<<"$entry"
  step "$resource_name is $repo@$digest"
  check test "$(resource "$resource_name")" = "$repo@$digest|$digest"
  step "  signature"
  check "$cosign" verify "${pin[@]}" "$(bare "$repo")@$digest"
done

if [ -n "${ASSETS_DIR:-}" ]; then
  for entry in install-manifest:install.yaml crds:crds.yaml; do
    step "${entry%%:*} matches $ASSETS_DIR/${entry#*:}"
    check test "$(resource "${entry%%:*}" | cut -d'|' -f2)" = "sha256:$(shasum -a 256 "$ASSETS_DIR/${entry#*:}" | awk '{print $1}')"
  done
fi

step "ocm re-digests every resource"
check "$ocm" verify componentversions "$ocm_repo//$name:$version"
step "descriptor tag still at $descriptor_digest"
check test "$("$crane" digest "$descriptor_ref:$version")" = "$descriptor_digest"

echo "component $name:$version verified: descriptor $descriptor_ref@$descriptor_digest"
