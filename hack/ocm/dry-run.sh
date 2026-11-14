#!/usr/bin/env bash
# Proves the OCM release step offline, on the outputs of hack/release/dry-run.sh: pushes the image and the chart by
# digest into a local registry and signs both with that run's throwaway key, builds and pushes the component version
# (hack/ocm/component.sh), signs and attests its descriptor, verifies it (hack/ocm/verify.sh), then transfers it with
# --copy-resources into a CTF directory and from there into a second local registry, as an air-gapped mirror would,
# and checks that every transferred resource keeps its digest.
# Usage: hack/ocm/dry-run.sh   (or make ocm-dry-run; make release-dry-run runs it last)
# Environment: OUT (the release dry-run outputs, default .tmp/release-dryrun), RELEASE_TAG, OCM, CRANE, ORAS, COSIGN,
# YQ.
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
out=${OUT:-$repo_root/.tmp/release-dryrun}
tag=${RELEASE_TAG:-v0.0.0-dryrun}
export OCM=${OCM:-ocm} CRANE=${CRANE:-crane} COSIGN=${COSIGN:-cosign} YQ=${YQ:-yq}
oras=${ORAS:-oras}

for tool in "$OCM" "$CRANE" "$COSIGN" "$YQ" "$oras" jq; do
  command -v "$tool" >/dev/null || { echo "ocm dry-run: $tool not found" >&2; exit 1; }
done
for path in oci/index.json chart-oci/index.json dist/install.yaml keys/dryrun.key provenance.json; do
  [ -e "$out/$path" ] || { echo "ocm dry-run: $out/$path not found; run make release-dry-run first" >&2; exit 1; }
done

work=$out/ocm
rm -rf -- "$work"
mkdir -p "$work"
# The registries and the transfer archive hold image copies; they live outside $OUT and go when the run ends.
scratch=$(mktemp -d)
pids=()
trap 'kill "${pids[@]}" 2>/dev/null || true; rm -rf -- "$scratch"' EXIT
mkdir -p "$scratch/docker" "$scratch/source-registry" "$scratch/mirror-registry"
# An empty docker config keeps the local tools away from the host's credential helpers.
export DOCKER_CONFIG=$scratch/docker

for name in source-registry mirror-registry; do
  "$CRANE" registry serve --address 127.0.0.1:0 --disk "$scratch/$name" >/dev/null 2>"$work/$name.log" &
  pids+=($!)
done

# address prints the host:port a local registry listens on, once it serves.
address() {
  local port
  for _ in $(seq 50); do
    port=$(sed -n 's/.*serving on port \([0-9]*\).*/\1/p' "$work/$1.log")
    [ -n "$port" ] && { echo "127.0.0.1:$port"; return; }
    sleep 0.1
  done
  echo "ocm dry-run: the $1 did not start" >&2
  return 1
}
source_registry=$(address source-registry)
mirror_registry=$(address mirror-registry)

image=http://$source_registry/alexandrust88/openbao-config-operator
chart=http://$source_registry/alexandrust88/charts/openbao-config-operator
ocm_repo=http://$source_registry/ocm
image_digest=$(jq -r '.manifests[0].digest' "$out/oci/index.json")
chart_digest=$(jq -r '.manifests[0].digest' "$out/chart-oci/index.json")
commit=$(git -C "$repo_root" rev-parse --verify -q HEAD || printf '0%.0s' {1..40})

offline=(--key "$out/keys/dryrun.key" --use-signing-config=false --tlog-upload=false --yes)

echo "== push the image and the chart by digest into $source_registry, sign both"
"$oras" cp -r --from-oci-layout "$out/oci@$image_digest" --to-plain-http "${image#http://}:$tag" >/dev/null
"$oras" cp --from-oci-layout "$out/chart-oci@$chart_digest" --to-plain-http "${chart#http://}:${tag#v}" >/dev/null
for ref in "${image#http://}@$image_digest" "${chart#http://}@$chart_digest"; do
  COSIGN_PASSWORD="" "$COSIGN" sign "${offline[@]}" "$ref" >/dev/null 2>&1
done

echo "== build and push the component version"
descriptor=$(VERSION=${tag#v} IMAGE=$image IMAGE_DIGEST=$image_digest CHART=$chart CHART_DIGEST=$chart_digest \
  COMMIT=$commit DIST=$out/dist OCM_REPO=$ocm_repo "$repo_root/hack/ocm/component.sh" "$work/ctf" 2>"$work/component.log")
name=$(awk '$1 == "name:" {print $2; exit}' "$repo_root/release/component-constructor.yaml")
echo "descriptor $descriptor"

echo "== a second push of the same version is refused"
if VERSION=${tag#v} IMAGE=$image IMAGE_DIGEST=$image_digest CHART=$chart CHART_DIGEST=$chart_digest COMMIT=$commit \
  DIST=$out/dist OCM_REPO=$ocm_repo "$repo_root/hack/ocm/component.sh" "$work/ctf-again" >/dev/null 2>"$work/again.log"; then
  echo "ocm dry-run: component.sh overwrote $name:${tag#v}" >&2
  exit 1
fi
grep -q 'already exists' "$work/again.log" || { echo "ocm dry-run: the second push failed otherwise:" >&2; cat "$work/again.log" >&2; exit 1; }
echo ok

echo "== sign and attest the component descriptor"
COSIGN_PASSWORD="" "$COSIGN" sign "${offline[@]}" "$descriptor" >/dev/null 2>&1
COSIGN_PASSWORD="" "$COSIGN" attest "${offline[@]}" --type slsaprovenance1 --predicate "$out/provenance.json" \
  "$descriptor" >/dev/null 2>&1

echo "== verify"
OCM_REPO=$ocm_repo IMAGE=$image CHART=$chart IMAGE_DIGEST=$image_digest CHART_DIGEST=$chart_digest \
  ASSETS_DIR=$out/dist COSIGN_KEY=$out/keys/dryrun.pub "$repo_root/hack/ocm/verify.sh" "$tag"

echo "== transfer with --copy-resources: $source_registry -> CTF -> $mirror_registry"
"$OCM" transfer componentversions --copy-resources "$ocm_repo//$name:${tag#v}" "$scratch/airgap-ctf" >"$work/transfer.log" 2>&1
"$OCM" transfer ctf "$scratch/airgap-ctf" "http://$mirror_registry/mirror" >>"$work/transfer.log" 2>&1
"$OCM" verify componentversions "http://$mirror_registry/mirror//$name:${tag#v}" >>"$work/transfer.log" 2>&1

# digests prints "<resource> sha256:<digest> <access reference>" for every resource of the component in an OCM repo.
digests() {
  "$OCM" get componentversions "$1//$name:${tag#v}" -o json 2>/dev/null | jq -r '.items[0].component.resources[]
    | "\(.name) sha256:\(.digest.value) \(.access.imageReference // .access.localReference)"'
}
source_digests=$(digests "$ocm_repo" | cut -d' ' -f1,2)
mirrored=$(digests "http://$mirror_registry/mirror")
[ "$(cut -d' ' -f1,2 <<<"$mirrored")" = "$source_digests" ] ||
  { echo "ocm dry-run: the mirrored resource digests differ from the source" >&2; exit 1; }
while read -r resource_name digest ref; do
  if [[ $ref == *@sha256:* ]] && [ "$("$CRANE" digest "${ref#http://}")" != "$digest" ]; then
    echo "ocm dry-run: $ref in the mirror is not $digest" >&2
    exit 1
  fi
  echo "ok $resource_name $digest $ref"
done <<<"$mirrored"

echo "== done: component $name:${tag#v} built, signed, verified and transferred; outputs in $work"
