#!/usr/bin/env bash
# Builds the classic Helm repository index for GitHub Pages from the release assets laid out as
# <assets-dir>/<tag>/{openbao-config-operator-<version>.tgz,sha256sums.txt,sha256sums.txt.sigstore.json}. Only release
# tags are indexed, and only after the chart matches that release's sha256sums.txt, signed by the release workflow run
# of exactly that tag; a failed check or a duplicate version fails the build. Each entry points at its release
# download URL, so Pages serves only index.yaml and the packages stay release assets.
# Usage: hack/docs/chart-index.sh <assets-dir> <out-dir> <download-base-url>
# Environment: REPOSITORY (owner/name, for the signer identity), HELM, YQ, COSIGN.
set -euo pipefail

[ $# -eq 3 ] || { echo "usage: $0 <assets-dir> <out-dir> <download-base-url>" >&2; exit 2; }
assets=$1 out=$2 base=${3%/}
repo_root=$(cd "$(dirname "$0")/../.." && pwd)
repository=${REPOSITORY:-alexandrust88/openbao-config-operator}
helm=${HELM:-helm}
yq=${YQ:-yq}
cosign=${COSIGN:-cosign}

die() { echo "chart-index: $*" >&2; exit 1; }

mkdir -p "$out"
index=$out/index.yaml
rm -f -- "$index"
"$helm" repo index "$out"
found=0
seen=" "
for dir in "$assets"/*/; do
  [ -d "$dir" ] || continue
  tag=$(basename "$dir")
  if ! "$repo_root/hack/release/check-version.sh" "$tag" 2>/dev/null; then
    echo "chart-index: skipped $tag: not a release tag"
    continue
  fi
  version=${tag#v}
  tgz=openbao-config-operator-$version.tgz
  extra=$(find "$dir" -mindepth 1 ! -name "$tgz" ! -name sha256sums.txt ! -name sha256sums.txt.sigstore.json)
  [ -z "$extra" ] || die "$tag has unexpected files: $extra"
  [ -f "$dir/$tgz" ] || die "$tag has no $tgz"
  [[ $seen != *" $version "* ]] || die "duplicate chart version $version"

  "$cosign" verify-blob --bundle "$dir/sha256sums.txt.sigstore.json" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-identity "https://github.com/$repository/.github/workflows/release.yml@refs/tags/$tag" \
    --certificate-github-workflow-trigger push \
    --certificate-github-workflow-repository "$repository" \
    --certificate-github-workflow-ref "refs/tags/$tag" \
    "$dir/sha256sums.txt" >/dev/null 2>&1 || die "$tag: sha256sums.txt is not signed by the $tag release run"
  line=$(grep -E "^[a-f0-9]{64}  $tgz\$" "$dir/sha256sums.txt") || die "$tag: sha256sums.txt has no $tgz"
  (cd "$dir" && shasum -a 256 -c - <<<"$line" >/dev/null) || die "$tag: $tgz does not match sha256sums.txt"
  [ "$("$helm" show chart "$dir/$tgz" | "$yq" '.version')" = "$version" ] || die "$tag: $tgz is not chart version $version"

  "$helm" repo index "$dir" --url "$base/$tag" --merge "$index"
  mv -- "$dir/index.yaml" "$index"
  seen="$seen$version "
  found=$((found + 1))
done

echo "chart-index: indexed $found release(s) into $index"
