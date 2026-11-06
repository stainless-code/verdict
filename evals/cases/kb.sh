#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q
  git config user.email "kb-eval@example.com"
  git config user.name "kb eval"
  echo ".verdict/state/" > .gitignore
  mkdir -p src docs
  echo "console.log(1)" > src/app.ts
  echo "# docs" > docs/guide.md
  git add -A
  git commit -qm init
)

run_review learn --rule "Check error paths on every await" --scope "src/*.ts" --provenance manual --rigor walked --id await-err --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "learn append exits 0"
else
  bad "learn append exits $EVAL_STATUS"
fi
row="$(cat "$EVAL_SANDBOX/.verdict/kb.jsonl" 2>/dev/null || true)"
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

run_review learn --rule "Docs need examples" --scope "docs/*.md" --id docs-ex --json >/dev/null
run_review learn --retire docs-ex --reason "this rule fires on every docs touch and is pure noise now" --json >/dev/null
if [ "$(jq -s -r '.[] | select(.id == "docs-ex") | .status' "$EVAL_SANDBOX/.verdict/kb.jsonl")" = "retired" ]; then
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
kb1="$(jq -r '.[0].kb | join(",")' "$EVAL_SANDBOX/.verdict/state/$PR/findings.dryrun.json")"
kb2="$(jq -r '.[1].kb | join(",")' "$EVAL_SANDBOX/.verdict/state/$PR/findings.dryrun.json")"
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

run_review triage acme/app "$PR" --sig "$sig1" --status addressed --rationale "verified the await path now handles errors, fix confirmed" --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "triage addressed exits 0"
else
  bad "triage addressed exits $EVAL_STATUS"
fi
if [ "$(jq -s -r '.[] | select(.id == "await-err") | "\(.hits)/\(.false_hits)/\(.judged)"' "$EVAL_SANDBOX/.verdict/kb.jsonl")" = "1/0/1" ]; then
  good "addressed flip credits hits+1, judged+1"
else
  bad "counters after addressed: $(cat "$EVAL_SANDBOX/.verdict/kb.jsonl")"
fi
if jq -e '.[0].history[-1] | has("kb_credit")' "$EVAL_SANDBOX/.verdict/state/$PR/findings.json" >/dev/null 2>&1; then
  good "flip history gains kb_credit"
else
  bad "flip history missing kb_credit"
fi
if [ "$(jq -c '.[0].history[-1].kb_credit' "$EVAL_SANDBOX/.verdict/state/$PR/findings.json")" = '[{"rule":"await-err","to":"addressed"}]' ]; then
  good "kb_credit stamp carries {rule, to}"
else
  bad "kb_credit shape: $(jq -c '.[0].history[-1].kb_credit' "$EVAL_SANDBOX/.verdict/state/$PR/findings.json")"
fi
if [ "$(wc -l <"$EVAL_SANDBOX/.verdict/kb-joins.jsonl" | tr -d ' ')" = "1" ]; then
  good "one audit joins row appended"
else
  bad "joins rows: $(cat "$EVAL_SANDBOX/.verdict/kb-joins.jsonl" 2>/dev/null)"
fi
run_review triage acme/app "$PR" --sig "$sig1" --status addressed --rationale "second look, still addressed, nothing new here at all" --json >/dev/null
if [ "$(jq -s -r '.[] | select(.id == "await-err") | "\(.hits)/\(.false_hits)/\(.judged)"' "$EVAL_SANDBOX/.verdict/kb.jsonl")" = "1/0/1" ]; then
  good "re-flip is idempotent (no double credit)"
else
  bad "re-flip moved counters"
fi
run_review triage acme/app "$PR" --sig "$sig2" --status false-positive --rationale "dry run should not touch any counters at all here" --dry-run --json >/dev/null
if [ "$(jq -s -r '.[] | select(.id == "await-err") | "\(.hits)/\(.false_hits)/\(.judged)"' "$EVAL_SANDBOX/.verdict/kb.jsonl")" = "1/0/1" ]; then
  good "dry-run credits nothing"
else
  bad "dry-run moved counters"
fi

run_review learn --rule "Doom rule that always misfires" --scope "src/*" --id kb-doom --json >/dev/null
PR=4252
# The retire threshold is a typed default, pinned by the capture.
printf '{"schema_version":"1","defaults":{"kb_policy":{"min_judged":1,"max_fp_rate":0}}}\n' >"$EVAL_SANDBOX/.verdict/config.json"
eval_mk_bundle "$PR"
dsig="223456789abcdef0123456789abcdef012345678"
eval_write_findings "$PR" <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Doom cited comment here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"critical","kb":["kb-doom"],"sig":"$dsig","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
run_review triage acme/app "$PR" --sig "$dsig" --status false-positive --rationale "re-read twice, this doom rule misfired completely here" --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "threshold flip exits 0 (never fails the flip)"
else
  bad "threshold flip exits $EVAL_STATUS"
fi
if [ "$(jq -s -r '.[] | select(.id == "kb-doom") | .status' "$EVAL_SANDBOX/.verdict/kb.jsonl")" = "retired" ] \
  && jq -s -e '.[] | select(.id == "kb-doom") | has("retiredAt") and has("retireReason")' "$EVAL_SANDBOX/.verdict/kb.jsonl" >/dev/null 2>&1; then
  good "threshold retires with a tombstone (line kept)"
else
  bad "no tombstone: $(grep kb-doom "$EVAL_SANDBOX/.verdict/kb.jsonl")"
fi
if grep -q "auto-retired" "$EVAL_ERR" && jq -e '.warnings | join(" ") | contains("auto-retired")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "auto-retire warns on stderr and in the envelope"
else
  bad "auto-retire warnings missing"
fi
# The capture pins the retirement policy. A valid environment override
# outranks the repository default at fetch; a later stage never rebuilds the
# value from ambient settings.
run_review learn --rule "Env floor probe rule" --scope "src/*" --id kb-envfloor --json >/dev/null
run_review learn --rule "Env capture probe rule" --scope "src/*" --id kb-envc --json >/dev/null
run_review learn --rule "Env invalid probe rule" --scope "src/*" --id kb-envinv --json >/dev/null
printf '{"schema_version":"1","defaults":{"kb_policy":{"min_judged":3,"max_fp_rate":0.5}}}\n' >"$EVAL_SANDBOX/.verdict/config.json"
PR=4255
eval_mk_bundle "$PR"
fsig="b23456789abcdef0123456789abcdef012345678"
eval_write_findings "$PR" <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Floor probe comment here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"informational","kb":["kb-envfloor"],"sig":"$fsig","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
REVIEW_KB_MIN_JUDGED=1 REVIEW_KB_MAX_FP_RATE=0 run_review triage acme/app "$PR" --sig "$fsig" --status false-positive --rationale "ambient thresholds must not rewrite the captured policy here" --json
floor="$(jq -s -r '.[] | select(.id == "kb-envfloor") | "\(.hits)/\(.false_hits)/\(.judged) \(.status)"' "$EVAL_SANDBOX/.verdict/kb.jsonl")"
if [ "$floor" = "0/1/1 active" ]; then
  good "the captured repository threshold outranks ambient triage thresholds"
else
  bad "ambient env rewrote the captured policy: '$floor'"
fi

PR=4256
REVIEW_KB_MIN_JUDGED=1 REVIEW_KB_MAX_FP_RATE=0 eval_mk_bundle "$PR"
csig="c23456789abcdef0123456789abcdef012345678"
eval_write_findings "$PR" <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Capture probe comment here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"informational","kb":["kb-envc"],"sig":"$csig","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
run_review triage acme/app "$PR" --sig "$csig" --status false-positive --rationale "captured env threshold retires the rule on this first flip" --json
if [ "$(jq -s -r '.[] | select(.id == "kb-envc") | .status' "$EVAL_SANDBOX/.verdict/kb.jsonl")" = "retired" ]; then
  good "a valid env override outranks the repository default at capture"
else
  bad "captured env override lost to the repository default: $(grep kb-envc "$EVAL_SANDBOX/.verdict/kb.jsonl")"
fi
captured="$(jq -c '.resolved.defaults.values | {min_judged: .kb_min_judged, max_fp_rate: .kb_max_fp_rate}' "$EVAL_SANDBOX/.verdict/state/$PR/run.json" 2>/dev/null || true)"
if [ "$captured" = '{"min_judged":1,"max_fp_rate":0}' ]; then
  good "the flip leaves the captured policy untouched"
else
  bad "captured policy is '$captured', want min_judged 1 / max_fp_rate 0"
fi

printf '{"schema_version":"1","defaults":{"kb_policy":{"min_judged":1,"max_fp_rate":0}}}\n' >"$EVAL_SANDBOX/.verdict/config.json"
eval_commit_controls
REVIEW_KB_MIN_JUDGED=bogus REVIEW_KB_MAX_FP_RATE=99 run_review fetch-local acme/app kb-envinv --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.warnings | join(" ") | contains("ignoring invalid REVIEW_KB_MIN_JUDGED")' <"$EVAL_OUT" >/dev/null 2>&1 \
  && jq -e '.warnings | join(" ") | contains("ignoring invalid REVIEW_KB_MAX_FP_RATE")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "invalid threshold overrides warn at the stage that consumes them"
else
  bad "invalid threshold override warnings missing (exit $EVAL_STATUS): $(jq -c '.warnings' <"$EVAL_OUT" 2>/dev/null)"
fi
isig="d23456789abcdef0123456789abcdef012345678"
eval_write_findings local-kb-envinv <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Invalid probe comment here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"informational","kb":["kb-envinv"],"sig":"$isig","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
run_review triage acme/app local-kb-envinv --sig "$isig" --status false-positive --rationale "invalid overrides fall back to the repository threshold here" --json
if [ "$(jq -s -r '.[] | select(.id == "kb-envinv") | .status' "$EVAL_SANDBOX/.verdict/kb.jsonl")" = "retired" ]; then
  good "invalid overrides fall back to the repository threshold (1/0 retires)"
else
  bad "invalid overrides did not fall back: $(grep kb-envinv "$EVAL_SANDBOX/.verdict/kb.jsonl")"
fi

# The trusted base holds policy, the worktree never does: an uncommitted
# invalid config is not trusted, and committed it refuses bad-config naming
# the field instead of guessing a fallback.
printf '{"schema_version":"1","defaults":{"kb_policy":{"min_judged":"bogus"}}}\n' >"$EVAL_SANDBOX/.verdict/config.json"
run_review fetch-local acme/app kbcfg-dirty --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "" ]; then
  good "an uncommitted invalid config is never trusted policy"
else
  bad "the dirty invalid config changed the capture (exit $EVAL_STATUS)"
fi
eval_commit_controls
run_review fetch-local acme/app kbcfg --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-config" ] \
  && grep -q "kb_policy.min_judged" "$EVAL_OUT"; then
  good "a malformed typed policy refuses bad-config naming the field"
else
  bad "malformed policy gave exit $EVAL_STATUS: $(head -c 300 "$EVAL_OUT")"
fi
rm -f "$EVAL_SANDBOX/.verdict/config.json"

(
  cd "$EVAL_SANDBOX" || exit 99
  echo "change" >> src/app.ts
)
eval_commit_controls
run_review fetch-local acme/app kbrecall1 --json
if [ "$EVAL_STATUS" -eq 0 ] && [ -f "$EVAL_SANDBOX/.verdict/state/local-kbrecall1/kb.txt" ]; then
  good "matching diff writes kb.txt"
else
  bad "kbrecall1 exit $EVAL_STATUS (kb.txt missing?)"
fi
assert_file_grep "recall keeps active matches" "$EVAL_SANDBOX/.verdict/state/local-kbrecall1/kb.txt" "await-err"
assert_file_nogrep "recall excludes retired rules" "$EVAL_SANDBOX/.verdict/state/local-kbrecall1/kb.txt" "docs-ex"
if jq -e '.data.artifacts | has("kb.txt")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "envelope carries the kb.txt artifact"
else
  bad "envelope missing kb.txt artifact"
fi
(
  cd "$EVAL_SANDBOX" || exit 99
  git add -A
  git commit -qm second
  echo "# readme" > README.md
  git add -A
  git commit -qm readme
  echo "more" >> README.md
)
eval_commit_controls
run_review fetch-local acme/app kbrecall2 --json
if [ "$EVAL_STATUS" -eq 0 ] && [ ! -f "$EVAL_SANDBOX/.verdict/state/local-kbrecall2/kb.txt" ]; then
  good "clean diff leaves no kb.txt"
else
  bad "kbrecall2 exit $EVAL_STATUS (unexpected kb.txt?)"
fi
mv "$EVAL_SANDBOX/.verdict/kb.jsonl" "$EVAL_SANDBOX/.verdict/kb.jsonl.hold"
eval_commit_controls
run_review fetch-local acme/app kbrecall3 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ ! -f "$EVAL_SANDBOX/.verdict/state/local-kbrecall3/kb.txt" ] \
  && ! jq -e '.data.artifacts | has("kb.txt")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "no kb.jsonl: zero new files, zero new envelope keys"
else
  bad "byte-identical defaults broke (exit $EVAL_STATUS)"
fi
mv "$EVAL_SANDBOX/.verdict/kb.jsonl.hold" "$EVAL_SANDBOX/.verdict/kb.jsonl"

# The rule has to be in the base the capture pins, not learned after it.
run_review learn --rule "Record path rule" --scope "notes.txt" --id rec-rule --json >/dev/null
eval_mk_bundle local-k6
cat > "$EVAL_SANDBOX/.verdict/state/local-k6/review.json" <<'EOF'
{"schema_version":"1","body":"> [!IMPORTANT]\n> Real record-path issue below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Rec verify me.","rigor":"walked","severity":"important","kb":["rec-rule"]},{"path":"notes.txt","line":3,"side":"RIGHT","body":"Rec triage me.","rigor":"pointed","severity":"informational","kb":["rec-rule"]}]}
EOF
run_review record acme/local-k6 local-k6 --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "record creates findings without credit"
else
  bad "record exits $EVAL_STATUS"
fi
if [ "$(jq -s -r '.[] | select(.id == "rec-rule") | "\(.hits)/\(.false_hits)/\(.judged)"' "$EVAL_SANDBOX/.verdict/kb.jsonl")" = "0/0/0" ]; then
  good "record without flips credits nothing"
else
  bad "record credited without flips"
fi
sigA="$(jq -r '.[0].sig' "$EVAL_SANDBOX/.verdict/state/local-k6/findings.json")"
sigB="$(jq -r '.[1].sig' "$EVAL_SANDBOX/.verdict/state/local-k6/findings.json")"
cat > "$EVAL_SANDBOX/.verdict/state/local-k6/review.json" <<EOF
{"schema_version":"1","body":"> [!IMPORTANT]\n> Second pass with evidence.","approved":false,"comments":[],"triage":[{"sig":"$sigB","status":"addressed","rationale":"agent triage with a long enough rationale here for sure"}]}
EOF
cat > "$EVAL_SANDBOX/.verdict/state/local-k6/verification.json" <<EOF
[{"sig":"$sigA","verdict":"VERIFIED","resultHead":"aaaaaaa","verifyCmd":"true"}]
EOF
run_review record acme/local-k6 local-k6 --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "second record with verification + triage exits 0"
else
  bad "second record exits $EVAL_STATUS"
fi
if [ "$(jq -s -r '.[] | select(.id == "rec-rule") | "\(.hits)/\(.false_hits)/\(.judged)"' "$EVAL_SANDBOX/.verdict/kb.jsonl")" = "2/0/2" ]; then
  good "verification-gated + agent-triage flips both credit in one commit"
else
  bad "record-path counters: $(jq -s -r '.[] | select(.id == "rec-rule") | "\(.hits)/\(.false_hits)/\(.judged)"' "$EVAL_SANDBOX/.verdict/kb.jsonl")"
fi
k6rows="$(jq -s -c --arg a "$sigA" --arg b "$sigB" '[.[] | select(.sig == $a or .sig == $b)]' "$EVAL_SANDBOX/.verdict/kb-joins.jsonl" 2>/dev/null || echo "[]")"
if [ "$(jq -r 'length' <<<"$k6rows")" = "2" ] \
  && [ "$(jq -r '[.[] | select(.bundle != "local-k6" or .review_id != null)] | length' <<<"$k6rows")" = "0" ]; then
  good "verification + agent-triage joins rows carry bundle + null review_id"
else
  bad "K6 joins rows: $k6rows"
fi
# Record enforces the same captured policy: a flip it performs must not
# borrow thresholds from the ambient environment.
run_review learn --rule "Record threshold probe rule" --scope "notes.txt" --id rec-thresh --json >/dev/null
eval_mk_bundle local-k7
cat > "$EVAL_SANDBOX/.verdict/state/local-k7/review.json" <<'EOF'
{"schema_version":"1","body":"> [!IMPORTANT]\n> Record threshold probe below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Record threshold me.","rigor":"walked","severity":"informational","kb":["rec-thresh"]}]}
EOF
run_review record acme/local-k7 local-k7 --json >/dev/null
tsig="$(jq -r '.[0].sig' "$EVAL_SANDBOX/.verdict/state/local-k7/findings.json")"
cat > "$EVAL_SANDBOX/.verdict/state/local-k7/review.json" <<EOF
{"schema_version":"1","body":"> [!IMPORTANT]\n> Record threshold probe below.","approved":false,"comments":[],"triage":[{"sig":"$tsig","status":"false-positive","rationale":"record path must keep the captured threshold here"}]}
EOF
REVIEW_KB_MIN_JUDGED=1 REVIEW_KB_MAX_FP_RATE=0 run_review record acme/local-k7 local-k7 --json >/dev/null
k7="$(jq -s -r '.[] | select(.id == "rec-thresh") | "\(.hits)/\(.false_hits)/\(.judged) \(.status)"' "$EVAL_SANDBOX/.verdict/kb.jsonl")"
if [ "$k7" = "0/1/1 active" ]; then
  good "record keeps the captured threshold over ambient env"
else
  bad "record borrowed ambient thresholds: '$k7'"
fi

eval_write_review 4251 <<'EOF'
{"schema_version":"1","body":"> [!IMPORTANT]\n> Real issue below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Unhandled await error path here.","rigor":"walked","severity":"important"},{"path":"notes.txt","line":2,"side":"RIGHT","body":"Second comment, no cites at all.","rigor":"pointed","severity":"informational"}]}
EOF
run_review post acme/app 4251 --dry-run --json >/dev/null
sig_nokb="$(jq -r '.[0].sig' "$EVAL_SANDBOX/.verdict/state/4251/findings.dryrun.json")"
if [ "$sig_nokb" = "$sig1" ]; then
  good "same body with and without kb cites shares the sig"
else
  bad "sig changed with kb: $sig1 vs $sig_nokb"
fi

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
eval_commit_controls
run_review fetch-local acme/app kbrecall4 --json >/dev/null
kb_order="$(grep '^# ord-' "$EVAL_SANDBOX/.verdict/state/local-kbrecall4/kb.txt" 2>/dev/null | cut -d' ' -f2 | tr '\n' ',' || true)"
if [ "$kb_order" = "ord-a,ord-b,ord-c," ]; then
  good "recall orders judged-precision first, unjudged at neutral prior, top-3 caps"
else
  bad "kb.txt order: '$kb_order'"
fi

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
if [ "$(jq -s -r '.[] | select(.id == "twin-r") | "\(.hits)/\(.false_hits)/\(.judged)"' "$EVAL_SANDBOX/.verdict/kb.jsonl")" = "1/0/1" ] \
  && [ "$(grep -c "\"sig\":\"$tsig\",\"rule\":\"twin-r\"" "$EVAL_SANDBOX/.verdict/kb-joins.jsonl")" = "1" ]; then
  good "adopted twins credit once (joins log dedups across bundles)"
else
  bad "twin counters/joins: $(jq -s -r '.[] | select(.id == "twin-r") | "\(.hits)/\(.false_hits)/\(.judged)"' "$EVAL_SANDBOX/.verdict/kb.jsonl") / $(grep -c twin-r "$EVAL_SANDBOX/.verdict/kb-joins.jsonl" || true) rows"
fi

run_review learn --restore docs-ex --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "bad-reason" ]; then
  good "restore without reason refuses as bad-reason"
else
  bad "restore without reason gave exit $EVAL_STATUS code '$code'"
fi
run_review learn --restore docs-ex --reason "rechecked, this docs rule caught a real miss last week" --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -s -r '.[] | select(.id == "docs-ex") | .status' "$EVAL_SANDBOX/.verdict/kb.jsonl")" = "active" ] \
  && jq -s -e '.[] | select(.id == "docs-ex") | (.history[-1].note // "") | contains("rechecked")' "$EVAL_SANDBOX/.verdict/kb.jsonl" >/dev/null 2>&1; then
  good "restore with reason reactivates and logs the note"
else
  bad "restore with reason gave exit $EVAL_STATUS"
fi

run_review learn --rule "Uncite probe rule" --scope "notes.txt" --id uncite-r --json >/dev/null
# The bundle comes from the real producer: a hand-written one carries no
# controls snapshot, so record refuses it as controls-unavailable.
eval_mk_bundle local-uncite
cat > "$EVAL_SANDBOX/.verdict/state/local-uncite/review.json" <<'EOF'
{"schema_version":"1","body":"> [!IMPORTANT]\n> Uncite probe issue below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Uncite cited finding here.","rigor":"walked","severity":"important","kb":["uncite-r"]}]}
EOF
run_review record acme/local-uncite local-uncite --json
usig="$(jq -r '.[0].sig' "$EVAL_SANDBOX/.verdict/state/local-uncite/findings.json")"
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.[0].kb | join(",")' "$EVAL_SANDBOX/.verdict/state/local-uncite/findings.json")" = "uncite-r" ]; then
  good "cited record stores the rule id"
else
  bad "cited record exit $EVAL_STATUS kb: $(jq -c '.[0].kb' "$EVAL_SANDBOX/.verdict/state/local-uncite/findings.json" 2>/dev/null)"
fi
cat > "$EVAL_SANDBOX/.verdict/state/local-uncite/review.json" <<'EOF'
{"schema_version":"1","body":"> [!IMPORTANT]\n> Uncite probe issue below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Uncite cited finding here.","rigor":"walked","severity":"important"}]}
EOF
run_review record acme/local-uncite local-uncite --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.[0].kb | join(",")' "$EVAL_SANDBOX/.verdict/state/local-uncite/findings.json")" = "uncite-r" ]; then
  good "absent kb re-post keeps the prior cite"
else
  bad "absent kb cleared the cite: $(jq -c '.[0].kb' "$EVAL_SANDBOX/.verdict/state/local-uncite/findings.json" 2>/dev/null)"
fi
cat > "$EVAL_SANDBOX/.verdict/state/local-uncite/review.json" <<'EOF'
{"schema_version":"1","body":"> [!IMPORTANT]\n> Uncite probe issue below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Uncite cited finding here.","rigor":"walked","severity":"important","kb":null}]}
EOF
run_review record acme/local-uncite local-uncite --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.[0].kb | join(",")' "$EVAL_SANDBOX/.verdict/state/local-uncite/findings.json")" = "uncite-r" ]; then
  good "null kb re-post keeps the prior cite"
else
  bad "null kb cleared the cite: $(jq -c '.[0].kb' "$EVAL_SANDBOX/.verdict/state/local-uncite/findings.json" 2>/dev/null)"
fi
cat > "$EVAL_SANDBOX/.verdict/state/local-uncite/review.json" <<'EOF'
{"schema_version":"1","body":"> [!IMPORTANT]\n> Uncite probe issue below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Uncite cited finding here.","rigor":"walked","severity":"important","kb":[]}]}
EOF
run_review record acme/local-uncite local-uncite --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.[0].kb | join(",")' "$EVAL_SANDBOX/.verdict/state/local-uncite/findings.json")" = "" ]; then
  good "explicit [] re-post clears the cite"
else
  bad "explicit [] kept the cite: $(jq -c '.[0].kb' "$EVAL_SANDBOX/.verdict/state/local-uncite/findings.json" 2>/dev/null)"
fi
cat > "$EVAL_SANDBOX/.verdict/state/local-uncite/review.json" <<'EOF'
{"schema_version":"1","body":"> [!IMPORTANT]\n> Uncite probe issue below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Uncite cited finding here.","rigor":"walked","severity":"important","kb":["uncite-r"]}]}
EOF
run_review record acme/local-uncite local-uncite --json >/dev/null
cat > "$EVAL_SANDBOX/.verdict/state/local-uncite/review.json" <<'EOF'
{"schema_version":"1","body":"> [!IMPORTANT]\n> Uncite probe issue below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Uncite cited finding here.","rigor":"walked","severity":"important","kb":["nope-missing"]}]}
EOF
run_review record acme/local-uncite local-uncite --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.[0].kb | join(",")' "$EVAL_SANDBOX/.verdict/state/local-uncite/findings.json")" = "" ] \
  && grep -q "ignoring unknown kb rule 'nope-missing'" "$EVAL_ERR"; then
  good "malformed re-post warns, drops to [], and clears (pinned)"
else
  bad "malformed re-post: $(jq -c '.[0].kb' "$EVAL_SANDBOX/.verdict/state/local-uncite/findings.json" 2>/dev/null)"
fi

cat > "$EVAL_SANDBOX/.verdict/state/local-uncite/review.json" <<'EOF'
{"schema_version":"1","body":"> [!IMPORTANT]\n> Uncite probe issue below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Uncite cited finding here.","rigor":"walked","severity":"important","kb":["uncite-r"]}]}
EOF
run_review record acme/local-uncite local-uncite --json >/dev/null
run_review triage acme/local-uncite local-uncite --sig "$usig" --status addressed --rationale "human confirms the uncite probe rule fired correctly here" --json
urow="$(jq -s -c --arg s "$usig" '[.[] | select(.sig == $s and .rule == "uncite-r")][-1]' "$EVAL_SANDBOX/.verdict/kb-joins.jsonl" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.bundle' <<<"$urow")" = "local-uncite" ] \
  && [ "$(jq -r '.review_id' <<<"$urow")" = "null" ]; then
  good "local joins row carries bundle + null review_id"
else
  bad "local joins row: $urow"
fi
PR=4271
eval_mk_bundle "$PR"
jsig="a23456789abcdef0123456789abcdef012345678"
eval_write_findings "$PR" <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Joins scope finding here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","kb":["uncite-r"],"sig":"$jsig","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
cat > "$EVAL_SANDBOX/.verdict/state/$PR/post.json" <<'EOF'
{"schema_version":"1","review":{"id":424242,"state":"COMMENTED"},"head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","posted_at":"2026-10-31T00:00:00Z","confidence":null,"security_summary":null}
EOF
run_review triage acme/app "$PR" --sig "$jsig" --status addressed --rationale "human confirms the joins scope row lands correctly here" --json >/dev/null
jrow="$(jq -s -c --arg s "$jsig" '[.[] | select(.sig == $s)][-1]' "$EVAL_SANDBOX/.verdict/kb-joins.jsonl" 2>/dev/null || true)"
if [ "$(jq -r '.bundle' <<<"$jrow")" = "4271" ] \
  && [ "$(jq -r '.review_id' <<<"$jrow")" = "424242" ] \
  && jq -e 'has("at") and has("sig") and has("rule") and has("outcome") and has("actor") and has("verb")' <<<"$jrow" >/dev/null 2>&1; then
  good "PR joins row carries bundle + review_id, old keys untouched"
else
  bad "PR joins row: $jrow"
fi

eval_end
