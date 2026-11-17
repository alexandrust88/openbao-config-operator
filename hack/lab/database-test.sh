#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=hack/lab/common.sh
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
cd "$repo_root"
provider=${1:?usage: database-test.sh PROVIDER}
make yq helm
python3 hack/lab/database.py up "$provider"
python3 hack/lab/database.py test "$provider"
