#!/usr/bin/env bash
# Builds the manager once and runs the load test (test/load) for every combination of object count and kind set.
#
# usage: hack/load/run.sh [-n "200 1000"] [-k "simple mixed"] [-c 4] [-s ramp,steady,drift,outage,delete]
#                         [-d 1m] [-b <write budget per minute, -1 disables>] [-o <out dir>]
# env: BAO_BIN, KUBEBUILDER_ASSETS, LOAD_MANAGER_BIN + LOAD_CRD_DIR (prebuilt pair), LOAD_OUTAGE (default 1m)
# results: <out dir>/<kinds>-n<N>-mcr<C>/{results.json,timeseries.csv,objects.csv,manager.log,bao.log}
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
counts="200 1000"
kind_sets="simple mixed"
max_concurrent=4
scenarios="ramp,steady,drift,outage,delete"
drift_interval=1m
write_budget=
out_dir="${repo_root}/.tmp/night/loadtest"

while getopts "n:k:c:s:d:b:o:h" opt; do
  case "${opt}" in
    n) counts=${OPTARG} ;;
    k) kind_sets=${OPTARG} ;;
    c) max_concurrent=${OPTARG} ;;
    s) scenarios=${OPTARG} ;;
    d) drift_interval=${OPTARG} ;;
    b) write_budget=${OPTARG} ;;
    o) out_dir=${OPTARG} ;;
    *) sed -n '2,7p' "${BASH_SOURCE[0]}"; exit 2 ;;
  esac
done

go_bin=$(command -v go || echo /opt/homebrew/bin/go)
bao_bin=${BAO_BIN:-$(command -v bao || true)}
if [[ -z "${bao_bin}" ]]; then
  echo "bao binary not found; set BAO_BIN" >&2
  exit 1
fi
assets=${KUBEBUILDER_ASSETS:-$(find "${repo_root}/bin/k8s" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | head -n 1)}
if [[ -z "${assets}" ]]; then
  echo "envtest binaries not found; run make setup-envtest or set KUBEBUILDER_ASSETS" >&2
  exit 1
fi

# LOAD_MANAGER_BIN and LOAD_CRD_DIR reuse a prebuilt pair; otherwise both are taken from the tree together, so a
# concurrent edit of config/crd/bases cannot split them.
manager_bin=${LOAD_MANAGER_BIN:-}
crd_dir=${LOAD_CRD_DIR:-}
if [[ -z "${manager_bin}" || -z "${crd_dir}" ]]; then
  build_dir=$(mktemp -d)
  trap 'rm -rf "${build_dir}"' EXIT
  cp -R "${repo_root}/config/crd/bases" "${build_dir}/crds"
  (cd "${repo_root}" && "${go_bin}" build -o "${build_dir}/manager" ./cmd/)
  manager_bin="${build_dir}/manager"
  crd_dir="${build_dir}/crds"
fi
mkdir -p "${out_dir}"

status=0
for kinds in ${kind_sets}; do
  for n in ${counts}; do
    run_out="${out_dir}/${kinds}-n${n}-mcr${max_concurrent}${write_budget:+-wb${write_budget}}"
    mkdir -p "${run_out}"
    echo "load: kinds=${kinds} n=${n} max-concurrent-reconciles=${max_concurrent} -> ${run_out}"
    if ! (cd "${repo_root}" && BAO_BIN="${bao_bin}" KUBEBUILDER_ASSETS="${assets}" \
      LOAD_MANAGER_BIN="${manager_bin}" LOAD_CRD_DIR="${crd_dir}" LOAD_N="${n}" LOAD_KINDS="${kinds}" \
      LOAD_MAX_CONCURRENT="${max_concurrent}" LOAD_SCENARIOS="${scenarios}" \
      LOAD_DRIFT_INTERVAL="${drift_interval}" LOAD_WRITE_BUDGET="${write_budget}" LOAD_OUT="${run_out}" \
      "${go_bin}" test -tags load -count=1 -timeout 0 -v -run TestLoad ./test/load/ 2>&1 | tee "${run_out}/test.log"); then
      status=1
    fi
  done
done
exit "${status}"
