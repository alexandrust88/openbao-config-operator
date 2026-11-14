#!/usr/bin/env bash
# Writes a CycloneDX 1.6 and an SPDX 2.3 SBOM for a packaged chart: the chart (with the sha256 of its .tgz) and every
# container image its default render runs, as dependencies. The chart holds no binaries, so this listing is its bill
# of materials.
# Usage: hack/release/chart-sbom.sh <chart.tgz> <out.cdx.json> <out.spdx.json>
set -euo pipefail

[ $# -eq 3 ] || { echo "usage: $0 <chart.tgz> <out.cdx.json> <out.spdx.json>" >&2; exit 2; }
tgz=$1 out=$2 out_spdx=$3
helm=${HELM:-helm}
yq=${YQ:-yq}

name=$("$helm" show chart "$tgz" | "$yq" '.name')
version=$("$helm" show chart "$tgz" | "$yq" '.version')
sha=$(shasum -a 256 "$tgz" | awk '{print $1}')
epoch=${SOURCE_DATE_EPOCH:-$(date +%s)}
timestamp=$(date -u -r "$epoch" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "@$epoch" +%Y-%m-%dT%H:%M:%SZ)

images=$("$helm" template "$name" "$tgz" --namespace "$name" |
  "$yq" -N '.. | select(has("containers") or has("initContainers")) | (.containers // []) + (.initContainers // []) | .[].image' |
  sort -u)
[ -n "$images" ] || { echo "chart-sbom: the chart renders no images" >&2; exit 1; }

boms=$(jq -n \
  --arg name "$name" --arg version "$version" --arg sha "$sha" --arg timestamp "$timestamp" --arg images "$images" '
  def purl: capture("^(?<repo>[^@:]+(:[0-9]+)?/[^@:]+)(:(?<tag>[^@]+))?(@(?<digest>sha256:[a-f0-9]{64}))?$")
    | (.repo | split("/")) as $p
    | "pkg:oci/\($p[-1])@\((.digest // .tag) | @uri)?repository_url=\(.repo | @uri)" + (if .digest and .tag then "&tag=\(.tag)" else "" end);
  ("pkg:helm/\($name)@\($version)") as $chart
  | ($images | split("\n") | map(select(length > 0)
      | {ref: ., purl: purl, name: sub("[:@].*$"; ""), version: (capture("@(?<d>sha256:.*)$").d // sub("^.*:"; ""))})) as $imgs
  | {
    cdx: {
      bomFormat: "CycloneDX",
      specVersion: "1.6",
      version: 1,
      metadata: {
        timestamp: $timestamp,
        tools: {components: [{type: "application", name: "hack/release/chart-sbom.sh"}]},
        component: {
          type: "application", "bom-ref": $chart, name: $name, version: $version, purl: $chart,
          hashes: [{alg: "SHA-256", content: $sha}]
        }
      },
      components: [$imgs[] | {type: "container", "bom-ref": .purl, name, version, purl}],
      dependencies: [{ref: $chart, dependsOn: [$imgs[].purl]}]
    },
    spdx: {
      spdxVersion: "SPDX-2.3",
      dataLicense: "CC0-1.0",
      SPDXID: "SPDXRef-DOCUMENT",
      name: "\($name)-\($version)",
      documentNamespace: "https://spdx.org/spdxdocs/\($name)-\($version)-\($sha)",
      creationInfo: {created: $timestamp, creators: ["Tool: hack/release/chart-sbom.sh"]},
      packages: (
        [{
          SPDXID: "SPDXRef-Package-chart", name: $name, versionInfo: $version, downloadLocation: "NOASSERTION",
          filesAnalyzed: false, primaryPackagePurpose: "APPLICATION",
          checksums: [{algorithm: "SHA256", checksumValue: $sha}],
          externalRefs: [{referenceCategory: "PACKAGE-MANAGER", referenceType: "purl", referenceLocator: $chart}]
        }] +
        [$imgs | to_entries[] | {
          SPDXID: "SPDXRef-Package-image-\(.key)", name: .value.name, versionInfo: .value.version,
          downloadLocation: "NOASSERTION", filesAnalyzed: false, primaryPackagePurpose: "CONTAINER",
          externalRefs: [{referenceCategory: "PACKAGE-MANAGER", referenceType: "purl", referenceLocator: .value.purl}]
        }]
      ),
      relationships: (
        [{spdxElementId: "SPDXRef-DOCUMENT", relationshipType: "DESCRIBES", relatedSpdxElement: "SPDXRef-Package-chart"}] +
        [range($imgs | length) | {spdxElementId: "SPDXRef-Package-chart", relationshipType: "DEPENDS_ON",
          relatedSpdxElement: "SPDXRef-Package-image-\(.)"}]
      )
    }
  }')
jq .cdx <<<"$boms" >"$out"
jq .spdx <<<"$boms" >"$out_spdx"
