#!/usr/bin/env bash
# drain refuses bad drafts, truncated feeds, and the unshipped post flag.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

SIG="6c3ee749e3d1f97870c9e8b5c1171fae2cb9ff4e"
HEAD="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
DRAIN_QUERY="$(awk '/QueryThreadsFull = `/{flag=1; sub(/^[^`]*`/, ""); print; next} flag && /`/{sub(/`.*/, ""); print; exit} flag{print}' "$EVAL_ROOT/internal/readcommon/gh.go")"
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
set -uo pipefail
echo "stub-gh $*" >>"$EVAL_STUBLOG"
unhandled() {
  echo "unhandled gh call ($*)" >>"$EVAL_STUBLOG"
  echo "unregistered gh call: $*" >&2
  exit 1
}
if [ "$#" -eq 4 ] && [ "$1" = api ] && [ "$2" = user ] && [ "$3" = --jq ] && [ "$4" = .login ]; then
  printf 'verdict-bot\n'
  exit 0
fi
if [ "$1" = api ] && [ "$2" = graphql ]; then
  expected=(api graphql -F owner=acme -F name=app -F pr=7 -f "query=$DRAIN_QUERY")
  if [ "$#" -eq "${#expected[@]}" ]; then
    match=1
    for i in "${!expected[@]}"; do
      idx=$((i + 1))
      if [ "${!idx}" != "${expected[i]}" ]; then match=0; break; fi
    done
    if [ "$match" -eq 1 ]; then cat "$DRAIN_PAGE"; exit 0; fi
  fi
  if [ -n "${DRAIN_CURSOR:-}" ]; then
    expected=(api graphql -F owner=acme -F name=app -F pr=7 -F "endCursor=$DRAIN_CURSOR" -f "query=$DRAIN_QUERY")
    if [ "$#" -eq "${#expected[@]}" ]; then
      match=1
      for i in "${!expected[@]}"; do
        idx=$((i + 1))
        if [ "${!idx}" != "${expected[i]}" ]; then match=0; break; fi
      done
      if [ "$match" -eq 1 ]; then cat "$DRAIN_PAGE"; exit 0; fi
    fi
  fi
fi
unhandled "$@"
GH
chmod +x "$EVAL_STUBBIN/gh"
export DRAIN_PAGE="$EVAL_SANDBOX/page.json" DRAIN_QUERY
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

export DRAIN_PAGE="$EVAL_SANDBOX/truncated.json" DRAIN_CURSOR=cursor
cp "$DIR/drain.json" "$EVAL_SANDBOX/drain-before.json"
run_review drain acme/app 7 --json
if [ "$EVAL_STATUS" -eq 1 ] && jq -e '.error.code == "drain-failed"
  and (.error.message | contains("truncated"))' <"$EVAL_OUT" >/dev/null; then
  good "R3 a truncated feed refuses drain-failed without planning"
else
  bad "R3 truncated feed: exit $EVAL_STATUS $(head -c 500 "$EVAL_OUT")"
fi
if cmp -s "$EVAL_SANDBOX/drain-before.json" "$DIR/drain.json"; then
  good "R3 drain.json is byte-identical after the refusal"
else
  bad "R3 truncated feed rewrote drain.json"
fi

STUBLOG_BEFORE="$(wc -c <"$EVAL_STUBLOG")"
run_review drain acme/app 7 --post --json
if [ "$EVAL_STATUS" -eq 2 ] && grep -q "unknown flag '--post'" "$EVAL_ERR" \
  && [ "$(wc -c <"$EVAL_STUBLOG")" = "$STUBLOG_BEFORE" ]; then
  good "R4 --post is an unknown flag and touches no host before refusal"
else
  bad "R4 --post: exit $EVAL_STATUS $(head -c 300 "$EVAL_ERR") log: $(cat "$EVAL_STUBLOG")"
fi
awk '/^stub-gh /' "$EVAL_STUBLOG" >"$EVAL_SANDBOX/gh-log"
if matches 'unhandled gh call' "$EVAL_STUBLOG" || matches -Eqi 'mutation|POST|PATCH|DELETE' "$EVAL_SANDBOX/gh-log"; then
  bad "a later run made an unregistered or mutating gh call: $(cat "$EVAL_STUBLOG")"
else
  good "every run used only the registered reads and no mutation"
fi

eval_end
