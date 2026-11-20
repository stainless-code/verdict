#!/usr/bin/env bash
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
if python3 "$EVAL_ROOT/contrib/scanner-evidence/test_scan.py"; then
  good "scanner evidence contract"
else
  bad "scanner evidence contract"
fi
eval_end
