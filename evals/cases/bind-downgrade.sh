#!/usr/bin/env bash
# Case bind-downgrade: binding verdicts need --bind, else COMMENT.
#
# Grades on a clean bundle (no open findings, so APPROVE is reachable):
#   B1  approved review without --bind posts event COMMENT and warns
#       "downgraded to COMMENT".
#   B2  same review with --bind posts event APPROVE and never downgrades.
#   B3  --request-changes with --bind posts REQUEST_CHANGES even with an
#       open finding (requesting changes alongside findings is normal).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

PR=4242
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}
EOF

# --- B1: binding verdict without --bind downgrades ---
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

# --- B2: --bind binds ---
run_review post acme/app "$PR" --dry-run --bind --json
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$event" = "APPROVE" ]; then
  good "--bind lands as APPROVE on a clean bundle"
else
  bad "--bind gave exit $EVAL_STATUS event '$event', want 0/APPROVE"
fi
if grep -q "downgraded" "$EVAL_ERR"; then
  bad "bound run still warns about a downgrade"
else
  good "bound run warns about no downgrade"
fi

# --- B3: REQUEST_CHANGES with findings is normal ---
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

if [ "$fails" -ne 0 ]; then echo "case bind-downgrade: FAIL ($fails)"; exit 1; fi
echo "case bind-downgrade: PASS"
