#!/usr/bin/env bash
# Builds the release install.yaml (CRDs and manager) and crds.yaml from a copy of config/, so the tree stays untouched.
# Usage: hack/release/installer.sh <image-ref> <out-dir>
set -euo pipefail

[ $# -eq 2 ] || { echo "usage: $0 <image-ref> <out-dir>" >&2; exit 2; }
img=$1 out=$2
repo_root=$(cd "$(dirname "$0")/../.." && pwd)
kustomize=$(command -v "${KUSTOMIZE:-kustomize}") || { echo "installer: kustomize not found" >&2; exit 1; }
[[ $kustomize == /* ]] || kustomize=$PWD/$kustomize

work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
cp -R "$repo_root/config" "$work/"
(cd "$work/config/manager" && "$kustomize" edit set image "controller=$img")

mkdir -p "$out"
"$kustomize" build "$work/config/default" >"$out/install.yaml"
"$kustomize" build "$work/config/crd" >"$out/crds.yaml"
