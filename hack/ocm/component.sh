#!/usr/bin/env bash
# Builds the OCM component version of a release into a CTF directory from release/component-constructor.yaml, with
# the image and the chart referenced by the digests the release pushed, and checks the result: every resource digest
# matches its input and ocm re-digests every resource. With OCM_REPO it then pushes the component version there,
# refusing to overwrite an existing one, and prints the component descriptor manifest reference, pinned by digest,
# for signing.
# Usage: hack/ocm/component.sh <ctf-dir>
# Environment: VERSION (X.Y.Z[-suffix]), IMAGE, IMAGE_DIGEST, CHART, CHART_DIGEST, COMMIT, DIST (the directory with
# install.yaml, crds.yaml, *.spdx.json and *.cdx.json), REPO_URL, OCM_REPO (e.g. ghcr.io/<owner>/ocm), OCM, CRANE.
set -euo pipefail

[ $# -eq 1 ] || { echo "usage: $0 <ctf-dir>" >&2; exit 2; }
ctf=$1
repo_root=$(cd "$(dirname "$0")/../.." && pwd)
ocm=${OCM:-ocm}
crane=${CRANE:-crane}
repo_url=${REPO_URL:-https://github.com/alexandrust88/openbao-config-operator}
dist=${DIST:-}

die() { echo "component: $*" >&2; exit 1; }
sha256() { shasum -a 256 "$1" | awk '{print $1}'; }

digest_re='^sha256:[a-f0-9]{64}$'
[[ ${VERSION:-} =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || die "VERSION must be X.Y.Z[-suffix], got '${VERSION:-}'"
[[ ${IMAGE_DIGEST:-} =~ $digest_re ]] || die "IMAGE_DIGEST must be sha256:<hex>, got '${IMAGE_DIGEST:-}'"
[[ ${CHART_DIGEST:-} =~ $digest_re ]] || die "CHART_DIGEST must be sha256:<hex>, got '${CHART_DIGEST:-}'"
[[ ${COMMIT:-} =~ ^[0-9a-f]{40}$ ]] || die "COMMIT must be a full git commit sha, got '${COMMIT:-}'"
[[ -n ${IMAGE:-} && -n ${CHART:-} ]] || die "IMAGE and CHART must be set"
[[ $IMAGE != *@* && $CHART != *@* ]] || die "IMAGE and CHART are repositories, without a digest"
for file in install.yaml crds.yaml; do [ -f "$dist/$file" ] || die "$dist/$file not found"; done
for pattern in '*.spdx.json' '*.cdx.json'; do
  compgen -G "$dist/$pattern" >/dev/null || die "$dist has no $pattern SBOM"
done
# ocm resolves input paths against the constructor's directory, not the working directory.
dist=$(cd "$dist" && pwd)

# The subst templater leaves an unset variable empty; every value is passed explicitly and checked above.
"$ocm" add componentversions --create --force --file "$ctf" "$repo_root/release/component-constructor.yaml" \
  VERSION="$VERSION" COMMIT="$COMMIT" REPO_URL="$repo_url" DIST="$dist" \
  IMAGE="$IMAGE" IMAGE_DIGEST="$IMAGE_DIGEST" CHART="$CHART" CHART_DIGEST="$CHART_DIGEST" >&2

descriptors=$("$ocm" get componentversions --repo "$ctf" -o json)
[ "$(jq '.items | length' <<<"$descriptors")" -eq 1 ] || die "$ctf must hold exactly one component version"
name=$(jq -r '.items[0].component.name' <<<"$descriptors")

jq -e --arg version "$VERSION" --arg image "$IMAGE@$IMAGE_DIGEST" --arg chart "$CHART@$CHART_DIGEST" \
  --arg install "$(sha256 "$dist/install.yaml")" --arg crds "$(sha256 "$dist/crds.yaml")" '
  .items[0].component
  | def res($name): [.resources[] | select(.name == $name)] | if length == 1 then .[0] else error("no single \($name)") end;
    def oci($name; $ref): res($name) | .access.type == "ociArtifact" and .access.imageReference == $ref
      and .digest.normalisationAlgorithm == "ociArtifactDigest/v1" and "sha256:" + .digest.value == ($ref | split("@")[1]);
  .version == $version
  and oci("operator-image"; $image) and oci("helm-chart"; $chart)
  and res("install-manifest").digest.value == $install and res("crds").digest.value == $crds
  and all(.resources[]; .digest.hashAlgorithm == "SHA-256" and (.digest.value | test("^[a-f0-9]{64}$")))
' <<<"$descriptors" >/dev/null || die "the component descriptor does not reference the expected digests"
"$ocm" verify componentversions --repo "$ctf" "$name:$VERSION" >&2

[ -n "${OCM_REPO:-}" ] || exit 0
# ocm reaches a plain-http registry (the dry-run) only through an http:// prefix, which crane does not take.
ref="${OCM_REPO#http://}/component-descriptors/$name:$VERSION"
if out=$("$crane" digest "$ref" 2>&1); then
  die "$ref already exists ($out); component versions are immutable, tag a new version"
elif ! grep -qE 'MANIFEST_UNKNOWN|NAME_UNKNOWN|DENIED|404' <<<"$out"; then
  die "cannot check whether $ref exists: $out"
fi
"$ocm" transfer ctf "$ctf" "$OCM_REPO" >&2
echo "${ref%:"$VERSION"}@$("$crane" digest "$ref")"
