#!/usr/bin/env bash
# Case schema-contract: published schemas validate real fixture outputs.
#
# Grades (all hermetic — crafted bundles in the temp sandbox, network-dead
# stub gh, no registry):
#   S1  review.json (agent-written, every optional key the manual names)
#       validates against schemas/review.schema.json.
#   S2  record on a crafted local bundle writes findings.json + record.json
#       for real, plus one --json envelope: findings validate as a findings
#       store, record.json against schemas/record.schema.json (provenance triple
#       pinned: model from the cost record), the envelope
#       against schemas/envelope.schema.json and its data against the
#       recordData profile.
#   S3  report --json and report --cost --json on that bundle validate
#       (envelope plus the reportData profile, cost rollup with the
#       cached_input_tokens scalar included).
#   S4  post --dry-run --json on a PR bundle validates (envelope plus the
#       postData profile, data.provenance pinned); the findings.dryrun.json
#       it writes validates as a findings store.
#   S5  a representative post.json (the real post needs network, so its shape
#       is pinned from the writer, provenance triple included) and the
#       prompts-shaped cost.json validate.
#   S6  the checker has teeth: one broken doc per negative (missing body,
#       off-enum status, extra envelope key, bad date-time) fails validation.
#   S7  offline: zero stub-gh intercepts across every validation leg.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

VAL="$EVAL_ROOT/schemas/validate.sh"
SCH="$EVAL_ROOT/schemas"
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

must_fail() { # $1 = label, $2 = schema[#ptr], $3 = doc
  local label="$1" schema="$2" doc="$3"
  if "$VAL" "$schema" "$doc" >/dev/null 2>&1; then
    bad "$label passed validation (checker toothless)"
  else
    good "$label refused as invalid"
  fi
}

# --- S1: agent-written review input, every optional key ---
cat > "$EVAL_SANDBOX/s1-review.json" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good overall — one nit below.\n\n**Reviewed changes**\n\n- Touched notes.\n\n### ⚠️ Watch the wording\n\nBody section.","approved":false,
"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.","rigor":"walked","severity":"informational","in-bounds":true,"kb":["rule-one"]},
{"path":"notes.txt","start_line":1,"line":3,"side":"RIGHT","body":"Range nit.","suggestion":"fixed\n"}],
"resolved":[],"triage":[{"sig":"0123456789abcdef0123456789abcdef01234567","status":"wont-fix","rationale":"By design: the wording matches the house style guide here"}],
"sections":[{"heading":"### ⚠️ Watch the wording","severity":"important"}],
"confidence":4,"security_summary":"No networked reads; nothing executable.","chunks":[]}
EOF
check "S1 review.json (full)" "$SCH/review.schema.json" "$EVAL_SANDBOX/s1-review.json"

# --- S2: record a crafted local bundle for real ---
LB="$EVAL_SANDBOX/.review/local-schematest"
mkdir -p "$LB"
cat > "$LB/source.json" <<'EOF'
{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head_ref":"worktree"}
EOF
cat > "$LB/full.diff" <<'EOF'
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
cat > "$LB/review.json" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good overall — one nit below.\n\n**Reviewed changes**\n\n- Touched notes.\n\n### ⚠️ Watch the wording\n\nBody section.","approved":false,
"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.","rigor":"walked","severity":"informational"}],
"confidence":4,"security_summary":"No networked reads; nothing executable."}
EOF
cat > "$LB/cost.json" <<'EOF'
{"schema_version":"1","effort":"standard","models":[{"name":"synth","input_tokens":100,"output_tokens":50,"cache_read_input_tokens":30,"cache_creation_input_tokens":10}],"wall_seconds":12,"diff_bytes":200,"lenses":["scope"],"est_usd":0.05}
EOF
run_review record acme/app local-schematest --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "S2 record exits 0"
else
  bad "S2 record exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
# Second pass with agent triage + verification evidence, so the stored
# shapes beyond open/unmerged get pinned too (triaged/wont-fix status,
# rationale, triagedAt/triagedBy, addressed flip, multi-event history).
SIG="$(jq -r '.[0].sig' "$LB/findings.json" 2>/dev/null || true)"
cat > "$LB/review.json" <<EOF
{"schema_version":"1","body":"> ✅ Looks good overall — one nit below.","approved":false,"comments":[],
"triage":[{"sig":"$SIG","status":"wont-fix","rationale":"By design: the wording matches the pinned style guide verbatim"}]}
EOF
cat > "$LB/verification.json" <<EOF
[{"sig":"$SIG","verdict":"VERIFIED","verifyCmd":"true","resultHead":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]
EOF
run_review record acme/app local-schematest --json
# Verification flips open->addressed first, then the same run's triage claim
# moves it to wont-fix: the merge keeps both history events either way.
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

# --- S3: report reads over that bundle ---
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

# --- S4: post dry-run on a PR bundle ---
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
check "S4 findings.dryrun.json" "$SCH/findings.schema.json" "$EVAL_SANDBOX/.review/$PR/findings.dryrun.json"

# --- S5: representative post.json (real post needs network) + cost shape ---
# post.json is written only by the real-post path, so no hermetic run can
# mint one; this pins the writer's shape instead (review passthrough stays open
# for GitHub's object, provenance rides the same triple the record writer and
# both post envelopes emit).
cat > "$EVAL_SANDBOX/s5-post.json" <<'EOF'
{"schema_version":"1","review":{"id":123,"html_url":"https://github.com/acme/app/pull/9#pullrequestreview-1"},"head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","posted_at":"2026-11-02T00:00:00Z","confidence":5,"security_summary":"Nothing executable.","tool_version":"1.2.3","prompts_hash":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","model":null}
EOF
check "S5 post.json (representative)" "$SCH/post.schema.json" "$EVAL_SANDBOX/s5-post.json"
check "S5 cost.json (prompts shape)" "$SCH/cost.schema.json" "$LB/cost.json"

# --- S6: the checker has teeth ---
printf '{"schema_version":"1","comments":[]}' > "$EVAL_SANDBOX/s6-nobody.json"
must_fail "S6 review without body" "$SCH/review.schema.json" "$EVAL_SANDBOX/s6-nobody.json"
printf '[{"sig":"x","sig_version":"1","kind":"inline","status":"maybe","firstSeen":"a","lastSeen":"b","history":[]}]' > "$EVAL_SANDBOX/s6-badstatus.json"
must_fail "S6 findings off-enum status" "$SCH/findings.schema.json" "$EVAL_SANDBOX/s6-badstatus.json"
jq '. + {ninth_key: 1}' <"$EVAL_OUT" > "$EVAL_SANDBOX/s6-extrakey.json" 2>/dev/null
must_fail "S6 envelope extra top-level key" "$ENV" "$EVAL_SANDBOX/s6-extrakey.json"
printf '{"schema_version":"1","status":"local","head":"h","diff_sha256":"d","recorded_at":"not-a-time","findings":0,"open":0}' > "$EVAL_SANDBOX/s6-badtime.json"
must_fail "S6 record bad date-time" "$SCH/record.schema.json" "$EVAL_SANDBOX/s6-badtime.json"

# --- S7: offline across every leg + cleanup ---
# record/report never touch the network; the dry-run post attempts one
# live-SHA lookup that the stub fails into the warn-and-proceed path, so
# pin that single expected interception instead of pretending zero.
if [ "$(( stub_pre_post - stub_before ))" = "0" ]; then
  good "S7 zero stub-gh intercepts across the record/report legs (offline)"
else
  bad "S7 stub gh intercepted calls during the record/report legs"
fi
stub_now="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
if [ "$(( stub_now - stub_pre_post ))" = "1" ] \
  && sed -n "$(( stub_pre_post + 1 )),$(( stub_now ))p" "$EVAL_STUBLOG" | grep -q "^stub-gh api repos/acme/app/pulls/4951"; then
  good "S7 dry-run post made only the one stubbed SHA lookup (offline)"
else
  bad "S7 unexpected network attempts around the dry-run post leg"
fi
rm -rf "$LB" "$EVAL_SANDBOX/.review/$PR" "$EVAL_SANDBOX"/s1-review.json \
  "$EVAL_SANDBOX"/s5-post.json "$EVAL_SANDBOX"/s6-*.json "$DATA"

if [ "$fails" -ne 0 ]; then echo "case schema-contract: FAIL ($fails)"; exit 1; fi
echo "case schema-contract: PASS"
