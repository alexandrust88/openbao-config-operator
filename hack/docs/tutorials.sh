#!/usr/bin/env bash
# Runs the four tutorials of docs/tutorials/ in order, on the cluster that hack/docs/quickstart.sh left (operator and
# in-cluster OpenBao), then the uninstall procedure of docs/operate/uninstall.md, which removes the operator. Each
# step builds on the one before it. CI runs this right after the quickstart.
#
# Environment:
#   KIND_CLUSTER      kind cluster the quickstart ran on (default openbao-quickstart)
#   SKIP_KIND_CREATE  accepted for symmetry with quickstart.sh; the tutorials never create a cluster
set -euo pipefail

cd "$(dirname "$0")/../.."

export KIND_CLUSTER=${KIND_CLUSTER:-openbao-quickstart}

for tutorial in platform-owner multiple-instances tenant-onboarding team-onboarding; do
  echo "tutorials: $tutorial"
  "hack/docs/tutorial-$tutorial.sh"
done

echo "tutorials: all four tutorials passed"

echo "tutorials: uninstall"
hack/docs/uninstall.sh
