#!/usr/bin/env bash
#
# STA-332: `verdict run --agent --rounds N` re-prompts an invalid review with
# the shape checker's fix list, up to N launch-to-check rounds, and refuses
# bad-review-input when no round is valid. The "agent" is
# evals/fixtures/run-agent/repair-stub.sh on PATH as `claude`: a cooperating
# script with no model, no credential, and no host. It reads the review path
# out of the launcher's own directive and changes its answer per invocation,
# counted in a file, so each leg grades the loop rather than one shot.
#
# (a) round 1 invalid, round 2 valid: exit 0, exactly 2 spawns, review.json
#     is round 2's, rounds_to_valid 2. (b) always invalid with --rounds 3:
#     exit 1 bad-review-input, exactly 3 spawns, error.problems non-empty. (c)
#     default (no flag): two rounds, one spawn on a valid first round, and the
#     repair profile the loop always records. (d) --rounds 1: exactly 1 spawn,
#     no data.repair, no launcher repair keys. (e) --print --rounds 2 and
#     --rounds 0/6/x refuse bad-flags.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

STUB_SRC="$EVAL_ROOT/evals/fixtures/run-agent/repair-stub.sh"
# shellcheck disable=SC2034 # SANDBOX is read by the assertions below
SANDBOX="$(cd "$EVAL_SANDBOX" && pwd)"
LOCAL=local-repair
LOCAL_DIR="$SANDBOX/.verdict/state/$LOCAL"
LAUNCHER="$LOCAL_DIR/launcher.json"

if [ ! -f "$STUB_SRC" ]; then
  bail "the committed repair stub agent is missing: $STUB_SRC"
fi
AGENTBIN="$SANDBOX/agentbin"
mkdir -p "$AGENTBIN"
cp "$STUB_SRC" "$AGENTBIN/claude"
chmod +x "$AGENTBIN/claude"
export PATH="$AGENTBIN:$PATH"
# Every review launch runs in the OS review sandbox, which lets the agent
# write only its snapshot, a private TMPDIR, and its own state paths: the
# stub's argv log lives in claude's state directory, relocated into the
# sandbox by CLAUDE_CONFIG_DIR so the case never touches the real ~/.claude.
export CLAUDE_CONFIG_DIR="$SANDBOX/claude-state"
mkdir -p "$CLAUDE_CONFIG_DIR"
export STUB_AGENT_LOG="$CLAUDE_CONFIG_DIR/agent-argv.log"
export STUB_REPAIR_COUNT="$CLAUDE_CONFIG_DIR/repair-count"

# spawns counts the agent processes the stub itself recorded. A leg that should
# not launch anything leaves the count where it was.
spawns() {
  if [ -f "$STUB_AGENT_LOG" ]; then
    grep -c '^cwd=' "$STUB_AGENT_LOG" 2>/dev/null || true
  else
    printf '0'
  fi
}

eval_mk_bundle "$LOCAL"

# --- (a) round 1 invalid, round 2 valid --------------------------------------
rm -f "$STUB_REPAIR_COUNT"
export STUB_REPAIR_MODE=fix-on-second
run_review run acme/app "$LOCAL" --finders 1 --agent claude --rounds 3 --json
assert_exit "invalid then valid exits 0" 0
assert_envelope "repair loop"
assert_json_field "rounds_to_valid is 2" "2" '.data.repair.rounds_to_valid'
assert_json_field "final_valid is true" "true" '.data.repair.final_valid'
assert_json_field "two attempts ran" "2" '.data.repair.attempts | length'
assert_json_field "round 1 failed its check" "false" '.data.repair.attempts[0].check_ok'
assert_json_field "round 2 passed its check" "true" '.data.repair.attempts[1].check_ok'
assert_json_field "round 1 carries problems" "true" '.data.repair.attempts[0].problems | length > 0'
assert_eq "exactly 2 agent processes ran" "2" "$(spawns)"
assert_file_grep "review.json is round 2's" "$LOCAL_DIR/review.json" \
  "Reviewed the change; one inline note" "-F"
assert_file_grep "human mode announces round 1" "$EVAL_ERR" "» round 1/3" "-F"
assert_file_grep "human mode announces round 2" "$EVAL_ERR" "» round 2/3" "-F"
for f in 01.review.json 01.check.json 02.review.json 02.check.json; do
  if [ -f "$LOCAL_DIR/.run.rounds/$f" ]; then
    good "per-round evidence kept: $f"
  else
    bad "per-round evidence missing: $f"
  fi
done
assert_file_grep "round 1 kept its invalid bytes" "$LOCAL_DIR/.run.rounds/01.review.json" \
  "not json first attempt" "-F"
assert_json_field "launcher.json records the loop" "2" '.repair.rounds_to_valid' "$LAUNCHER"
assert_json_field "launcher.json records both attempts" "2" '.attempts | length' "$LAUNCHER"

# --- (b) always invalid -------------------------------------------------------
rm -f "$STUB_REPAIR_COUNT"
before_spawns="$(spawns)"
export STUB_REPAIR_MODE=always-invalid
run_review run acme/app "$LOCAL" --finders 1 --agent claude --rounds 3 --json
assert_exit "an always-invalid agent refuses" 1
assert_refusal "repair exhausted" "bad-review-input"
assert_eq "exactly 3 agent processes ran" "3" "$(( $(spawns) - before_spawns ))"
if jq -e '.error.problems | length > 0' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "error.problems carries the final list"
else
  bad "error.problems is empty"
fi

# --- (c) the default: two rounds, one spawn when round 1 is already valid -----
rm -f "$STUB_REPAIR_COUNT"
before_spawns="$(spawns)"
export STUB_REPAIR_MODE=valid
run_review run acme/app "$LOCAL" --finders 1 --agent claude --json
assert_exit "the default exits 0 on a valid first round" 0
assert_eq "a valid first round spends one of the two default rounds" "1" "$(( $(spawns) - before_spawns ))"
assert_json_field "the default runs two rounds" "2" '.data.repair.rounds'
assert_json_field "a valid first round is the round it took" "1" '.data.repair.rounds_to_valid'
assert_json_field "the default launcher records the loop" "2" '.repair.rounds' "$LAUNCHER"

# --- (d) --rounds 1: one shot, no repair profile ------------------------------
rm -f "$STUB_REPAIR_COUNT"
before_spawns="$(spawns)"
run_review run acme/app "$LOCAL" --finders 1 --agent claude --rounds 1 --json
assert_exit "--rounds 1 exits 0" 0
assert_eq "--rounds 1 spawns exactly 1 agent process" "1" "$(( $(spawns) - before_spawns ))"
if jq -e '.data | has("repair") | not' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "--rounds 1 carries no data.repair"
else
  bad "--rounds 1 gained data.repair"
fi
if jq -e 'has("repair") or has("attempts") | not' <"$LAUNCHER" >/dev/null 2>&1; then
  good "--rounds 1 launcher.json keeps the one-shot shape"
else
  bad "--rounds 1 launcher.json gained repair keys"
fi

# --- (e) round-count refusals -------------------------------------------------
before_spawns="$(spawns)"
run_review run acme/app "$LOCAL" --finders 1 --agent claude --print --rounds 2 --json
assert_exit "--print with --rounds refuses" 1
assert_refusal "--print with --rounds" "bad-flags"
for bad in 0 6 x; do
  run_review run acme/app "$LOCAL" --finders 1 --agent claude --rounds "$bad" --json
  assert_exit "--rounds $bad refuses" 1
  assert_refusal "--rounds $bad" "bad-flags"
done
assert_eq "no refusal launched an agent process" "$before_spawns" "$(spawns)"

eval_end
