#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

RECIPE="$EVAL_ROOT/docs/ci-recipe.md"
WORKFLOW="$EVAL_ROOT/examples/verdict-review.yml"

if [ -f "$RECIPE" ]; then
  good "docs/ci-recipe.md exists"
else
  bad "docs/ci-recipe.md missing"
fi
for anchor in "setup-verdict" "verdict fetch" "review.json" "verdict post" "report --check" "ready --check" "branch protection" "permissions" "40-hex"; do
  if grep -q -i -F -- "$anchor" "$RECIPE" 2>/dev/null; then
    good "recipe page names $anchor"
  else
    bad "recipe page misses $anchor"
  fi
done

if [ -f "$WORKFLOW" ]; then
  good "examples/verdict-review.yml exists"
else
  bad "examples/verdict-review.yml missing"
fi
uses_total="$(grep -c -E '^[[:space:]-]*uses:' "$WORKFLOW" 2>/dev/null || true)"
uses_pinned="$(grep -c -E 'uses:[^#]*@[0-9a-f]{40}([[:space:]]|$)' "$WORKFLOW" 2>/dev/null || true)"
case "$uses_total" in ''|0) uses_total=0;; esac
case "$uses_pinned" in ''|0) uses_pinned=0;; esac
if [ "$uses_total" -ge 2 ] && [ "$uses_pinned" = "$uses_total" ]; then
  good "workflow pins all $uses_total uses: entries by full SHA"
else
  bad "workflow has $uses_total uses: entries but only $uses_pinned pinned by full SHA"
fi
assert_stream_nogrep "workflow floats on no moving tag" "$WORKFLOW" 'uses:[^#]*@(v[0-9]|latest|main|master)' "-E"
for anchor in "setup-verdict" "verdict fetch" "verdict post" "verdict report" "verdict ready" "--check"; do
  if grep -q -F -- "$anchor" "$WORKFLOW" 2>/dev/null; then
    good "workflow runs $anchor"
  else
    bad "workflow misses $anchor"
  fi
done

mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_BUDGET_USD REVIEW_MODEL || true
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  git config user.email "recipe@example.com"
  git config user.name "recipe case"
  printf 'top\nold line\nbottom\n' > notes.txt
  git add notes.txt
  git commit -qm base 2>/dev/null || true
  printf 'top\nnew line\nbottom\n' > notes.txt
)
gate_body='{"schema_version":"1","body":"> ✅ Recipe gate review, one note.\\n> Basis: 0 reproduced · 0 ran · 1 walked · 0 pointed · 0 said · 0 rules cited · 0 verified fixes","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"RECIPEBODY","rigor":"walked","severity":"RECIPESEV"}]}'
run_review fetch-local acme/app gatecrit
printf '%s' "$gate_body" | sed 's/RECIPEBODY/Critical: verify this line./; s/RECIPESEV/critical/' \
  > "$EVAL_SANDBOX/.review/local-gatecrit/review.json"
run_review record acme/app local-gatecrit --json
if [ "$EVAL_STATUS" -ne 0 ]; then
  bad "record exits $EVAL_STATUS on the critical review"
else
  good "record merges the critical review"
fi
run_review report acme/app local-gatecrit --check critical
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "gate fails" "$EVAL_ERR"; then
  good "report --check critical fails the gate on the open critical (exit 1)"
else
  bad "red gate exits $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- notes.txt 2>/dev/null || true
  printf 'top\nanother line\nbottom\n' > notes.txt
)
run_review fetch-local acme/app gateok
printf '%s' "$gate_body" | sed 's/RECIPEBODY/Nit: clarify this line./; s/RECIPESEV/informational/' \
  > "$EVAL_SANDBOX/.review/local-gateok/review.json"
run_review record acme/app local-gateok --json
if [ "$EVAL_STATUS" -ne 0 ]; then
  bad "record exits $EVAL_STATUS on the informational review"
else
  good "record merges the informational review"
fi
run_review report acme/app local-gateok --check critical
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "gate passes" "$EVAL_ERR"; then
  good "report --check critical passes the gate with no critical findings (exit 0)"
else
  bad "green gate exits $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi

(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- notes.txt 2>/dev/null || true
)
rm -rf "$EVAL_SANDBOX/.review" "$EVAL_SANDBOX/xdg-empty"
unset XDG_CONFIG_HOME || true

eval_end
