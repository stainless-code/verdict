#!/usr/bin/env bash
# Case terminal-write: exactly-one terminal write + the no-review-note rule.
#
# Grades:
#   T1  doctrine pins: SKILL.md names exactly one terminal write per router row,
#       and the delta row resolves to "one review OR no-review note".
#   T2  no-review-note rule, enforced: a degenerate body (short, no verdict
#       marker) is refused with degenerate-body and writes no findings file —
#       an empty review never ships.
#   T3  exactly-one terminal write, behavioral: a valid review via
#       post --dry-run --json emits exactly one JSON doc on stdout carrying a
#       single event, and records exactly one findings file.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

# --- T1: doctrine pins ---
if grep -q "Exactly one terminal write" "$EVAL_ROOT/SKILL.md"; then
  good "SKILL.md pins exactly-one terminal write"
else
  bad "SKILL.md missing exactly-one-terminal-write doctrine"
fi
if grep -q "one review OR no-review note" "$EVAL_ROOT/SKILL.md"; then
  good "router maps the delta row to one review OR no-review note"
else
  bad "router delta row does not name the no-review-note terminal write"
fi
if [ -f "$EVAL_ROOT/prompts/incremental-review.md" ] \
  && grep -qi "noreview\.md" "$EVAL_ROOT/prompts/incremental-review.md"; then
  good "incremental mode defines the single noreview.md note"
else
  bad "incremental mode missing the noreview.md terminal write"
fi

PR=4241
eval_mk_bundle "$PR"

# --- T2: degenerate bodies never ship ---
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"hi","approved":false,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -ne 0 ]; then
  good "degenerate body refused (exit $EVAL_STATUS)"
else
  bad "degenerate body accepted (exit 0)"
fi
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$code" = "degenerate-body" ]; then
  good "refusal names degenerate-body"
else
  bad "refusal names '$code', want degenerate-body"
fi
if [ ! -f "$EVAL_SANDBOX/.review/$PR/findings.dryrun.json" ]; then
  good "refused review wrote no findings file"
else
  bad "refused review still wrote findings.dryrun.json"
fi

# --- T3: one valid review in, one terminal write out ---
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good overall — one nit below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.","rigor":"pointed","severity":"informational"}]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "valid review accepted (exit 0)"
else
  bad "valid review refused (exit $EVAL_STATUS)"
fi
docs="$(stdout_docs)"
if [ "$docs" = "1" ]; then
  good "stdout holds exactly one JSON envelope"
else
  bad "stdout holds $docs JSON docs, want exactly 1"
fi
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
case "$event" in
  COMMENT|APPROVE|REQUEST_CHANGES) good "envelope carries a single event ($event)" ;;
  *) bad "envelope event is '$event', want one of COMMENT/APPROVE/REQUEST_CHANGES" ;;
esac
if [ -f "$EVAL_SANDBOX/.review/$PR/findings.dryrun.json" ]; then
  good "exactly one findings file recorded (findings.dryrun.json)"
else
  bad "findings.dryrun.json missing"
fi

if [ "$fails" -ne 0 ]; then echo "case terminal-write: FAIL ($fails)"; exit 1; fi
echo "case terminal-write: PASS"
