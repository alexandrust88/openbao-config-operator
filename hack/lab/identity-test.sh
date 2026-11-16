#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=hack/lab/common.sh
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
python3 "$repo_root/hack/lab/identity.py" test
