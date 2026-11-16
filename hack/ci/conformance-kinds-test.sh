#!/usr/bin/env bash
# Tests hack/ci/conformance-kinds.sh against the kinds of this tree.
# Usage: hack/ci/conformance-kinds-test.sh
set -euo pipefail

cd "$(dirname "$0")/../.."
failures=0

# select_kinds <changed path...> prints the selection for the paths, NUL-separated like git diff -z.
select_kinds() {
  if [ "$#" -gt 0 ]; then printf '%s\0' "$@"; fi | hack/ci/conformance-kinds.sh
}

# expect <case> <regex the mode and conf_kinds lines must match> [changed path...]
expect() {
  local name=$1 want=$2 got
  shift 2
  got=$(select_kinds "$@" | sed -n '1,2p' | paste -sd' ' -)
  if [[ $got =~ $want ]]; then
    echo "ok   $name"
  else
    echo "FAIL $name: got '$got', want /$want/"
    failures=$((failures + 1))
  fi
}

# reject <case> <regex the whole output must not match> [changed path...]
reject() {
  local name=$1 unwanted=$2 got
  shift 2
  got=$(select_kinds "$@" | paste -sd' ' -)
  if [[ $got =~ $unwanted ]]; then
    echo "FAIL $name: got '$got', must not match /$unwanted/"
    failures=$((failures + 1))
  else
    echo "ok   $name"
  fi
}

expect "empty diff" '^mode=none conf_kinds=$'
expect "unrelated paths" '^mode=none conf_kinds=$' README.md docs/index.md charts/openbao-config-operator/values.yaml
expect "unit test and testdata only" '^mode=none conf_kinds=$' \
  internal/controller/secrets/kvsecret_test.go internal/engine/compare_test.go internal/controller/auth/testdata/x.json
expect "generated deepcopy only" '^mode=none conf_kinds=$' api/secrets/v1alpha1/zz_generated.deepcopy.go
expect "example of no kind" '^mode=none conf_kinds=$' examples/live/kustomization.yaml \
  examples/sys/auditrequestheader-attribution.yaml
expect "example of an external-plugin kind" '^mode=none conf_kinds=$' examples/auth/ldapauthconfig.yaml
expect "external-plugin kind only" '^mode=none conf_kinds=$' config/crd/bases/auth.openbaoconfig.io_ldapauthconfigs.yaml
expect "one kind from its data files" '^mode=kinds conf_kinds=\(\?i\)\^\(kvsecret\)\$$' \
  examples-generated/secrets/kvsecret.yaml config/crd/bases/secrets.openbaoconfig.io_kvsecrets.yaml
for f in api/secrets/v1alpha1/kvsecret_types.go internal/controller/secrets/kvsecret.go; do
  expect "a kind's Go file selects its group: $f" '^mode=kinds conf_kinds=\(\?i\)\^\(.*kvsecret\|.*transitkey.*\)\$$' "$f"
done
expect "a kind's adapter selects the kinds sharing its helpers" \
  '^mode=kinds conf_kinds=\(\?i\)\^\(.*entity\|.*group\|.*\)\$$' internal/controller/identity/entity.go
expect "CRD of a kind" '^mode=kinds conf_kinds=\(\?i\)\^\(passwordpolicy\)\$$' \
  config/crd/bases/sys.openbaoconfig.io_passwordpolicies.yaml
expect "CRD of a connection kind" '^mode=all conf_kinds=$' config/crd/bases/core.openbaoconfig.io_openbaoconnections.yaml
expect "deleted CRD" '^mode=all conf_kinds=$' config/crd/bases/secrets.openbaoconfig.io_removeds.yaml
expect "hand-written example" '^mode=all conf_kinds=$' examples/secrets/kvsecret.yaml
expect "two kinds, sorted" '^mode=kinds conf_kinds=\(\?i\)\^\(kvsecret\|pkirole\)\$$' \
  test/conformance/fixtures/secrets/pkirole.yaml config/crd/bases/secrets.openbaoconfig.io_kvsecrets.yaml
expect "external-plugin kind dropped" '^mode=kinds conf_kinds=\(\?i\)\^\(kvsecret\)\$$' \
  config/crd/bases/auth.openbaoconfig.io_ldapauthconfigs.yaml config/crd/bases/secrets.openbaoconfig.io_kvsecrets.yaml
expect "shared group file selects the group" '^mode=kinds conf_kinds=\(\?i\)\^\(.*\|tokenrole\|.*\)\$$' \
  internal/controller/auth/helpers.go
for f in internal/controller/sys/mount.go api/sys/v1alpha1/mount_types.go; do
  expect "types and adapter without a CRD select the group: $f" \
    '^mode=kinds conf_kinds=\(\?i\)\^\(.*authmount\|.*secretsmount.*\)\$$' "$f"
  reject "types and adapter without a CRD are no kind: $f" '[(|]mount[|)]' "$f"
done
expect "engine" '^mode=all conf_kinds=$' internal/controller/secrets/kvsecret.go internal/engine/reconciler.go
expect "openbao client" '^mode=all conf_kinds=$' internal/openbao/client.go
expect "connection code" '^mode=all conf_kinds=$' internal/connection/resolver.go
expect "connection kinds" '^mode=all conf_kinds=$' api/core/v1alpha1/connection_types.go
expect "manager" '^mode=all conf_kinds=$' cmd/main.go
expect "other command" '^mode=all conf_kinds=$' cmd/obc-import/main.go
expect "operator code of no rule" '^mode=all conf_kinds=$' internal/importer/importer.go
expect "operator path that is not a kind name" '^mode=all conf_kinds=$' 'internal/controller/secrets/::error::x.go'
reject "never echoes a path" '::error::' 'internal/controller/secrets/::error::x.go'
reject "never echoes an unselected path" '::error::' 'examples/::error::x.yaml' '::error::x'
expect "path quoted by git" '^mode=all conf_kinds=$' '"internal/controller/secrets/kvsecr\303\251t.go"'
expect "newline-separated input" '^mode=all conf_kinds=$' $'README.md\ninternal/controller/secrets/kvsecret.go'
expect "kind catalogue" '^mode=all conf_kinds=$' docs/kinds.md
expect "conformance suite" '^mode=all conf_kinds=$' test/conformance/nowrite_test.go
expect "conformance exceptions" '^mode=all conf_kinds=$' test/conformance/exceptions.yaml
expect "fixture of a kind" '^mode=kinds conf_kinds=\(\?i\)\^\(openbaoendpoint\)\$$' test/conformance/fixtures/core/openbaoendpoint.yaml
expect "fixture of no kind" '^mode=all conf_kinds=$' test/conformance/fixtures/sys/removed.yaml
expect "go.sum" '^mode=all conf_kinds=$' go.sum
expect "workflow" '^mode=all conf_kinds=$' .github/workflows/conformance.yml
expect "selection script" '^mode=all conf_kinds=$' hack/ci/conformance-kinds.sh

[ "$failures" -eq 0 ] || { echo "$failures case(s) failed" >&2; exit 1; }
