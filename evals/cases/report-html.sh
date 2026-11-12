#!/usr/bin/env bash
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

eval_mk_bundle 1234
EXPORT_PARENT="$(cd "$EVAL_SANDBOX" && pwd -P)"
run_review report --html "$EXPORT_PARENT/dashboard"
assert_exit "HTML export" 0
files="$(find "$EXPORT_PARENT/dashboard" -type f -exec basename {} \; | LC_ALL=C sort)"
assert_eq "HTML file set" "$(printf '%s\n' app.css app.js data.json index.html manifest.json)" "$files"
assert_eq "bundle envelope key" true "$(jq 'has("/v1/bundles")' "$EXPORT_PARENT/dashboard/data.json")"
run_review report --html "$EXPORT_PARENT/dashboard"
assert_exit "HTML refresh" 0
run_review report --html "$EXPORT_PARENT/discovered" --root "$EVAL_SANDBOX"
assert_exit "HTML root discovery" 0
assert_eq "discovered bundles" 1 "$(jq '.["/v1/bundles"].data.rows | length' "$EXPORT_PARENT/discovered/data.json")"
eval_end
