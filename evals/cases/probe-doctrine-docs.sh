#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

if grep -q "Machine modes never prompt" "$EVAL_ROOT/docs/manual.md" \
  && awk '/--json/ && /never/ && /interactive/ {found=1} END {exit !found}' \
    "$EVAL_ROOT/docs/manual.md"; then
  good "doctrine line present (Machine surface names --json + never + interactive)"
else
  bad "doctrine line missing from docs/manual.md"
fi
if "$EVAL_CLI" --help 2>/dev/null | matches -- "--json"; then
  good "usage text still names --json"
else
  bad "usage text misses --json"
fi

if grep -q "Pre-push probe" "$EVAL_ROOT/docs/manual.md" \
  && grep -q "pre-push" "$EVAL_ROOT/docs/manual.md" \
  && grep -q "\.git/hooks/pre-push" "$EVAL_ROOT/docs/manual.md" \
  && grep -q "report.*--check.*--completeness" "$EVAL_ROOT/docs/manual.md"; then
  good "probe subsection present (pre-push + combined recipe + hook path)"
else
  bad "probe subsection missing from docs/manual.md"
fi
if awk '/^### Pre-push probe/,/^## /' "$EVAL_ROOT/docs/manual.md" | matches -- "--base-sha" \
  && awk '/^### Pre-push probe/,/^## /' "$EVAL_ROOT/docs/manual.md" | matches -i "offline"; then
  good "probe subsection cross-links --base-sha recovery and notes offline safety"
else
  bad "probe subsection misses the fetch cross-link or the offline note"
fi

eval_end
