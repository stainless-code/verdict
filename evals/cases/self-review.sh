#!/usr/bin/env bash
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
mkdir -p "$EVAL_SANDBOX/agentbin"
for agent in claude codex opencode; do
  cp "$EVAL_ROOT/evals/fixtures/self-review/agent.sh" "$EVAL_SANDBOX/agentbin/$agent"
  chmod +x "$EVAL_SANDBOX/agentbin/$agent"
done
PATH="$EVAL_SANDBOX/agentbin:$(dirname "$EVAL_CLI"):$PATH"
export PATH
for agent in claude codex opencode; do
  for mode in fix never empty stale; do
    repo="$EVAL_SANDBOX/repo-$agent-$mode"
    eval_fixture_repo "$repo"
    export LOOP_MODE="$mode" LOOP_LOG="$EVAL_SANDBOX/$agent-$mode.log"
    output="$EVAL_SANDBOX/$agent-$mode"
    set +e
    (cd "$repo" && python3 "$EVAL_ROOT/docs/recipes/self-review.py" acme/app \
      --agent "$agent" --max-rounds 2 --budget 60 --output "$output") >"$EVAL_OUT" 2>"$EVAL_ERR"
    # shellcheck disable=SC2034 # assert_exit reads this status
    EVAL_STATUS=$?
    set -e
    if [ "$mode" = stale ]; then
      assert_exit "$agent edit after fetch refuses" 1
      assert_json_field "stale review cannot pass" false '.passed' "$output/summary.json"
      assert_json_field "edit after fetch is typed" coverage-stale '.stop' "$output/summary.json"
      continue
    fi
    if [ "$mode" = empty ]; then
      assert_exit "$agent empty review refuses" 1
      assert_json_field "empty review is not a pass" false '.passed' "$output/summary.json"
      assert_json_field "required record coverage refuses" coverage-incomplete '.stop' "$output/summary.json"
      assert_json_field "empty review never reaches gate" null '.rounds[0].gate' "$output/summary.json"
      assert_eq "empty review stops after one spawn" 1 "$(wc -l <"$LOOP_LOG" | tr -d ' ')"
      continue
    fi
    if [ "$mode" = fix ]; then
      assert_exit "$agent fixes and passes round 2" 0
      assert_json_field "pass on last gate" true '.rounds[-1].gate' "$output/summary.json"
    else
      assert_exit "$agent exhausts without a false pass" 1
      assert_json_field "max rounds" max-rounds '.stop' "$output/summary.json"
      assert_json_field "failed last gate" false '.rounds[-1].gate' "$output/summary.json"
    fi
    assert_json_field "two receipts" 2 '.rounds | length' "$output/summary.json"
    assert_eq "one spawn per round" 2 "$(wc -l <"$LOOP_LOG" | tr -d ' ')"
  done
done
assert_stream_nogrep "no host calls" "$EVAL_STUBLOG" 'stub-gh|stub-glab|stub-curl'
python3 -B -m unittest discover -s "$EVAL_ROOT/docs/recipes" || bad "recipe unit tests"
eval_end
