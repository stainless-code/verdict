#!/usr/bin/env bash
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

eval_mk_bundle 1234
run_review report --html "$EVAL_SANDBOX/dashboard"
assert_exit "HTML export" 0
files="$(find "$EVAL_SANDBOX/dashboard" -type f -exec basename {} \; | LC_ALL=C sort)"
assert_eq "HTML file set" "$(printf '%s\n' app.css app.js data.json index.html)" "$files"
assert_eq "bundle envelope key" true "$(jq 'has("/v1/bundles")' "$EVAL_SANDBOX/dashboard/data.json")"
run_review report --html "$EVAL_SANDBOX/dashboard"
assert_exit "HTML refresh" 0
run_review report --html "$EVAL_SANDBOX/discovered" --root "$EVAL_SANDBOX"
assert_exit "HTML root discovery" 0
assert_eq "discovered bundles" 1 "$(jq '.["/v1/bundles"].data.rows | length' "$EVAL_SANDBOX/discovered/data.json")"
eval_end
