#!/usr/bin/env bash
# Case chunk-manual-recipe: the hand-sliced manual recipe survives chunking.
#
# Grades (all hermetic — sandbox git repo, stub gh, no network):
#   M1  opted-out refusal names the manual path: the multi-file over-cap
#       fixture without the knob refuses diff-too-large with the
#       split-or-narrow hint (naming --range/--base/--base-sha and the paths
#       filter), no chunk mention, no chunks dir; --json keeps code +
#       candidates + hint.
#   M2  manual recipe completes for opted-out: hand-sliced range re-fetches
#       (each under the cap) and a paths-narrowed re-fetch all succeed with
#       full bundles (full.diff + toc.txt + hunks.tsv).
#   M3  degenerate refusal names the manual path: one file over the cap with
#       --chunk keeps the advisory refusal (single-file notice + the same
#       split-or-narrow hint) with no chunks dir and no manifest.
#   M4  manual recipe completes for degenerate: the range-half-split re-fetch
#       of the single huge file succeeds under the cap.
#   M5  prose presence: the operator docs present chunking as the opted-in
#       path and keep the manual recipe documented (usage names --chunk).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

# Deterministic surface: no settings file, no ambient REVIEW_* knobs.
mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_BUDGET_USD REVIEW_MODEL || true

# Scratch git repo at the sandbox root (run_review runs with cwd=sandbox).
# Multi-file fixture (ticket-02 shape): 6 files x 1500 changed lines each.
# History is three slice commits so --range hand-slices stay under the cap.
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  git config user.email "chunk-recipe@example.com"
  git config user.name "chunk recipe"
  if [ ! -f .gitignore ]; then
    cat > .gitignore <<'EOF'
.review/
stubbin/
fakebin/
*.log
eval-out.bin
eval-err.txt
case-results.txt
EOF
  fi
  for i in 1 2 3 4 5 6; do
    awk -v f="$i" 'BEGIN{for(j=0;j<1500;j++) printf "base line %06d file%d padding padding padding padding\n", j, f}' > "mrec-f$i.txt"
  done
  git add mrec-f1.txt mrec-f2.txt mrec-f3.txt mrec-f4.txt mrec-f5.txt mrec-f6.txt .gitignore
  git commit -qm mrec-base 2>/dev/null || true
  MREC_BASE="$(git rev-parse HEAD)"
  for i in 1 2; do
    awk -v f="$i" 'BEGIN{for(j=0;j<1500;j++) printf "changed line %06d file%d completely new words words words\n", j, f}' > "mrec-f$i.txt"
  done
  git add mrec-f1.txt mrec-f2.txt
  git commit -qm mrec-slice-a 2>/dev/null || true
  MREC_A="$(git rev-parse HEAD)"
  for i in 3 4; do
    awk -v f="$i" 'BEGIN{for(j=0;j<1500;j++) printf "changed line %06d file%d completely new words words words\n", j, f}' > "mrec-f$i.txt"
  done
  git add mrec-f3.txt mrec-f4.txt
  git commit -qm mrec-slice-b 2>/dev/null || true
  MREC_B="$(git rev-parse HEAD)"
  for i in 5 6; do
    awk -v f="$i" 'BEGIN{for(j=0;j<1500;j++) printf "changed line %06d file%d completely new words words words\n", j, f}' > "mrec-f$i.txt"
  done
  git add mrec-f5.txt mrec-f6.txt
  git commit -qm mrec-slice-c 2>/dev/null || true
  MREC_C="$(git rev-parse HEAD)"
  # Degenerate fixture (ticket-01 shape, single file): appended in halves so
  # the full file is over the cap while each half-range stays under it.
  awk 'BEGIN{for(i=0;i<7500;i++) printf "base line %06d padding padding padding padding padding\n", i}' > mrec-huge.txt
  git add mrec-huge.txt
  git commit -qm mrec-huge-half 2>/dev/null || true
  MREC_H0="$(git rev-parse HEAD)"
  awk 'BEGIN{for(i=7500;i<15000;i++) printf "base line %06d padding padding padding padding padding\n", i}' >> mrec-huge.txt
  git add mrec-huge.txt
  git commit -qm mrec-huge-full 2>/dev/null || true
  MREC_H1="$(git rev-parse HEAD)"
  printf '%s %s %s %s %s %s\n' "$MREC_BASE" "$MREC_A" "$MREC_B" "$MREC_C" "$MREC_H0" "$MREC_H1" > .mrec-shas
)
read -r MREC_BASE MREC_A MREC_B MREC_C MREC_H0 MREC_H1 < "$EVAL_SANDBOX/.mrec-shas"
rm -f "$EVAL_SANDBOX/.mrec-shas"
FULL_DIFF_BYTES="$(cd "$EVAL_SANDBOX" && git diff "$MREC_BASE" "$MREC_C" -- mrec-f1.txt mrec-f2.txt mrec-f3.txt mrec-f4.txt mrec-f5.txt mrec-f6.txt | wc -c | tr -d ' ')"
if [ "$FULL_DIFF_BYTES" -le 512000 ]; then
  bad "fixture diff $FULL_DIFF_BYTES bytes is not over the 512000 cap"
fi

# --- M1: opted-out refusal names the working manual path ---
run_review fetch-local acme/app mrecopt --range "$MREC_BASE..$MREC_C"
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "exceeds cap 512000 bytes" "$EVAL_ERR" \
  && grep -q "try: split the range" "$EVAL_ERR" \
  && grep -q -- "--range" "$EVAL_ERR" \
  && grep -q "paths filter" "$EVAL_ERR" \
  && ! grep -qi "chunk" "$EVAL_ERR"; then
  good "opted-out: multi-file over-cap refuses with the manual split-or-narrow hint, no chunk mention"
else
  bad "opted-out refusal exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
if [ ! -d "$EVAL_SANDBOX/.review/local-mrecopt/chunks" ]; then
  good "opted-out refusal writes no chunks dir"
else
  bad "opted-out refusal left a chunks dir"
fi
run_review fetch-local acme/app mrecopt --range "$MREC_BASE..$MREC_C" --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT")" = "diff-too-large" ] \
  && jq -e '.error.candidates | length > 0' <"$EVAL_OUT" >/dev/null 2>&1 \
  && jq -r '.error.message' <"$EVAL_OUT" | grep -q "try: split the range"; then
  good "--json refusal keeps code + candidates + the manual hint"
else
  bad "--json refusal exits $EVAL_STATUS"
fi

# --- M2: the manual recipe completes for opted-out (hand-sliced ranges + paths) ---
run_review fetch-local acme/app mreca --range "$MREC_BASE..$MREC_A"
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ -s "$EVAL_SANDBOX/.review/local-mreca/full.diff" ] \
  && [ -s "$EVAL_SANDBOX/.review/local-mreca/toc.txt" ] \
  && [ -s "$EVAL_SANDBOX/.review/local-mreca/hunks.tsv" ]; then
  good "hand slice 1/3 (--range base..slice-a) completes with a full bundle"
else
  bad "hand slice 1/3 exits $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi
run_review fetch-local acme/app mrecb --range "$MREC_A..$MREC_B"
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ -s "$EVAL_SANDBOX/.review/local-mrecb/full.diff" ]; then
  good "hand slice 2/3 (--range slice-a..slice-b) completes"
else
  bad "hand slice 2/3 exits $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi
run_review fetch-local acme/app mrecc --range "$MREC_B..$MREC_C"
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ -s "$EVAL_SANDBOX/.review/local-mrecc/full.diff" ]; then
  good "hand slice 3/3 (--range slice-b..slice-c) completes"
else
  bad "hand slice 3/3 exits $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi
# Stitch check: the three hand slices partition the refused full diff.
if [ "$(( $(wc -c <"$EVAL_SANDBOX/.review/local-mreca/full.diff" | tr -d ' ') + $(wc -c <"$EVAL_SANDBOX/.review/local-mrecb/full.diff" | tr -d ' ') + $(wc -c <"$EVAL_SANDBOX/.review/local-mrecc/full.diff" | tr -d ' ') ))" = "$FULL_DIFF_BYTES" ]; then
  good "hand slices partition the refused input (slice bytes sum to the full diff)"
else
  bad "hand-slice sizes do not sum to the refused diff"
fi
run_review fetch-local acme/app mrecpath --range "$MREC_BASE..$MREC_C" -- mrec-f1.txt mrec-f2.txt
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ -s "$EVAL_SANDBOX/.review/local-mrecpath/full.diff" ] \
  && [ "$(wc -c <"$EVAL_SANDBOX/.review/local-mrecpath/full.diff" | tr -d ' ')" -le 512000 ]; then
  good "paths-narrowed re-fetch of the refused range completes under the cap"
else
  bad "paths-narrowed re-fetch exits $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi

# --- M3: degenerate refusal names the manual path ---
# Single huge file, one file over the cap: appended whole (15000 added lines,
# ~900KB) so no file-boundary slice can save it — the degenerate input.
(
  cd "$EVAL_SANDBOX" || exit 99
  awk 'BEGIN{for(i=0;i<15000;i++) printf "changed line %06d completely new words words words words\n", i}' > mrec-huge-1file.txt
  git add mrec-huge-1file.txt
  git commit -qm mrec-1file-full 2>/dev/null || true
  git rev-parse HEAD > .mrec-1f
  git rev-parse 'HEAD^' > .mrec-1f-base
)
MREC_1F="$(cat "$EVAL_SANDBOX/.mrec-1f")"
MREC_1F_BASE="$(cat "$EVAL_SANDBOX/.mrec-1f-base")"
rm -f "$EVAL_SANDBOX/.mrec-1f" "$EVAL_SANDBOX/.mrec-1f-base"
run_review fetch-local acme/app mrecdegen --chunk --range "$MREC_1F_BASE..$MREC_1F" -- mrec-huge-1file.txt
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "single file over cap" "$EVAL_ERR" \
  && grep -q "exceeds cap 512000 bytes" "$EVAL_ERR" \
  && grep -q "try: split the range" "$EVAL_ERR"; then
  good "degenerate: single over-cap file with --chunk keeps the advisory refusal naming the manual path"
else
  bad "degenerate refusal exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
if [ ! -d "$EVAL_SANDBOX/.review/local-mrecdegen/chunks" ] \
  && [ ! -f "$EVAL_SANDBOX/.review/local-mrecdegen/chunks/manifest.json" ]; then
  good "degenerate refusal writes no chunks dir and no manifest (no half-write)"
else
  bad "degenerate refusal left chunk output behind"
fi

# --- M4: the manual recipe completes for degenerate (range-half-split) ---
run_review fetch-local acme/app mrecdeghalf --range "$MREC_H0..$MREC_H1" -- mrec-huge.txt
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ -s "$EVAL_SANDBOX/.review/local-mrecdeghalf/full.diff" ] \
  && [ "$(wc -c <"$EVAL_SANDBOX/.review/local-mrecdeghalf/full.diff" | tr -d ' ')" -le 512000 ]; then
  good "range-half-split re-fetch of the single huge file completes under the cap"
else
  bad "range-half-split exits $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi

# --- M5: prose presence (chunking opted-in path + manual recipe documented) ---
if grep -q "## Over-cap diffs" "$EVAL_ROOT/docs/manual.md" \
  && grep -q "chunks/manifest.json" "$EVAL_ROOT/docs/manual.md" \
  && grep -q -- "--chunk" "$EVAL_ROOT/docs/manual.md" \
  && grep -q "split the range" "$EVAL_ROOT/docs/manual.md" \
  && grep -q '"chunks"' "$EVAL_ROOT/docs/manual.md"; then
  good "operator manual presents the chunked path (knob, manifest, attestation) and the manual recipe"
else
  bad "operator manual misses the chunking/manual-recipe prose"
fi
if grep -q -- "--chunk" "$EVAL_ROOT/scripts/review.sh"; then
  good "usage text names the --chunk opt-in"
else
  bad "usage text misses --chunk"
fi

# --- cleanup: range-mode fetches never touch the worktree, so the tree stays
# clean for later cases; only the harness surface is removed ---
rm -rf "$EVAL_SANDBOX/xdg-empty"
unset XDG_CONFIG_HOME || true

if [ "$fails" -ne 0 ]; then echo "case chunk-manual-recipe: FAIL ($fails)"; exit 1; fi
echo "case chunk-manual-recipe: PASS"
