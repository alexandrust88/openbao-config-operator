#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=hack/lab/common.sh
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
case "${1:-}" in
  authentik|zitadel) provider=$1 ;;
  *) echo 'usage: provider-test.sh authentik|zitadel' >&2; exit 2 ;;
esac
python3 -u "$repo_root/hack/lab/$provider.py" up
python3 -u "$repo_root/hack/lab/$provider.py" apply
python3 -u "$repo_root/hack/lab/$provider.py" test
