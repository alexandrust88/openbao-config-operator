#!/usr/bin/env bash
# Pushes a packaged chart by digest only, in the layout helm push writes (helm config, chart layer, provenance layer when
# <chart.tgz>.prov exists, OCI annotations from Chart.yaml), so it can be signed and attested before a version tag
# points at it. Prints the manifest digest.
# Usage: hack/release/push-chart.sh <chart.tgz> <oci-repository>   (no tag; or an OCI layout dir with ORAS_FLAGS)
# Environment: HELM, YQ, ORAS, ORAS_FLAGS (e.g. --oci-layout or --plain-http), SOURCE_DATE_EPOCH.
set -euo pipefail

[ $# -eq 2 ] || { echo "usage: $0 <chart.tgz> <oci-repository>" >&2; exit 2; }
tgz=$1 repo=$2
helm=${HELM:-helm}
yq=${YQ:-yq}
oras=$(command -v "${ORAS:-oras}") || { echo "push-chart: oras not found" >&2; exit 1; }
[[ $oras == /* ]] || oras=$PWD/$oras
read -r -a oras_flags <<<"${ORAS_FLAGS:-}"

epoch=${SOURCE_DATE_EPOCH:-$(date +%s)}
created=$(date -u -r "$epoch" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "@$epoch" +%Y-%m-%dT%H:%M:%SZ)

work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
cp "$tgz" "$work/chart.tgz"
layers=(chart.tgz:application/vnd.cncf.helm.chart.content.v1.tar+gzip)
if [ -f "$tgz.prov" ]; then
  cp "$tgz.prov" "$work/chart.tgz.prov"
  layers+=(chart.tgz.prov:application/vnd.cncf.helm.chart.provenance.v1.prov)
fi
"$helm" show chart "$tgz" | "$yq" -o json '(.. | select(tag == "!!int" or tag == "!!float")) |= to_string' >"$work/config.json"

# helm push semantics: chart annotations override the generated ones, except title and version.
jq --arg created "$created" '{"$manifest": ((
  {
    "org.opencontainers.image.description": .description,
    "org.opencontainers.image.title": .name,
    "org.opencontainers.image.version": .version,
    "org.opencontainers.image.url": .home,
    "org.opencontainers.image.created": $created,
    "org.opencontainers.image.source": (.sources // [])[0],
    "org.opencontainers.image.authors":
      ([.maintainers[]? | (.name // "") + (if .email then " (\(.email))" else "" end)] | join(", "))
  }
  | with_entries(select(.value != null and (.value | test("\\S"))))
  ) + (.annotations // {} | del(."org.opencontainers.image.title", ."org.opencontainers.image.version")))
}' "$work/config.json" >"$work/annotations.json"

(cd "$work" && "$oras" push ${oras_flags[@]+"${oras_flags[@]}"} "$repo" --export-manifest manifest.json \
  --config config.json:application/vnd.cncf.helm.config.v1+json --annotation-file annotations.json \
  "${layers[@]}" >&2)
echo "sha256:$(shasum -a 256 "$work/manifest.json" | awk '{print $1}')"
