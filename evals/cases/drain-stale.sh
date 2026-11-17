#!/usr/bin/env bash
# A comment landing between draft and post skips only that thread.
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
node() {
  jq -n --arg id "$1" --arg path "$2" --argjson comments "$3" \
    '{id:$id,isResolved:false,isOutdated:false,path:$path,line:10,startLine:null,diffSide:"RIGHT",comments:{totalCount:($comments|length),nodes:$comments}}'
}
comment() {
  jq -n --arg id "$1" --arg who "$2" --arg kind "$3" --arg at "$4" --arg body "$5" \
    '{id:$id,databaseId:($id|tonumber),author:{__typename:$kind,login:$who},body:$body,createdAt:$at,url:"https://example.invalid/c"}'
}
ROOT1="$(comment 101 verdict-bot Bot 2026-11-01T00:00:00Z "$RB")"
ROOT2="$(comment 201 verdict-bot Bot 2026-11-01T00:00:00Z "$RB")"
HUMAN1="$(comment 102 alice User 2026-11-02T00:00:00Z 'why?')"
HUMAN2="$(comment 202 bob User 2026-11-02T00:00:00Z 'also?')"
NEW="$(comment 999 alice User 2026-11-06T00:00:00Z 'a new question')"
jq -n --argjson a "$(node PRRT_1 a.go "[$ROOT1,$HUMAN1]")" \
  --argjson b "$(node PRRT_2 b.go "[$ROOT2,$HUMAN2]")" \
  '{data:{repository:{pullRequest:{reviewThreads:{pageInfo:{hasNextPage:false,endCursor:null},nodes:[$a,$b]}}}}}' >"$EVAL_SANDBOX/page.json"
jq --argjson new "$NEW" '.data.repository.pullRequest.reviewThreads.nodes |= map(if .id == "PRRT_1" then .comments.nodes += [$new] else . end)' \
  "$EVAL_SANDBOX/page.json" >"$EVAL_SANDBOX/stale.json"

MARK2="<!-- verdict-reply thread=PRRT_2 to=202 -->"
TWO_NL=$'\n\n'
export DRAIN_PAGE="$EVAL_SANDBOX/page.json" DRAIN_QUERY MARK2 TWO_NL
: >"$EVAL_STUBLOG"

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
expected=(api graphql -F owner=acme -F name=app -F pr=7 -f "query=$DRAIN_QUERY")
if [ "$1" = api ] && [ "$2" = graphql ] && [ "$#" -eq "${#expected[@]}" ]; then
  match=1
  for i in "${!expected[@]}"; do
    idx=$((i + 1))
    if [ "${!idx}" != "${expected[i]}" ]; then match=0; break; fi
  done
  if [ "$match" -eq 1 ]; then cat "$DRAIN_PAGE"; exit 0; fi
fi
if [ "$1" = api ] && [ "$2" = --method ] && [ "$3" = POST ]; then
  expected=(api --method POST repos/acme/app/pulls/7/comments/201/replies -f "body=Answer two.${TWO_NL}${MARK2}")
  if [ "$#" -eq "${#expected[@]}" ]; then
    match=1
    for i in "${!expected[@]}"; do
      idx=$((i + 1))
      if [ "${!idx}" != "${expected[i]}" ]; then match=0; break; fi
    done
    if [ "$match" -eq 1 ]; then printf '{"id":502}\n'; exit 0; fi
  fi
fi
unhandled "$@"
GH
chmod +x "$EVAL_STUBBIN/gh"

cat >"$EVAL_STUBBIN/claude" <<'AGENT'
#!/usr/bin/env bash
echo "stub-agent $*" >>"$EVAL_STUBLOG"
digest="$(jq -r .plan_digest .verdict/state/7/drain.json)"
jq -n --arg digest "$digest" '{schema_version:"1",plan_digest:$digest,replies:[
  {thread_id:"PRRT_1",waiting_comment_id:"102",disposition:"answer",body:"Answer one."},
  {thread_id:"PRRT_2",waiting_comment_id:"202",disposition:"answer",body:"Answer two."}]}' \
  >.verdict/state/7/drain.replies.json
AGENT
chmod +x "$EVAL_STUBBIN/claude"

run_review drain acme/app 7 --agent claude --json
if [ "$EVAL_STATUS" -ne 0 ]; then
  bad "draft: exit $EVAL_STATUS $(head -c 400 "$EVAL_ERR")"
  eval_end
fi
export DRAIN_PAGE="$EVAL_SANDBOX/stale.json"
run_review drain acme/app 7 --post --json
if [ "$EVAL_STATUS" -eq 0 ] && jq -e '(.data.posted | length == 1)
  and (.data.skipped | map(select(.reason == "stale-thread")) | map(.thread_id) == ["PRRT_1"])' \
  <"$EVAL_OUT" >/dev/null; then
  good "S1 the stale thread is skipped and the fresh thread posts"
else
  bad "S1 post: exit $EVAL_STATUS $(head -c 500 "$EVAL_OUT") $(head -c 300 "$EVAL_ERR")"
fi
if [ "$(grep -c 'body=Answer one' "$EVAL_STUBLOG")" = "0" ] && \
   [ "$(grep -c 'body=Answer two' "$EVAL_STUBLOG")" = "1" ]; then
  good "S2 only the fresh thread reached the host"
else
  bad "S2 writes: $(grep 'body=Answer' "$EVAL_STUBLOG" || true)"
fi
if jq -e 'length == 1 and .[0].reply_id == "502"' "$DIR/replies.json" >/dev/null; then
  good "S3 only one ledger entry was appended"
else
  bad "S3 ledger: $(jq -c . "$DIR/replies.json" 2>/dev/null)"
fi
if matches 'unhandled gh call' "$EVAL_STUBLOG"; then
  bad "a later run made an unregistered or mutating gh call: $(cat "$EVAL_STUBLOG")"
else
  good "every gh call was registered and only the fresh reply was a write"
fi
eval_end
