#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

STUB_SAVED="$EVAL_SANDBOX/gh-stub-orig"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
restore_stub() { cp "$STUB_SAVED" "$EVAL_STUBBIN/gh"; }
trap restore_stub EXIT
cat > "$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $(printf '%s' "$*" | tr '\n' '~')" >> "${EVAL_STUBLOG:-/dev/null}"
if [ "${1:-}" = "api" ] && [ "${2:-}" = "graphql" ]; then
  cat "${EVAL_SANDBOX:-/tmp}/cat-threads.json"
  exit 0
fi
if [ "${1:-}" = "pr" ] && [ "${2:-}" = "view" ]; then
  printf '%s' '{"headRefOid":"cccccccccccccccccccccccccccccccccccccccc","state":"OPEN","mergedAt":null}'
  exit 0
fi
if [ "$*" = "api repos/acme/app/pulls/4960/comments --paginate --slurp" ]; then
  printf '%s' '[[{"pull_request_review_id":7,"body":"Security boundary bug here with enough substance.","path":"notes.txt","reactions":{"+1":1,"-1":1}}]]'
  exit 0
fi
if [ "${1:-}" = "api" ]; then
  printf '%s' '["notes.txt"]'
  exit 0
fi
echo "evals-stub-gh: network disabled ($*)" >&2
exit 1
EOF
chmod +x "$EVAL_STUBBIN/gh"

mark() { wc -l <"$EVAL_STUBLOG" | tr -d ' '; }

PR=4960
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Category review with enough substance here.","approved":false,
"comments":[
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Security boundary bug here with enough substance.","rigor":"walked","severity":"critical","category":"security"},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Style nit here with enough substance to keep.","rigor":"pointed","severity":"informational","category":"style"},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Untagged observation here with enough substance.","rigor":"pointed","severity":"informational"}]}
EOF

# 2. post --dry-run keeps the categories, and sigs ignore them.
m="$(mark)"
run_review post acme/app "$PR" --dry-run --json
assert_exit "post --dry-run exits 0" 0
DRY="$EVAL_SANDBOX/.verdict/state/$PR/findings.dryrun.json"
if [ "$(jq -r '.[0].category' "$DRY")" = "security" ] \
  && [ "$(jq -r '.[1].category' "$DRY")" = "style" ] \
  && [ "$(jq -r '.[2].category' "$DRY")" = "null" ]; then
  good "dry-run findings carry security, style, and null"
else
  bad "dry-run categories: $(jq -c '[.[].category]' "$DRY")"
fi
jq -r '.[].sig' "$DRY" | LC_ALL=C sort > "$EVAL_SANDBOX/cat-sigs.txt"
jq -c '.[] | del(.category)' "$DRY" > /dev/null || bad "dry-run findings unreadable"
if [ "$(( $(mark) - m ))" = "1" ] \
  && tail -n 1 "$EVAL_STUBLOG" | matches "^stub-gh api repos/acme/app/pulls/"; then
  good "dry-run post made only the one stubbed SHA lookup (offline)"
else
  bad "unexpected network around the dry-run post leg"
fi
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Category review with enough substance here.","approved":false,
"comments":[
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Security boundary bug here with enough substance.","rigor":"walked","severity":"critical"},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Style nit here with enough substance to keep.","rigor":"pointed","severity":"informational"},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Untagged observation here with enough substance.","rigor":"pointed","severity":"informational"}]}
EOF
run_review post acme/app "$PR" --dry-run --json
assert_exit "uncategorized post --dry-run exits 0" 0
jq -r '.[].sig' "$DRY" | LC_ALL=C sort > "$EVAL_SANDBOX/nocat-sigs.txt"
if cmp -s "$EVAL_SANDBOX/cat-sigs.txt" "$EVAL_SANDBOX/nocat-sigs.txt"; then
  good "sigs match with the categories removed (category never enters the sig)"
else
  bad "sigs moved when the categories were removed"
fi

# 3-4. report filter, check gate, and the bad-category usage error.
SEC_SIG="$(jq -r '.[0].sig' "$DRY")"
eval_write_findings "$PR" <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Security boundary bug here with enough substance.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"critical","category":"security","sig":"$SEC_SIG","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Style nit here with enough substance to keep.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","category":"style","sig":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Untagged observation here with enough substance.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","category":null,"sig":"cccccccccccccccccccccccccccccccccccccccc","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
run_review report acme/app "$PR" --json
assert_exit "report --json exits 0" 0
if [ "$(jq -c '.data.counts.by_category' <"$EVAL_OUT")" = '{"accidental-commit":0,"docs":0,"edge-case":0,"logic":0,"other":0,"performance":0,"security":1,"style":1,"untagged":1}' ]; then
  good "report by_category has every key plus untagged, zero-filled"
else
  bad "report by_category: $(jq -c '.data.counts.by_category' <"$EVAL_OUT")"
fi
run_review report acme/app "$PR" --category security --json
assert_exit "report --category security exits 0" 0
if [ "$(jq -r '.data.items | length' <"$EVAL_OUT")" = "1" ] \
  && [ "$(jq -r '.data.items[0].sig' <"$EVAL_OUT")" = "$SEC_SIG" ]; then
  good "report --category security lists only the security finding"
else
  bad "report --category security items: $(jq -c '.data.items | map(.sig)' <"$EVAL_OUT")"
fi
run_review report acme/app "$PR" --check critical --category security --live skip --json
assert_exit "gate on the security critical exits 1" 1
assert_file_grep "gate failure names the category" "$EVAL_ERR" "in security"
run_review report acme/app "$PR" --check critical --category style --live skip --json
assert_exit "gate on style passes (no critical style finding)" 0
run_review report acme/app "$PR" --category nope --json
assert_exit "report --category nope exits 2" 2
assert_file_grep "filter typo is bad-category" "$EVAL_ERR" "bad-category"

# 5. calibrate joins per-category precision; a zero denominator reads null.
jq -n --arg sec "Security boundary bug here with enough substance." \
  --arg sty "Style nit here with enough substance to keep." \
  '{data:{repository:{pullRequest:{reviewThreads:{nodes:[
    {isResolved:true,comments:{nodes:[{body:$sec,path:"notes.txt"}]}},
    {isResolved:false,comments:{nodes:[{body:$sty,path:"notes.txt"}]}}
  ]}}}}}' > "$EVAL_SANDBOX/cat-threads.json"
jq -n --arg sig "$SEC_SIG" \
  '{schema_version:"1",review:{id:7},head:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",posted_at:"2026-11-02T00:00:00Z",confidence:null,security_summary:null}' \
  > "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
m="$(mark)"
run_review calibrate acme/app "$PR" --json
assert_exit "calibrate --json exits 0" 0
if jq -e '.data.precision.by_category.security == {"fixed":1,"resolved_unfixed":0,"open":0,"unknown":0,"judged":1,"resolution_rate":1}' <"$EVAL_OUT" >/dev/null; then
  good "calibrate security cell counts the fixed finding at rate 1"
else
  bad "calibrate security cell: $(jq -c '.data.precision.by_category.security' <"$EVAL_OUT")"
fi
if jq -e '.data.precision.by_category.untagged.resolution_rate == null and .data.precision.by_category.untagged.judged == 0' <"$EVAL_OUT" >/dev/null; then
  good "zero-denominator untagged cell renders resolution_rate null"
else
  bad "untagged cell: $(jq -c '.data.precision.by_category.untagged' <"$EVAL_OUT")"
fi
if [ "$(( $(mark) - m ))" = "3" ]; then
  good "calibrate made exactly the three stubbed reads (threads, PR, compare)"
else
  bad "calibrate network legs: $(( $(mark) - m )), want 3"
fi

# 6. trend carries the map on the rollup and the per-bundle row.
run_review trend acme/app --rules --json
assert_exit "trend --json exits 0" 0
if jq -e --arg pr "$PR" '.data.per_bundle[] | select(.bundle == $pr) | .by_category.security == 1' <"$EVAL_OUT" >/dev/null; then
  good "trend per-bundle row carries the security count"
else
  bad "trend per-bundle rows: $(jq -c '.data.per_bundle | map({bundle, by_category})' <"$EVAL_OUT")"
fi
if jq -e '.data.by_category | type == "object"' <"$EVAL_OUT" >/dev/null; then
  good "trend rollup carries by_category"
else
  bad "trend rollup misses by_category"
fi


if jq -e '.data.rules | type == "object"' <"$EVAL_OUT" >/dev/null; then
  good "trend --rules publishes rule buckets"
else
  bad "trend --rules missing rule buckets"
fi

run_review calibrate acme/app "$PR" --reactions --json
assert_exit "reaction read exits 0" 0
if jq -e '.data.proposals | length == 1 and .[0].proposed == "helpful"' <"$EVAL_OUT" >/dev/null; then
  good "unreasoned downvote never becomes a claim"
else
  bad "unexpected unreasoned reaction proposals"
fi
run_review calibrate acme/app "$PR" --reactions --reaction-reason 'The cited branch is unreachable.' --json
assert_exit "reasoned reaction read exits 0" 0
if jq -e '.data.proposals | map(select(.proposed == "false-positive"))[0].claim.rationale == "The cited branch is unreachable."' <"$EVAL_OUT" >/dev/null; then
  good "reasoned downvote proposes an existing triage claim"
else
  bad "missing reasoned triage claim"
fi

eval_end
