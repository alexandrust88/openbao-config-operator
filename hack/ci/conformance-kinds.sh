#!/usr/bin/env bash
# Selects the conformance kinds a pull request affects from its changed paths (stdin, NUL-separated as
# `git diff -z --name-only` prints them) and prints mode=all|kinds|none, conf_kinds=<CONF_KINDS regex> and
# reason=<text>, ready for $GITHUB_OUTPUT.
# Shared code (engine, OpenBao client, connections, the suite, the build, docs/kinds.md, this selection) selects every
# kind, and so does any other operator code outside a group package. Any Go file of a group, a kind's own adapter or
# types included, selects the group's kinds: a package shares unexported helpers and types between its kinds. A CRD,
# fixture or generated example selects its own kind. A hand-written example selects every kind: the suite resolves references and applies examples
# across all of them. The output never echoes a path: paths come from the pull request and could carry workflow
# commands.
# Usage: hack/ci/conformance-kinds.sh <changed-files
set -euo pipefail

cd "$(dirname "$0")/../.."

all() {
  printf 'mode=all\nconf_kinds=\nreason=%s\n' "$1"
  exit 0
}

# A kind has a CRD (config/crd/bases/<group>.openbaoconfig.io_<plural>.yaml with singular: <kind>).
has_crd() {
  [[ $1 =~ ^[a-z0-9]+$ && $2 =~ ^[a-z0-9]+$ ]] &&
    grep -qxE "[[:space:]]+singular: $2" "config/crd/bases/$1.openbaoconfig.io_"*.yaml 2>/dev/null
}

# A kind also has its API types and its adapter under the same lowercase name.
is_kind() { [ -f "api/$1/v1alpha1/$2_types.go" ] && [ -f "internal/controller/$1/$2.go" ] && has_crd "$1" "$2"; }

group_kinds() {
  local types kind
  for types in "api/$1/v1alpha1/"*_types.go; do
    kind=$(basename "$types" _types.go)
    if is_kind "$1" "$kind"; then echo "$kind"; fi
  done
}

# The suite cannot measure external-plugin kinds (bao server -dev has no plugins); docs/kinds.md is its source too.
plugins=$(awk -F'|' '/^\| \[?`/ { gsub(/\\\|/, "/"); split($2, label, "`"); kind = label[2]; avail = $4; gsub(/^ +/, "", avail)
  if (avail ~ /^external-plugin/) print tolower(kind) }' docs/kinds.md | sort -u)
is_plugin() { grep -qxF "$1" <<<"$plugins"; }

selected=""
while IFS= read -r -d '' path || [ -n "$path" ]; do
  [ -n "$path" ] || continue
  # git quotes unusual paths when not run with -z; a newline means the input was not NUL-separated.
  if [[ $path == \"* || $path == *$'\n'* ]]; then all "a changed path is quoted or not NUL-separated"; fi
  case $path in
    go.mod | go.sum | Makefile | .github/workflows/conformance.yml | .github/actions/setup-go/* | hack/ci/conformance-kinds*.sh)
      all "build or kind selection changed" ;;
    docs/kinds.md)
      all "kind catalogue changed" ;;
    test/conformance/fixtures/*) ;;
    test/conformance/* | test/testenv/*)
      all "conformance suite changed" ;;
    *_test.go | */testdata/* | */zz_generated.deepcopy.go)
      continue ;;
    cmd/* | api/common/* | api/core/* | internal/controller/core/* | internal/engine/* | internal/openbao/* | \
      internal/connection/* | internal/refs/*)
      all "shared operator code changed" ;;
  esac
  if [[ $path =~ ^test/conformance/fixtures/([a-z0-9]+)/([a-z0-9]+)\.yaml$ ]]; then
    kind=${BASH_REMATCH[2]}
    is_kind "${BASH_REMATCH[1]}" "$kind" || all "conformance suite changed"
    selected+="$kind"$'\n'
  elif [[ $path == test/conformance/fixtures/* ]]; then
    all "conformance suite changed"
  elif [[ $path =~ ^config/crd/bases/([a-z0-9]+)\.openbaoconfig\.io_[a-z0-9]+\.yaml$ ]]; then
    group=${BASH_REMATCH[1]}
    kind=""
    if [ -f "$path" ]; then kind=$(awk '$1 == "singular:" { print $2; exit }' "$path"); fi
    if is_kind "$group" "$kind"; then selected+="$kind"$'\n'; else all "CRD of no single kind changed"; fi
  elif [[ $path =~ ^examples/([a-z0-9]+)/([a-z0-9]+)\.yaml$ ]]; then
    kind=${BASH_REMATCH[2]}
    if has_crd "${BASH_REMATCH[1]}" "$kind" && ! is_plugin "$kind"; then all "example changed"; fi
  elif [[ $path =~ ^examples-generated/([a-z0-9]+)/([a-z0-9]+)\.yaml$ ]]; then
    # A generated example only feeds its own kind, unless the kind has no hand-written example.
    group=${BASH_REMATCH[1]} kind=${BASH_REMATCH[2]}
    if is_kind "$group" "$kind" && [ -f "examples/$group/$kind.yaml" ]; then
      selected+="$kind"$'\n'
    elif has_crd "$group" "$kind" && ! is_plugin "$kind"; then
      all "generated example changed"
    fi
  elif [[ $path =~ ^(api/([a-z0-9]+)/v1alpha1|internal/controller/([a-z0-9]+))/[a-z0-9_]+\.go$ ]]; then
    kinds=$(group_kinds "${BASH_REMATCH[2]:-${BASH_REMATCH[3]}}")
    [ -n "$kinds" ] || all "operator code outside a kind changed"
    selected+="$kinds"$'\n'
  elif [[ $path == api/* || $path == internal/* ]]; then
    all "operator code outside a kind changed"
  fi
done

kinds=$(sort -u <<<"$selected" | sed '/^$/d' | { grep -vxF -f <(printf '%s\n' "$plugins") || true; })
skipped=$(sort -u <<<"$selected" | sed '/^$/d' | { grep -xF -f <(printf '%s\n' "$plugins") || true; })

if [ -z "$kinds" ]; then
  reason="no kinds affected"
  [ -z "$skipped" ] || reason+=" (external-plugin kinds only: $(tr '\n' ' ' <<<"$skipped" | sed 's/ $//'))"
  printf 'mode=none\nconf_kinds=\nreason=%s\n' "$reason"
  exit 0
fi
printf 'mode=kinds\nconf_kinds=(?i)^(%s)$\nreason=kinds: %s\n' "$(paste -sd'|' - <<<"$kinds")" "$(paste -sd' ' - <<<"$kinds")"
