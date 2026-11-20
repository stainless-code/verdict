#!/usr/bin/env bash
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
SLUG="local-reproduction"
LB="$EVAL_SANDBOX/.verdict/state/$SLUG"
eval_mk_bundle "$SLUG"
printf 'assert False\n' > "$LB/probe.py"
eval_write_review_at "$LB/review.json" <<'JSON'
{"schema_version":"1","findings":[{"path":"notes.txt","line":2,"side":"RIGHT","severity":"important","claim":"The supplied input reproduces a defect.","quote":"new line","confidence":"high","rigor":"reproduced","reproduction":{"path":"probe.py","command":"python3 {artifact}"}}]}
JSON
run_review record acme/app "$SLUG" --dry-run --json
assert_exit "dry run accepts draft" 0
if [ ! -e "$LB/tests" ]; then good "dry run stores nothing"; else bad "dry run wrote tests"; fi
run_review record acme/app "$SLUG" --json
assert_exit "record accepts reproduction" 0
SIG="$(jq -r '.[0].sig' "$LB/findings.json")"
ARTIFACT="$(jq -r '.[0].reproduction.path' "$LB/findings.json")"
if [ "$ARTIFACT" = "tests/$SIG-161139e764cc.py" ] && [ "$(cat "$LB/$ARTIFACT")" = 'assert False' ]; then
  good "retained artifact names its finding and keeps its test"
else bad "artifact missing or wrong"; fi
if jq -e --arg cmd "python3 tests/$SIG-161139e764cc.py" '.command == $cmd' "$LB/tests/$SIG-161139e764cc.command.json" >/dev/null; then
  good "rerun command retained"
else bad "rerun command missing"; fi
run_review show acme/app "$SLUG" --sig "$SIG" --json
assert_json_field "show points at artifact" "$ARTIFACT" '.data.item.reproduction.path'
run_review report acme/app "$SLUG"
if matches -F "$ARTIFACT" < "$EVAL_OUT"; then good "report points at artifact"; else bad "report omits artifact"; fi
jq 'del(.findings[0].reproduction)' "$LB/review.json" > "$LB/review.new"
mv "$LB/review.new" "$LB/review.json"
rm "$LB/findings.json"
run_review record acme/app "$SLUG" --json
assert_exit "record keeps an unsupported reproduced claim" 0
if jq -e '.warnings | join(" ") | contains("has no runnable artifact")' "$EVAL_OUT" >/dev/null; then
 good "missing reproduction flagged"
else bad "missing reproduction silently accepted"; fi
eval_end
