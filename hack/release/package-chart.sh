#!/usr/bin/env bash
# Packages the chart for a release from a copy of charts/openbao-config-operator, so the tree stays untouched: sets
# version and appVersion from the tag, pins the image digest, and writes the Artifact Hub annotations (images,
# prerelease, changes from the conventional commits since the previous tag). With SIGN_KEYRING it also signs the package
# into <tgz>.prov (Helm provenance) and sets artifacthub.io/signKey to the key's fingerprint and SIGN_KEY_URL.
# Usage: hack/release/package-chart.sh <vX.Y.Z> <image-repository> <image-digest> <out-dir>
# Environment: HELM, YQ; SIGN_KEYRING (a legacy GnuPG secret keyring holding exactly one key), SIGN_PASSPHRASE_FILE,
# SIGN_KEY_URL (where the armored public key is published); signing needs gpg.
set -euo pipefail

[ $# -eq 4 ] || { echo "usage: $0 <vX.Y.Z> <image-repository> <image-digest> <out-dir>" >&2; exit 2; }
tag=$1 image=$2 digest=$3 out=$4
version=${tag#v}

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
"$repo_root/hack/release/check-version.sh" "$tag" || exit 2
[[ $digest =~ ^sha256:[a-f0-9]{64}$ ]] || { echo "package-chart: bad digest $digest" >&2; exit 2; }

helm=${HELM:-helm}
yq=${YQ:-yq}

sign=()
if [ -n "${SIGN_KEYRING:-}" ]; then
  : "${SIGN_PASSPHRASE_FILE:?SIGN_KEYRING needs SIGN_PASSPHRASE_FILE}" "${SIGN_KEY_URL:?SIGN_KEYRING needs SIGN_KEY_URL}"
  keys=$(gpg --batch --quiet --no-auto-check-trustdb --with-colons --show-keys "$SIGN_KEYRING")
  fingerprint=$(awk -F: '$1 == "sec" {sec = 1; next} sec && $1 == "fpr" {print $10; sec = 0}' <<<"$keys")
  uid=$(awk -F: '$1 == "uid" {print $10; exit}' <<<"$keys")
  [[ $fingerprint =~ ^[A-F0-9]{40}$ && -n $uid ]] ||
    { echo "package-chart: $SIGN_KEYRING must hold exactly one secret key" >&2; exit 2; }
  # helm --key matches the user ID, not the fingerprint.
  sign=(--sign --key "$uid" --keyring "$SIGN_KEYRING" --passphrase-file "$SIGN_PASSPHRASE_FILE")
fi

work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
cp -R "$repo_root/charts/openbao-config-operator" "$work/"
chart=$work/openbao-config-operator

prerelease=false
[[ $version == *-* ]] && prerelease=true

prev=$(git -C "$repo_root" describe --tags --abbrev=0 --match 'v*' "$tag^" 2>/dev/null || true)
range=${prev:+$prev..}${tag}
git -C "$repo_root" rev-parse -q --verify "$tag^{commit}" >/dev/null 2>&1 || range=${prev:+$prev..}HEAD
changes=$({ git -C "$repo_root" log --no-merges --format=%s "$range" 2>/dev/null || true; } | jq -Rn '
  [inputs | capture("^(?<type>[a-z]+)(\\([^)]*\\))?!?: (?<description>.+)$")?
    | {kind: {feat: "added", fix: "fixed", perf: "changed", security: "security", revert: "removed"}[.type],
       description}
    | select(.kind)] | .[:30]')
[ "$changes" != "[]" ] && [ -n "$changes" ] || changes=$(jq -n --arg tag "$tag" '[{kind: "changed", description: ("Release " + $tag)}]')

images=$(jq -n --arg ref "$image@$digest" '[{name: "manager", image: $ref, platforms: ["linux/amd64", "linux/arm64"]}]')

# yaml_stream concatenates YAML files into one multi-document stream.
yaml_stream() { find "$@" -name '*.yaml' -exec awk 'FNR == 1 {print "---"} {print}' {} +; }
# shellcheck disable=SC2016 # yq variables, not shell
crds=$(yaml_stream "$repo_root/config/crd/bases" | "$yq" ea -o json -I0 '[select(.kind == "CustomResourceDefinition")
  | ([.spec.versions[] | select(.storage)][0]) as $stored
  | {"kind": .spec.names.kind, "version": $stored.name, "name": .metadata.name, "displayName": .spec.names.kind,
     "description": ($stored.schema.openAPIV3Schema.description // .spec.names.kind)}]
  | sort_by(.name)' | jq -c 'map(.description |= (sub("(?s)\\.\\s.*"; ".") | if length > 120 then .[:117] + "..." else . end))')
# config/crd/kustomization.yaml lists the built-in CRDs as bases/<group>_<plural>.yaml.
builtin=$("$yq" -o json -I0 '[.resources[] | sub("^bases/"; "") | sub("\.yaml$"; "") | split("_") | .[1] + "." + .[0]]' \
  "$repo_root/config/crd/kustomization.yaml")
# The smallest example of each built-in kind from examples/<group> and the minimal live components; plugin kinds get
# none, which keeps Chart.yaml (copied into every index.yaml entry and OCI annotation) small.
examples=$(yaml_stream "$repo_root/examples" -not \( -path "$repo_root/examples/live/*" \
  -not -path "$repo_root/examples/live/components/*/minimal/*" \) |
  "$yq" ea -o json -I0 '[select(.apiVersion | test("\.openbaoconfig\.io/")) | del(.status, .metadata.annotations, .metadata.namespace)]')
crds_examples=$(jq -c --argjson crds "$crds" --argjson builtin "$builtin" '
  [$crds[] | select(.name as $name | $builtin | index($name)) | .kind] as $kinds
  | [.[] | select(.kind as $kind | $kinds | index($kind))] | group_by(.kind) | map(min_by(tojson | length))' <<<"$examples")

# Deep Insights: metrics, a ServiceMonitor, PrometheusRule alerts and drift detection; nothing auto-tunes (Auto Pilot).
VERSION=$version IMAGES=$images CHANGES=$changes PRERELEASE=$prerelease CRDS=$crds CRDS_EXAMPLES=$crds_examples "$yq" -i '
  .version = strenv(VERSION) |
  .appVersion = strenv(VERSION) |
  .annotations["artifacthub.io/images"] = (strenv(IMAGES) | from_yaml | ... style="" | to_yaml) |
  .annotations["artifacthub.io/changes"] = (strenv(CHANGES) | from_yaml | ... style="" | to_yaml) |
  .annotations["artifacthub.io/prerelease"] = strenv(PRERELEASE) |
  .annotations["artifacthub.io/crds"] = (strenv(CRDS) | from_yaml | ... style="" | to_yaml) |
  .annotations["artifacthub.io/crdsExamples"] = (strenv(CRDS_EXAMPLES) | from_yaml | ... style="" | to_yaml) |
  .annotations["artifacthub.io/operatorCapabilities"] = "Deep Insights"
' "$chart/Chart.yaml"
if [ -n "${fingerprint:-}" ]; then
  FINGERPRINT=$fingerprint KEY_URL=$SIGN_KEY_URL "$yq" -i '.annotations["artifacthub.io/signKey"] =
    ({"fingerprint": strenv(FINGERPRINT), "url": strenv(KEY_URL)} | ... style="" | to_yaml)' "$chart/Chart.yaml"
fi
chart_bytes=$(wc -c <"$chart/Chart.yaml" | tr -d ' ')
[ "$chart_bytes" -lt 65536 ] || { echo "package-chart: Chart.yaml is $chart_bytes bytes, limit 64 KiB" >&2; exit 1; }
IMAGE=$image DIGEST=$digest "$yq" -i '.image.repository = strenv(IMAGE) | .image.digest = strenv(DIGEST)' "$chart/values.yaml"

"$helm" lint --strict "$chart" >/dev/null
mkdir -p "$out"
"$helm" package "$chart" --destination "$out" ${sign[@]+"${sign[@]}"} >/dev/null
echo "$out/openbao-config-operator-$version.tgz"
