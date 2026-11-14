#!/usr/bin/env bash
# Verifies a published release: the image index and every platform image, and the chart, are signed by the release
# workflow run of exactly this tag (keyless, GitHub OIDC issuer, push trigger), carry SPDX + CycloneDX SBOM and SLSA
# provenance attestations, the image provenance names this tag, the chart also carries the legacy cosign signature
# Artifact Hub detects, and the verified chart pins the verified image digest. With ASSETS_DIR it also checks the
# signed sha256sums.txt and the provenance (.intoto.jsonl) of the downloaded release assets, with the gh CLI on PATH
# the GitHub artifact attestations, and with the ocm CLI on PATH (or OCM) the OCM component version
# (hack/ocm/verify.sh; skipped for releases published before the OCM component existed).
# Usage: hack/release/verify-release.sh <vX.Y.Z>
# Environment: REPOSITORY (owner/name), IMAGE, CHART, IMAGE_DIGEST and CHART_DIGEST (from the release notes; resolved
# from the tags otherwise), ASSETS_DIR, YQ.
# Needs: cosign v3, crane, jq, yq.
set -euo pipefail

[ $# -eq 1 ] || { echo "usage: $0 <vX.Y.Z>" >&2; exit 2; }
tag=$1
"$(dirname "$0")/check-version.sh" "$tag" || exit 2
version=${tag#v}
repository=${REPOSITORY:-alexandrust88/openbao-config-operator}
owner=$(tr '[:upper:]' '[:lower:]' <<<"${repository%%/*}")
image=${IMAGE:-ghcr.io/$(tr '[:upper:]' '[:lower:]' <<<"$repository")}
chart=${CHART:-ghcr.io/$owner/charts/openbao-config-operator}
yq=${YQ:-yq}
workflow=$repository/.github/workflows/release.yml

# The exact tag in the identity: a signature from any other release, older or newer, does not verify as this one.
pin=(--certificate-oidc-issuer https://token.actions.githubusercontent.com
  --certificate-identity "https://github.com/$workflow@refs/tags/$tag"
  --certificate-github-workflow-trigger push
  --certificate-github-workflow-repository "$repository"
  --certificate-github-workflow-ref "refs/tags/$tag")

step() { printf '%-64s' "$1"; }

# check runs a verification quietly and prints its output only when it fails.
check() {
  local out
  if out=$("$@" 2>&1); then echo ok; else echo FAILED; echo "$out" >&2; exit 1; fi
}

image_digest=${IMAGE_DIGEST:-$(crane digest "$image:$tag")}
chart_digest=${CHART_DIGEST:-$(crane digest "$chart:$version")}

verify_subject() {
  local ref=$1 type
  shift
  step "signature $ref"
  check cosign verify "${pin[@]}" "$ref"
  for type in "$@"; do
    step "  attestation $type"
    check cosign verify-attestation "${pin[@]}" --type "$type" "$ref"
  done
}

verify_subject "$image@$image_digest" spdxjson cyclonedx slsaprovenance1
step "  provenance names refs/tags/$tag"
provenance_ref() {
  cosign verify-attestation "${pin[@]}" --type slsaprovenance1 "$image@$image_digest" 2>/dev/null |
    jq -rs '[.[].payload | @base64d | fromjson | .predicate.buildDefinition.externalParameters.workflow.ref] | unique | join(",")'
}
check test "$(provenance_ref)" = "refs/tags/$tag"
while read -r platform digest; do
  [ -n "$digest" ] || continue
  echo "platform $platform"
  verify_subject "$image@$digest" spdxjson cyclonedx slsaprovenance1
done < <(crane manifest "$image@$image_digest" |
  jq -r '.manifests[]? | select(.platform.os != "unknown") | "\(.platform.os)/\(.platform.architecture) \(.digest)"')

verify_subject "$chart@$chart_digest" spdxjson cyclonedx slsaprovenance1
step "  legacy signature (sha256-<digest>.sig, Artifact Hub)"
check cosign verify "${pin[@]}" --new-bundle-format=false "$chart@$chart_digest"

# Read from the chart digest just verified, never from the version tag, which may have moved since.
step "chart pins image $image_digest"
chart_pin() {
  local layer
  layer=$(crane manifest "$chart@$chart_digest" |
    jq -r '[.layers[] | select(.mediaType == "application/vnd.cncf.helm.chart.content.v1.tar+gzip")][0].digest')
  crane blob "$chart@$layer" | tar -xzOf - openbao-config-operator/values.yaml | "$yq" '.image.digest'
}
check test "$(chart_pin)" = "$image_digest"

if [ -n "${ASSETS_DIR:-}" ]; then
  step "release assets sha256sums.txt"
  check cosign verify-blob "${pin[@]}" --bundle "$ASSETS_DIR/sha256sums.txt.sigstore.json" "$ASSETS_DIR/sha256sums.txt"
  step "release assets match sha256sums.txt"
  assets_match() { (cd "$ASSETS_DIR" && shasum -a 256 -c sha256sums.txt); }
  check assets_match
  step "release assets provenance (.intoto.jsonl)"
  check cosign verify-blob-attestation "${pin[@]}" --type slsaprovenance1 \
    --bundle "$ASSETS_DIR/openbao-config-operator.intoto.jsonl" "$ASSETS_DIR/install.yaml"
fi

if command -v gh >/dev/null; then
  for ref in "$image@$image_digest" "$chart@$chart_digest"; do
    step "github attestation $ref"
    check gh attestation verify "oci://$ref" --repo "$repository" --signer-workflow "$workflow" \
      --source-ref "refs/tags/$tag"
  done
fi

if command -v "${OCM:-ocm}" >/dev/null; then
  echo "ocm component"
  REPOSITORY=$repository IMAGE=$image CHART=$chart IMAGE_DIGEST=$image_digest CHART_DIGEST=$chart_digest \
    ALLOW_ABSENT=1 "$(dirname "$0")/../ocm/verify.sh" "$tag"
fi

echo "release $tag verified: image $image@$image_digest, chart $chart@$chart_digest"
