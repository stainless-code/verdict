#!/usr/bin/env bash
#
# STA-332: `verdict run --agent --rounds N` re-prompts an invalid review with
# the shape checker's fix list, up to N launch-to-check rounds, and refuses
# repair-exhausted when no round is valid. The "agent" is
# evals/fixtures/run-agent/repair-stub.sh on PATH as `claude`: a cooperating
# script with no model, no credential, and no host. It reads the review path
# out of the launcher's own directive and changes its answer per invocation,
# counted in a file, so each leg grades the loop rather than one shot.
#
# (a) round 1 invalid, round 2 valid: exit 0, exactly 2 spawns, review.json
#     is round 2's, rounds_to_valid 2. (b) always invalid with --rounds 3:
#     exit 1 repair-exhausted, exactly 3 spawns, error.problems non-empty. (c)
#     default (no flag): exactly 1 spawn, no data.repair, no launcher repair
#     keys. (d) --print --rounds 2 and --rounds 0/6/x refuse bad-flags.
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
export STUB_AGENT_LOG="$SANDBOX/agent-argv.log"
export STUB_REPAIR_COUNT="$SANDBOX/repair-count"

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
run_review run acme/app "$LOCAL" --agent claude --rounds 3 --json
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
run_review run acme/app "$LOCAL" --agent claude --rounds 3 --json
assert_exit "an always-invalid agent refuses" 1
assert_refusal "repair exhausted" "repair-exhausted"
assert_eq "exactly 3 agent processes ran" "3" "$(( $(spawns) - before_spawns ))"
if jq -e '.error.problems | length > 0' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "error.problems carries the final list"
else
  bad "error.problems is empty"
fi

# --- (c) default: one shot, today's shape ------------------------------------
rm -f "$STUB_REPAIR_COUNT"
before_spawns="$(spawns)"
export STUB_REPAIR_MODE=valid
run_review run acme/app "$LOCAL" --agent claude --json
assert_exit "the default still exits 0" 0
assert_eq "the default spawns exactly 1 agent process" "1" "$(( $(spawns) - before_spawns ))"
if jq -e '.data | has("repair") | not' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "the default envelope carries no data.repair"
else
  bad "the default envelope gained data.repair"
fi
if jq -e 'has("repair") or has("attempts") | not' <"$LAUNCHER" >/dev/null 2>&1; then
  good "the default launcher.json keeps its old shape"
else
  bad "the default launcher.json gained repair keys"
fi

# --- (d) round-count refusals -------------------------------------------------
before_spawns="$(spawns)"
run_review run acme/app "$LOCAL" --agent claude --print --rounds 2 --json
assert_exit "--print with --rounds refuses" 1
assert_refusal "--print with --rounds" "bad-flags"
for bad in 0 6 x; do
  run_review run acme/app "$LOCAL" --agent claude --rounds "$bad" --json
  assert_exit "--rounds $bad refuses" 1
  assert_refusal "--rounds $bad" "bad-flags"
done
assert_eq "no refusal launched an agent process" "$before_spawns" "$(spawns)"

eval_end
