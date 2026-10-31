#!/usr/bin/env bash
# Case kb-recall: machine-readable recall manifest beside the human note.
#
# Grades (hermetic — crafted KB rows plus a scratch git repo in the sandbox;
# the network-dead gh stub stays first on PATH throughout):
#   R1  manifest carries recalled ids in rank order with the ranker's scores;
#       ids+order agree with kb.txt by construction (single sort, two
#       renderings); kb.txt bytes unchanged by the manifest.
#   R2  scores match the ranker (unjudged 0.5 else hits/judged) and the
#       precision columns in kb.txt; top-3 cap holds; envelope carries both
#       artifacts on a match.
#   R3  absent-on-clean parity: a diff with no scope match leaves neither
#       file and the envelope carries neither key.
#   R4  old-bundle tolerance: a bundle with kb.txt but no manifest still reads
#       fine (fix surfaces kb.txt, exit 0); re-fetch regenerates the manifest
#       with kb.txt bytes identical.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

# Scratch git repo (standalone-safe: init is a no-op when kb.sh already made
# one; unique scope m1recall/ isolates this case from other rules).
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  git config user.email "kb-recall-eval@example.com"
  git config user.name "kb recall eval"
  grep -qxF '.review/' .gitignore 2>/dev/null || echo ".review/" >> .gitignore
  mkdir -p m1recall
  echo "base" > m1recall/a.ts
  git add -A
  git commit -qm m1recall-base 2>/dev/null || true
)

# Four rules on an isolated scope: m1-a judged high, m1-b/c unjudged,
# m1-d judged low. Top-3 must read m1-a, m1-b, m1-c.
run_review learn --rule "M1 high precision rule" --scope "m1recall/*.ts" --id m1-a --json >/dev/null
run_review learn --rule "M1 unjudged rule bee" --scope "m1recall/*.ts" --id m1-b --json >/dev/null
run_review learn --rule "M1 unjudged rule cee" --scope "m1recall/*.ts" --id m1-c --json >/dev/null
run_review learn --rule "M1 low precision rule" --scope "m1recall/*.ts" --id m1-d --json >/dev/null

PR=4360
eval_mk_bundle "$PR"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Hi one.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","kb":["m1-a"],"sig":"e123456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":3,"anchored":true,"body":"Hi two.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","kb":["m1-a"],"sig":"e223456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":4,"anchored":true,"body":"Lo one.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"informational","kb":["m1-d"],"sig":"e323456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":5,"anchored":true,"body":"Lo two.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"informational","kb":["m1-d"],"sig":"e423456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":6,"anchored":true,"body":"Lo three.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"informational","kb":["m1-d"],"sig":"e523456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
run_review triage acme/app "$PR" --sig "e123456789abcdef0123456789abcdef01234567" --status addressed --rationale "confirmed m1 high precision rule fires correctly here indeed" --json >/dev/null
run_review triage acme/app "$PR" --sig "e223456789abcdef0123456789abcdef01234567" --status addressed --rationale "confirmed m1 high precision rule fires correctly here again" --json >/dev/null
run_review triage acme/app "$PR" --sig "e323456789abcdef0123456789abcdef01234567" --status false-positive --rationale "low precision m1 rule misfired on this finding completely here" --json >/dev/null
run_review triage acme/app "$PR" --sig "e423456789abcdef0123456789abcdef01234567" --status false-positive --rationale "low precision m1 rule misfired on this finding completely here" --json >/dev/null
run_review triage acme/app "$PR" --sig "e523456789abcdef0123456789abcdef01234567" --status false-positive --rationale "low precision m1 rule misfired on this finding completely here" --json >/dev/null

# Matching diff for the recall legs.
(
  cd "$EVAL_SANDBOX" || exit 99
  echo "more" >> m1recall/a.ts
)
run_review fetch-local acme/app m1recall1 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ -f "$EVAL_SANDBOX/.review/local-m1recall1/kb.txt" ] \
  && [ -f "$EVAL_SANDBOX/.review/local-m1recall1/kb-recall.json" ]; then
  good "matching diff writes kb.txt + kb-recall.json"
else
  bad "m1recall1 exit $EVAL_STATUS (both files?)"
fi

# --- R1: ids/order agree by construction ---
man_ids="$(jq -r '.[].id' "$EVAL_SANDBOX/.review/local-m1recall1/kb-recall.json" 2>/dev/null | tr '\n' ',' || true)"
txt_ids="$(grep '^# m1-' "$EVAL_SANDBOX/.review/local-m1recall1/kb.txt" 2>/dev/null | cut -d' ' -f2 | tr '\n' ',' || true)"
if [ "$man_ids" = "m1-a,m1-b,m1-c," ] && [ "$txt_ids" = "$man_ids" ]; then
  good "manifest + kb.txt agree on ids/order (m1-a,m1-b,m1-c)"
else
  bad "ids differ manifest='$man_ids' kb.txt='$txt_ids'"
fi
if head -n 1 "$EVAL_SANDBOX/.review/local-m1recall1/kb.txt" | grep -q "^# kb recall"; then
  good "kb.txt keeps its header line byte-identically"
else
  bad "kb.txt header changed"
fi
if [ "$(jq 'length' "$EVAL_SANDBOX/.review/local-m1recall1/kb-recall.json" 2>/dev/null)" = "3" ]; then
  good "manifest top-3 caps"
else
  bad "manifest length is $(jq 'length' "$EVAL_SANDBOX/.review/local-m1recall1/kb-recall.json" 2>/dev/null)"
fi
if [ -f "$EVAL_SANDBOX/.review/local-m1recall1/findings.json" ] \
  || [ -f "$EVAL_SANDBOX/.review/local-m1recall1/review.json" ]; then
  bad "recall minted agent-owned content"
else
  good "recall writes only tool-owned files"
fi

# --- R2: scores vs the note ---
if [ "$(jq -c '[.[] | .score]' "$EVAL_SANDBOX/.review/local-m1recall1/kb-recall.json" 2>/dev/null)" = "[1,0.5,0.5]" ]; then
  good "manifest scores read 1 then neutral 0.5 prior"
else
  bad "manifest scores: $(jq -c '[.[] | .score]' "$EVAL_SANDBOX/.review/local-m1recall1/kb-recall.json" 2>/dev/null)"
fi
if [ "$(jq -r '[.[] | "\(.id) \(.hits)/\(.judged)"] | join(",")' "$EVAL_SANDBOX/.review/local-m1recall1/kb-recall.json" 2>/dev/null)" = "m1-a 2/2,m1-b 0/0,m1-c 0/0" ]; then
  good "manifest hits/judged match the credited counts"
else
  bad "manifest counts: $(jq -c '.' "$EVAL_SANDBOX/.review/local-m1recall1/kb-recall.json" 2>/dev/null)"
fi
prec_a="$(grep '^# m1-a ' "$EVAL_SANDBOX/.review/local-m1recall1/kb.txt" 2>/dev/null | grep -o 'precision [0-9][0-9]*/[0-9][0-9]*' || true)"
prec_b="$(grep '^# m1-b ' "$EVAL_SANDBOX/.review/local-m1recall1/kb.txt" 2>/dev/null | grep -o 'precision [0-9][0-9]*/[0-9][0-9]*' || true)"
if [ "$prec_a" = "precision 2/2" ] && [ "$prec_b" = "precision 0/0" ]; then
  good "kb.txt precision columns match manifest hits/judged"
else
  bad "kb.txt precisions: '$prec_a' / '$prec_b'"
fi
if jq -e '.data.artifacts | has("kb.txt") and has("kb-recall.json")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "envelope carries both kb.txt and kb-recall.json"
else
  bad "envelope missing a recall artifact"
fi

# kb.txt bytes identical with/without the manifest: hash survives a
# manifest delete + re-fetch of the same diff.
kb_hash_before="$(shasum -a 1 "$EVAL_SANDBOX/.review/local-m1recall1/kb.txt" | cut -d' ' -f1)"
rm -f "$EVAL_SANDBOX/.review/local-m1recall1/kb-recall.json"
run_review fetch-local acme/app m1recall1 --json >/dev/null
kb_hash_after="$(shasum -a 1 "$EVAL_SANDBOX/.review/local-m1recall1/kb.txt" | cut -d' ' -f1)"
if [ "$kb_hash_before" = "$kb_hash_after" ] \
  && [ -f "$EVAL_SANDBOX/.review/local-m1recall1/kb-recall.json" ]; then
  good "kb.txt bytes identical across manifest regeneration"
else
  bad "kb.txt hash moved ($kb_hash_before vs $kb_hash_after)"
fi

# --- R3: absent-on-clean parity ---
(
  cd "$EVAL_SANDBOX" || exit 99
  git add -A
  git commit -qm m1recall-done 2>/dev/null || true
  echo "readme" > README-m1.md
)
run_review fetch-local acme/app m1recall-clean --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ ! -f "$EVAL_SANDBOX/.review/local-m1recall-clean/kb.txt" ] \
  && [ ! -f "$EVAL_SANDBOX/.review/local-m1recall-clean/kb-recall.json" ] \
  && ! jq -e '.data.artifacts | has("kb.txt")' <"$EVAL_OUT" >/dev/null 2>&1 \
  && ! jq -e '.data.artifacts | has("kb-recall.json")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "clean diff leaves neither file nor envelope key"
else
  bad "clean parity broke (exit $EVAL_STATUS)"
fi
(
  cd "$EVAL_SANDBOX" || exit 99
  rm -f README-m1.md
  git add -A
  git commit -qm m1recall-tidy 2>/dev/null || true
)

# --- R4: old-bundle tolerance (kb.txt, no manifest) ---
OLD="$EVAL_SANDBOX/.review/local-oldm1"
mkdir -p "$OLD"
cat > "$OLD/source.json" <<'EOF'
{"schema_version":"1","kind":"worktree","repo":"acme/app","slug":"oldm1","base_ref":null,"head_ref":"work","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_sha":"","range":"HEAD:worktree","dirty":true,"diff_sha256":"0","files":1,"additions":1,"deletions":1,"tool_version":"0.0.0-test","model":null,"created_at":"2026-01-01T00:00:00Z"}
EOF
cat > "$OLD/full.diff" <<'EOF'
diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 top
-old line
+new line
 bottom
EOF
cp "$EVAL_SANDBOX/.review/local-m1recall1/kb.txt" "$OLD/kb.txt"
rm -f "$OLD/kb-recall.json"
cat > "$OLD/findings.json" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Old bundle finding here.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"f123456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
run_review fix acme/app local-oldm1 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.data.kb.content | contains("m1-a")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "old bundle (kb.txt, no manifest) still fixes fine"
else
  bad "old-bundle fix gave exit $EVAL_STATUS"
fi

if [ "$fails" -ne 0 ]; then echo "case kbmanifest: FAIL ($fails)"; exit 1; fi
echo "case kbmanifest: PASS"
