#!/usr/bin/env bash
# drain --post answers two threads, resolves a withdraw, and is idempotent.
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
HUMAN2="$(comment 202 bob User 2026-11-02T00:00:00Z 'is this wrong?')"
jq -n --argjson a "$(node PRRT_1 a.go "[$ROOT1,$HUMAN1]")" \
  --argjson b "$(node PRRT_2 b.go "[$ROOT2,$HUMAN2]")" \
  '{data:{repository:{pullRequest:{reviewThreads:{pageInfo:{hasNextPage:false,endCursor:null},nodes:[$a,$b]}}}}}' >"$EVAL_SANDBOX/page.json"
cp "$EVAL_SANDBOX/page.json" "$EVAL_SANDBOX/page.initial.json"

MARK1="<!-- verdict-reply thread=PRRT_1 to=102 -->"
MARK2="<!-- verdict-reply thread=PRRT_2 to=202 -->"
TWO_NL=$'\n\n'
EVAL_BODYLOG="$EVAL_SANDBOX/reply-bodies.jsonl"
export DRAIN_PAGE="$EVAL_SANDBOX/page.json" DRAIN_QUERY MARK1 MARK2 TWO_NL EVAL_BODYLOG
: >"$EVAL_STUBLOG"
: >"$EVAL_BODYLOG"
store_digests() {
  local f
  for f in replies.json drain.json threads.json drain.dryrun.json; do
    if [ -f "$DIR/$f" ]; then cksum <"$DIR/$f"; else printf 'absent\n'; fi
  done
}
write_count() { grep -Ec -- '--method POST|threadId=' "$EVAL_STUBLOG" || true; }

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
if [ "$1" = api ] && [ "$2" = graphql ] && [[ " $* " == *" threadId=PRRT_2"* ]]; then
  printf '{"data":{"resolveReviewThread":{"thread":{"id":"PRRT_2","isResolved":true}}}}\n'
  exit 0
fi
reply() {
  local endpoint="$1" marker="$2" id="$3" body="$4"
  shift 4
  expected=(api --method POST "repos/acme/app/pulls/7/comments/$endpoint/replies" -f "body=$body")
  [ "$#" -eq "${#expected[@]}" ] || return 1
  for i in "${!expected[@]}"; do
    idx=$((i + 1))
    [ "${!idx}" = "${expected[i]}" ] || return 1
  done
  for arg in "$@"; do
    case "$arg" in
      body=*) printf '%s' "${arg#body=}" | jq -Rs . >>"$EVAL_BODYLOG" ;;
    esac
  done
  jq --arg marker "$marker" \
    --arg prefix "$endpoint" \
    '.data.repository.pullRequest.reviewThreads.nodes |= map(if (.comments.nodes | any(.id | startswith($prefix))) then .comments.nodes[-1].body += "\n\n" + $marker else . end)' \
    "$DRAIN_PAGE" >"$DRAIN_PAGE.next"
  mv "$DRAIN_PAGE.next" "$DRAIN_PAGE"
  printf '{"id":%s}\n' "$id"
  exit 0
}
if [ "$1" = api ] && [ "$2" = --method ] && [ "$3" = POST ]; then
  reply 101 "$MARK1" 501 "Answer one.${TWO_NL}${MARK1}" "$@" && exit 0
  reply 201 "$MARK2" 502 "Answer two.${TWO_NL}${MARK2}" "$@" && exit 0
fi
unhandled "$@"
GH
chmod +x "$EVAL_STUBBIN/gh"

cat >"$EVAL_STUBBIN/claude" <<'AGENT'
#!/usr/bin/env bash
set -euo pipefail
echo "stub-agent $*" >>"$EVAL_STUBLOG"
prompt="${@: -1}"
target="${prompt#*Write exactly one file, }"
target="${target%%,*}"
[ -n "$target" ] && [ "$target" != "$prompt" ]
plan="$(dirname "$target")/drain.json"
digest="$(jq -r .plan_digest "$plan")"
jq -n --arg digest "$digest" '{schema_version:"1",plan_digest:$digest,replies:[
  {thread_id:"PRRT_1",waiting_comment_id:"102",disposition:"answer",body:"Answer one."},
  {thread_id:"PRRT_2",waiting_comment_id:"202",disposition:"withdraw",body:"Answer two."}]}' \
  >"$target"
AGENT
chmod +x "$EVAL_STUBBIN/claude"

run_review drain acme/app 7 --agent claude --json
if [ "$EVAL_STATUS" -ne 0 ] || ! jq -e '.data.drafts.count == 2' <"$EVAL_OUT" >/dev/null; then
  bad "draft: exit $EVAL_STATUS $(head -c 400 "$EVAL_ERR") $(head -c 400 "$EVAL_OUT")"
  eval_end
fi
good "two drafts were validated before the post run"

DRY_WRITES_BEFORE="$(write_count)"
DRY_BODIES_BEFORE="$(wc -c <"$EVAL_BODYLOG" | tr -d ' ')"
DRY_FILES_BEFORE="$(store_digests)"
run_review drain acme/app 7 --post --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] && jq -e --arg mark1 "$MARK1" --arg mark2 "$MARK2" '
  .next == "verdict drain acme/app 7 --post" and
  (.data.planned | map({thread_id, root_comment_id, disposition}) == [
    {thread_id:"PRRT_1", root_comment_id:"101", disposition:"answer"},
    {thread_id:"PRRT_2", root_comment_id:"201", disposition:"withdraw"}]) and
  (.data.planned[0].body == ("Answer one." + "\n\n" + $mark1)) and
  (.data.planned[1].body == ("Answer two." + "\n\n" + $mark2)) and
  (.data.skipped | length == 0)' <"$EVAL_OUT" >/dev/null; then
  good "D1 dry run reports both final marked bodies and the post-only next"
else
  bad "D1 dry run data: exit $EVAL_STATUS $(head -c 500 "$EVAL_OUT") $(head -c 300 "$EVAL_ERR")"
fi
if [ "$(write_count)" = "$DRY_WRITES_BEFORE" ] && \
   [ "$(wc -c <"$EVAL_BODYLOG" | tr -d ' ')" = "$DRY_BODIES_BEFORE" ] && \
   [ "$(store_digests)" != "$DRY_FILES_BEFORE" ] && \
   [ -f "$DIR/drain.dryrun.json" ]; then
  good "D2 dry run made zero host writes and left the store byte-identical"
else
  bad "D2 dry run writes: $(write_count) vs $DRY_WRITES_BEFORE; files $(store_digests)"
fi
if [ "$(for f in replies.json drain.json threads.json; do
  if [ -f "$DIR/$f" ]; then cksum <"$DIR/$f"; else printf 'absent\n'; fi
done)" = "$(printf '%s\n' "$DRY_FILES_BEFORE" | head -3)" ]; then
  good "D3 the three core bundle files kept their exact bytes"
else
  bad "D3 core files changed across the dry run"
fi
jq '.data' <"$EVAL_OUT" >"$EVAL_SANDBOX/dry-data.json"
if jq -e --slurpfile want "$EVAL_SANDBOX/dry-data.json" '. == $want[0]' "$DIR/drain.dryrun.json" >/dev/null && \
   matches -F '» 2 planned, 0 skipped (dry run; nothing posted) ->' "$EVAL_ERR"; then
  good "D4 the preview file equals envelope data and names the human summary"
else
  bad "D4 preview: $(head -c 400 "$DIR/drain.dryrun.json" 2>/dev/null) $(head -c 200 "$EVAL_ERR")"
fi
jq -c '[.data.planned[].body]' <"$EVAL_OUT" >"$EVAL_SANDBOX/dry-bodies.json"

run_review drain acme/app 7 --post --json
if [ "$EVAL_STATUS" -eq 0 ] && jq -e '(.data.posted | length == 2)
  and (.data.failed | length == 0) and (.next | contains("threads"))' <"$EVAL_OUT" >/dev/null; then
  good "P1 post writes both marked replies"
else
  bad "P1 post: exit $EVAL_STATUS $(head -c 500 "$EVAL_OUT") $(head -c 300 "$EVAL_ERR")"
fi
if jq -e 'length == 2 and (map(.by) | all(. == "drain"))
  and (map(.in_reply_to) | sort) == ["102","202"]
  and (map(.reply_id) | sort) == ["501","502"]' "$DIR/replies.json" >/dev/null; then
  good "P2 two drain ledger entries carry the waiting comment ids"
else
  bad "P2 ledger: $(jq -c . "$DIR/replies.json" 2>/dev/null)"
fi
if jq -e '(.posted | length == 2) and ((.posted | map(select(.disposition == "withdraw" and .resolved)) | length) == 1)
  and .failed == [] and .posted_at != null' "$DIR/drain.json" >/dev/null; then
  good "P3 the final receipt records two posts and one resolve"
else
  bad "P3 receipt: $(jq -c '{posted,failed,posted_at}' "$DIR/drain.json")"
fi
run_review threads acme/app 7 --json
if jq -e --arg marker "$MARK1" '.threads[] | select(.id == "PRRT_1") | (.comments[-1].author_kind == "verdict"
  and (.comments[-1].body | contains($marker)))' "$DIR/threads.json" >/dev/null; then
  good "P4 the posted marker round-trips as author_kind verdict"
else
  bad "P4 marker round-trip: $(jq -c '.threads[] | select(.id == "PRRT_1") | .comments[-1]' "$DIR/threads.json")"
fi
if [ "$(grep -c 'body=Answer' "$EVAL_STUBLOG")" = "2" ] && \
   [ "$(grep -c 'threadId=PRRT_2' "$EVAL_STUBLOG")" = "1" ]; then
  good "P5 two replies and exactly one resolve reached the host"
else
  bad "P5 writes: $(grep -E 'body=|threadId=' "$EVAL_STUBLOG")"
fi
if jq -e -s --slurpfile planned "$EVAL_SANDBOX/dry-bodies.json" \
   '. == $planned[0]' "$EVAL_BODYLOG" >/dev/null; then
  good "D5 the real post sent exactly the bodies the dry run planned"
else
  bad "D5 dry-run bodies: $(cat "$EVAL_SANDBOX/dry-bodies.json"); posted: $(jq -s -c . "$EVAL_BODYLOG")"
fi

WRITES_BEFORE="$(grep -Ec 'POST|threadId=' "$EVAL_STUBLOG" || true)"
LEDGER_BEFORE="$(wc -c <"$DIR/replies.json")"
run_review drain acme/app 7 --post --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(grep -Ec 'POST|threadId=' "$EVAL_STUBLOG" || true)" = "$WRITES_BEFORE" ] \
  && [ "$(wc -c <"$DIR/replies.json")" = "$LEDGER_BEFORE" ] \
  && jq -e '.data | (.posted | length == 2)' <"$EVAL_OUT" >/dev/null \
  && jq -e '.posted | length == 2' "$DIR/drain.json" >/dev/null; then
  good "P6 a rerun makes zero writes and adds no ledger entry"
else
  bad "P6 rerun: exit $EVAL_STATUS $(head -c 400 "$EVAL_OUT")"
fi

cp "$EVAL_SANDBOX/page.initial.json" "$EVAL_SANDBOX/page.json"
rm -f "$DIR/replies.json" "$DIR/drain.json" "$DIR/drain.replies.json"
AGENT_DRY_WRITES="$(write_count)"
AGENT_DRY_BODIES="$(wc -c <"$EVAL_BODYLOG" | tr -d ' ')"
run_review drain acme/app 7 --agent claude --post --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] && jq -e '(.data.planned | length == 2) and (.data.skipped | length == 0)' <"$EVAL_OUT" >/dev/null && \
   [ "$(write_count)" = "$AGENT_DRY_WRITES" ] && \
   [ "$(wc -c <"$EVAL_BODYLOG" | tr -d ' ')" = "$AGENT_DRY_BODIES" ] && \
   [ ! -e "$DIR/replies.json" ] && jq -e '(.posted | length == 0)' "$DIR/drain.json" >/dev/null; then
  good "D6 --agent --post --dry-run plans, drafts, and previews without posting"
else
  bad "D6 agent dry run: exit $EVAL_STATUS $(head -c 500 "$EVAL_OUT") $(head -c 300 "$EVAL_ERR")"
fi
run_review drain acme/app 7 --agent claude --post --json
if [ "$EVAL_STATUS" -eq 0 ] && jq -e '.data.drafts.count == 2 and (.data.posted | length == 2)
  and (.data.failed | length == 0) and .data.posted_at != null
  and ((.data.posted | map(select(.disposition == "withdraw" and .resolved)) | length) == 1)' <"$EVAL_OUT" >/dev/null \
  && jq -e 'length == 2 and (map(.by) | all(. == "drain"))' "$DIR/replies.json" >/dev/null; then
  good "P7 --agent --post plans, drafts, validates, and posts in one run"
else
  bad "P7 one-run: exit $EVAL_STATUS $(head -c 500 "$EVAL_OUT") $(head -c 300 "$EVAL_ERR")"
fi

if matches 'unhandled gh call' "$EVAL_STUBLOG"; then
  bad "a later run made an unregistered gh call: $(cat "$EVAL_STUBLOG")"
else
  good "every gh call was registered"
fi
eval_end
