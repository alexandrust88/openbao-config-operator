#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=hack/lab/common.sh
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
python3 -u "$repo_root/hack/lab/keycloak_oidc.py" apply
python3 -u "$repo_root/hack/lab/keycloak_oidc.py" test
python3 -u "$repo_root/hack/lab/keycloak_jwt.py"
