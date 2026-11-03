#!/usr/bin/env bash
# Case fetch-worktree: fetch-local --worktree builds the bundle from a
# temporary isolated checkout (ticket 11).
#
# Grades (all hermetic — sandbox git repo, real git, stub gh first on
# PATH, no network):
#   W1  dirty isolation: uncommitted changes land in the bundle, the
#       human stream names the checkout location, and the run leaves no
#       worktree behind, no stash entry, and the working tree undisturbed.
#   W2  committed isolation: a --range bundle built with --worktree reads
#       byte-identical to the committed endpoint diff even with live-tree
#       dirt present, and the dirt survives untouched.
#   W3  staged mode: --staged --worktree materializes the index tree and
#       cleans up.
#   W4  refusal hygiene: a bad range with --worktree refuses without
#       creating a checkout or a bundle (mid-run failures are graded by
#       the scripted-argv unit tests, which force the add and the diff to
#       fail and assert the remove still fires).
#   W5  default unchanged: without the flag no worktree line prints and
#       no checkout is ever created, and the bundle reads identical bytes
#       to the isolated run.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

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
  git config user.email "worktree@example.com"
  git config user.name "worktree case"
  printf 'line one\nline two\nline three\n' > a.txt
  git add a.txt
  git commit -qm base 2>/dev/null || true
  printf 'line one\nline TWO\nline three\n' > a.txt
)
worktrees() { ( cd "$EVAL_SANDBOX" && git worktree list --porcelain 2>/dev/null | grep -c '^worktree ' || true ); }
status_a() { ( cd "$EVAL_SANDBOX" && git status --porcelain -- a.txt || true ); }

# --- W1: dirty isolation ---
BEFORE="$(status_a)"
run_review fetch-local acme/app wtdirty --worktree
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "line TWO" "$EVAL_SANDBOX/.review/local-wtdirty/full.diff" \
  && grep -q "worktree: .review/.worktree-wtdirty (temporary isolated checkout; removed on exit)" "$EVAL_OUT"; then
  good "dirty --worktree bundles the uncommitted change and reports the checkout location"
else
  bad "dirty --worktree exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
cp "$EVAL_SANDBOX/.review/local-wtdirty/full.diff" "$EVAL_SANDBOX/wt-dirty.diff"
if [ "$(worktrees)" = "1" ] \
  && [ ! -e "$EVAL_SANDBOX/.review/.worktree-wtdirty" ] \
  && [ -z "$(cd "$EVAL_SANDBOX" && git stash list)" ] \
  && [ "$(status_a)" = "$BEFORE" ]; then
  good "dirty --worktree leaves no checkout, no stash entry, and the working tree undisturbed"
else
  bad "dirty --worktree leaks state (worktrees: $(worktrees), status: $(status_a))"
fi

# --- W2: committed isolation with live-tree dirt present ---
(
  cd "$EVAL_SANDBOX" || exit 99
  git add a.txt && git commit -qm second 2>/dev/null || true
  WT_B="$(git rev-parse HEAD)"
  WT_A="$(git rev-parse HEAD~1)"
  printf '%s %s\n' "$WT_A" "$WT_B" > "$EVAL_SANDBOX/wt-range.txt"
  git diff "$WT_A" "$WT_B" > "$EVAL_SANDBOX/wt-expected.diff"
  printf 'line one\nline DIRT\nline three\n' > a.txt
)
read -r WT_A WT_B < "$EVAL_SANDBOX/wt-range.txt"
run_review fetch-local acme/app wtrange --range "$WT_A..$WT_B" --worktree
if [ "$EVAL_STATUS" -eq 0 ] \
  && cmp -s "$EVAL_SANDBOX/.review/local-wtrange/full.diff" "$EVAL_SANDBOX/wt-expected.diff"; then
  good "range --worktree reads the committed endpoint diff byte-identically despite live dirt"
else
  bad "range --worktree exits $EVAL_STATUS or the diff drifts from the committed endpoints"
fi
if grep -q "line DIRT" "$EVAL_SANDBOX/.review/local-wtrange/full.diff"; then
  bad "range --worktree leaks live-tree dirt into the bundle"
else
  good "range --worktree excludes live-tree dirt from the bundle"
fi
if [ "$(worktrees)" = "1" ] \
  && [ ! -e "$EVAL_SANDBOX/.review/.worktree-wtrange" ] \
  && grep -q "line DIRT" "$EVAL_SANDBOX/a.txt"; then
  good "range --worktree cleans up and leaves the dirt in place"
else
  bad "range --worktree leaks state or disturbs the dirt"
fi

# --- W3: staged mode ---
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- a.txt 2>/dev/null || true
  printf 'line one\nline STAGED\nline three\n' > a.txt
  git add a.txt
)
run_review fetch-local acme/app wtstaged --staged --worktree
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "line STAGED" "$EVAL_SANDBOX/.review/local-wtstaged/full.diff"; then
  good "staged --worktree bundles the index tree"
else
  bad "staged --worktree exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
if [ "$(worktrees)" = "1" ] && [ ! -e "$EVAL_SANDBOX/.review/.worktree-wtstaged" ]; then
  good "staged --worktree cleans up"
else
  bad "staged --worktree leaves a checkout behind"
fi
( cd "$EVAL_SANDBOX" && git reset -q 2>/dev/null || true )

# --- W4: refusal hygiene ---
run_review fetch-local acme/app wtbad --range deadbeef..cafef00 --worktree
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(worktrees)" = "1" ] \
  && [ ! -e "$EVAL_SANDBOX/.review/local-wtbad" ] \
  && [ ! -e "$EVAL_SANDBOX/.review/.worktree-wtbad" ]; then
  good "bad range with --worktree refuses with no checkout and no bundle"
else
  bad "bad range exits $EVAL_STATUS or leaks state"
fi

# --- W5: default behavior unchanged ---
(
  cd "$EVAL_SANDBOX" || exit 99
  printf 'line one\nline TWO\nline three\n' > a.txt
)
run_review fetch-local acme/app wtdirty --worktree
cp "$EVAL_SANDBOX/.review/local-wtdirty/full.diff" "$EVAL_SANDBOX/wt-current-flag.diff"
run_review fetch-local acme/app wtdirty
if [ "$EVAL_STATUS" -eq 0 ] \
  && ! grep -q "worktree:" "$EVAL_OUT" \
  && cmp -s "$EVAL_SANDBOX/.review/local-wtdirty/full.diff" "$EVAL_SANDBOX/wt-current-flag.diff" \
  && [ "$(worktrees)" = "1" ]; then
  good "without the flag no checkout is created and the bundle bytes match the isolated run"
else
  bad "default run exits $EVAL_STATUS or drifts from the isolated bytes"
fi

# --- cleanup: restore the shared sandbox (fixtures stay, probe state goes) ---
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- a.txt 2>/dev/null || true
)
rm -rf "$EVAL_SANDBOX/.review" "$EVAL_SANDBOX/xdg-empty"
rm -f "$EVAL_SANDBOX"/wt-dirty.diff "$EVAL_SANDBOX"/wt-current-flag.diff "$EVAL_SANDBOX"/wt-expected.diff "$EVAL_SANDBOX"/wt-range.txt
unset XDG_CONFIG_HOME || true

if [ "$fails" -ne 0 ]; then echo "case fetch-worktree: FAIL ($fails)"; exit 1; fi
echo "case fetch-worktree: PASS"
