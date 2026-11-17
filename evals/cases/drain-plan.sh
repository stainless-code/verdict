#!/usr/bin/env bash
# drain plans the threads waiting on verdict, with no host writes.
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
jq -n '[{"thread":"PRRT_legacy","reply_id":"900","at":"2026-11-04T00:00:00Z"}]' >"$DIR/replies.json"

RB="finding"$'\n\n'"<!-- verdict-finding sig=$SIG -->"
node() {
  jq -n --arg id "$1" --argjson resolved "$2" --arg path "$3" --argjson line "$4" --argjson comments "$5" \
    '{id:$id,isResolved:$resolved,isOutdated:false,path:$path,line:$line,startLine:null,diffSide:"RIGHT",comments:{totalCount:($comments|length),nodes:$comments}}'
}
comment() {
  jq -n --arg id "$1" --arg who "$2" --arg kind "$3" --arg at "$4" --arg body "$5" \
    '{id:$id,databaseId:($id|tonumber),author:{__typename:$kind,login:$who},body:$body,createdAt:$at,url:"https://example.invalid/c"}'
}
HUMAN="$(comment 102 alice User 2026-11-02T00:00:00Z 'a human question')"
BOT="$(comment 202 'dependabot[bot]' Bot 2026-11-02T00:00:00Z 'bump')"
V302="$(comment 302 alice User 2026-11-02T00:00:00Z 'question')"
V402="$(comment 402 alice User 2026-11-02T00:00:00Z 'question')"
VERDICT_REPLY="$(comment 403 verdict-bot Bot 2026-11-03T00:00:00Z "$RB")"
L502="$(comment 502 alice User 2026-11-02T00:00:00Z 'question')"
O601="$(comment 601 alice User 2026-11-01T00:00:00Z 'my own finding')"
O602="$(comment 602 bob User 2026-11-02T00:00:00Z 'question')"
SOLO="$(comment 702 verdict-bot User 2026-11-02T00:00:00Z 'a question from my own login')"
ROOT101="$(comment 101 verdict-bot Bot 2026-11-01T00:00:00Z "$RB")"
ROOT201="$(comment 201 verdict-bot Bot 2026-11-01T00:00:00Z "$RB")"
ROOT301="$(comment 301 verdict-bot Bot 2026-11-01T00:00:00Z "$RB")"
ROOT401="$(comment 401 verdict-bot Bot 2026-11-01T00:00:00Z "$RB")"
ROOT501="$(comment 501 verdict-bot Bot 2026-11-01T00:00:00Z "$RB")"
ROOT701="$(comment 701 verdict-bot Bot 2026-11-01T00:00:00Z "$RB")"

jq -n \
  --argjson human "$(node PRRT_human false a.go 10 "[$ROOT101,$HUMAN]")" \
  --argjson bot "$(node PRRT_bot false a.go 11 "[$ROOT201,$BOT]")" \
  --argjson resolved "$(node PRRT_resolved true a.go 12 "[$ROOT301,$V302]")" \
  --argjson answered "$(node PRRT_answered false a.go 13 "[$ROOT401,$V402,$VERDICT_REPLY]")" \
  --argjson legacy "$(node PRRT_legacy false a.go 14 "[$ROOT501,$L502]")" \
  --argjson other "$(node PRRT_other false a.go 15 "[$O601,$O602]")" \
  --argjson solo "$(node PRRT_solo false a.go 16 "[$ROOT701,$SOLO]")" \
  '{data:{repository:{pullRequest:{reviewThreads:{pageInfo:{hasNextPage:false,endCursor:null},
    nodes:[$human,$bot,$resolved,$answered,$legacy,$other,$solo]}}}}}' >"$EVAL_SANDBOX/page.json"

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
: >"$EVAL_STUBLOG"

run_review drain acme/app 7 --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(stdout_docs)" = "1" ] \
  && jq -e '.verb == "drain" and .repo == "acme/app" and .pr == 7 and .error == null
    and (.data | .pending | length == 2)
    and (.data | .other_threads == 1)
    and (.data | .plan_digest | test("^[0-9a-f]{64}$"))
    and (.data | .drafts == null and .posted == [] and .failed == [] and .posted_at == null)
    and (.next | contains("--agent"))' <"$EVAL_OUT" >/dev/null; then
  good "P1 drain plans two waiting threads with a pinned digest"
else
  bad "P1 drain envelope: exit $EVAL_STATUS $(head -c 500 "$EVAL_ERR") $(head -c 500 "$EVAL_OUT")"
fi
PLAN="$DIR/drain.json"
if jq -e --arg sig "$SIG" '
  .schema_version == "1" and .provider == "github" and .pr == 7 and .head_sha == "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  and .self == "verdict-bot" and (.pending | map(.thread_id) | sort) == ["PRRT_human","PRRT_solo"]
  and (.pending[0].finding_sig == $sig and .pending[0].path == "a.go")
  and (.skipped | map([.thread_id, .reason]) | sort) == [
    ["PRRT_answered","already-replied"],["PRRT_bot","bot-reply"],["PRRT_legacy","answered"],["PRRT_resolved","resolved"]]
  and .other_threads == 1 and .drafts == null and .posted == [] and .failed == [] and .posted_at == null' "$PLAN" >/dev/null; then
  good "P2 pending, every skip reason, and the joined finding land in drain.json"
else
  bad "P2 plan: $(jq -c '{pending,skipped,other_threads}' "$PLAN" 2>/dev/null | head -c 600)"
fi
if "$EVAL_SKILL/schemas/validate.sh" "$EVAL_SKILL/schemas/drain.schema.json" "$PLAN" >/dev/null 2>&1; then
  good "P3 the plan validates against schemas/drain.schema.json"
else
  bad "P3 schema: $("$EVAL_SKILL/schemas/validate.sh" "$EVAL_SKILL/schemas/drain.schema.json" "$PLAN" 2>&1 | head -3)"
fi
assert_stream_nogrep "P4 zero host writes on the stub log" "$EVAL_STUBLOG" \
  'POST|PATCH|mutation|method[[:space:]]+:([[:space:]]*)?"(POST|PATCH|DELETE)' -Eqi
if [ "$(grep -c 'api graphql' "$EVAL_STUBLOG")" = "1" ] && [ "$(grep -c 'api user --jq .login' "$EVAL_STUBLOG")" = "1" ]; then
  good "P5 one thread refresh and one login read"
else
  bad "P5 host reads: $(cat "$EVAL_STUBLOG")"
fi
run_review drain acme/app 7 --max-threads 1 --json
if [ "$EVAL_STATUS" -eq 0 ] && jq -e '.data.pending | length == 1' <"$EVAL_OUT" >/dev/null \
  && jq -e '.skipped | map(select(.reason == "deferred")) | length == 1' "$PLAN" >/dev/null; then
  good "P6 --max-threads defers the oldest-excess thread"
else
  bad "P6 cap: exit $EVAL_STATUS $(head -c 300 "$EVAL_ERR")"
fi

cat >"$EVAL_STUBBIN/claude" <<'AGENT'
#!/usr/bin/env bash
echo "stub-agent $*" >>"$EVAL_STUBLOG"
if [ "${DRAIN_AGENT_MODE:-good}" = good ]; then
  digest="$(jq -r .plan_digest .verdict/state/7/drain.json)"
  jq -n --arg digest "$digest" '{schema_version:"1",plan_digest:$digest,replies:[
    {thread_id:"PRRT_human",waiting_comment_id:"102",disposition:"answer",body:"Here is the answer."}]}' \
    >.verdict/state/7/drain.replies.json
fi
exit 0
AGENT
chmod +x "$EVAL_STUBBIN/claude"
export DRAIN_AGENT_MODE=good
run_review drain acme/app 7 --agent claude --json
if [ "$EVAL_STATUS" -eq 0 ] && jq -e '.data.drafts.count == 1 and .data.drafts.dispositions.answer == 1
  and (.next | contains("--post"))' <"$EVAL_OUT" >/dev/null; then
  good "P7 a good stub draft validates and lands in the plan"
else
  bad "P7 draft: exit $EVAL_STATUS $(head -c 500 "$EVAL_ERR") $(head -c 500 "$EVAL_OUT")"
fi
rm -f "$DIR/drain.replies.json"
DRAIN_AGENT_MODE=missing run_review drain acme/app 7 --agent claude --json
if [ "$EVAL_STATUS" -eq 1 ] && jq -e '.error.code == "no-review-artifact"' <"$EVAL_OUT" >/dev/null; then
  good "P8 a launch with no draft refuses no-review-artifact"
else
  bad "P8 missing artifact: exit $EVAL_STATUS $(head -c 300 "$EVAL_ERR")"
fi
printf '{"schema_version":"1"}' >"$DIR/drain.replies.json"
touch -t 200001010000 "$DIR/drain.replies.json"
DRAIN_AGENT_MODE=missing run_review drain acme/app 7 --agent claude --json
if [ "$EVAL_STATUS" -eq 1 ] && jq -e '.error.code == "stale-review-artifact"' <"$EVAL_OUT" >/dev/null; then
  good "P9 a draft older than the launch refuses stale-review-artifact"
else
  bad "P9 stale artifact: exit $EVAL_STATUS $(head -c 300 "$EVAL_ERR")"
fi
rm -f "$DIR/drain.replies.json"
jq -n '{data:{repository:{pullRequest:{reviewThreads:{pageInfo:{hasNextPage:false,endCursor:null},nodes:[]}}}}}' >"$EVAL_SANDBOX/empty.json"
export DRAIN_PAGE="$EVAL_SANDBOX/empty.json"
rm -f "$DIR/threads.json"
run_review drain acme/app 7 --agent claude --json
if [ "$EVAL_STATUS" -eq 0 ] && jq -e '.data.pending | length == 0' <"$EVAL_OUT" >/dev/null \
  && [ "$(grep -c 'stub-agent' "$EVAL_STUBLOG")" = "3" ]; then
  good "P10 zero pending launches no agent"
else
  bad "P10 zero pending: exit $EVAL_STATUS $(head -c 300 "$EVAL_ERR")"
fi
eval_end
