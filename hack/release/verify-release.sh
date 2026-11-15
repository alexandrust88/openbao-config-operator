#!/usr/bin/env bash
# Verifies a published release: the image index and every platform image, and the chart, are signed by the release
# workflow run of exactly this tag (keyless, GitHub OIDC issuer, push trigger), carry SPDX + CycloneDX SBOM and SLSA
# provenance attestations, the image provenance names this tag, the chart also carries the legacy cosign signature
# Artifact Hub detects, and the verified chart pins the verified image digest. The chart release asset and its Helm
# provenance file (.prov, what Artifact Hub shows as signed) verify with the GPG public key published on GitHub Pages,
# and are the chart and provenance layers of the verified OCI chart (skipped for the releases published before the
# provenance file). With ASSETS_DIR it also checks the
# signed sha256sums.txt and the provenance (.intoto.jsonl) of the downloaded release assets, and that the examples
# bundle (examples-<version>.tar.gz) is listed there and holds only examples/ and examples-generated/ (skipped for the
# releases published before the bundle existed), with the gh CLI on PATH
# the GitHub artifact attestations, and with the ocm CLI on PATH (or OCM) the OCM component version
# (hack/ocm/verify.sh; skipped for releases published before the OCM component existed).
# Usage: hack/release/verify-release.sh <vX.Y.Z>
# Environment: REPOSITORY (owner/name), IMAGE, CHART, IMAGE_DIGEST and CHART_DIGEST (from the release notes; resolved
# from the tags otherwise), ASSETS_DIR, YQ, HELM, SIGN_KEY_URL (the published chart signing public key).
# Needs: cosign v3, crane, jq, yq, curl, helm v4 (reads the armored public key).
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
helm=${HELM:-helm}
sign_key_url=${SIGN_KEY_URL:-https://$owner.github.io/${repository#*/}/charts/pgp-public-key.asc}
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
index=$(crane manifest "$image@$image_digest")
platforms=$(jq -r '.manifests[] | select(.platform.os != "unknown") | "\(.platform.os)/\(.platform.architecture) \(.digest)"' <<<"$index")
# The platforms release.yml builds; a missing one would otherwise go unverified.
for want in linux/amd64 linux/arm64; do
  grep -q "^$want sha256:" <<<"$platforms" || { echo "image index $image@$image_digest has no $want image" >&2; exit 1; }
done
while read -r platform digest; do
  echo "platform $platform"
  verify_subject "$image@$digest" spdxjson cyclonedx slsaprovenance1
done <<<"$platforms"

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

tgz=openbao-config-operator-$version.tgz
step "chart provenance $tgz.prov"
case $tag in
  v0.1.0 | v0.1.1 | v0.2.0) echo "skipped (published before the provenance file)" ;;
  *)
    prov_dir=$(mktemp -d)
    trap 'rm -rf -- "$prov_dir"' EXIT
    chart_prov() {
      local url=https://github.com/$repository/releases/download/$tag/$tgz
      curl -fsSL -o "$prov_dir/pgp-public-key.asc" "$sign_key_url" &&
        curl -fsSL -o "$prov_dir/$tgz" "$url" &&
        curl -fsSL -o "$prov_dir/$tgz.prov" "$url.prov" &&
        "$helm" verify --keyring "$prov_dir/pgp-public-key.asc" "$prov_dir/$tgz"
    }
    check chart_prov
    step "  release asset and .prov are layers of $chart@$chart_digest"
    oci_layers() {
      crane manifest "$chart@$chart_digest" | jq -e \
        --arg chart "sha256:$(shasum -a 256 "$prov_dir/$tgz" | awk '{print $1}')" \
        --arg prov "sha256:$(shasum -a 256 "$prov_dir/$tgz.prov" | awk '{print $1}')" '
        [.layers[] | select(.mediaType == "application/vnd.cncf.helm.chart.content.v1.tar+gzip").digest] == [$chart]
        and [.layers[] | select(.mediaType == "application/vnd.cncf.helm.chart.provenance.v1.prov").digest] == [$prov]'
    }
    check oci_layers
    ;;
esac

if [ -n "${ASSETS_DIR:-}" ]; then
  step "release assets sha256sums.txt"
  check cosign verify-blob "${pin[@]}" --bundle "$ASSETS_DIR/sha256sums.txt.sigstore.json" "$ASSETS_DIR/sha256sums.txt"
  step "release assets match sha256sums.txt"
  assets_match() { (cd "$ASSETS_DIR" && shasum -a 256 -c sha256sums.txt); }
  check assets_match
  step "release assets provenance (.intoto.jsonl)"
  check cosign verify-blob-attestation "${pin[@]}" --type slsaprovenance1 \
    --bundle "$ASSETS_DIR/openbao-config-operator.intoto.jsonl" "$ASSETS_DIR/install.yaml"
  examples=examples-$version.tar.gz
  step "release assets include $examples"
  case $tag in
    v0.1.0 | v0.1.1) echo "skipped (published before the examples bundle)" ;;
    *)
      examples_listed() { awk '{sub(/^\*/, "", $2); print $2}' "$ASSETS_DIR/sha256sums.txt" | grep -qxF "$examples"; }
      check examples_listed
      step "  provenance (.intoto.jsonl)"
      check cosign verify-blob-attestation "${pin[@]}" --type slsaprovenance1 \
        --bundle "$ASSETS_DIR/openbao-config-operator.intoto.jsonl" "$ASSETS_DIR/$examples"
      step "  holds only examples/ and examples-generated/"
      only_examples() {
        local listing
        listing=$(tar -tzf "$ASSETS_DIR/$examples") || return 1
        ! grep -vE '^examples(-generated)?/[^./][^/]*(/[^./][^/]*)*$' <<<"$listing"
      }
      check only_examples
      ;;
  esac
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
