#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

HEAD_A="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
HEAD_C="cccccccccccccccccccccccccccccccccccccccc"
HEAD_D="dddddddddddddddddddddddddddddddddddddddd"

comp_mk_post() {
  local pr="$1" head="$2"
  local dir="$EVAL_SANDBOX/.verdict/state/$pr"
  if [ "$head" = "CORRUPT" ]; then
    printf 'not json{{{' > "$dir/post.json"
  else
    # The stored evidence receipt a completed post leaves beside the head: the
    # always-on evidence dimension grades it under --check.
    jq -n --arg h "$head" --argjson e "$(eval_evidence_receipt "$dir" "$head")" \
      '{schema_version:"1",review:{id:99},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null,evidence:$e}' \
      > "$dir/post.json"
  fi
}

CRIT_FINDING='[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Critical boundary bug here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"critical","sig":"cccccccccccccccccccccccccccccccccccccccc","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]'

PR=4261
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
comp_mk_post "$PR" "$HEAD_A"
run_review gate acme/app "$PR" --require-head --coverage warn --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "match exits 0"
else
  bad "match exits $EVAL_STATUS, want 0"
fi
if grep -qE "^gate: head +pass" "$EVAL_ERR"; then
  good "match prints a passing head line"
else
  bad "match stderr: $(head -c 300 "$EVAL_ERR")"
fi
run_review gate acme/app "$PR" --require-head --coverage warn --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.dimensions[] | select(.id == "head") | .status' <"$EVAL_OUT")" = "pass" ] \
  && [ "$(jq -r '.data.dimensions[] | select(.id == "head") | .detail' <"$EVAL_OUT")" = "HEAD ${HEAD_A:0:7} reviewed (post matches fetch)" ]; then
  good "--json carries a passing head dimension naming the matched head"
else
  bad "--json head dimension: exit $EVAL_STATUS $(jq -c '.data.dimensions // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi
if [ "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null)" = "at data error next pr repo schema_version verb warnings" ] \
  && [ "$(jq -r '.data.passed' <"$EVAL_OUT" 2>/dev/null)" = "true" ] \
  && jq -e '.error == null' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "--json keeps the envelope keys with the verdict under data"
else
  bad "--json envelope shape broke: $(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null)"
fi

PR=4262
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
comp_mk_post "$PR" "$HEAD_C"
run_review gate acme/app "$PR" --require-head --coverage warn --json
if [ "$EVAL_STATUS" -eq 1 ]; then
  good "mismatch exits 1"
else
  bad "mismatch exits $EVAL_STATUS, want 1"
fi
if grep -q "stale-head" "$EVAL_ERR" && grep -q "fetch acme/app 4262 --base-sha ccccccc" "$EVAL_ERR" && grep -qE "^gate: head +fail" "$EVAL_ERR"; then
  good "mismatch names stale-head plus the --base-sha advance command"
else
  bad "mismatch stderr: $(head -c 300 "$EVAL_ERR")"
fi
run_review gate acme/app "$PR" --require-head --coverage warn --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.data.dimensions[] | select(.id == "head") | .status' <"$EVAL_OUT")" = "fail" ] \
  && jq -e --arg d "posted HEAD ${HEAD_C:0:7} != fetched HEAD ${HEAD_A:0:7}" '.data.dimensions[] | select(.id == "head") | .detail | startswith($d)' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "--json fails the head dimension with fetched vs posted heads"
else
  bad "mismatch --json: exit $EVAL_STATUS $(jq -c '.data.dimensions // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi

PR=4263
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
run_review gate acme/app "$PR" --require-head --coverage warn --json
if [ "$EVAL_STATUS" -eq 1 ]; then
  good "missing post exits 1"
else
  bad "missing post exits $EVAL_STATUS, want 1"
fi
assert_file_grep "missing post names the typed code" "$EVAL_ERR" "missing-post"
assert_file_grep "missing post names the plain re-fetch command" "$EVAL_ERR" "fetch acme/app 4263"
assert_stream_nogrep "missing post keeps no --base-sha" "$EVAL_ERR" "--base-sha"

PR=4264
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
comp_mk_post "$PR" "CORRUPT"
run_review gate acme/app "$PR" --require-head --coverage warn --json
if [ "$EVAL_STATUS" -eq 1 ]; then
  good "unparsable post exits 1 (gate, not tool error)"
else
  bad "unparsable post exits $EVAL_STATUS, want 1"
fi
if grep -q "corrupt-post" "$EVAL_ERR"; then
  good "unparsable post names corrupt-post"
else
  bad "corrupt stderr: $(head -c 300 "$EVAL_ERR")"
fi
PR=4265
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
comp_mk_post "$PR" ""
run_review gate acme/app "$PR" --require-head --coverage warn --json
if [ "$EVAL_STATUS" -eq 1 ]; then
  good "head-less post exits 1"
else
  bad "head-less post exits $EVAL_STATUS, want 1"
fi

PR=4266
eval_mk_bundle "$PR"
printf '%s' "$CRIT_FINDING" | eval_write_findings "$PR"
comp_mk_post "$PR" "$HEAD_A"
run_review gate acme/app "$PR" --max-severity critical --require-head --coverage warn --json
if [ "$EVAL_STATUS" -eq 1 ]; then
  good "tier-hits + reviewed still exits 1"
else
  bad "tier-hits + reviewed exits $EVAL_STATUS, want 1"
fi
if grep -qE "^gate: findings +fail" "$EVAL_ERR"; then
  good "combined failure keeps the findings line"
else
  bad "combined stderr: $(head -c 300 "$EVAL_ERR")"
fi
run_review gate acme/app 4262 --max-severity critical --require-head --coverage warn --json
if [ "$EVAL_STATUS" -eq 1 ]; then
  good "clean tier + stale post exits 1"
else
  bad "clean tier + stale post exits $EVAL_STATUS, want 1"
fi
if grep -qE "^gate: findings +pass" "$EVAL_ERR" \
  && grep -q "stale-head" "$EVAL_ERR"; then
  good "tier pass and head fail lines both print"
else
  bad "combined lines: $(head -c 300 "$EVAL_ERR")"
fi
run_review gate acme/app 4261 --max-severity critical --require-head --coverage warn --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "clean tier + reviewed exits 0"
else
  bad "clean tier + reviewed exits $EVAL_STATUS, want 0"
fi
if grep -qE "^gate: findings +pass" "$EVAL_ERR" \
  && grep -qE "^gate: head +pass" "$EVAL_ERR"; then
  good "combined pass prints both dimension lines"
else
  bad "combined pass stderr: $(head -c 300 "$EVAL_ERR")"
fi

run_review report acme/app 4262
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "same stale bundle without the flag stays advisory (exit 0)"
else
  bad "plain report exits $EVAL_STATUS, want 0"
fi
assert_stream_nogrep "plain report prints no check: line" "$EVAL_ERR" "check:"
run_review report acme/app 4262 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data | has("completeness")' <"$EVAL_OUT" 2>/dev/null)" = "false" ]; then
  good "plain --json carries no data.completeness key"
else
  bad "plain --json leaks completeness: $(jq -c '.data.completeness // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi
# The bundle whose post names the fetched head is the one a check can pass: a
# receipt naming another head fails the always-on evidence dimension, which
# --require-head does not govern.
run_review gate acme/app 4261 --max-severity critical --coverage warn --json
assert_exit "--max-severity alone exits 0" 0
assert_stream_nogrep "--max-severity alone grades no head dimension" "$EVAL_ERR" "gate: head"

PR=4267
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] && [ ! -f "$EVAL_SANDBOX/.verdict/state/$PR/post.json" ]; then
  good "dry run posts nothing and writes no post.json"
else
  bad "dry run exits $EVAL_STATUS / post.json present"
fi
run_review gate acme/app "$PR" --require-head --coverage warn --json
if [ "$EVAL_STATUS" -eq 1 ] && grep -q "missing-post" "$EVAL_ERR"; then
  good "gate still fails after the dry run"
else
  bad "gate after dry run exits $EVAL_STATUS"
fi

mkdir -p "$EVAL_SANDBOX/.verdict/state/local-compwork"
cat > "$EVAL_SANDBOX/.verdict/state/local-compwork/source.json" <<EOF
{"schema_version":"1","kind":"range","repo":"acme/app","slug":"compwork","base_ref":null,"head_ref":"v2","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_sha":"$HEAD_D","range":"v1..v2","dirty":false,"diff_sha256":"0","files":1,"additions":1,"deletions":1,"tool_version":"0.0.0-test","model":null,"created_at":"2026-01-01T00:00:00Z"}
EOF
printf '[]' > "$EVAL_SANDBOX/.verdict/state/local-compwork/findings.json"
jq -n --arg h "$HEAD_D" \
  '{schema_version:"1",review:{id:7},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null}' \
  > "$EVAL_SANDBOX/.verdict/state/local-compwork/post.json"
# The bundle carries no diff, so its receipt attests the empty change it
# reviews; a gate mode that graded the head without one would be grading a
# review nobody evidenced.
eval_write_record_evidence local-compwork
run_review gate acme/app local-compwork --require-head --coverage warn --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "local bundle matches on source.json head_sha"
else
  bad "local match exits $EVAL_STATUS, want 0"
fi
assert_stream_nogrep "local pass prints no re-fetch hint" "$EVAL_ERR" "fetch-local"
jq -n --arg h "$HEAD_C" \
  '{schema_version:"1",review:{id:7},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null}' \
  > "$EVAL_SANDBOX/.verdict/state/local-compwork/post.json"
run_review gate acme/app local-compwork --require-head --coverage warn --json
if [ "$EVAL_STATUS" -eq 1 ] && grep -q "fetch-local acme/app compwork" "$EVAL_ERR"; then
  good "local mismatch names the fetch-local re-fetch command"
else
  bad "local mismatch exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi

eval_end
