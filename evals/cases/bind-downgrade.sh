#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

PR=4242
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}
EOF

run_review post acme/app "$PR" --dry-run --json
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$event" = "COMMENT" ]; then
  good "unbound APPROVE lands as COMMENT"
else
  bad "unbound APPROVE gave exit $EVAL_STATUS event '$event', want 0/COMMENT"
fi
if grep -q "downgraded to COMMENT" "$EVAL_ERR"; then
  good "downgrade warning names the remedy on stderr"
else
  bad "stderr missing the downgrade warning"
fi

run_review post acme/app "$PR" --dry-run --bind --json
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$event" = "APPROVE" ]; then
  good "--bind lands as APPROVE on a clean bundle"
else
  bad "--bind gave exit $EVAL_STATUS event '$event', want 0/APPROVE"
fi
assert_stream_nogrep "bound run warns about no downgrade" "$EVAL_ERR" "downgraded"

eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> [!CAUTION] Blocking issue below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"This line is wrong.","rigor":"walked","severity":"critical"}]}
EOF
run_review post acme/app "$PR" --dry-run --request-changes --bind --json
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$event" = "REQUEST_CHANGES" ]; then
  good "--request-changes --bind lands as REQUEST_CHANGES with findings open"
else
  bad "request-changes gave exit $EVAL_STATUS event '$event', want 0/REQUEST_CHANGES"
fi

eval_end
