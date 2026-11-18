#!/usr/bin/env bash
# Runs every release step locally and pushes nothing: builds the multi-arch image into an OCI layout, generates SPDX
# and CycloneDX SBOMs per platform, gates on vulnerabilities, writes SLSA provenance, packages the chart with the
# image digest pinned and signed with a throwaway GPG key (Helm provenance, checked with helm verify) and pushes it
# by digest into an OCI layout, builds install.yaml/crds.yaml and the examples
# bundle (twice, to prove it is reproducible), signs and attests
# every subject with a throwaway cosign key (no Rekor, no Fulcio, no TUF), then verifies all of it, and last builds,
# verifies and transfers the OCM component against local registries (hack/ocm/dry-run.sh). Outputs land in
# $OUT (default .tmp/release-dryrun).
# Usage: hack/release/dry-run.sh   (or make release-dry-run)
# Environment: RELEASE_TAG (default v0.0.0-dryrun), PLATFORMS, BUILDKIT_IMAGE, BUILDER (an existing buildx builder),
# BUILDX (default "docker buildx"), REPRO_CHECK=1 (rebuild without cache and require the same digest),
# IMAGE_OCI_TAR (skip the build and use this multi-arch OCI layout tar, e.g. from an earlier run),
# HELM, YQ, KUSTOMIZE, SYFT, GRYPE, ORAS, COSIGN, OCM, CRANE. Needs gpg.
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
tag=${RELEASE_TAG:-v0.0.0-dryrun}
out=${OUT:-$repo_root/.tmp/release-dryrun}
platforms=${PLATFORMS:-linux/amd64,linux/arm64}
image=${IMAGE:-ghcr.io/alexandrust88/openbao-config-operator}
chart=${CHART:-ghcr.io/alexandrust88/charts/openbao-config-operator}
read -r -a buildx <<<"${BUILDX:-docker buildx}"
syft=${SYFT:-syft}
cosign=${COSIGN:-cosign}
export HELM=${HELM:-helm} YQ=${YQ:-yq} KUSTOMIZE=${KUSTOMIZE:-kustomize} GRYPE=${GRYPE:-grype} ORAS=${ORAS:-oras}

"$repo_root/hack/release/check-version.sh" "$tag" || exit 2
for tool in "${buildx[0]}" "$syft" "$cosign" "$HELM" "$YQ" "$KUSTOMIZE" "$GRYPE" "$ORAS" jq gpg gpgconf; do
  command -v "$tool" >/dev/null || { echo "dry-run: $tool not found" >&2; exit 1; }
done

rm -rf -- "${out:?}"
mkdir -p "$out/oci" "$out/dist" "$out/sig" "$out/keys"
# The throwaway GnuPG home lives in mktemp, not $out: gpg-agent fails when its socket path is too long.
gnupg=$(mktemp -d)
cleanup() {
  gpgconf --homedir "$gnupg" --kill gpg-agent >/dev/null 2>&1 || true
  rm -rf -- "$gnupg"
  [ -z "${remove_builder:-}" ] || "${buildx[@]}" rm "$builder" >/dev/null 2>&1 || true
}
trap cleanup EXIT

export SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-$(git -C "$repo_root" log -1 --format=%ct 2>/dev/null || echo 0)}
commit=$(git -C "$repo_root" rev-parse --verify -q HEAD || echo unknown)
created=$(date -u -r "$SOURCE_DATE_EPOCH" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "@$SOURCE_DATE_EPOCH" +%Y-%m-%dT%H:%M:%SZ)

builder=${BUILDER:-}
if [ -z "$builder" ] && [ -z "${IMAGE_OCI_TAR:-}" ]; then
  : "${BUILDKIT_IMAGE:?set BUILDKIT_IMAGE (make release-dry-run sets the pinned one) or BUILDER}"
  builder=openbao-config-operator-dryrun
  if ! "${buildx[@]}" inspect "$builder" >/dev/null 2>&1; then
    "${buildx[@]}" create --name "$builder" --driver docker-container --driver-opt "image=$BUILDKIT_IMAGE" >/dev/null
    [ "${KEEP_BUILDER:-0}" = 1 ] || remove_builder=1
  fi
fi

build() {
  local dest=$1
  shift
  "${buildx[@]}" build --builder "$builder" --platform "$platforms" --provenance=false --sbom=false \
    --build-arg VERSION="$tag" --build-arg COMMIT="$commit" --build-arg CREATED="$created" \
    --build-arg SOURCE_DATE_EPOCH="$SOURCE_DATE_EPOCH" \
    --output "type=oci,dest=$dest,rewrite-timestamp=true" "$@" "$repo_root"
}

blob() { echo "$out/oci/blobs/${1%%:*}/${1#*:}"; }

if [ -n "${IMAGE_OCI_TAR:-}" ]; then
  echo "== reuse the image layout $IMAGE_OCI_TAR"
  cp "$IMAGE_OCI_TAR" "$out/image.oci.tar"
else
  echo "== build $platforms"
  build "$out/image.oci.tar"
fi
tar -xf "$out/image.oci.tar" -C "$out/oci"
index_digest=$(jq -r '.manifests[0].digest' "$out/oci/index.json")
if [ "$(jq -r '.mediaType' "$(blob "$index_digest")")" = "application/vnd.oci.image.index.v1+json" ]; then
  platform_digests=$(jq -r '.manifests[] | select(.platform.os != "unknown")
    | "\(.platform.os)/\(.platform.architecture) \(.digest)"' "$(blob "$index_digest")")
else
  platform_digests="$platforms $index_digest"
fi
echo "image index $index_digest"
echo "$platform_digests"

if [ "${REPRO_CHECK:-0}" = 1 ] && [ -z "${IMAGE_OCI_TAR:-}" ]; then
  echo "== reproducibility: rebuild without cache"
  build "$out/image-rebuild.oci.tar" --no-cache
  rebuilt=$(tar -xOf "$out/image-rebuild.oci.tar" index.json | jq -r '.manifests[0].digest')
  [ "$rebuilt" = "$index_digest" ] || { echo "dry-run: rebuild gave $rebuilt, not $index_digest" >&2; exit 1; }
  echo "rebuild matches $index_digest"
fi

echo "== check image labels"
while read -r platform digest; do
  config=$(jq -r '.config.digest' "$(blob "$digest")")
  jq -e --arg rev "$commit" --arg ver "$tag" --arg created "$created" '.config.Labels
    | .["org.opencontainers.image.revision"] == $rev and .["org.opencontainers.image.version"] == $ver
      and .["org.opencontainers.image.created"] == $created and .["org.opencontainers.image.licenses"] == "Apache-2.0"
      and (.["org.opencontainers.image.source"] | startswith("https://github.com/"))' "$(blob "$config")" >/dev/null ||
    { echo "dry-run: $platform image labels are incomplete" >&2; exit 1; }
  jq -e '.config.User == "65532:65532"' "$(blob "$config")" >/dev/null ||
    { echo "dry-run: $platform image does not run as 65532" >&2; exit 1; }
done <<<"$platform_digests"
echo "ok"

echo "== sboms"
sboms_spdx=()
while read -r platform digest; do
  arch=${platform#*/}
  "$syft" scan "oci-dir:$out/oci" --platform "$platform" --source-name "$image" --source-version "$tag" --quiet \
    --output "spdx-json=$out/dist/sbom-$arch.spdx.json" --output "cyclonedx-json=$out/dist/sbom-$arch.cdx.json"
  sboms_spdx+=("$out/dist/sbom-$arch.spdx.json")
done <<<"$platform_digests"

echo "== vulnerability gate"
"$repo_root/hack/release/scan.sh" "${sboms_spdx[@]}"

echo "== provenance"
"$repo_root/hack/release/provenance.sh" >"$out/provenance.json"

echo "== chart, signed with a throwaway GPG key"
printf 'dryrun\n' >"$gnupg/passphrase"
gpg_batch=(gpg --homedir "$gnupg" --batch --pinentry-mode loopback --passphrase-file "$gnupg/passphrase")
"${gpg_batch[@]}" --quick-gen-key "openbao-config-operator dry-run <dryrun@example.invalid>" rsa4096 sign 1d >/dev/null 2>&1
"${gpg_batch[@]}" --export-secret-keys >"$out/keys/dryrun-helm.key"
"${gpg_batch[@]}" --armor --export >"$out/keys/dryrun-helm.asc"
tgz=$(GNUPGHOME=$gnupg SIGN_KEYRING=$out/keys/dryrun-helm.key SIGN_PASSPHRASE_FILE=$gnupg/passphrase \
  SIGN_KEY_URL=https://docs.obco.dev/charts/pgp-public-key.asc \
  "$repo_root/hack/release/package-chart.sh" "$tag" "$image" "$index_digest" "$out/dist")
"$HELM" verify --keyring "$out/keys/dryrun-helm.asc" "$tgz" >/dev/null || { echo "dry-run: helm verify failed for $tgz" >&2; exit 1; }
fingerprint=$("${gpg_batch[@]}" --with-colons --show-keys "$out/keys/dryrun-helm.asc" | awk -F: '$1 == "fpr" {print $10; exit}')
sign_key=$("$HELM" show chart "$tgz" | "$YQ" '.annotations["artifacthub.io/signKey"] | from_yaml | .fingerprint')
[ "$sign_key" = "$fingerprint" ] || { echo "dry-run: artifacthub.io/signKey names $sign_key, not $fingerprint" >&2; exit 1; }
echo "ok $(basename "$tgz").prov, signKey $fingerprint"
"$repo_root/hack/release/chart-sbom.sh" "$tgz" "$out/dist/chart.cdx.json" "$out/dist/chart.spdx.json"
pinned=$("$HELM" show values "$tgz" | "$YQ" '.image.digest')
[ "$pinned" = "$index_digest" ] || { echo "dry-run: chart pins $pinned, not $index_digest" >&2; exit 1; }

echo "== chart push by digest, into an OCI layout"
chart_digest=$(ORAS_FLAGS=--oci-layout "$repo_root/hack/release/push-chart.sh" "$tgz" "$out/chart-oci" 2>/dev/null)
jq -e --arg layer "sha256:$(shasum -a 256 "$tgz" | awk '{print $1}')" \
  --arg prov "sha256:$(shasum -a 256 "$tgz.prov" | awk '{print $1}')" '.config.mediaType == "application/vnd.cncf.helm.config.v1+json"
  and ([.layers[] | {digest, mediaType}] | sort_by(.mediaType)) == [
    {digest: $layer, mediaType: "application/vnd.cncf.helm.chart.content.v1.tar+gzip"},
    {digest: $prov, mediaType: "application/vnd.cncf.helm.chart.provenance.v1.prov"}]' \
  "$out/chart-oci/blobs/sha256/${chart_digest#sha256:}" >/dev/null ||
  { echo "dry-run: the chart manifest $chart_digest is not a signed helm chart of $tgz" >&2; exit 1; }
echo "chart $chart_digest"

echo "== install.yaml and crds.yaml"
"$repo_root/hack/release/installer.sh" "$image@$index_digest" "$out/dist"
grep -q "image: $image@$index_digest" "$out/dist/install.yaml" || { echo "dry-run: install.yaml lacks the digest" >&2; exit 1; }

echo "== examples bundle"
examples=$("$repo_root/hack/release/examples-bundle.sh" "$tag" "$out/dist")
rebuilt=$("$repo_root/hack/release/examples-bundle.sh" "$tag" "$out/examples-rebuild")
cmp -s "$examples" "$rebuilt" || { echo "dry-run: $(basename "$examples") is not reproducible" >&2; exit 1; }
rm -rf -- "$out/examples-rebuild"
listing=$(tar -tzf "$examples")
for want in examples/core/baoserver.yaml examples-generated/core/baoserver.yaml; do
  grep -qxF "$want" <<<"$listing" || { echo "dry-run: $(basename "$examples") lacks $want" >&2; exit 1; }
done
if grep -vE '^examples(-generated)?/[^./][^/]*(/[^./][^/]*)*$' <<<"$listing"; then
  echo "dry-run: $(basename "$examples") holds files outside examples/ and examples-generated/" >&2
  exit 1
fi
echo "$(basename "$examples"): $(wc -l <<<"$listing" | tr -d ' ') files"

echo "== sign and attest with a throwaway key"
COSIGN_PASSWORD="" "$cosign" generate-key-pair --output-key-prefix "$out/keys/dryrun" >/dev/null 2>&1
# Both flags are needed for a fully offline cosign v3 run; either alone contacts TUF or uploads to public Rekor.
offline=(--key "$out/keys/dryrun.key" --use-signing-config=false --tlog-upload=false --yes)
checks=()

sign() {
  local name=$1 subject=$2
  COSIGN_PASSWORD="" "$cosign" sign-blob "${offline[@]}" --bundle "$out/sig/$name.sig.json" "$subject" >/dev/null 2>&1
  checks+=("sig|-|$out/sig/$name.sig.json|$subject")
}

attest() {
  local name=$1 type=$2 predicate=$3 subject=$4
  COSIGN_PASSWORD="" "$cosign" attest-blob "${offline[@]}" --type "$type" --predicate "$predicate" \
    --bundle "$out/sig/$name.$type.json" "$subject" >/dev/null 2>&1
  checks+=("att|$type|$out/sig/$name.$type.json|$subject")
}

index_blob=$(blob "$index_digest")
sign image-index "$index_blob"
attest image-index slsaprovenance1 "$out/provenance.json" "$index_blob"
while read -r platform digest; do
  arch=${platform#*/}
  sign "image-$arch" "$(blob "$digest")"
  attest "image-$arch" spdxjson "$out/dist/sbom-$arch.spdx.json" "$(blob "$digest")"
  attest "image-$arch" cyclonedx "$out/dist/sbom-$arch.cdx.json" "$(blob "$digest")"
  attest "image-$arch" slsaprovenance1 "$out/provenance.json" "$(blob "$digest")"
  attest "image-index-$arch" spdxjson "$out/dist/sbom-$arch.spdx.json" "$index_blob"
  attest "image-index-$arch" cyclonedx "$out/dist/sbom-$arch.cdx.json" "$index_blob"
done <<<"$platform_digests"
if [ "$(jq '.statements | length' "$repo_root/hack/release/vex.openvex.json")" -gt 0 ]; then
  attest image-index openvex "$repo_root/hack/release/vex.openvex.json" "$index_blob"
fi
sign chart "$tgz"
attest chart spdxjson "$out/dist/chart.spdx.json" "$tgz"
attest chart cyclonedx "$out/dist/chart.cdx.json" "$tgz"
attest chart slsaprovenance1 "$out/provenance.json" "$tgz"

(cd "$out/dist" && shasum -a 256 -- * >sha256sums.txt)
COSIGN_PASSWORD="" "$cosign" sign-blob "${offline[@]}" --bundle "$out/dist/sha256sums.txt.sigstore.json" \
  "$out/dist/sha256sums.txt" >/dev/null 2>&1
checks+=("sig|-|$out/dist/sha256sums.txt.sigstore.json|$out/dist/sha256sums.txt")

echo "== verify"
for entry in "${checks[@]}"; do
  IFS='|' read -r kind type bundle subject <<<"$entry"
  if [ "$kind" = sig ]; then
    "$cosign" verify-blob --key "$out/keys/dryrun.pub" --insecure-ignore-tlog=true --bundle "$bundle" "$subject" >/dev/null 2>&1
  else
    "$cosign" verify-blob-attestation --key "$out/keys/dryrun.pub" --insecure-ignore-tlog=true --type "$type" \
      --bundle "$bundle" "$subject" >/dev/null 2>&1
  fi || { echo "dry-run: verify failed for $(basename "$bundle")" >&2; exit 1; }
  echo "ok $(basename "$bundle")"
done
(cd "$out/dist" && shasum -a 256 -c sha256sums.txt >/dev/null)
echo "ok release assets match sha256sums.txt"

TAG=$tag IMAGE=$image IMAGE_DIGEST=$index_digest PLATFORM_DIGESTS=$platform_digests CHART=$chart \
  CHART_DIGEST=$chart_digest \
  REPOSITORY=${GITHUB_REPOSITORY:-alexandrust88/openbao-config-operator} \
  "$repo_root/hack/release/notes.sh" >"$out/release-notes.md"

echo "== ocm component"
OUT=$out RELEASE_TAG=$tag COSIGN=$cosign "$repo_root/hack/ocm/dry-run.sh"

echo "== done: ${#checks[@]} signatures and attestations verified, outputs in $out"
