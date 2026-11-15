#!/usr/bin/env bash
# Builds the manager once and runs the load test (test/load) for every combination of object count and kind set;
# the tree kind set runs once per namespace shape instead of per object count.
#
# usage: hack/load/run.sh [-n "200 1000"] [-k "simple mixed tree"] [-t "100x10x20"] [-c 4]
#                         [-s ramp,steady,drift,outage,delete] [-d 1m] [-b <write budget per minute, -1 disables>]
#                         [-o <out dir>]
#   -t: tree shapes, parents x children per parent x kv mounts per namespace [x k8s auth roles x policies], roles
#       and policies default to 3
# env: BAO_BIN, KUBEBUILDER_ASSETS, LOAD_MANAGER_BIN + LOAD_CRD_DIR (prebuilt pair), LOAD_OUTAGE (default 1m)
# results: <out dir>/<kinds>-{n<N>,<shape>}-mcr<C>/{results.json,timeseries.csv,objects.csv,manager.log,bao.log}
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
counts="200 1000"
kind_sets="simple mixed"
tree_shapes="100x10x20"
max_concurrent=4
scenarios="ramp,steady,drift,outage,delete"
drift_interval=1m
write_budget=
out_dir="${repo_root}/.tmp/night/loadtest"

while getopts "n:k:t:c:s:d:b:o:h" opt; do
  case "${opt}" in
    n) counts=${OPTARG} ;;
    k) kind_sets=${OPTARG} ;;
    t) tree_shapes=${OPTARG} ;;
    c) max_concurrent=${OPTARG} ;;
    s) scenarios=${OPTARG} ;;
    d) drift_interval=${OPTARG} ;;
    b) write_budget=${OPTARG} ;;
    o) out_dir=${OPTARG} ;;
    *) sed -n '2,12p' "${BASH_SOURCE[0]}"; exit 2 ;;
  esac
done

known=0
for scenario in ${scenarios//,/ }; do
  case "${scenario}" in
    ramp | steady | drift | outage | delete) known=$((known + 1)) ;;
    *) echo "unknown scenario ${scenario}; want ramp, steady, drift, outage or delete" >&2; exit 2 ;;
  esac
done
if [[ "${known}" -eq 0 ]]; then
  echo "no scenario given; want ramp, steady, drift, outage or delete" >&2
  exit 2
fi

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
out_dir=$(cd "${out_dir}" && pwd)

status=0
for kinds in ${kind_sets}; do
  sizes=${counts}
  if [[ "${kinds}" == tree ]]; then
    sizes=${tree_shapes}
  fi
  for size in ${sizes}; do
    n=${size} shape= label=n${size}
    if [[ "${kinds}" == tree ]]; then
      n= shape=${size} label=${size}
    fi
    run_out="${out_dir}/${kinds}-${label}-mcr${max_concurrent}${write_budget:+-wb${write_budget}}"
    mkdir -p "${run_out}"
    echo "load: kinds=${kinds} ${label} max-concurrent-reconciles=${max_concurrent} -> ${run_out}"
    if ! (cd "${repo_root}" && BAO_BIN="${bao_bin}" KUBEBUILDER_ASSETS="${assets}" \
      LOAD_MANAGER_BIN="${manager_bin}" LOAD_CRD_DIR="${crd_dir}" LOAD_N="${n}" LOAD_TREE="${shape}" LOAD_KINDS="${kinds}" \
      LOAD_MAX_CONCURRENT="${max_concurrent}" LOAD_SCENARIOS="${scenarios}" \
      LOAD_DRIFT_INTERVAL="${drift_interval}" LOAD_WRITE_BUDGET="${write_budget}" LOAD_OUT="${run_out}" \
      "${go_bin}" test -tags load -count=1 -timeout 0 -v -run TestLoad ./test/load/ 2>&1 | tee "${run_out}/test.log"); then
      status=1
    fi
  done
done
exit "${status}"
