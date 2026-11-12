#!/usr/bin/env bash
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
# shellcheck source=../lib/hoststub.sh
. "$HERE/../lib/hoststub.sh"
hoststub_init gh
hoststub_prove_dead gh
PR=7
eval_mk_bundle "$PR"
DIR="$EVAL_SANDBOX/.verdict/state/$PR"
QUERY='
    query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
      repository(owner:$owner, name:$name) {
        pullRequest(number:$pr) {
          reviewThreads(first:100, after:$endCursor) {
            nodes { isResolved comments(first:30) { nodes { body path } } }
          }
        }
      }
    }'

jq -n '[range(0;6) | {
 kind:"inline",path:(if .==3 then "unchanged.txt" else "notes.txt" end),
 side:"RIGHT",line:2,anchored:true,body:("Finding "+tostring+" with enough substance."),
 suggestion:null,inBounds:true,rigor:"walked",severity:"important",
 sig:((tostring)*40),sig_version:"1",firstSeen:"aaaaaaa",lastSeen:"aaaaaaa",
 status:"open",rationale:"",history:[],kb:["rule-a","rule-b"]
}]' > "$DIR/findings.json"
jq -n '{schema_version:"1",review:{id:7},head:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",posted_at:"2026-11-03T00:00:00Z",confidence:null,security_summary:null}' > "$DIR/post.json"
jq -n '{headRefOid:"cccccccccccccccccccccccccccccccccccccccc",state:"OPEN",mergedAt:null}' > "$EVAL_SANDBOX/live.json"
jq -n '["notes.txt"]' > "$EVAL_SANDBOX/changed.json"
jq -n '{data:{repository:{pullRequest:{reviewThreads:{nodes:[
 range(0;5) | {isResolved:(.<4),comments:{nodes:[{body:("Finding "+tostring+" with enough substance."),path:(if .==3 then "unchanged.txt" else "notes.txt" end)}]}}
]}}}}}' > "$EVAL_SANDBOX/threads.json"
# The route table is line-based; preserve query newlines as tildes on both sides.
awk '/^printf .*stub-gh/ { print "match=\"$(printf \"%s\" \"$match\" | tr \"\\n\" \"~\")\"" } { print }' "$EVAL_STUBBIN/gh" > "$EVAL_STUBBIN/gh-lines"
mv "$EVAL_STUBBIN/gh-lines" "$EVAL_STUBBIN/gh"
chmod +x "$EVAL_STUBBIN/gh"
QUERY="$(printf '%s' "$QUERY" | tr '\n' '~')"
hoststub_route gh "api graphql --paginate -F owner=acme -F name=app -F pr=7 -f query=$QUERY" "$EVAL_SANDBOX/threads.json"
hoststub_route gh "pr view 7 --repo acme/app --json headRefOid,state,mergedAt" "$EVAL_SANDBOX/live.json"
hoststub_route gh "api repos/acme/app/compare/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa...cccccccccccccccccccccccccccccccccccccccc --jq [.files[].filename]" "$EVAL_SANDBOX/changed.json"
run_review calibrate acme/app 7 --json
assert_exit "live calibration" 0
assert_json_field "fixed" 3 '.data.precision.fixed'
assert_json_field "judged" 5 '.data.precision.judged'
assert_json_field "sample size" 6 '.data.precision.sample_size'
assert_json_field "resolution rate" 0.6 '.data.precision.resolution_rate'
if "$EVAL_SKILL/schemas/validate.sh" "$EVAL_SKILL/schemas/calibration.schema.json" "$DIR/calibration.json" >/dev/null 2>&1; then
  good "saved receipt validates"
else
  bad "saved receipt fails calibration schema"
fi
assert_json_field "receipt version" 1 '.schema_version' "$DIR/calibration.json"
assert_file_grep "human resolution" "$EVAL_ERR" "resolution: 3/5 (60.0%)"
run_review trend acme/app --resolution --json
assert_exit "offline resolution" 0
assert_json_field "model sample size" 5 '.data.resolution.by_model.unmarked.n'
assert_json_field "panel sample size" 5 '.data.resolution.by_panel.unpinned.n'
assert_json_field "reviews" 1 '.data.reviews'
eval_mk_bundle 8
eval_write_findings 8 <<'EOF'
[]
EOF
run_review trend acme/app --resolution --json
assert_exit "missing calibration is unmeasured" 0
assert_json_field "unmeasured" 1 '.data.resolution.unmeasured'
assert_json_field "clean reviews" 1 '.data.reviews_clean'
assert_json_field "clean rate" 0.5 '.data.clean_rate'
run_review report acme/app 8
assert_exit "clean report" 0
assert_file_grep "clean headline" "$EVAL_OUT" "no findings — checked"

jq '[.[5]]' "$DIR/findings.json" > "$EVAL_SANDBOX/unknown.json"
cp "$EVAL_SANDBOX/unknown.json" "$DIR/findings.json"
run_review calibrate acme/app 7 --json
assert_exit "zero denominator calibration" 0
assert_json_field "zero judged" 0 '.data.precision.judged'
assert_json_field "zero denominator is null" null '.data.precision.resolution_rate'
if "$EVAL_SKILL/schemas/validate.sh" "$EVAL_SKILL/schemas/calibration.schema.json" "$DIR/calibration.json" >/dev/null 2>&1; then
  good "null resolution rate validates"
else
  bad "null resolution rate fails calibration schema"
fi
assert_file_grep "human unmeasured rate" "$EVAL_ERR" "0/0 (n/a)"
jq 'map(.status="addressed")' "$DIR/findings.json" > "$EVAL_SANDBOX/addressed.json"
cp "$EVAL_SANDBOX/addressed.json" "$DIR/findings.json"
run_review report acme/app 7
assert_exit "addressed report" 0
assert_file_grep "addressed headline" "$EVAL_OUT" "no open findings — 1 addressed, 0 terminal"
hoststub_assert_all_routed
eval_end
