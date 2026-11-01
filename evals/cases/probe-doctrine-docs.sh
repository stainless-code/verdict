#!/usr/bin/env bash
# Case probe-doctrine-docs: gate doctrine + pre-push probe docs presence.
#
# Grades (all hermetic — docs-presence greps, no network, no model):
#   D1  doctrine line: the Machine-surface passage names --json, never, and
#       interactive together (one line: machine modes never prompt; the
#       script never blocks on interactive setup or confirm); usage text
#       still names --json.
#   D2  probe subsection: the manual contains pre-push, the combined
#       report --check --completeness recipe, and the .git/hooks/pre-push
#       path.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

# --- D1: machine-modes-never-prompt doctrine in Machine surface ---
if grep -q "Machine modes never prompt" "$EVAL_ROOT/docs/manual.md" \
  && awk '/--json/ && /never/ && /interactive/ {found=1} END {exit !found}' \
    "$EVAL_ROOT/docs/manual.md"; then
  good "doctrine line present (Machine surface names --json + never + interactive)"
else
  bad "doctrine line missing from docs/manual.md"
fi
if grep -q -- "--json" "$EVAL_ROOT/scripts/review.sh"; then
  good "usage text still names --json"
else
  bad "usage text misses --json"
fi

# --- D2: pre-push probe subsection ---
if grep -q "Pre-push probe" "$EVAL_ROOT/docs/manual.md" \
  && grep -q "pre-push" "$EVAL_ROOT/docs/manual.md" \
  && grep -q "\.git/hooks/pre-push" "$EVAL_ROOT/docs/manual.md" \
  && grep -q "report.*--check.*--completeness" "$EVAL_ROOT/docs/manual.md"; then
  good "probe subsection present (pre-push + combined recipe + hook path)"
else
  bad "probe subsection missing from docs/manual.md"
fi
if awk '/^### Pre-push probe/,/^## /' "$EVAL_ROOT/docs/manual.md" | grep -q -- "--base-sha" \
  && awk '/^### Pre-push probe/,/^## /' "$EVAL_ROOT/docs/manual.md" | grep -qi "offline"; then
  good "probe subsection cross-links --base-sha recovery and notes offline safety"
else
  bad "probe subsection misses the fetch cross-link or the offline note"
fi

if [ "$fails" -ne 0 ]; then echo "case probe-doctrine-docs: FAIL ($fails)"; exit 1; fi
echo "case probe-doctrine-docs: PASS"
