#!/usr/bin/env bash
# Rewrites the CRD list of config/crd/kustomization.yaml (built-in kinds) and config/crd/plugin-kinds/kustomization.yaml
# (the kinds in hack/chart/plugin-crds.txt) from config/crd/bases.
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
crd_dir=$repo_root/config/crd
plugins=$repo_root/hack/chart/plugin-crds.txt
k=$crd_dir/kustomization.yaml

{
  sed -n '1,/^resources:/p' "$k"
  for f in "$crd_dir"/bases/*.yaml; do
    grep -qxF "$(basename "$f")" "$plugins" || echo "- bases/$(basename "$f")"
  done
  sed -n '/^# +kubebuilder:scaffold:crdkustomizeresource/,$p' "$k"
} >"$k.new"
mv "$k.new" "$k"

mkdir -p "$crd_dir/plugin-kinds"
{
  echo '# CRDs of the kinds that need an external OpenBao plugin; the manager runs them only with --enable-plugin-kinds.'
  echo '# They live outside this directory: kustomize build --load-restrictor LoadRestrictionsNone config/crd/plugin-kinds'
  echo 'resources:'
  sed 's|^|- ../bases/|' "$plugins"
} >"$crd_dir/plugin-kinds/kustomization.yaml"

echo "sync-crd-kustomization: $(grep -c '^- bases/' "$k") built-in, $(wc -l <"$plugins" | tr -d ' ') plugin CRDs"
