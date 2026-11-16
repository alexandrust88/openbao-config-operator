#!/usr/bin/env bash
# Enable the published LDAP binary on every disposable raft pod before registering it.
set -euo pipefail
# shellcheck source=hack/lab/common.sh
source "$(dirname "${BASH_SOURCE[0]}")/../common.sh"
"$repo_root/hack/lab/bootstrap.sh"
"$repo_root/hack/lab/plugins/build-image.sh"
python3 "$repo_root/hack/lab/plugins/rollout.py"
python3 "$repo_root/hack/lab/plugins/install.py"
