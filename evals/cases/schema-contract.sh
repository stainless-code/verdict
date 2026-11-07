#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

VAL="$EVAL_SKILL/schemas/validate.sh"
SCH="$EVAL_SKILL/schemas"
ENV="$SCH/envelope.schema.json"
DATA="$EVAL_SANDBOX/schema-data.json"

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

check() { # $1 = label, $2 = schema[#ptr], $3 = doc
  local label="$1" schema="$2" doc="$3" err
  if err="$("$VAL" "$schema" "$doc" 2>&1)"; then
    good "$label validates"
  else
    bad "$label fails validation: $err"
  fi
}

must_fail() { # $1 = label, $2 = schema[#ptr], $3 = doc, $4 = expected stderr rule
  local label="$1" schema="$2" doc="$3" want="$4" err
  if [ ! -s "$doc" ]; then
    bad "$label fixture is missing or empty"
    return
  fi
  if err="$("$VAL" "$schema" "$doc" 2>&1)"; then
    bad "$label passed validation (checker toothless)"
  elif grep -Fq "$want" <<<"$err"; then
    good "$label refused as invalid ($want)"
  else
    bad "$label failed for the wrong reason (want $want): $err"
  fi
}

cat > "$EVAL_SANDBOX/s1-review.json" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good overall — one nit below.\n\n**Reviewed changes**\n\n- Touched notes.\n\n### ⚠️ Watch the wording\n\nBody section.","approved":false,
"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.","rigor":"walked","severity":"informational","in-bounds":true,"kb":["rule-one"]},
{"path":"notes.txt","start_line":1,"line":3,"side":"RIGHT","body":"Range nit.","suggestion":"fixed\n"}],
"resolved":[],"triage":[{"sig":"0123456789abcdef0123456789abcdef01234567","status":"wont-fix","rationale":"By design: the wording matches the house style guide here"}],
"sections":[{"heading":"### ⚠️ Watch the wording","severity":"important"}],
"confidence":4,"security_summary":"No networked reads; nothing executable.","chunks":[]}
EOF
check "S1 review.json (full)" "$SCH/review.schema.json" "$EVAL_SANDBOX/s1-review.json"

LB="$EVAL_SANDBOX/.verdict/state/local-schematest"
eval_mk_bundle local-schematest
cat > "$LB/review.json" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good overall — one nit below.\n\n**Reviewed changes**\n\n- Touched notes.\n\n### ⚠️ Watch the wording\n\nBody section.","approved":false,
"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.","rigor":"walked","severity":"informational"}],
"confidence":4,"security_summary":"No networked reads; nothing executable."}
EOF
cat > "$LB/cost.json" <<'EOF'
{"schema_version":"2","effort":"standard","models":[{"name":"synth","input_tokens":100,"output_tokens":50,"cache_read_input_tokens":30,"cache_creation_input_tokens":10}],"wall_seconds":12,"diff_bytes":200,"lenses":[{"group":"b703173f532ebdf36d9eef44d8d3b5c3f3a4314591c039e6e84d6df2771c8663","labels":["scope"]}],"group_costs":[{"group":"b703173f532ebdf36d9eef44d8d3b5c3f3a4314591c039e6e84d6df2771c8663","effort":"standard","est_usd":0.05}],"est_usd":0.05}
EOF
run_review record acme/app local-schematest --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "S2 record exits 0"
else
  bad "S2 record exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
SIG="$(jq -r '.[0].sig' "$LB/findings.json" 2>/dev/null || true)"
cat > "$LB/review.json" <<EOF
{"schema_version":"1","body":"> ✅ Looks good overall — one nit below.","approved":false,"comments":[],
"triage":[{"sig":"$SIG","status":"wont-fix","rationale":"By design: the wording matches the pinned style guide verbatim"}]}
EOF
cat > "$LB/verification.json" <<EOF
[{"sig":"$SIG","verdict":"VERIFIED","verifyCmd":"true","resultHead":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]
EOF
run_review record acme/app local-schematest --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.[0].status' "$LB/findings.json" 2>/dev/null)" = "wont-fix" ]; then
  good "S2 re-record merges triage + verification (wont-fix, history kept)"
else
  bad "S2 re-record exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
check "S2 review.json input" "$SCH/review.schema.json" "$LB/review.json"
check "S2 cost.json" "$SCH/cost.schema.json" "$LB/cost.json"
check "S2 findings.json (record-written)" "$SCH/findings.schema.json" "$LB/findings.json"
check "S2 record.json (record-written)" "$SCH/record.schema.json" "$LB/record.json"
if [ "$(jq -r '.tool_version | type' "$LB/record.json" 2>/dev/null)" = "string" ] \
  && [ "$(jq -r '.prompts_hash | type' "$LB/record.json" 2>/dev/null)" = "string" ] \
  && [ "$(jq -r '.model' "$LB/record.json" 2>/dev/null)" = "synth" ]; then
  good "S2 record.json carries the provenance triple (model from the cost record)"
else
  bad "S2 record.json misses the provenance triple: $(jq -c '{tool_version, prompts_hash, model}' "$LB/record.json" 2>/dev/null)"
fi
check "S2 record envelope" "$ENV" "$EVAL_OUT"
jq '.data' <"$EVAL_OUT" > "$DATA" 2>/dev/null || echo "null" > "$DATA"
check "S2 record data profile" "$ENV#/\$defs/recordData" "$DATA"

run_review report acme/app local-schematest --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "S3 report --json exits 0"
else
  bad "S3 report --json exits $EVAL_STATUS"
fi
check "S3 report envelope" "$ENV" "$EVAL_OUT"
jq '.data' <"$EVAL_OUT" > "$DATA" 2>/dev/null || echo "null" > "$DATA"
check "S3 report data profile" "$ENV#/\$defs/reportData" "$DATA"
run_review report acme/app local-schematest --cost --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.cost.runs' <"$EVAL_OUT" 2>/dev/null)" = "1" ] \
  && [ "$(jq -r '.data.cost.cached_input_tokens' <"$EVAL_OUT" 2>/dev/null)" = "40" ]; then
  good "S3 report --cost rolls the one run with its cached tokens"
else
  bad "S3 report --cost exits $EVAL_STATUS: $(jq -c '.data.cost // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi
check "S3 report --cost envelope" "$ENV" "$EVAL_OUT"
jq '.data' <"$EVAL_OUT" > "$DATA" 2>/dev/null || echo "null" > "$DATA"
check "S3 report --cost data profile" "$ENV#/\$defs/reportData" "$DATA"

# S3b: the gate verb publishes its own envelope, so the reportGate definition
# is exercised by a live run, and a gating report carries it inside data.
PR=4950
eval_mk_bundle "$PR"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Usable body here with enough substance.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"critical","sig":"cccccccccccccccccccccccccccccccccccccccc","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
run_review gate acme/app "$PR" --max-severity critical --live skip --json
if [ "$EVAL_STATUS" -eq 1 ] && [ "$(jq -r '.verb' <"$EVAL_OUT" 2>/dev/null)" = "gate" ]; then
  good "S3b gate --json exits 1 on the open critical with a gate verb"
else
  bad "S3b gate --json exits $EVAL_STATUS: $(jq -c '{verb,error}' <"$EVAL_OUT" 2>/dev/null)"
fi
check "S3b gate envelope" "$ENV" "$EVAL_OUT"
jq '.data' <"$EVAL_OUT" > "$DATA" 2>/dev/null || echo "null" > "$DATA"
check "S3b gate data profile" "$ENV#/\$defs/reportGate" "$DATA"
run_review report acme/app "$PR" --check critical --live skip --json
jq '.data' <"$EVAL_OUT" > "$EVAL_SANDBOX/s3b-gaterpt.json" 2>/dev/null || echo "null" > "$EVAL_SANDBOX/s3b-gaterpt.json"
if [ "$(jq -r '.gate.dimensions[0].id' <"$EVAL_SANDBOX/s3b-gaterpt.json" 2>/dev/null)" = "findings" ]; then
  good "S3b report --check data carries the gate block"
else
  bad "S3b report --check data.gate: $(jq -c '.gate // "absent"' <"$EVAL_SANDBOX/s3b-gaterpt.json" 2>/dev/null)"
fi
check "S3b report gating profile with data.gate" "$ENV#/\$defs/reportData" "$EVAL_SANDBOX/s3b-gaterpt.json"
jq -n '{"passed": true, "dimensions": [{"id": "findings", "status": "clean", "detail": "d", "count": 0}]}' \
  > "$EVAL_SANDBOX/s3b-badstatus.json"
must_fail "S3b gate off-enum status" "$ENV#/\$defs/reportGate" "$EVAL_SANDBOX/s3b-badstatus.json" "not one of"
jq -n '{"passed": true, "dimensions": [{"id": "findings", "status": "pass", "detail": "d", "count": "one"}]}' \
  > "$EVAL_SANDBOX/s3b-badcount.json"
must_fail "S3b gate non-integer count" "$ENV#/\$defs/reportGate" "$EVAL_SANDBOX/s3b-badcount.json" "want type [\"integer\",\"null\"]"
run_review report acme/app "$PR" --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(jq -r 'has("gate")' <<<"$(jq -c '.data' <"$EVAL_OUT")" 2>/dev/null)" = "false" ]; then
  good "S3b a default report carries no data.gate key"
else
  bad "S3b default report data keys: $(jq -c '.data | keys' <"$EVAL_OUT" 2>/dev/null)"
fi
run_review report acme/app "$PR" --check critical --live skip --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && jq -e '.data.gate.passed == false' <"$EVAL_OUT" >/dev/null 2>&1 \
  && jq '.data' <"$EVAL_OUT" > "$DATA"; then
  good "S3b report --check carries data.gate with passed false"
  check "S3b report gating data profile" "$ENV#/\$defs/reportData" "$DATA"
else
  bad "S3b report --check exits $EVAL_STATUS: $(jq -c '.data.gate // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi

PR=4951
stub_pre_post="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good overall — one nit below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.","rigor":"pointed","severity":"informational"}],"confidence":5,"security_summary":"Nothing executable."}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "S4 post --dry-run exits 0"
else
  bad "S4 post --dry-run exits $EVAL_STATUS"
fi
check "S4 post envelope" "$ENV" "$EVAL_OUT"
jq '.data' <"$EVAL_OUT" > "$DATA" 2>/dev/null || echo "null" > "$DATA"
check "S4 post data profile" "$ENV#/\$defs/postData" "$DATA"
if [ "$(jq -r '.provenance.tool_version | type' "$DATA" 2>/dev/null)" = "string" ] \
  && [ "$(jq -r '.provenance.prompts_hash | type' "$DATA" 2>/dev/null)" = "string" ]; then
  good "S4 dry-run data carries provenance (model null with no cost record)"
else
  bad "S4 dry-run data misses provenance: $(jq -c '.provenance // "absent"' "$DATA" 2>/dev/null)"
fi
check "S4 findings.dryrun.json" "$SCH/findings.schema.json" "$EVAL_SANDBOX/.verdict/state/$PR/findings.dryrun.json"

# mint one; this pins the writer's shape instead (review passthrough stays open
# for GitHub's object, provenance rides the same triple the record writer and
# both post envelopes emit).
cat > "$EVAL_SANDBOX/s5-post.json" <<'EOF'
{"schema_version":"1","review":{"id":123,"html_url":"https://github.com/acme/app/pull/9#pullrequestreview-1"},"head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","posted_at":"2026-11-02T00:00:00Z","confidence":5,"security_summary":"Nothing executable.","tool_version":"1.2.3","prompts_hash":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","model":null}
EOF
check "S5 post.json (representative)" "$SCH/post.schema.json" "$EVAL_SANDBOX/s5-post.json"
check "S5 cost.json (prompts shape)" "$SCH/cost.schema.json" "$LB/cost.json"

printf '{"schema_version":"1","comments":[]}' > "$EVAL_SANDBOX/s6-nobody.json"
must_fail "S6 review without body" "$SCH/review.schema.json" "$EVAL_SANDBOX/s6-nobody.json" "missing required key(s): body"
printf '[{"sig":"x","sig_version":"1","kind":"inline","status":"maybe","firstSeen":"a","lastSeen":"b","history":[]}]' > "$EVAL_SANDBOX/s6-badstatus.json"
must_fail "S6 findings off-enum status" "$SCH/findings.schema.json" "$EVAL_SANDBOX/s6-badstatus.json" "not one of"
jq '. + {ninth_key: 1}' <"$EVAL_OUT" > "$EVAL_SANDBOX/s6-extrakey.json" 2>/dev/null
must_fail "S6 envelope extra top-level key" "$ENV" "$EVAL_SANDBOX/s6-extrakey.json" "additional properties not allowed: ninth_key"
printf '{"schema_version":"1","status":"local","head":"h","diff_sha256":"d","recorded_at":"not-a-time","findings":0,"open":0}' > "$EVAL_SANDBOX/s6-badtime.json"
must_fail "S6 record bad date-time" "$SCH/record.schema.json" "$EVAL_SANDBOX/s6-badtime.json" "not a date-time"

if [ "$(( stub_pre_post - stub_before ))" = "0" ]; then
  good "S7 zero stub-gh intercepts across the record/report legs (offline)"
else
  bad "S7 stub gh intercepted calls during the record/report legs"
fi
stub_now="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
if [ "$(( stub_now - stub_pre_post ))" = "1" ] \
  && sed -n "$(( stub_pre_post + 1 )),$(( stub_now ))p" "$EVAL_STUBLOG" | matches "^stub-gh api repos/acme/app/pulls/4951"; then
  good "S7 dry-run post made only the one stubbed SHA lookup (offline)"
else
  bad "S7 unexpected network attempts around the dry-run post leg"
fi
# S8: finding categories ride the review, findings, and report data schemas.
PR=4952
eval_mk_bundle "$PR"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Security boundary bug here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"critical","category":"security","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Style nit here with enough substance.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","category":"style","sig":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Untagged observation with enough substance.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","category":null,"sig":"cccccccccccccccccccccccccccccccccccccccc","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
check "S8 findings.json with categories" "$SCH/findings.schema.json" "$EVAL_SANDBOX/.verdict/state/$PR/findings.json"
jq '.[0] | .category = 3' "$EVAL_SANDBOX/.verdict/state/$PR/findings.json" > "$EVAL_SANDBOX/s8-badcat.json"
must_fail "S8 findings non-string category" "$SCH/findings.schema.json" "$EVAL_SANDBOX/s8-badcat.json" "want type"
jq -n '{"schema_version":"1","body":"> ✅ Category coverage with enough substance.","approved":false,"comments":[
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Logic bug here.","rigor":"walked","severity":"critical","category":"logic"},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Edge case here with enough substance.","rigor":"walked","severity":"important","category":"edge-case"},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Security hole here with enough substance.","rigor":"walked","severity":"critical","category":"security"},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Slow path here with enough substance.","rigor":"walked","severity":"informational","category":"performance"},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Stray file here with enough substance.","rigor":"pointed","severity":"informational","category":"accidental-commit"},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Naming nit here with enough substance.","rigor":"pointed","severity":"informational","category":"style"},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Docs gap here with enough substance.","rigor":"pointed","severity":"informational","category":"docs"},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Odd finding here with enough substance.","rigor":"pointed","severity":"informational","category":"other"},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Untagged here with enough substance.","rigor":"pointed","severity":"informational","category":null}
]}' > "$EVAL_SANDBOX/s8-review.json"
check "S8 review.json with every category and null" "$SCH/review.schema.json" "$EVAL_SANDBOX/s8-review.json"
jq '.comments[0].category = "nope"' "$EVAL_SANDBOX/s8-review.json" > "$EVAL_SANDBOX/s8-badreview.json"
must_fail "S8 review unknown category" "$SCH/review.schema.json" "$EVAL_SANDBOX/s8-badreview.json" "not one of"
run_review report acme/app "$PR" --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "S8 report --json exits 0"
else
  bad "S8 report --json exits $EVAL_STATUS"
fi
jq '.data' <"$EVAL_OUT" > "$DATA" 2>/dev/null || echo "null" > "$DATA"
check "S8 report data profile with by_category" "$ENV#/\$defs/reportData" "$DATA"
if [ "$(jq -c '.counts.by_category | keys' "$DATA")" = '["accidental-commit","docs","edge-case","logic","other","performance","security","style","untagged"]' ]; then
  good "S8 by_category carries every key plus untagged"
else
  bad "S8 by_category keys: $(jq -c '.counts.by_category' "$DATA")"
fi
rm -rf "$EVAL_SANDBOX/.verdict/state/$PR" "$EVAL_SANDBOX"/s8-*.json
rm -rf "$LB" "$EVAL_SANDBOX/.verdict/state/$PR" "$EVAL_SANDBOX/.verdict/state/4950" "$EVAL_SANDBOX"/s1-review.json \
  "$EVAL_SANDBOX"/s5-post.json "$EVAL_SANDBOX"/s6-*.json "$EVAL_SANDBOX"/s3b-*.json "$DATA"

eval_end
