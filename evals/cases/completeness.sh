#!/usr/bin/env bash
# Case completeness: opt-in HEAD-reviewed gate on the report verb.
#
# Grades (all hermetic — crafted bundles, stub gh, no network):
#   C1  match-pass: post.json head == fetched HEAD -> --completeness exits 0
#       with a "completeness passes" check: line; --json carries
#       data.completeness {reviewed true, matching heads} with the envelope
#       keys otherwise untouched.
#   C2  mismatch-fail: post head != fetched HEAD -> exit 1, stderr names
#       stale-head plus the --base-sha advance command; --json still reports
#       reviewed false with both heads.
#   C3  missing-post-fail: no post.json -> exit 1, stderr names missing-post
#       plus the plain re-fetch command (no --base-sha: nothing to advance
#       from).
#   C4  corrupt/empty-post-fail: unparsable post.json and head-less post.json
#       both exit 1 (a gate failure, never a tool exit 3).
#   C5  combined: --check <tier> --completeness fails when EITHER dimension
#       fails and passes only when both do.
#   C6  absent-flag parity: without --completeness the same stale bundle is
#       advisory (exit 0, no check: line, no data.completeness key), and
#       --check alone never mentions completeness.
#   C7  dry-run never satisfies: post --dry-run writes no post.json, so the
#       gate still fails afterwards.
#   C8  local bundle: source.json head_sha is the fetched HEAD when PR.json
#       is absent.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

HEAD_A="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
HEAD_C="cccccccccccccccccccccccccccccccccccccccc"
HEAD_D="dddddddddddddddddddddddddddddddddddddddd"

# comp_mk_post <pr> <head|CORRUPT> — craft a completed-post record.
comp_mk_post() {
  local pr="$1" head="$2"
  local dir="$EVAL_SANDBOX/.review/$pr"
  if [ "$head" = "CORRUPT" ]; then
    printf 'not json{{{' > "$dir/post.json"
  else
    jq -n --arg h "$head" \
      '{schema_version:"1",review:{id:99},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null}' \
      > "$dir/post.json"
  fi
}

CRIT_FINDING='[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Critical boundary bug here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"critical","sig":"cccccccccccccccccccccccccccccccccccccccc","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]'

# --- C1: match-pass ---
PR=4261
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
comp_mk_post "$PR" "$HEAD_A"
run_review report acme/app "$PR" --completeness
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "match exits 0"
else
  bad "match exits $EVAL_STATUS, want 0"
fi
if grep -q "completeness passes" "$EVAL_ERR" && grep -q "^check:" "$EVAL_ERR"; then
  good "match prints a check: pass line"
else
  bad "match stderr: $(head -c 300 "$EVAL_ERR")"
fi
run_review report acme/app "$PR" --completeness --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.data.completeness.reviewed == true' <"$EVAL_OUT" >/dev/null 2>&1 \
  && [ "$(jq -r '.data.completeness.fetched_head // empty' <"$EVAL_OUT")" = "$HEAD_A" ] \
  && [ "$(jq -r '.data.completeness.posted_head // empty' <"$EVAL_OUT")" = "$HEAD_A" ]; then
  good "--json carries data.completeness reviewed true with matching heads"
else
  bad "--json completeness: exit $EVAL_STATUS $(jq -c '.data.completeness // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi
if [ "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null)" = "at data error next pr repo schema_version verb warnings" ] \
  && [ "$(jq -r '.data | has("completeness")' <"$EVAL_OUT" 2>/dev/null)" = "true" ] \
  && jq -e '.error == null' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "--json keeps the envelope keys with completeness added under data"
else
  bad "--json envelope shape broke: $(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null)"
fi

# --- C2: mismatch-fail ---
PR=4262
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
comp_mk_post "$PR" "$HEAD_C"
run_review report acme/app "$PR" --completeness
if [ "$EVAL_STATUS" -eq 1 ]; then
  good "mismatch exits 1"
else
  bad "mismatch exits $EVAL_STATUS, want 1"
fi
if grep -q "stale-head" "$EVAL_ERR" && grep -q "fetch acme/app 4262 --base-sha ccccccc" "$EVAL_ERR" && grep -q "^check:" "$EVAL_ERR"; then
  good "mismatch names stale-head plus the --base-sha advance command"
else
  bad "mismatch stderr: $(head -c 300 "$EVAL_ERR")"
fi
run_review report acme/app "$PR" --completeness --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && jq -e '.data.completeness.reviewed == false' <"$EVAL_OUT" >/dev/null 2>&1 \
  && [ "$(jq -r '.data.completeness.fetched_head // empty' <"$EVAL_OUT")" = "$HEAD_A" ] \
  && [ "$(jq -r '.data.completeness.posted_head // empty' <"$EVAL_OUT")" = "$HEAD_C" ]; then
  good "--json reports reviewed false with fetched vs posted heads"
else
  bad "mismatch --json: exit $EVAL_STATUS $(jq -c '.data.completeness // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi

# --- C3: missing-post-fail ---
PR=4263
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
run_review report acme/app "$PR" --completeness
if [ "$EVAL_STATUS" -eq 1 ]; then
  good "missing post exits 1"
else
  bad "missing post exits $EVAL_STATUS, want 1"
fi
if grep -q "missing-post" "$EVAL_ERR" && grep -q "fetch acme/app 4263" "$EVAL_ERR" && ! grep -q -- "--base-sha" "$EVAL_ERR"; then
  good "missing post names missing-post plus the plain re-fetch command"
else
  bad "missing-post stderr: $(head -c 300 "$EVAL_ERR")"
fi

# --- C4: corrupt / empty-head post ---
PR=4264
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
comp_mk_post "$PR" "CORRUPT"
run_review report acme/app "$PR" --completeness
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
run_review report acme/app "$PR" --completeness
if [ "$EVAL_STATUS" -eq 1 ]; then
  good "head-less post exits 1"
else
  bad "head-less post exits $EVAL_STATUS, want 1"
fi

# --- C5: combined tier + completeness ---
PR=4266
eval_mk_bundle "$PR"
printf '%s' "$CRIT_FINDING" | eval_write_findings "$PR"
comp_mk_post "$PR" "$HEAD_A"
run_review report acme/app "$PR" --check critical --completeness
if [ "$EVAL_STATUS" -eq 1 ]; then
  good "tier-hits + reviewed still exits 1"
else
  bad "tier-hits + reviewed exits $EVAL_STATUS, want 1"
fi
if grep -q "at-or-above critical — gate fails" "$EVAL_ERR"; then
  good "combined failure keeps the tier check: line"
else
  bad "combined stderr: $(head -c 300 "$EVAL_ERR")"
fi
run_review report acme/app 4262 --check critical --completeness
if [ "$EVAL_STATUS" -eq 1 ]; then
  good "clean tier + stale post exits 1"
else
  bad "clean tier + stale post exits $EVAL_STATUS, want 1"
fi
if grep -q "no open findings at-or-above critical — gate passes" "$EVAL_ERR" \
  && grep -q "stale-head" "$EVAL_ERR"; then
  good "tier pass and completeness fail lines both print"
else
  bad "combined lines: $(head -c 300 "$EVAL_ERR")"
fi
run_review report acme/app 4261 --check critical --completeness
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "clean tier + reviewed exits 0"
else
  bad "clean tier + reviewed exits $EVAL_STATUS, want 0"
fi
if grep -q "no open findings at-or-above critical — gate passes" "$EVAL_ERR" \
  && grep -q "completeness passes" "$EVAL_ERR"; then
  good "combined pass prints both check: lines"
else
  bad "combined pass stderr: $(head -c 300 "$EVAL_ERR")"
fi

# --- C6: absent-flag parity ---
run_review report acme/app 4262
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "same stale bundle without the flag stays advisory (exit 0)"
else
  bad "plain report exits $EVAL_STATUS, want 0"
fi
if grep -q "check:" "$EVAL_ERR"; then
  bad "plain report prints a check: line"
else
  good "plain report prints no check: line"
fi
run_review report acme/app 4262 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data | has("completeness")' <"$EVAL_OUT" 2>/dev/null)" = "false" ]; then
  good "plain --json carries no data.completeness key"
else
  bad "plain --json leaks completeness: $(jq -c '.data.completeness // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi
run_review report acme/app 4262 --check critical
if [ "$EVAL_STATUS" -eq 0 ] && ! grep -q "completeness" "$EVAL_ERR"; then
  good "--check alone gates only on the tier (exit 0, no completeness)"
else
  bad "--check alone exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi

# --- C7: dry-run never satisfies ---
PR=4267
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] && [ ! -f "$EVAL_SANDBOX/.review/$PR/post.json" ]; then
  good "dry run posts nothing and writes no post.json"
else
  bad "dry run exits $EVAL_STATUS / post.json present"
fi
run_review report acme/app "$PR" --completeness
if [ "$EVAL_STATUS" -eq 1 ] && grep -q "missing-post" "$EVAL_ERR"; then
  good "gate still fails after the dry run"
else
  bad "gate after dry run exits $EVAL_STATUS"
fi

# --- C8: local bundle reads source.json head ---
mkdir -p "$EVAL_SANDBOX/.review/local-compwork"
cat > "$EVAL_SANDBOX/.review/local-compwork/source.json" <<EOF
{"schema_version":"1","kind":"range","repo":"acme/app","slug":"compwork","base_ref":null,"head_ref":"v2","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_sha":"$HEAD_D","range":"v1..v2","dirty":false,"diff_sha256":"0","files":1,"additions":1,"deletions":1,"tool_version":"0.0.0-test","model":null,"created_at":"2026-01-01T00:00:00Z"}
EOF
printf '[]' > "$EVAL_SANDBOX/.review/local-compwork/findings.json"
jq -n --arg h "$HEAD_D" \
  '{schema_version:"1",review:{id:7},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null}' \
  > "$EVAL_SANDBOX/.review/local-compwork/post.json"
run_review report acme/app local-compwork --completeness
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "local bundle matches on source.json head_sha"
else
  bad "local match exits $EVAL_STATUS, want 0"
fi
if grep -q "fetch-local" "$EVAL_ERR"; then
  bad "local pass should name no re-fetch: $(head -c 200 "$EVAL_ERR")"
else
  good "local pass prints no re-fetch hint"
fi
jq -n --arg h "$HEAD_C" \
  '{schema_version:"1",review:{id:7},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null}' \
  > "$EVAL_SANDBOX/.review/local-compwork/post.json"
run_review report acme/app local-compwork --completeness
if [ "$EVAL_STATUS" -eq 1 ] && grep -q "fetch-local acme/app compwork" "$EVAL_ERR"; then
  good "local mismatch names the fetch-local re-fetch command"
else
  bad "local mismatch exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi

if [ "$fails" -ne 0 ]; then echo "case completeness: FAIL ($fails)"; exit 1; fi
echo "case completeness: PASS"
