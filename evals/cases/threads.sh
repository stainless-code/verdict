#!/usr/bin/env bash
# Case threads: `verdict threads` refreshes the normalized PR-conversation
# contract from the host.
#
# Grades (all hermetic — a canned gh shim first on PATH, no network):
#   T1  refresh + envelope: the verb re-reads the two GraphQL pages, writes
#       only .review/<pr>/threads.json (the diff and PR.json stay byte-equal),
#       and emits the nine-key envelope with the feed tally.
#   T2  contract fields: host thread id, numeric comment ids, resolved and
#       outdated from the host's own fields, author_kind verdict/bot/human,
#       finding_sig from verdict's final-line marker, and the 8 KiB comment
#       cap with its truncated flag.
#   T3  schema: the written feed validates against the generated
#       schemas/threads.schema.json.
#   T4  human mode and a re-run after resolution: done/next lines and the
#       next hint flip once every thread is resolved.
#   T5  failure posture: a failed total read refuses typed and leaves the
#       previous feed untouched (never silently emptied); a bad provider
#       refuses, and a missing bundle names the fetch step.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

SIG="6c3ee749e3d1f97870c9e8b5c1171fae2cb9ff4e"
HEAD="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

mkdir -p "$EVAL_SANDBOX/.review/7"
cat >"$EVAL_SANDBOX/.review/7/PR.json" <<EOF
{"state":"OPEN","headRefOid":"$HEAD","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"teammate"}}
EOF
printf 'diff sentinel\n' >"$EVAL_SANDBOX/.review/7/full.diff"
cp "$EVAL_SANDBOX/.review/7/PR.json" "$EVAL_SANDBOX/pr.before"
cp "$EVAL_SANDBOX/.review/7/full.diff" "$EVAL_SANDBOX/diff.before"

long_body="$(dd if=/dev/zero bs=9000 count=1 2>/dev/null | tr '\0' 'x')"
jq -n --arg sig "$SIG" --arg long "$long_body" '
  {data:{repository:{pullRequest:{reviewThreads:{
    pageInfo:{hasNextPage:true,endCursor:"c2"},
    nodes:[{id:"PRRT_1",isResolved:false,isOutdated:false,path:"notes.txt",line:2,
      startLine:null,diffSide:"RIGHT",
      comments:{totalCount:2,nodes:[
        {id:"PRRC_1",databaseId:101,author:{__typename:"Bot",login:"verdict[bot]"},
         body:("nit\n\n<!-- verdict-finding sig=" + $sig + " -->"),
         createdAt:"2026-11-01T10:00:00Z",url:"https://example.invalid/c/101"},
        {id:"PRRC_2",databaseId:102,author:{__typename:"User",login:"mate"},
         body:$long,createdAt:"2026-11-01T11:00:00Z",url:"https://example.invalid/c/102"}
      ]}}]}}}}}' >"$EVAL_SANDBOX/page1.json"
jq -n '
  {data:{repository:{pullRequest:{reviewThreads:{
    pageInfo:{hasNextPage:false,endCursor:null},
    nodes:[
      {id:"PRRT_2",isResolved:true,isOutdated:true,path:"notes.txt",line:null,
       startLine:null,diffSide:"LEFT",
       comments:{totalCount:1,nodes:[{id:"PRRC_3",databaseId:103,
         author:{__typename:"Bot",login:"dependabot[bot]"},body:"bump",
         createdAt:"2026-10-01T09:00:00Z",url:"https://example.invalid/c/103"}]}},
      {id:"PRRT_3",isResolved:false,isOutdated:false,path:"notes.txt",line:1,
       startLine:null,diffSide:"RIGHT",
       comments:{totalCount:1,nodes:[{id:"PRRC_4",databaseId:104,
         author:{__typename:"User",login:"ana"},body:"question",
         createdAt:"2026-11-02T09:00:00Z",url:"https://example.invalid/c/104"}]}}
    ]}}}}}' >"$EVAL_SANDBOX/page2.json"

# Smart gh shim: serves the pages, branches on the cursor, fails on demand.
cat >"$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >>"$EVAL_STUBLOG"
case "$*" in
  *"api graphql"*)
    if [ "${THREADS_GH_FAIL:-0}" = "1" ]; then
      echo "gh: threads down" >&2
      exit 1
    fi
    case "$*" in
      *endCursor=c2*) cat "$THREADS_PAGE2"; exit 0 ;;
      *) cat "$THREADS_PAGE1"; exit 0 ;;
    esac
    ;;
esac
echo "unexpected gh call: $*" >&2
exit 1
EOF
chmod +x "$EVAL_STUBBIN/gh"
export THREADS_PAGE1="$EVAL_SANDBOX/page1.json" THREADS_PAGE2="$EVAL_SANDBOX/page2.json"

# --- T1/T2/T3: refresh, contract, schema ---
: >"$EVAL_STUBLOG"
run_review threads acme/app 7 --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(stdout_docs)" = "1" ] \
  && jq -e --arg head "$HEAD" '
    .verb == "threads" and .repo == "acme/app" and .pr == 7 and .error == null
    and (.data | .provider == "github" and .head_sha == $head and .truncated == false
      and .threads == 3 and .open == 2 and .outdated == 1 and .verdict == 1)
    and (.next | contains("address open threads"))' <"$EVAL_OUT" >/dev/null; then
  good "T1 refresh emits the threads envelope with the feed tally"
else
  bad "T1 threads --json exit $EVAL_STATUS: $(head -c 400 "$EVAL_ERR")"
fi
if [ "$(jq -s -c '.[0] | keys' <"$EVAL_OUT")" = '["at","data","error","next","pr","repo","schema_version","verb","warnings"]' ]; then
  good "T1 envelope keeps exactly the nine published keys"
else
  bad "T1 envelope keys drifted: $(jq -s -c '.[0] | keys' <"$EVAL_OUT")"
fi
assert_eq "T1 re-reads both thread pages" "2" "$(grep -c 'api graphql' "$EVAL_STUBLOG")"
assert_stream_nogrep "T1 never refetches the diff" "$EVAL_STUBLOG" ' pr diff | repos/.*/pulls/' "-E"
if cmp -s "$EVAL_SANDBOX/pr.before" "$EVAL_SANDBOX/.review/7/PR.json" \
  && cmp -s "$EVAL_SANDBOX/diff.before" "$EVAL_SANDBOX/.review/7/full.diff"; then
  good "T1 touches only threads.json"
else
  bad "T1 rewrote the bundle metadata or diff"
fi
FEED="$EVAL_SANDBOX/.review/7/threads.json"
if jq -e --arg head "$HEAD" --arg sig "$SIG" '
  .schema_version == "1" and .provider == "github" and .pr == 7 and .head_sha == $head
  and .truncated == false and (.threads | length) == 3
  and (.threads[0] | .id == "PRRT_1" and .resolved == false and .outdated == false
    and .path == "notes.txt" and .line == 2 and .side == "RIGHT"
    and .author_kind == "verdict" and .finding_sig == $sig
    and .comments[0].id == "101"
    and .comments[1].truncated == true
    and (.comments[1].body | length) == 8192)
  and (.threads[1] | .resolved == true and .outdated == true and .author_kind == "bot")
  and (.threads[2] | .resolved == false and .author_kind == "human")' "$FEED" >/dev/null; then
  good "T2 host ids, resolved/outdated, author kinds, finding_sig, and the 8 KiB cap land"
else
  bad "T2 feed shape: $(jq -c '.threads[0]' "$FEED" 2>/dev/null | head -c 400)"
fi
if "$EVAL_SKILL/schemas/validate.sh" "$EVAL_SKILL/schemas/threads.schema.json" "$FEED" >/dev/null 2>&1; then
  good "T3 feed validates against schemas/threads.schema.json"
else
  bad "T3 feed fails schema validation: $("$EVAL_SKILL/schemas/validate.sh" "$EVAL_SKILL/schemas/threads.schema.json" "$FEED" 2>&1 | head -3)"
fi

# --- T4: human mode, then a re-run after resolution ---
run_review threads acme/app 7
if [ "$EVAL_STATUS" -eq 0 ] && grep -q '^done: 3 threads (2 open, 1 outdated)' "$EVAL_OUT" \
  && grep -q '^next: address open threads' "$EVAL_OUT"; then
  good "T4 human mode prints done/next and the resolved hint"
else
  bad "T4 human output: $(head -c 300 "$EVAL_OUT")"
fi
jq '.data.repository.pullRequest.reviewThreads.nodes[0].isResolved = true' \
  "$EVAL_SANDBOX/page1.json" >"$EVAL_SANDBOX/page1-resolved.json"
jq '.data.repository.pullRequest.reviewThreads.nodes[1].isResolved = true' \
  "$EVAL_SANDBOX/page2.json" >"$EVAL_SANDBOX/page2-resolved.json"
export THREADS_PAGE1="$EVAL_SANDBOX/page1-resolved.json" THREADS_PAGE2="$EVAL_SANDBOX/page2-resolved.json"
run_review threads acme/app 7 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.data.open == 0 and (.next | contains("no open threads"))' <"$EVAL_OUT" >/dev/null \
  && jq -e '.threads[0].resolved == true and .threads[2].resolved == true' "$FEED" >/dev/null; then
  good "T4 a resolved thread refreshes the feed and flips next"
else
  bad "T4 resolved re-run: exit $EVAL_STATUS $(head -c 300 "$EVAL_ERR")"
fi

# --- T5: failure posture ---
before_sum="$( (shasum -a 256 "$FEED" 2>/dev/null || sha256sum "$FEED") | cut -d' ' -f1)"
: >"$EVAL_STUBLOG"
THREADS_GH_FAIL=1 run_review threads acme/app 7 --json
after_sum="$( (shasum -a 256 "$FEED" 2>/dev/null || sha256sum "$FEED") | cut -d' ' -f1)"
if [ "$EVAL_STATUS" -eq 1 ] \
  && jq -e '.error.code == "threads-failed" and .data == null' <"$EVAL_OUT" >/dev/null \
  && [ "$before_sum" = "$after_sum" ]; then
  good "T5 a failed total read refuses typed and keeps the previous feed"
else
  bad "T5 failure posture: exit $EVAL_STATUS, feed changed=$([ "$before_sum" = "$after_sum" ] && echo no || echo yes)"
fi
run_review threads acme/app 7 --provider gitea --json
if [ "$EVAL_STATUS" -eq 1 ] && jq -e '.error.code == "bad-provider"' <"$EVAL_OUT" >/dev/null; then
  good "T5 an unknown provider refuses typed"
else
  bad "T5 bad provider: exit $EVAL_STATUS $(head -c 200 "$EVAL_ERR")"
fi
run_review threads acme/app 99 --json
if [ "$EVAL_STATUS" -eq 1 ] && jq -e '.error.code == "fetch-failed"' <"$EVAL_OUT" >/dev/null \
  && jq -r '.error.message' <"$EVAL_OUT" | matches 'fetch the PR first'; then
  good "T5 a missing bundle names the fetch step"
else
  bad "T5 missing bundle: exit $EVAL_STATUS $(head -c 200 "$EVAL_ERR")"
fi
run_review threads acme/app 7 --bogus
if [ "$EVAL_STATUS" -eq 2 ] && [ ! -s "$EVAL_OUT" ] && grep -q -- "--bogus" "$EVAL_ERR"; then
  good "T5 an unknown flag is a usage error with no envelope"
else
  bad "T5 unknown flag: exit $EVAL_STATUS $(head -c 200 "$EVAL_ERR")"
fi

rm -f "$EVAL_SANDBOX"/page1.json "$EVAL_SANDBOX"/page1-resolved.json \
  "$EVAL_SANDBOX"/page2.json "$EVAL_SANDBOX"/page2-resolved.json \
  "$EVAL_SANDBOX"/pr.before "$EVAL_SANDBOX"/diff.before

eval_end
