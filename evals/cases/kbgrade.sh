#!/usr/bin/env bash
# Case kbgrade: self-grading probe over closed findings (calibrate --probe).
#
# Grades (hermetic — crafted bundle fixtures in the sandbox; the
# network-dead gh stub stays first on PATH throughout):
#   P1  grade rows are well-formed: one per finding, verdict from the
#       recorded disposition (addressed = real, false-positive = false),
#       first cited rule + helpfulness, grader + timestamp, bundle key.
#   P2  additive-only: findings.json, kb.jsonl, and kb-joins.jsonl are
#       byte-identical after a probe run; re-running is idempotent.
#   P3  no-ground-truth edge: open/wont-fix/deferred findings grade
#       unknown with null helpfulness — never forced real/false.
#   P4  offline: no post.json needed, local bundles grade, zero gh
#       interceptions across the probe runs.
#   P5  missing/unreadable findings refuse typed (missing-findings /
#       corrupt-store) and write no grades file.
#   P6  agent judgment loop: --judge/--judge-file merge agent_verdict/
#       agent_helpful/agreement columns that may diverge from the baseline
#       (agree + disagree shapes), unknown sigs refuse typed, malformed input
#       refuses typed, baseline columns untouched by judgments, the dissent
#       rate computable with sample size, judgments sticky across re-runs,
#       still offline.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

S1="f111111111111111111111111111111111111111"
S2="f222222222222222222222222222222222222222"
S3="f333333333333333333333333333333333333333"
S4="f444444444444444444444444444444444444444"
S5="f555555555555555555555555555555555555555"

# Bundle 4501: addressed cited, false-positive cited, addressed uncited,
# open cited (no ground truth yet), deferred cited. Deliberately no
# post.json — the probe must not need one.
mkdir -p "$EVAL_SANDBOX/.review/4501"
cat > "$EVAL_SANDBOX/.review/4501/findings.json" <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Real cited finding here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","kb":["g-real"],"sig":"$S1","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"addressed","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":3,"anchored":true,"body":"False alarm cited finding here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"informational","kb":["g-false"],"sig":"$S2","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"false-positive","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":4,"anchored":true,"body":"Real uncited finding here now.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"important","kb":[],"sig":"$S3","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"addressed","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":5,"anchored":true,"body":"Still open cited finding here.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":["g-real"],"sig":"$S4","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":6,"anchored":true,"body":"Deferred cited finding here now.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":["g-real"],"sig":"$S5","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"deferred","rationale":"","history":[]}]
EOF

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
before_findings="$(shasum -a 1 "$EVAL_SANDBOX/.review/4501/findings.json" | cut -d' ' -f1)"
before_kb="absent"; [ -f "$EVAL_SANDBOX/.review/kb.jsonl" ] \
  && before_kb="$(shasum -a 1 "$EVAL_SANDBOX/.review/kb.jsonl" | cut -d' ' -f1)"
before_joins="absent"; [ -f "$EVAL_SANDBOX/.review/kb-joins.jsonl" ] \
  && before_joins="$(shasum -a 1 "$EVAL_SANDBOX/.review/kb-joins.jsonl" | cut -d' ' -f1)"

run_review calibrate acme/app 4501 --probe --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "calibrate --probe exits 0 without a post record"
else
  bad "calibrate --probe exits $EVAL_STATUS"
fi

# --- P1: well-formed rows ---
if [ "$(jq -r '.schema_version' <"$EVAL_OUT" 2>/dev/null)" = "1" ] \
  && [ "$(jq -r '.verb' <"$EVAL_OUT" 2>/dev/null)" = "calibrate" ] \
  && jq -e '.data.bundle == "4501" and (.data.grades | length) == 5
    and .data.counts == {findings: 5, real: 2, false: 1, unknown: 2}' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "envelope: bundle 4501, 5 grades, counts 2/1/2"
else
  bad "envelope: $(jq -c '{schema_version, verb, data: .data.counts}' <"$EVAL_OUT" 2>/dev/null)"
fi
row() { jq -c --arg s "$1" '.data.grades | map(select(.sig == $s))[0]' <"$EVAL_OUT"; }
if [ "$(row "$S1" | jq -r '[.verdict, .rule, .helpful, .grader, .bundle] | join(",")')" = "real,g-real,true,calibrate-probe,4501" ] \
  && [ "$(row "$S1" | jq -r '.at | type')" = "string" ]; then
  good "addressed cited grades real + rule + helpful true"
else
  bad "S1 row: $(row "$S1")"
fi
if [ "$(row "$S2" | jq -r '[.verdict, .rule, .helpful] | join(",")')" = "false,g-false,false" ]; then
  good "false-positive cited grades false + rule + helpful false"
else
  bad "S2 row: $(row "$S2")"
fi
if [ "$(row "$S3" | jq -c '[.verdict, .rule, .helpful]')" = '["real",null,null]' ]; then
  good "addressed uncited grades real with null rule/helpful"
else
  bad "S3 row: $(row "$S3")"
fi
if [ "$(jq -r '.sig[0:8] | length' "$EVAL_SANDBOX/.review/4501/kb-grades.jsonl" 2>/dev/null | head -1)" = "8" ] \
  && [ "$(wc -l <"$EVAL_SANDBOX/.review/4501/kb-grades.jsonl" | tr -d ' ')" = "5" ] \
  && jq -e . "$EVAL_SANDBOX/.review/4501/kb-grades.jsonl" >/dev/null 2>&1; then
  good "kb-grades.jsonl holds 5 parseable rows"
else
  bad "kb-grades.jsonl shape broke"
fi
if grep -q "probe: acme/app#4501" "$EVAL_ERR" \
  && grep -q "real=2 false=1 unknown=2" "$EVAL_ERR"; then
  good "human progress names the probe counts"
else
  bad "human probe line missing (exit $EVAL_STATUS)"
fi
run_review calibrate acme/app 4501 --probe
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "probe: acme/app#4501" "$EVAL_OUT" \
  && grep -q "real=2 false=1 unknown=2" "$EVAL_OUT" \
  && grep -q "f1111111 \[real\]" "$EVAL_OUT"; then
  good "text mode prints the probe table"
else
  bad "text-mode probe table missing (exit $EVAL_STATUS)"
fi
run_review calibrate acme/app 4501 --probe --json

# --- P3: no-ground-truth edge (unknown, never forced) ---
if [ "$(row "$S4" | jq -c '[.verdict, .rule, .helpful]')" = '["unknown","g-real",null]' ] \
  && [ "$(row "$S5" | jq -c '[.verdict, .rule, .helpful]')" = '["unknown","g-real",null]' ]; then
  good "open + deferred grade unknown with the cite kept and helpful null"
else
  bad "unknown edge: S4=$(row "$S4") S5=$(row "$S5")"
fi

# --- P2: additive-only + idempotent ---
after_findings="$(shasum -a 1 "$EVAL_SANDBOX/.review/4501/findings.json" | cut -d' ' -f1)"
after_kb="absent"; [ -f "$EVAL_SANDBOX/.review/kb.jsonl" ] \
  && after_kb="$(shasum -a 1 "$EVAL_SANDBOX/.review/kb.jsonl" | cut -d' ' -f1)"
after_joins="absent"; [ -f "$EVAL_SANDBOX/.review/kb-joins.jsonl" ] \
  && after_joins="$(shasum -a 1 "$EVAL_SANDBOX/.review/kb-joins.jsonl" | cut -d' ' -f1)"
if [ "$before_findings" = "$after_findings" ] \
  && [ "$before_kb" = "$after_kb" ] \
  && [ "$before_joins" = "$after_joins" ]; then
  good "findings/kb/joins byte-identical after the probe"
else
  bad "probe mutated a store it must not touch"
fi
grades_body_before="$(jq -c 'del(.at)' "$EVAL_SANDBOX/.review/4501/kb-grades.jsonl")"
run_review calibrate acme/app 4501 --probe --json
grades_body_after="$(jq -c 'del(.at)' "$EVAL_SANDBOX/.review/4501/kb-grades.jsonl")"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$grades_body_before" = "$grades_body_after" ]; then
  good "re-run is idempotent (rows stable apart from the timestamp)"
else
  bad "re-run moved the grade rows"
fi

# --- P4: offline, local bundles included ---
stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
if [ "$stub_before" = "$stub_after" ]; then
  good "zero gh interceptions across the probe runs"
else
  bad "probe touched the network ($stub_before -> $stub_after)"
fi
mkdir -p "$EVAL_SANDBOX/.review/local-g1"
cat > "$EVAL_SANDBOX/.review/local-g1/source.json" <<'EOF'
{"schema_version":"1","kind":"worktree","repo":"acme/app","slug":"g1","base_ref":null,"head_ref":"work","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_sha":"","range":"HEAD:worktree","dirty":true,"diff_sha256":"0","files":1,"additions":1,"deletions":1,"tool_version":"0.0.0-test","model":null,"created_at":"2026-01-01T00:00:00Z"}
EOF
cat > "$EVAL_SANDBOX/.review/local-g1/findings.json" <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Local real cited finding here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","kb":["g-real"],"sig":"$S1","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"addressed","rationale":"","history":[]}]
EOF
run_review calibrate acme/app local-g1 --probe --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.data.bundle == "local-g1" and (.data.grades | length) == 1
    and .data.grades[0].verdict == "real"' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "local bundle grades offline (live calibrate still refuses it)"
else
  bad "local probe gave exit $EVAL_STATUS"
fi

# --- P5: typed refusals, no grades file ---
run_review calibrate acme/app 4599 --probe --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)" = "missing-findings" ] \
  && [ ! -f "$EVAL_SANDBOX/.review/4599/kb-grades.jsonl" ]; then
  good "missing findings refuses as missing-findings, writing nothing"
else
  bad "missing-findings leg gave exit $EVAL_STATUS"
fi
mkdir -p "$EVAL_SANDBOX/.review/4598"
echo "not a findings array" > "$EVAL_SANDBOX/.review/4598/findings.json"
run_review calibrate acme/app 4598 --probe --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)" = "corrupt-store" ] \
  && [ ! -f "$EVAL_SANDBOX/.review/4598/kb-grades.jsonl" ]; then
  good "unreadable findings refuses as corrupt-store, writing nothing"
else
  bad "corrupt-store leg gave exit $EVAL_STATUS"
fi

# --- P6: agent judgment loop (judgments that can diverge) ---
stub_p6_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
mkdir -p "$EVAL_SANDBOX/.review/4502"
cat > "$EVAL_SANDBOX/.review/4502/findings.json" <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Real cited finding here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","kb":["g-real"],"sig":"$S1","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"addressed","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":3,"anchored":true,"body":"False alarm cited finding here.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":["g-false"],"sig":"$S2","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"false-positive","rationale":"","history":[]}]
EOF
findings_4502_before="$(shasum -a 1 "$EVAL_SANDBOX/.review/4502/findings.json" | cut -d' ' -f1)"
run_review calibrate acme/app 4502 --probe --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.data.counts == {findings: 2, real: 1, false: 1, unknown: 0}' <"$EVAL_OUT" >/dev/null 2>&1 \
  && jq -e '.data.judgment == {sampled: 0, judged: 0, agreed: 0, disagreed: 0, agreement_rate: null}' <"$EVAL_OUT" >/dev/null 2>&1 \
  && jq -e '[.data.grades[] | select(.agent_verdict != null or .agreement != null)] | length == 0' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "unjudged baseline: counts shape kept, no agent columns, null rate"
else
  bad "unjudged baseline leg gave exit $EVAL_STATUS"
fi
base_4502_before="$(jq -c '[.data.grades[] | {sig, verdict, rule, helpful}]' <"$EVAL_OUT")"
cat > "$EVAL_SANDBOX/j1.jsonl" <<EOF
{"sig":"$S1","agent_verdict":"real","agent_helpful":true}
{"sig":"$S2","agent_verdict":"real","agent_helpful":true}
EOF
run_review calibrate acme/app 4502 --probe --judge-file "$EVAL_SANDBOX/j1.jsonl" --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "judge-file run exits 0"
else
  bad "judge-file run exits $EVAL_STATUS"
fi
jrow() { jq -c --arg s "$1" '.data.grades | map(select(.sig == $s))[0]' <"$EVAL_OUT"; }
if [ "$(jrow "$S1" | jq -c '{a: .agent_verdict, h: .agent_helpful, g: .agreement, v: .verdict}')" = '{"a":"real","h":true,"g":true,"v":"real"}' ]; then
  good "agree shape: agent real matches baseline real, agreement true"
else
  bad "agree row: $(jrow "$S1")"
fi
if [ "$(jrow "$S2" | jq -c '{a: .agent_verdict, h: .agent_helpful, g: .agreement, v: .verdict}')" = '{"a":"real","h":true,"g":false,"v":"false"}' ]; then
  good "disagree shape: agent real diverges from baseline false, agreement false"
else
  bad "disagree row: $(jrow "$S2")"
fi
if [ "$(jq -c '[.data.grades[] | {sig, verdict, rule, helpful}]' <"$EVAL_OUT")" = "$base_4502_before" ]; then
  good "baseline columns untouched by the judgment merge"
else
  bad "baseline moved under judgments"
fi
if jq -e '.data.judgment == {sampled: 2, judged: 2, agreed: 1, disagreed: 1, agreement_rate: 0.5}' <"$EVAL_OUT" >/dev/null 2>&1 \
  && grep -q "judged=2 agreed=1 disagreed=1" "$EVAL_ERR"; then
  good "dissent rate computable: 1/2 agree, sample size recorded"
else
  bad "judgment summary: $(jq -c '.data.judgment' <"$EVAL_OUT" 2>/dev/null)"
fi
run_review calibrate acme/app 4502 --probe
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "agent=real agree" "$EVAL_OUT" \
  && grep -q "agent=real DISAGREE" "$EVAL_OUT" \
  && grep -q "judgments: sampled=2 judged=2" "$EVAL_OUT"; then
  good "text mode prints the judgment marks and summary"
else
  bad "text-mode judgment table missing (exit $EVAL_STATUS)"
fi
run_review calibrate acme/app 4502 --probe --judge "$S2:false" --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jrow "$S2" | jq -c '{a: .agent_verdict, g: .agreement}')" = '{"a":"false","g":true}' ] \
  && [ "$(jrow "$S1" | jq -c '{a: .agent_verdict, h: .agent_helpful, g: .agreement}')" = '{"a":"real","h":true,"g":true}' ] \
  && jq -e '.data.judgment == {sampled: 2, judged: 2, agreed: 2, disagreed: 0, agreement_rate: 1}' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "--judge flag overlays (S2 flips to agree) while the file judgment stays sticky (S1)"
else
  bad "flag overlay/sticky leg: S1=$(jrow "$S1") S2=$(jrow "$S2")"
fi
grades_4502_ok="$(shasum -a 1 "$EVAL_SANDBOX/.review/4502/kb-grades.jsonl" | cut -d' ' -f1)"
run_review calibrate acme/app 4502 --probe --judge "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef:real" --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)" = "unknown-judgment" ] \
  && [ "$(shasum -a 1 "$EVAL_SANDBOX/.review/4502/kb-grades.jsonl" | cut -d' ' -f1)" = "$grades_4502_ok" ]; then
  good "unknown sig refuses as unknown-judgment, grades file untouched"
else
  bad "unknown-sig leg gave exit $EVAL_STATUS"
fi
run_review calibrate acme/app 4502 --probe --judge "$S1:bogus" --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)" = "bad-judgment" ] \
  && [ "$(shasum -a 1 "$EVAL_SANDBOX/.review/4502/kb-grades.jsonl" | cut -d' ' -f1)" = "$grades_4502_ok" ]; then
  good "bad verdict enum refuses as bad-judgment, grades file untouched"
else
  bad "bad-enum leg gave exit $EVAL_STATUS"
fi
echo "not json at all" > "$EVAL_SANDBOX/jbad.jsonl"
run_review calibrate acme/app 4502 --probe --judge-file "$EVAL_SANDBOX/jbad.jsonl" --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)" = "bad-judgment" ] \
  && [ "$(shasum -a 1 "$EVAL_SANDBOX/.review/4502/kb-grades.jsonl" | cut -d' ' -f1)" = "$grades_4502_ok" ]; then
  good "malformed judge file refuses as bad-judgment, grades file untouched"
else
  bad "bad-file leg gave exit $EVAL_STATUS"
fi
mkdir -p "$EVAL_SANDBOX/.review/4503"
cat > "$EVAL_SANDBOX/.review/4503/findings.json" <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":5,"anchored":true,"body":"Still open cited finding here.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":["g-real"],"sig":"$S4","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
cat > "$EVAL_SANDBOX/j2.json" <<EOF
[{"sig":"$S4","agent_verdict":"real"}]
EOF
run_review calibrate acme/app 4503 --probe --judge-file "$EVAL_SANDBOX/j2.json" --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -c '.data.grades[0] | {a: .agent_verdict, h: .agent_helpful, g: .agreement, v: .verdict}' <"$EVAL_OUT")" = '{"a":"real","h":null,"g":null,"v":"unknown"}' ] \
  && jq -e '.data.judgment == {sampled: 1, judged: 0, agreed: 0, disagreed: 0, agreement_rate: null}' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "judgment on unknown baseline stays agreement-null and out of the rate"
else
  bad "unknown-baseline leg: $(jq -c '{grade: .data.grades[0], judgment: .data.judgment}' <"$EVAL_OUT" 2>/dev/null)"
fi
if [ "$(shasum -a 1 "$EVAL_SANDBOX/.review/4502/findings.json" | cut -d' ' -f1)" = "$findings_4502_before" ]; then
  good "4502 findings byte-identical after the judgment runs"
else
  bad "4502 findings mutated by a judgment run"
fi
stub_p6_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
if [ "$stub_p6_before" = "$stub_p6_after" ]; then
  good "zero gh interceptions across the judgment runs"
else
  bad "judgment runs touched the network ($stub_p6_before -> $stub_p6_after)"
fi

if [ "$fails" -ne 0 ]; then echo "case kbgrade: FAIL ($fails)"; exit 1; fi
echo "case kbgrade: PASS"
