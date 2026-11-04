#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

PR=4243
eval_mk_bundle "$PR"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Consider clarifying this line.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"0123456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
run_review report acme/app "$PR" --json
assert_exit "report --json" 0
assert_keys "report"
assert_json_field "report envelope pins schema_version" 1 '.schema_version'
assert_json_field "report envelope echoes the verb" "report" '.verb'
assert_json_field "report envelope carries the numeric pr" 4243 '.pr'
if jq -e '.error == null and (.warnings | type == "array")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "report success envelope has null error and array warnings"
else
  bad "report success envelope breaks the error/warnings contract"
fi
assert_json_field "report envelope verdict counts the one open finding" 1 '.data.verdict.open'

PR=4244
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good overall — one nit below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.","rigor":"pointed","severity":"informational"}]}
EOF
run_review post acme/app "$PR" --dry-run --json
assert_exit "post --dry-run --json" 0
assert_keys "post success"
assert_json_field "post success envelope echoes the verb" "post" '.verb'
if jq -e '.error == null' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "post success envelope has a null error"
else
  bad "post success envelope breaks the error contract"
fi

eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"placeholder","approved":false,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -ne 0 ]; then
  good "refused post exits non-zero ($EVAL_STATUS)"
else
  bad "refused post exits 0"
fi
assert_refusal "post refusal"

eval_end
