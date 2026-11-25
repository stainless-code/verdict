#!/usr/bin/env bash
#
# STA-344: `verdict loop` is the built-in agent self-review loop. Every leg is
# offline: the agent is evals/fixtures/self-review/agent.sh copied onto PATH
# under each shipped name it grades, plus one operator-defined settings.json
# agent, so the loop resolves real executables through the same table `run`
# uses. The fixture repository's notes.txt carries the one changed line the
# stub reviews; the fix rewrites it.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
mkdir -p "$EVAL_SANDBOX/agentbin" "$EVAL_SANDBOX/home/.config/verdict"
for agent in claude codex opencode my-agent; do
  cp "$EVAL_ROOT/evals/fixtures/self-review/agent.sh" "$EVAL_SANDBOX/agentbin/$agent"
  chmod +x "$EVAL_SANDBOX/agentbin/$agent"
done
# An operator-defined agent: the loop takes any settings.json row, not a
# hardcoded list.
printf '{"agents":{"mine":{"binary":"my-agent","args":["exec"]}}}\n' \
  >"$EVAL_SANDBOX/home/.config/verdict/settings.json"
PATH="$EVAL_SANDBOX/agentbin:$(dirname "$EVAL_CLI"):$PATH"
HOME="$EVAL_SANDBOX/home"
XDG_CONFIG_HOME="$EVAL_SANDBOX/home/.config"
export PATH HOME XDG_CONFIG_HOME

# loop_leg <leg> <agent> <mode> [loop flags...] runs one loop from a fresh
# fixture repository with receipts under $EVAL_SANDBOX/<leg>.
loop_leg() {
  local leg="$1" agent="$2" mode="$3"
  shift 3
  repo="$EVAL_SANDBOX/repo-$leg"
  output="$EVAL_SANDBOX/$leg"
  eval_fixture_repo "$repo"
  export LOOP_MODE="$mode" LOOP_LOG="$EVAL_SANDBOX/$leg.log"
  : >"$LOOP_LOG"
  set +e
  (cd "$repo" && "$EVAL_CLI" loop acme/app --agent "$agent" --output "$output" --json "$@") \
    >"$EVAL_OUT" 2>"$EVAL_ERR"
  # shellcheck disable=SC2034 # assert_exit reads this status
  EVAL_STATUS=$?
  set -e
}

for agent in claude codex opencode mine; do
  loop_leg "$agent-fix" "$agent" fix --max-rounds 3
  assert_exit "$agent fixes and passes round 2" 0
  assert_json_field "$agent pass" pass '.stop' "$output/summary.json"
  assert_json_field "$agent two receipts" 2 '.rounds | length' "$output/summary.json"
  assert_json_field "$agent round 1 gate failed" false '.gate' "$output/round-1.json"
  assert_json_field "$agent round 1 open finding" 1 '.open' "$output/round-1.json"
  assert_json_field "$agent fix carries the verify receipt" 0 '.fix.verify.exit' "$output/round-1.json"
  assert_json_field "$agent round 2 gate passed" true '.gate' "$output/round-2.json"
  assert_json_field "$agent envelope is the summary" pass '.data.stop' "$EVAL_OUT"
  bin="$agent"
  [ "$agent" = mine ] && bin=my-agent
  assert_eq "$agent review, fix, review" "$bin review,$bin fix,$bin review" "$(paste -sd, "$LOOP_LOG")"
  assert_eq "$agent fix landed in the working tree only" "fixed line" "$(sed -n 2p "$repo/notes.txt")"
  assert_eq "$agent never commits" "eval-base" "$(git -C "$repo" log -1 --format=%s)"
done

loop_leg plateau claude never --max-rounds 3
assert_exit "a fix that changes nothing stops" 1
assert_json_field "plateau stop" no-progress '.stop' "$output/summary.json"
assert_json_field "plateau after two rounds" 2 '.rounds | length' "$output/summary.json"
assert_json_field "no fix after the plateau" null '.rounds[1].fix' "$output/summary.json"

loop_leg one-round claude never --max-rounds 1
assert_exit "one round exhausts" 1
assert_json_field "max rounds" max-rounds '.stop' "$output/summary.json"
assert_eq "no fix on the last round" "claude review" "$(paste -sd, "$LOOP_LOG")"

loop_leg empty codex empty
assert_exit "empty review refuses" 1
# run grades the artifact with record's own checker, so an empty review of a
# measurable change refuses at the review phase, before record is reached.
assert_json_field "an unattested review refuses" review-insufficient '.refusal.code' "$output/summary.json"
assert_json_field "a refusal is the refused stop" refused '.stop' "$output/summary.json"
assert_json_field "empty review never reaches gate" null '.rounds[0].gate' "$output/summary.json"

loop_leg stale opencode stale
assert_exit "edit during review refuses" 1
assert_json_field "edit after fetch is typed" coverage-stale '.refusal.code' "$output/summary.json"

loop_leg fail claude fail
assert_exit "agent failure stops" 1
assert_json_field "agent-failed stop" agent-failed '.stop' "$output/summary.json"
assert_json_field "run's refusal surfaces as-is" agent-failed '.refusal.code' "$output/summary.json"

loop_leg slow claude slow --budget 2
assert_exit "wall budget stops" 1
assert_json_field "wall-budget stop" wall-budget '.stop' "$output/summary.json"

loop_leg cost claude cost --max-cost-usd 0.5
assert_exit "cost budget stops" 1
assert_json_field "cost-budget stop" cost-budget '.stop' "$output/summary.json"
assert_json_field "round cost from the receipt" 0.6 '.rounds[0].cost_usd' "$output/summary.json"
assert_eq "no fix past the cost budget" "claude review" "$(paste -sd, "$LOOP_LOG")"

loop_leg print claude fix --print
assert_exit "print plans" 0
assert_json_field "print stop" planned '.data.stop' "$EVAL_OUT"
assert_eq "print spawns nothing" "" "$(cat "$LOOP_LOG")"
if [ -e "$output" ] || [ -e "$repo/.verdict" ]; then
  bad "print wrote receipts or bundles"
fi

assert_stream_nogrep "no host calls" "$EVAL_STUBLOG" 'stub-gh|stub-glab|stub-curl'
eval_end
