#!/usr/bin/env bash
# Case kb: structured local knowledge base end to end (offline).
#
# Grades (hermetic — crafted PR bundles plus a scratch git repo in the
# sandbox; the network-dead gh stub stays first on PATH throughout):
#   K1  learn appends a rule row (zeros + status active); exact-duplicate
#       rule text refuses as already-learned; bad rigor refuses as bad-rigor;
#       bad id refuses as bad-id; taken id refuses as id-taken.
#   K2  citation warn-and-drop: unknown/malformed/retired ids warn per
#       element and drop; the valid id normalizes into the findings entry
#       (and stays out of the sig).
#   K3  flip credits: human triage addressed/false-positive bumps hits/
#       false_hits + judged, stamps kb_credit history, appends joins rows;
#       re-flip is idempotent; --dry-run credits nothing.
#   K4  retirement fires at the threshold (tombstone kept, warnings land);
#       invalid threshold config warns and falls back to defaults.
#   K5  recall: fetch-local writes kb.txt for matching diffs (+ artifacts
#       entry); clean diffs leave no file; no kb.jsonl reads byte-identical
#       (no kb.txt, no envelope key).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

# Scratch git repo for the fetch-local recall legs. Everything git runs in a
# subshell pinned to the sandbox cwd — never the caller's cwd, never the
# repo checkout under test (a stray `git add` there would stage the harness).
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q
  git config user.email "kb-eval@example.com"
  git config user.name "kb eval"
  echo ".review/" > .gitignore
  mkdir -p src docs
  echo "console.log(1)" > src/app.ts
  echo "# docs" > docs/guide.md
  git add -A
  git commit -qm init
)

# --- K1: learn append + refusals ---
run_review learn --rule "Check error paths on every await" --scope "src/*.ts" --provenance manual --rigor walked --id await-err --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "learn append exits 0"
else
  bad "learn append exits $EVAL_STATUS"
fi
row="$(cat "$EVAL_SANDBOX/.review/kb.jsonl" 2>/dev/null || true)"
if [ "$(printf '%s' "$row" | jq -s -r '.[0] | "\(.id) \(.provenance) \(.rigor) \(.hits)/\(.false_hits)/\(.judged) \(.status)"')" = "await-err manual walked 0/0/0 active" ]; then
  good "learn appends a zeroed active row"
else
  bad "kb.jsonl row is '$row'"
fi
run_review learn --rule "Check error paths on every await" --scope "src/*.ts" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "already-learned" ]; then
  good "exact-duplicate rule text refuses as already-learned"
else
  bad "duplicate gave exit $EVAL_STATUS code '$code'"
fi
run_review learn --rule "Another rule text here" --scope "src/*.ts" --rigor legendary --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "bad-rigor" ]; then
  good "bad rigor refuses as bad-rigor"
else
  bad "bad rigor gave exit $EVAL_STATUS code '$code'"
fi
run_review learn --rule "Yet another rule text" --scope "src/*.ts" --id "Bad ID!" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "bad-id" ]; then
  good "bad id refuses as bad-id"
else
  bad "bad id gave exit $EVAL_STATUS code '$code'"
fi
run_review learn --rule "Totally different rule text" --scope "src/*.ts" --id await-err --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "id-taken" ]; then
  good "taken id refuses as id-taken"
else
  bad "taken id gave exit $EVAL_STATUS code '$code'"
fi
if grep -q "try: " "$EVAL_OUT" 2>/dev/null || jq -e '.error.message | contains("try:")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "refusals carry try: hints"
else
  bad "refusals missing try: hints"
fi

# A retired rule for the citation-drop leg.
run_review learn --rule "Docs need examples" --scope "docs/*.md" --id docs-ex --json >/dev/null
run_review learn --retire docs-ex --reason "this rule fires on every docs touch and is pure noise now" --json >/dev/null
if [ "$(jq -s -r '.[] | select(.id == "docs-ex") | .status' "$EVAL_SANDBOX/.review/kb.jsonl")" = "retired" ]; then
  good "learn --retire tombstones the rule"
else
  bad "retire did not tombstone docs-ex"
fi
run_review learn --status --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.rules | length' <"$EVAL_OUT")" = "2" ] \
  && [ "$(jq -r '.data.rules[] | select(.id == "docs-ex") | .precision' <"$EVAL_OUT")" = "n/a" ]; then
  good "learn --status lists rules with precision"
else
  bad "learn --status exit $EVAL_STATUS"
fi

# --- K2: citation warn-and-drop via post --dry-run (record refuses
# GitHub-shaped bundles; dry-run still normalizes + warns, credits nothing) ---
PR=4251
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> [!IMPORTANT]\n> Real issue below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Unhandled await error path here.","rigor":"walked","severity":"important","kb":["await-err","nope-missing",123,"docs-ex"]},{"path":"notes.txt","line":2,"side":"RIGHT","body":"Second comment, no cites at all.","rigor":"pointed","severity":"informational"}]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "post --dry-run with mixed kb cites exits 0"
else
  bad "post --dry-run exits $EVAL_STATUS"
fi
kb1="$(jq -r '.[0].kb | join(",")' "$EVAL_SANDBOX/.review/$PR/findings.dryrun.json")"
kb2="$(jq -r '.[1].kb | join(",")' "$EVAL_SANDBOX/.review/$PR/findings.dryrun.json")"
if [ "$kb1" = "await-err" ] && [ "$kb2" = "" ]; then
  good "valid id kept, uncited reads []"
else
  bad "findings kb is '$kb1' / '$kb2'"
fi
for w in "unknown kb rule 'nope-missing'" "malformed kb id" "retired kb rule 'docs-ex'"; do
  if grep -q "$w" "$EVAL_ERR"; then
    good "warn-and-drop names: $w"
  else
    bad "stderr missing warn: $w"
  fi
done
sig1="0123456789abcdef0123456789abcdef01234567"
sig2="123456789abcdef0123456789abcdef012345678"
eval_write_findings "$PR" <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Unhandled await error path here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","kb":["await-err"],"sig":"$sig1","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Second comment, no cites at all.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"$sig2","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF

# --- K3: flip credits, idempotency, dry-run ---
run_review triage acme/app "$PR" --sig "$sig1" --status addressed --rationale "verified the await path now handles errors, fix confirmed" --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "triage addressed exits 0"
else
  bad "triage addressed exits $EVAL_STATUS"
fi
if [ "$(jq -s -r '.[] | select(.id == "await-err") | "\(.hits)/\(.false_hits)/\(.judged)"' "$EVAL_SANDBOX/.review/kb.jsonl")" = "1/0/1" ]; then
  good "addressed flip credits hits+1, judged+1"
else
  bad "counters after addressed: $(cat "$EVAL_SANDBOX/.review/kb.jsonl")"
fi
if jq -e '.[0].history[-1] | has("kb_credit")' "$EVAL_SANDBOX/.review/$PR/findings.json" >/dev/null 2>&1; then
  good "flip history gains kb_credit"
else
  bad "flip history missing kb_credit"
fi
if [ "$(jq -c '.[0].history[-1].kb_credit' "$EVAL_SANDBOX/.review/$PR/findings.json")" = '[{"rule":"await-err","to":"addressed"}]' ]; then
  good "kb_credit stamp carries {rule, to}"
else
  bad "kb_credit shape: $(jq -c '.[0].history[-1].kb_credit' "$EVAL_SANDBOX/.review/$PR/findings.json")"
fi
if [ "$(wc -l <"$EVAL_SANDBOX/.review/kb-joins.jsonl" | tr -d ' ')" = "1" ]; then
  good "one audit joins row appended"
else
  bad "joins rows: $(cat "$EVAL_SANDBOX/.review/kb-joins.jsonl" 2>/dev/null)"
fi
run_review triage acme/app "$PR" --sig "$sig1" --status addressed --rationale "second look, still addressed, nothing new here at all" --json >/dev/null
if [ "$(jq -s -r '.[] | select(.id == "await-err") | "\(.hits)/\(.false_hits)/\(.judged)"' "$EVAL_SANDBOX/.review/kb.jsonl")" = "1/0/1" ]; then
  good "re-flip is idempotent (no double credit)"
else
  bad "re-flip moved counters"
fi
run_review triage acme/app "$PR" --sig "$sig2" --status false-positive --rationale "dry run should not touch any counters at all here" --dry-run --json >/dev/null
if [ "$(jq -s -r '.[] | select(.id == "await-err") | "\(.hits)/\(.false_hits)/\(.judged)"' "$EVAL_SANDBOX/.review/kb.jsonl")" = "1/0/1" ]; then
  good "dry-run credits nothing"
else
  bad "dry-run moved counters"
fi

# --- K4: retirement at threshold, invalid config falls back ---
run_review learn --rule "Doom rule that always misfires" --scope "src/*" --id kb-doom --json >/dev/null
PR=4252
eval_mk_bundle "$PR"
dsig="223456789abcdef0123456789abcdef012345678"
eval_write_findings "$PR" <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Doom cited comment here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"critical","kb":["kb-doom"],"sig":"$dsig","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
REVIEW_KB_MIN_JUDGED=1 REVIEW_KB_MAX_FP_RATE=0 run_review triage acme/app "$PR" --sig "$dsig" --status false-positive --rationale "re-read twice, this doom rule misfired completely here" --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "threshold flip exits 0 (never fails the flip)"
else
  bad "threshold flip exits $EVAL_STATUS"
fi
if [ "$(jq -s -r '.[] | select(.id == "kb-doom") | .status' "$EVAL_SANDBOX/.review/kb.jsonl")" = "retired" ] \
  && jq -s -e '.[] | select(.id == "kb-doom") | has("retiredAt") and has("retireReason")' "$EVAL_SANDBOX/.review/kb.jsonl" >/dev/null 2>&1; then
  good "threshold retires with a tombstone (line kept)"
else
  bad "no tombstone: $(grep kb-doom "$EVAL_SANDBOX/.review/kb.jsonl")"
fi
if grep -q "auto-retired" "$EVAL_ERR" && jq -e '.warnings | join(" ") | contains("auto-retired")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "auto-retire warns on stderr and in the envelope"
else
  bad "auto-retire warnings missing"
fi
run_review learn --rule "Safe rule for config test" --scope "src/*" --id kb-safe --json >/dev/null
PR=4253
eval_mk_bundle "$PR"
ssig="323456789abcdef0123456789abcdef012345678"
eval_write_findings "$PR" <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Safe cited comment here.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":["kb-safe"],"sig":"$ssig","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
REVIEW_KB_MIN_JUDGED=bogus REVIEW_KB_MAX_FP_RATE=99 run_review triage acme/app "$PR" --sig "$ssig" --status false-positive --rationale "re-read twice, this safe rule misfired completely here" --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -s -r '.[] | select(.id == "kb-safe") | .status' "$EVAL_SANDBOX/.review/kb.jsonl")" = "active" ] \
  && grep -q "ignoring invalid REVIEW_KB_MIN_JUDGED" "$EVAL_ERR" \
  && grep -q "ignoring invalid REVIEW_KB_MAX_FP_RATE" "$EVAL_ERR"; then
  good "invalid threshold config warns + falls back to defaults (flip proceeds, no retire)"
else
  bad "invalid config handling broke (exit $EVAL_STATUS)"
fi

# --- K5: recall on fetch-local ---
(
  cd "$EVAL_SANDBOX" || exit 99
  echo "change" >> src/app.ts
)
run_review fetch-local acme/app kbrecall1 --json
if [ "$EVAL_STATUS" -eq 0 ] && [ -f "$EVAL_SANDBOX/.review/local-kbrecall1/kb.txt" ]; then
  good "matching diff writes kb.txt"
else
  bad "kbrecall1 exit $EVAL_STATUS (kb.txt missing?)"
fi
if grep -q "await-err" "$EVAL_SANDBOX/.review/local-kbrecall1/kb.txt" \
  && ! grep -q "docs-ex" "$EVAL_SANDBOX/.review/local-kbrecall1/kb.txt"; then
  good "recall keeps active matches, excludes retired"
else
  bad "kb.txt content: $(cat "$EVAL_SANDBOX/.review/local-kbrecall1/kb.txt" 2>/dev/null)"
fi
if jq -e '.data.artifacts | has("kb.txt")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "envelope carries the kb.txt artifact"
else
  bad "envelope missing kb.txt artifact"
fi
# A diff touching nothing scoped leaves no file.
(
  cd "$EVAL_SANDBOX" || exit 99
  git add -A
  git commit -qm second
  echo "# readme" > README.md
  git add -A
  git commit -qm readme
  echo "more" >> README.md
)
run_review fetch-local acme/app kbrecall2 --json
if [ "$EVAL_STATUS" -eq 0 ] && [ ! -f "$EVAL_SANDBOX/.review/local-kbrecall2/kb.txt" ]; then
  good "clean diff leaves no kb.txt"
else
  bad "kbrecall2 exit $EVAL_STATUS (unexpected kb.txt?)"
fi
# No kb.jsonl reads byte-identical: no file, no envelope key.
mv "$EVAL_SANDBOX/.review/kb.jsonl" "$EVAL_SANDBOX/.review/kb.jsonl.hold"
run_review fetch-local acme/app kbrecall3 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ ! -f "$EVAL_SANDBOX/.review/local-kbrecall3/kb.txt" ] \
  && ! jq -e '.data.artifacts | has("kb.txt")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "no kb.jsonl: zero new files, zero new envelope keys"
else
  bad "byte-identical defaults broke (exit $EVAL_STATUS)"
fi
mv "$EVAL_SANDBOX/.review/kb.jsonl.hold" "$EVAL_SANDBOX/.review/kb.jsonl"

# --- K6: record-path credit (verification + agent triage[] share
# validate_and_merge with post) ---
mkdir -p "$EVAL_SANDBOX/.review/local-k6"
cat > "$EVAL_SANDBOX/.review/local-k6/source.json" <<'EOF'
{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}
EOF
cat > "$EVAL_SANDBOX/.review/local-k6/full.diff" <<'EOF'
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
eval_write_review() { :; } 2>/dev/null || true
cat > "$EVAL_SANDBOX/.review/local-k6/review.json" <<'EOF'
{"schema_version":"1","body":"> [!IMPORTANT]\n> Real record-path issue below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Rec verify me.","rigor":"walked","severity":"important","kb":["rec-rule"]},{"path":"notes.txt","line":3,"side":"RIGHT","body":"Rec triage me.","rigor":"pointed","severity":"informational","kb":["rec-rule"]}]}
EOF
run_review learn --rule "Record path rule" --scope "notes.txt" --id rec-rule --json >/dev/null
run_review record acme/local-k6 local-k6 --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "record creates findings without credit"
else
  bad "record exits $EVAL_STATUS"
fi
if [ "$(jq -s -r '.[] | select(.id == "rec-rule") | "\(.hits)/\(.false_hits)/\(.judged)"' "$EVAL_SANDBOX/.review/kb.jsonl")" = "0/0/0" ]; then
  good "record without flips credits nothing"
else
  bad "record credited without flips"
fi
sigA="$(jq -r '.[0].sig' "$EVAL_SANDBOX/.review/local-k6/findings.json")"
sigB="$(jq -r '.[1].sig' "$EVAL_SANDBOX/.review/local-k6/findings.json")"
cat > "$EVAL_SANDBOX/.review/local-k6/review.json" <<EOF
{"schema_version":"1","body":"> [!IMPORTANT]\n> Second pass with evidence.","approved":false,"comments":[],"triage":[{"sig":"$sigB","status":"addressed","rationale":"agent triage with a long enough rationale here for sure"}]}
EOF
cat > "$EVAL_SANDBOX/.review/local-k6/verification.json" <<EOF
[{"sig":"$sigA","verdict":"VERIFIED","resultHead":"aaaaaaa","verifyCmd":"true"}]
EOF
run_review record acme/local-k6 local-k6 --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "second record with verification + triage exits 0"
else
  bad "second record exits $EVAL_STATUS"
fi
if [ "$(jq -s -r '.[] | select(.id == "rec-rule") | "\(.hits)/\(.false_hits)/\(.judged)"' "$EVAL_SANDBOX/.review/kb.jsonl")" = "2/0/2" ]; then
  good "verification-gated + agent-triage flips both credit in one commit"
else
  bad "record-path counters: $(jq -s -r '.[] | select(.id == "rec-rule") | "\(.hits)/\(.false_hits)/\(.judged)"' "$EVAL_SANDBOX/.review/kb.jsonl")"
fi

# --- K7: kb stays out of the sig ---
eval_write_review 4251 <<'EOF'
{"schema_version":"1","body":"> [!IMPORTANT]\n> Real issue below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Unhandled await error path here.","rigor":"walked","severity":"important"},{"path":"notes.txt","line":2,"side":"RIGHT","body":"Second comment, no cites at all.","rigor":"pointed","severity":"informational"}]}
EOF
run_review post acme/app 4251 --dry-run --json >/dev/null
sig_nokb="$(jq -r '.[0].sig' "$EVAL_SANDBOX/.review/4251/findings.dryrun.json")"
if [ "$sig_nokb" = "$sig1" ]; then
  good "same body with and without kb cites shares the sig"
else
  bad "sig changed with kb: $sig1 vs $sig_nokb"
fi

# --- K8: neutral-prior ordering, top-3 cap, tiebreaks (isolated scope so
# earlier legs' rules cannot match) ---
run_review learn --rule "Order high precision rule" --scope "ord/*.ts" --id ord-a --json >/dev/null
run_review learn --rule "Order unjudged rule bee" --scope "ord/*.ts" --id ord-b --json >/dev/null
run_review learn --rule "Order unjudged rule cee" --scope "ord/*.ts" --id ord-c --json >/dev/null
run_review learn --rule "Order low precision rule" --scope "ord/*.ts" --id ord-d --json >/dev/null
PR=4254
eval_mk_bundle "$PR"
osig1="423456789abcdef0123456789abcdef012345678"
osig2="523456789abcdef0123456789abcdef012345678"
osig3="623456789abcdef0123456789abcdef012345678"
osig4="723456789abcdef0123456789abcdef012345678"
osig5="823456789abcdef0123456789abcdef012345678"
eval_write_findings "$PR" <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Hi one.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","kb":["ord-a"],"sig":"$osig1","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":3,"anchored":true,"body":"Hi two.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","kb":["ord-a"],"sig":"$osig2","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":4,"anchored":true,"body":"Lo one.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"informational","kb":["ord-d"],"sig":"$osig3","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":5,"anchored":true,"body":"Lo two.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"informational","kb":["ord-d"],"sig":"$osig4","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":6,"anchored":true,"body":"Lo three.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"informational","kb":["ord-d"],"sig":"$osig5","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
run_review triage acme/app "$PR" --sig "$osig1" --status addressed --rationale "confirmed high precision rule fires correctly here indeed" --json >/dev/null
run_review triage acme/app "$PR" --sig "$osig2" --status addressed --rationale "confirmed high precision rule fires correctly here again" --json >/dev/null
run_review triage acme/app "$PR" --sig "$osig3" --status false-positive --rationale "low precision rule misfired on this finding completely" --json >/dev/null
run_review triage acme/app "$PR" --sig "$osig4" --status false-positive --rationale "low precision rule misfired on this finding completely" --json >/dev/null
run_review triage acme/app "$PR" --sig "$osig5" --status false-positive --rationale "low precision rule misfired on this finding completely" --json >/dev/null
(
  cd "$EVAL_SANDBOX" || exit 99
  mkdir -p ord
  echo "ord" > ord/x.ts
  git add -A
  git commit -qm ord
  echo "more" >> ord/x.ts
)
run_review fetch-local acme/app kbrecall4 --json >/dev/null
kb_order="$(grep '^# ord-' "$EVAL_SANDBOX/.review/local-kbrecall4/kb.txt" 2>/dev/null | cut -d' ' -f2 | tr '\n' ',' || true)"
if [ "$kb_order" = "ord-a,ord-b,ord-c," ]; then
  good "recall orders judged-precision first, unjudged at neutral prior, top-3 caps"
else
  bad "kb.txt order: '$kb_order'"
fi

# --- K9: adopted twins credit once (cross-bundle idempotency) ---
run_review learn --rule "Twin rule for adopted pairs" --scope "notes.txt" --id twin-r --json >/dev/null
tsig="933456789abcdef0123456789abcdef012345678"
for twin in 4260 4261; do
  eval_mk_bundle "$twin"
  eval_write_findings "$twin" <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Twin cited comment here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","kb":["twin-r"],"sig":"$tsig","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
done
run_review triage acme/app 4260 --sig "$tsig" --status addressed --rationale "first twin bundle confirms the twin rule here for sure" --json >/dev/null
run_review triage acme/app 4261 --sig "$tsig" --status addressed --rationale "second twin bundle confirms the twin rule here as well" --json >/dev/null
if [ "$(jq -s -r '.[] | select(.id == "twin-r") | "\(.hits)/\(.false_hits)/\(.judged)"' "$EVAL_SANDBOX/.review/kb.jsonl")" = "1/0/1" ] \
  && [ "$(grep -c "\"sig\":\"$tsig\",\"rule\":\"twin-r\"" "$EVAL_SANDBOX/.review/kb-joins.jsonl")" = "1" ]; then
  good "adopted twins credit once (joins log dedups across bundles)"
else
  bad "twin counters/joins: $(jq -s -r '.[] | select(.id == "twin-r") | "\(.hits)/\(.false_hits)/\(.judged)"' "$EVAL_SANDBOX/.review/kb.jsonl") / $(grep -c twin-r "$EVAL_SANDBOX/.review/kb-joins.jsonl" || true) rows"
fi

# --- K10: restore needs its rationale too ---
run_review learn --restore docs-ex --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "bad-reason" ]; then
  good "restore without reason refuses as bad-reason"
else
  bad "restore without reason gave exit $EVAL_STATUS code '$code'"
fi
run_review learn --restore docs-ex --reason "rechecked, this docs rule caught a real miss last week" --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -s -r '.[] | select(.id == "docs-ex") | .status' "$EVAL_SANDBOX/.review/kb.jsonl")" = "active" ] \
  && jq -s -e '.[] | select(.id == "docs-ex") | (.history[-1].note // "") | contains("rechecked")' "$EVAL_SANDBOX/.review/kb.jsonl" >/dev/null 2>&1; then
  good "restore with reason reactivates and logs the note"
else
  bad "restore with reason gave exit $EVAL_STATUS"
fi

if [ "$fails" -ne 0 ]; then echo "case kb: FAIL ($fails)"; exit 1; fi
echo "case kb: PASS"
