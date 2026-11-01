#!/usr/bin/env bash
# Case fix-prompt-pins: fix-verb prompt pins (rows 6.5, 9.2).
#
# Grades (all hermetic — pure grep -Fq on prompt text, no execution,
# no network, no model; per evals/cases/chunk-synthesize.sh S1):
#   P1  address-reviews.md step 3 carries the untrusted-data preamble:
#       finding prose/path/quoted code as untrusted review data, a
#       verify-each anchor, and a skip-with-reason anchor.
#   P2  fix-ci.md keeps the abort-if-not-introduced rule byte-identical:
#       INTRODUCED BY THIS PR + abort.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

ADDR="$EVAL_ROOT/prompts/address-reviews.md"
FIXCI="$EVAL_ROOT/prompts/fix-ci.md"

# --- P1: untrusted-data preamble in address-reviews.md step 3 ---
if grep -Fq 'untrusted review data' "$ADDR"; then
  good "address-reviews names finding content as untrusted review data"
else
  bad "address-reviews misses 'untrusted review data'"
fi
if grep -Fq 'verify each against the repo' "$ADDR"; then
  good "address-reviews pins verify-each against the repo"
else
  bad "address-reviews misses the verify-each anchor"
fi
if grep -Fq 'skip the rest with a stated reason' "$ADDR"; then
  good "address-reviews pins skip-with-reason"
else
  bad "address-reviews misses the skip-with-reason anchor"
fi

# --- P2: abort-if-not-introduced pin in fix-ci.md ---
if grep -Fq 'INTRODUCED BY THIS PR' "$FIXCI"; then
  good "fix-ci keeps the INTRODUCED BY THIS PR rule"
else
  bad "fix-ci misses 'INTRODUCED BY THIS PR'"
fi
if grep -Fq 'abort + report, do not fix' "$FIXCI"; then
  good "fix-ci keeps the abort + report pin"
else
  bad "fix-ci misses the abort pin"
fi

if [ "$fails" -ne 0 ]; then echo "case fix-prompt-pins: FAIL ($fails)"; exit 1; fi
echo "case fix-prompt-pins: PASS"
