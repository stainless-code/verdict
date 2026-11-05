#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_BUDGET_USD REVIEW_MODEL || true

PROMPT="$EVAL_SKILL/prompts/review.md"

if grep -Fq 'chunks/manifest.json' "$PROMPT"; then
  good "prompt names the chunk manifest as the loop entry point"
else
  bad "prompt never names chunks/manifest.json"
fi
if grep -Fq 'each `chunks/chunk-NN.diff` end-to-end against its own' "$PROMPT" \
  && grep -Fq 'chunks/chunk-NN.toc.txt' "$PROMPT"; then
  good "prompt reads each chunk diff fully against its own toc checklist"
else
  bad "prompt chunk read loop is missing or vague"
fi
if grep -Fq 'an unread chunk is as visible as an unread TOC range' "$PROMPT"; then
  good "prompt pins unread-chunk visibility like an unread TOC range"
else
  bad "prompt misses the unread-chunk visibility rule"
fi
if grep -Fq 'chunk-01.diff: /path/.review/N/chunks/chunk-01.diff' "$PROMPT" \
  && grep -Fq 'contract is unchanged' "$PROMPT"; then
  good "prompt names chunk diff paths like full diffs, lens contract unchanged"
else
  bad "prompt lens chunk-path naming is missing"
fi
if grep -Fq 'one document total across all chunks, never one per chunk' "$PROMPT"; then
  good "prompt pins exactly one synthesized review document across chunks"
else
  bad "prompt misses the exactly-one-document synthesis rule"
fi
if grep -Fq 'sum every rung count across all chunks into the single Basis' "$PROMPT" \
  && grep -Fq 'replaces a disagreeing line' "$PROMPT"; then
  good "prompt sums Basis rungs across chunks and names CLI replacement"
else
  bad "prompt Basis-sum rule is missing"
fi
if grep -Fq 'rungs unchanged' "$PROMPT"; then
  good "prompt keeps evidence rungs unchanged across chunks"
else
  bad "prompt misses the rungs-unchanged pin"
fi

PR=4631
dir="$EVAL_SANDBOX/.review/$PR"
mkdir -p "$dir/chunks"
HEADA="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
BASEB="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
cat > "$dir/PR.json" <<EOF
{"state":"OPEN","headRefOid":"$HEADA","baseRefOid":"$BASEB","author":{"login":"teammate"},"files":[{"path":"a.txt","additions":1,"deletions":0},{"path":"b.txt","additions":1,"deletions":0}]}
EOF
cat > "$dir/full.diff" <<'EOF'
diff --git a/a.txt b/a.txt
index 1111111..2222222 100644
--- a/a.txt
+++ b/a.txt
@@ -1 +1,2 @@
 top
+line-a
diff --git a/b.txt b/b.txt
index 3333333..4444444 100644
--- a/b.txt
+++ b/b.txt
@@ -1 +1,2 @@
 top
+line-b
EOF
head -n 7 "$dir/full.diff" > "$dir/chunks/chunk-01.diff"
tail -n +8 "$dir/full.diff" > "$dir/chunks/chunk-02.diff"
cat > "$dir/chunks/chunk-01.toc.txt" <<'EOF'
# Chunk 1/2 — read ranges as coverage checklist

diff --git a/a.txt b/a.txt

## files
- a.txt
EOF
cat > "$dir/chunks/chunk-02.toc.txt" <<'EOF'
# Chunk 2/2 — read ranges as coverage checklist

diff --git a/b.txt b/b.txt

## files
- b.txt
EOF
printf '## source\n- a.txt\n' > "$dir/chunks/chunk-01.roles.txt"
printf '## source\n- b.txt\n' > "$dir/chunks/chunk-02.roles.txt"
printf 'a.txt\tNEW\t1\t2\nb.txt\tNEW\t1\t2\n' > "$dir/hunks.tsv"
TOTAL="$(wc -c < "$dir/full.diff" | tr -d ' ')"
B1="$(wc -c < "$dir/chunks/chunk-01.diff" | tr -d ' ')"
B2="$(wc -c < "$dir/chunks/chunk-02.diff" | tr -d ' ')"
jq -n --arg hs "$HEADA" --arg bs "$BASEB" \
  --argjson total "$TOTAL" --argjson b1 "$B1" --argjson b2 "$B2" \
  '{schema_version:"1",bundle:".review/4631",
    head_sha:$hs,base_sha:$bs,cap:{bytes:512000,source:null},
    total_bytes:$total,chunk_count:2,
    chunks:[{index:1,name:"chunk-01",files:["a.txt"],estimated_bytes:$b1,bytes:$b1,
      diff:".review/4631/chunks/chunk-01.diff",toc:".review/4631/chunks/chunk-01.toc.txt",roles:".review/4631/chunks/chunk-01.roles.txt"},
      {index:2,name:"chunk-02",files:["b.txt"],estimated_bytes:$b2,bytes:$b2,
      diff:".review/4631/chunks/chunk-02.diff",toc:".review/4631/chunks/chunk-02.toc.txt",roles:".review/4631/chunks/chunk-02.roles.txt"}]}' \
  > "$dir/chunks/manifest.json"

MAN="$dir/chunks/manifest.json"
if [ "$(jq -r '.schema_version' <"$MAN" 2>/dev/null)" = "1" ] \
  && [ "$(jq -r '.head_sha' <"$MAN" 2>/dev/null)" = "$HEADA" ] \
  && [ "$(jq -r '.base_sha' <"$MAN" 2>/dev/null)" = "$BASEB" ] \
  && [ "$(jq -r '.cap.bytes' <"$MAN" 2>/dev/null)" = "512000" ] \
  && [ "$(jq -r '.cap.source' <"$MAN" 2>/dev/null)" = "null" ] \
  && [ "$(jq -r '.total_bytes' <"$MAN" 2>/dev/null)" = "$TOTAL" ] \
  && [ "$(jq -r '.chunk_count' <"$MAN" 2>/dev/null)" = "2" ]; then
  good "manifest carries schema 1, fetched SHAs, cap 512000 + null source, totals"
else
  bad "manifest header: $(jq -c '{schema_version,head_sha,base_sha,cap,total_bytes,chunk_count}' <"$MAN" 2>/dev/null || echo MISSING)"
fi
rm -f "$EVAL_SANDBOX/synth-concat.diff"
cat "$dir"/chunks/chunk-01.diff "$dir"/chunks/chunk-02.diff > "$EVAL_SANDBOX/synth-concat.diff"
if cmp -s "$EVAL_SANDBOX/synth-concat.diff" "$dir/full.diff"; then
  good "ordered chunk diffs concatenate byte-identically to full.diff"
else
  bad "chunk concat differs from full.diff"
fi
missing_refs=0
for key in diff toc roles; do
  for idx in 0 1; do
    ref="$(jq -r ".chunks[$idx].$key" <"$MAN" 2>/dev/null)"
    if [ ! -s "$EVAL_SANDBOX/$ref" ]; then missing_refs=$((missing_refs + 1)); fi
  done
done
if [ "$missing_refs" -eq 0 ]; then
  good "every manifest diff/toc/roles reference resolves to a non-empty file"
else
  bad "$missing_refs manifest reference(s) missing on disk"
fi
if grep -Fq -- '- a.txt' "$dir/chunks/chunk-01.toc.txt" \
  && grep -Fq -- '- b.txt' "$dir/chunks/chunk-02.toc.txt"; then
  good "each chunk toc names its member files (per-chunk checklist)"
else
  bad "chunk tocs miss their member files"
fi

eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Two chunks reviewed, one minor note each.\n> Basis: 0 reproduced · 1 ran · 1 walked · 0 pointed · 0 said · 2 rules cited · 0 verified fixes","approved":false,"comments":[{"path":"a.txt","line":2,"side":"RIGHT","body":"Chunk-01 note: clarify this line.","rigor":"ran","severity":"informational","kb":["synth-rule-one"]},{"path":"b.txt","line":2,"side":"RIGHT","body":"Chunk-02 note: clarify this line.","rigor":"walked","severity":"informational","kb":["synth-rule-two"]}]}
EOF
printf '%s\n' '{"id":"synth-rule-one","status":"active"}' '{"id":"synth-rule-two","status":"active"}' > "$EVAL_SANDBOX/.review/kb.jsonl"
run_review post acme/app "$PR" --dry-run --json
assert_exit "summed Basis post succeeds" 0
assert_stream_nogrep "summed Basis (1 ran + 1 walked, 2 rules) passes with no warning" "$EVAL_ERR" "basis line counts"

eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Two chunks reviewed, one minor note each.\n> Basis: 9 reproduced · 9 ran · 9 walked · 9 pointed · 9 said · 9 rules cited · 9 verified fixes","approved":false,"comments":[{"path":"a.txt","line":2,"side":"RIGHT","body":"Chunk-01 note: clarify this line.","rigor":"ran","severity":"informational","kb":["synth-rule-one"]},{"path":"b.txt","line":2,"side":"RIGHT","body":"Chunk-02 note: clarify this line.","rigor":"walked","severity":"informational","kb":["synth-rule-two"]}]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "basis line counts (9 9 9 9 9 9 9) differ from the computed counts (0 1 1 0 0 2 0) — replaced it (basis-replaced)" "$EVAL_ERR" \
  && grep -Fq -- 'Reviewed with' <<<"$(jq -r '.data.body.footer // empty' <"$EVAL_OUT" 2>/dev/null)"; then
  good "wrong Basis exits 0, warns with review counts, keeps the footer"
else
  bad "mismatch path gave exit $EVAL_STATUS"
fi

eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Two chunks reviewed, one minor note each.\n> Basis: 0 reproduced · 1 ran · 1 walked · 0 pointed · 0 said · 2 rules cited · 0 verified fixes","approved":false,"comments":[{"path":"a.txt","line":2,"side":"RIGHT","body":"Chunk-01 note: clarify this line.","rigor":"ran","severity":"informational","kb":["synth-rule-one"]},{"path":"b.txt","line":2,"side":"RIGHT","body":"Chunk-02 note: clarify this line.","rigor":"walked","severity":"informational","kb":["synth-rule-two"]}]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(stdout_docs)" = "1" ]; then
  good "synthesis emits exactly one JSON envelope"
else
  bad "synthesis stdout holds $(stdout_docs) docs, want exactly 1"
fi
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
case "$event" in
  COMMENT|APPROVE|REQUEST_CHANGES) good "envelope carries a single event ($event)" ;;
  *) bad "envelope event is '$event', want one of COMMENT/APPROVE/REQUEST_CHANGES" ;;
esac
if [ -f "$EVAL_SANDBOX/.review/$PR/findings.dryrun.json" ] \
  && [ -z "$(ls "$dir"/chunks/*.review.json 2>/dev/null || true)" ]; then
  good "exactly one findings file, no per-chunk review files"
else
  bad "findings file or per-chunk review files wrong"
fi

rm -f "$EVAL_SANDBOX/.review/kb.jsonl"
rm -rf "$EVAL_SANDBOX/xdg-empty"
rm -f "$EVAL_SANDBOX/synth-concat.diff"
unset XDG_CONFIG_HOME || true

eval_end
