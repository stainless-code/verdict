#!/usr/bin/env bash
# Case envelope-shape: every --json stdout is exactly one envelope with the
# documented shape (schema_version "1", verb/repo/pr/at/next/warnings/data/
# error), error null on success and {data null, error code} on refusal.
#
# Grades:
#   E1  report --json success envelope: exact key set, schema_version "1",
#       verb echo, numeric pr, error null, warnings array, open verdict of 1.
#   E2  post --dry-run --json success envelope: same key set, verb post.
#   E3  post refusal envelope: same key set, exactly one doc, data null,
#       error.code present, non-zero exit.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

WANT_KEYS="at data error next pr repo schema_version verb warnings"
check_keys() { # $1 = label
  local label="$1" have
  have="$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null || true)"
  if [ "$have" = "$WANT_KEYS" ]; then
    good "$label envelope carries exactly the documented keys"
  else
    bad "$label envelope keys are '$have'"
  fi
}

# --- E1: report success envelope ---
PR=4243
eval_mk_bundle "$PR"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Consider clarifying this line.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"0123456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
run_review report acme/app "$PR" --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "report --json exits 0"
else
  bad "report --json exits $EVAL_STATUS"
fi
check_keys "report"
sv="$(jq -r '.schema_version // empty' <"$EVAL_OUT" 2>/dev/null || true)"
verb="$(jq -r '.verb // empty' <"$EVAL_OUT" 2>/dev/null || true)"
prn="$(jq -r '.pr // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$sv" = "1" ] && [ "$verb" = "report" ] && [ "$prn" = "4243" ]; then
  good "report envelope stamps schema_version 1, verb report, pr 4243"
else
  bad "report envelope stamps schema_version='$sv' verb='$verb' pr='$prn'"
fi
if jq -e '.error == null and (.warnings | type == "array")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "report success envelope has null error and array warnings"
else
  bad "report success envelope breaks the error/warnings contract"
fi
open_n="$(jq -r '.data.verdict.open // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$open_n" = "1" ]; then
  good "report envelope verdict counts the one open finding"
else
  bad "report envelope verdict.open is '$open_n', want 1"
fi

# --- E2: post success envelope ---
PR=4244
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good overall — one nit below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.","rigor":"pointed","severity":"informational"}]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "post --dry-run --json exits 0"
else
  bad "post --dry-run --json exits $EVAL_STATUS"
fi
check_keys "post success"
verb="$(jq -r '.verb // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$verb" = "post" ] && jq -e '.error == null' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "post success envelope echoes verb post with null error"
else
  bad "post success envelope breaks the verb/error contract"
fi

# --- E3: refusal envelope keeps the shape ---
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"placeholder","approved":false,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -ne 0 ]; then
  good "refused post exits non-zero ($EVAL_STATUS)"
else
  bad "refused post exits 0"
fi
check_keys "post refusal"
docs="$(stdout_docs)"
if [ "$docs" = "1" ]; then
  good "refusal still emits exactly one JSON doc"
else
  bad "refusal emits $docs JSON docs, want exactly 1"
fi
if jq -e '.data == null and (.error.code | type == "string")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "refusal envelope carries null data and a string error.code"
else
  bad "refusal envelope breaks the data/error contract"
fi

if [ "$fails" -ne 0 ]; then echo "case envelope-shape: FAIL ($fails)"; exit 1; fi
echo "case envelope-shape: PASS"
