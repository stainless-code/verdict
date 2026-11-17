#!/usr/bin/env bash
# drain refuses bad drafts, truncated feeds, and the unshipped post flag.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

SIG="6c3ee749e3d1f97870c9e8b5c1171fae2cb9ff4e"
HEAD="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
DIR="$EVAL_SANDBOX/.verdict/state/7"
mkdir -p "$DIR"
jq -n --arg head "$HEAD" '{state:"OPEN",headRefOid:$head,baseRefOid:"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",author:{login:"teammate"}}' >"$DIR/PR.json"
printf 'diff sentinel\n' >"$DIR/full.diff"
RB="finding"$'\n\n'"<!-- verdict-finding sig=$SIG -->"
jq -n --arg body "$RB" '{data:{repository:{pullRequest:{reviewThreads:{pageInfo:{hasNextPage:false,endCursor:null},nodes:[
  {id:"PRRT_1",isResolved:false,isOutdated:false,path:"a.go",line:10,startLine:null,diffSide:"RIGHT",comments:{totalCount:2,nodes:[
    {id:"101",databaseId:101,author:{__typename:"Bot",login:"verdict-bot"},body:$body,createdAt:"2026-11-01T00:00:00Z"},
    {id:"102",databaseId:102,author:{__typename:"User",login:"alice"},body:"a human question",createdAt:"2026-11-02T00:00:00Z"}]}}
]}}}}}' >"$EVAL_SANDBOX/page.json"
jq '.data.repository.pullRequest.reviewThreads.pageInfo |= {hasNextPage:true,endCursor:"cursor"}' "$EVAL_SANDBOX/page.json" >"$EVAL_SANDBOX/truncated.json"

cat >"$EVAL_STUBBIN/gh" <<'GH'
#!/usr/bin/env bash
echo "stub-gh $*" >>"$EVAL_STUBLOG"
case "$*" in
  "api user --jq .login") printf 'verdict-bot\n'; exit 0;;
  *"api graphql"*) cat "$DRAIN_PAGE"; exit 0;;
esac
echo "unexpected gh call: $*" >&2
exit 1
GH
chmod +x "$EVAL_STUBBIN/gh"
export DRAIN_PAGE="$EVAL_SANDBOX/page.json"
cat >"$EVAL_STUBBIN/claude" <<'AGENT'
#!/usr/bin/env bash
echo "stub-agent $*" >>"$EVAL_STUBLOG"
case "${DRAIN_AGENT_MODE:-}" in
  bad-key) digest="$(jq -r .plan_digest "$DIR/drain.json")";
    jq -n --arg digest "$digest" '{schema_version:"1",plan_digest:$digest,replies:[{thread_id:"PRRT_1",waiting_comment_id:"102",disposition:"answer",body:"answer",extra:true}]}' >"$DIR/drain.replies.json";;
  stale) jq -n '{schema_version:"1",plan_digest:"old",replies:[]}' >"$DIR/drain.replies.json";;
esac
exit 0
AGENT
chmod +x "$EVAL_STUBBIN/claude"
export DIR
: >"$EVAL_STUBLOG"

rm -f "$DIR/drain.replies.json"
DRAIN_AGENT_MODE=bad-key run_review drain acme/app 7 --agent claude --json
if [ "$EVAL_STATUS" -eq 1 ] && jq -e '.error.code == "drafts-invalid"
  and (.error.message | contains("drain.replies.json replies[0].extra"))' <"$EVAL_OUT" >/dev/null; then
  good "R1 an unknown draft key refuses drafts-invalid naming the site"
else
  bad "R1 bad key: exit $EVAL_STATUS $(head -c 500 "$EVAL_OUT")"
fi

rm -f "$DIR/drain.replies.json"
DRAIN_AGENT_MODE=stale run_review drain acme/app 7 --agent claude --json
if [ "$EVAL_STATUS" -eq 1 ] && jq -e '.error.code == "drafts-stale"
  and (.error.message | contains("plan_digest"))' <"$EVAL_OUT" >/dev/null; then
  good "R2 a draft against an old plan refuses drafts-stale"
else
  bad "R2 stale draft: exit $EVAL_STATUS $(head -c 500 "$EVAL_OUT")"
fi

export DRAIN_PAGE="$EVAL_SANDBOX/truncated.json"
run_review drain acme/app 7 --json
if [ "$EVAL_STATUS" -eq 1 ] && jq -e '.error.code == "drain-failed"
  and (.error.message | contains("truncated"))' <"$EVAL_OUT" >/dev/null; then
  good "R3 a truncated feed refuses drain-failed without planning"
else
  bad "R3 truncated feed: exit $EVAL_STATUS $(head -c 500 "$EVAL_OUT")"
fi

: >"$EVAL_STUBLOG"
run_review drain acme/app 7 --post --json
if [ "$EVAL_STATUS" -eq 2 ] && grep -q "unknown flag '--post'" "$EVAL_ERR" && [ ! -s "$EVAL_STUBLOG" ]; then
  good "R4 --post is an unknown flag and touches no host before refusal"
else
  bad "R4 --post: exit $EVAL_STATUS $(head -c 300 "$EVAL_ERR") log: $(cat "$EVAL_STUBLOG")"
fi

eval_end
