#!/usr/bin/env bash
#
# STA-300: `verdict run --agent` is a shipped recipe, so it is graded as one:
# one configured agent process, launched against a real (produced) bundle by
# the shipped agent table, writing exactly one fresh review.json, then handed
# to `record` (local bundle) and `post --dry-run` (PR bundle) unchanged.
#
# The "agent" is evals/fixtures/run-agent/agent-stub.sh copied onto PATH as
# `claude`: a cooperating script with no model, no credential, and no host. It
# reads the review path out of the launcher's own directive, so a directive
# that stopped naming its artifact fails here. Every leg stays offline; the
# only intercepted gh call in the whole case is the PR dry-run's head lookup.
#
# The recipe's honesty is graded too: docs/ci-and-tests.md must claim exactly
# "not older than the launch" at one-second granularity, never "written by this
# agent", and must say `run` never posts.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

RECIPE="$EVAL_ROOT/docs/ci-and-tests.md"
WORKFLOW="$EVAL_ROOT/.github/workflows/ci.yml"
STUB_SRC="$EVAL_ROOT/evals/fixtures/run-agent/agent-stub.sh"
# The harness hands the sandbox over as ${TMPDIR}/x, and macOS TMPDIR ends in
# a slash: canonicalize once so the strings this case compares (the resolved
# binary the launcher reports, the child's own $PWD) match what the CLI sees.
SANDBOX="$(cd "$EVAL_SANDBOX" && pwd)"
LOCAL=local-launch
PR=7301
LOCAL_DIR="$SANDBOX/.verdict/state/$LOCAL"
PR_DIR="$SANDBOX/.verdict/state/$PR"
LAUNCHER="$LOCAL_DIR/launcher.json"
SECRET="sk-ant-eval-redact-0001"

# The stub stands in for a shipped agent: the launcher resolves the name
# through its own table, so PATH carries `claude` and nothing in the launcher
# needs to know it is a script.
if [ ! -f "$STUB_SRC" ]; then
  bail "the committed stub agent is missing: $STUB_SRC"
fi
AGENTBIN="$SANDBOX/agentbin"
mkdir -p "$AGENTBIN"
cp "$STUB_SRC" "$AGENTBIN/claude"
chmod +x "$AGENTBIN/claude"
export PATH="$AGENTBIN:$PATH"
export STUB_AGENT_LOG="$SANDBOX/agent-argv.log"

# A PATH carrying only the tools the launcher itself needs, for the
# agent-missing leg: the point is that no agent binary resolves, not that the
# CLI loses git.
MINBIN="$SANDBOX/minbin"
mkdir -p "$MINBIN"
for tool in git jq sed grep cat dirname mkdir date env; do
  found="$(command -v "$tool" 2>/dev/null || true)"
  [ -n "$found" ] || continue
  ln -sf "$found" "$MINBIN/$tool"
done

# run_cli_path <PATH> <args...> — one CLI call with an explicit PATH, still
# from the sandbox root so the repository root resolves as it does for
# run_review.
run_cli_path() {
  local path="$1"
  shift
  set +e
  (
    cd "$EVAL_SANDBOX" || exit 99
    PATH="$path" "$EVAL_CLI" "$@" >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  # shellcheck disable=SC2034 # EVAL_STATUS is read by assert_exit in the caller
  EVAL_STATUS=$?
  set -e
  return 0
}

# spawns counts the agent processes the stub itself recorded. A leg that should
# not launch anything leaves the count where it was.
spawns() {
  if [ -f "$STUB_AGENT_LOG" ]; then
    grep -c '^cwd=' "$STUB_AGENT_LOG" 2>/dev/null || true
  else
    printf '0'
  fi
}

# The two bundles come from the real producer, so every later verb reads the
# artifact a fetch writes: one local bundle (handoff: record) and one PR bundle
# (handoff: post).
eval_mk_bundle "$LOCAL"
eval_mk_bundle "$PR"
for dir in "$LOCAL_DIR" "$PR_DIR"; do
  if [ -f "$dir/full.diff" ] && [ -f "$dir/hunks.tsv" ]; then
    good "produced bundle $(basename "$dir") carries full.diff and hunks.tsv"
  else
    bail "produced bundle $dir is incomplete"
  fi
done

# --- --print: the escape hatch renders the command and does nothing else -----
before_spawns="$(spawns)"
run_review run acme/app "$LOCAL" --agent claude --print --json -- --model opus
assert_exit "--print exits 0 without launching" 0
assert_envelope "--print"
assert_json_field "--print names the agent" "claude" '.data.agent'
assert_json_field "--print is marked printed" "true" '.data.printed'
assert_json_field "--print keeps the operator's flags before the directive" \
  "claude -p --model opus [prompt]" \
  '.data.argv | "\(.[0:4] | join(" ")) [prompt]"'
assert_json_field "--print's directive names the one review file" "true" \
  '.data.argv[-1] | startswith("Review acme/app local change local-launch") and contains("Write .verdict/state/local-launch/review.json with ")'
assert_eq "--print launched no agent process" "$before_spawns" "$(spawns)"
if [ ! -e "$LAUNCHER" ] && [ ! -e "$LOCAL_DIR/review.json" ]; then
  good "--print wrote no launcher record and no review"
else
  bad "--print wrote into the bundle (launcher.json or review.json)"
fi

run_review run acme/app "$LOCAL" --agent claude --print -- --model opus
assert_exit "--print (human) exits 0" 0
assert_file_grep "--print (human) renders the exact argv" "$EVAL_OUT" \
  "claude -p --model opus 'Review acme/app local change local-launch"
assert_eq "--print (human) launched no agent process" "$before_spawns" "$(spawns)"

# --- the launch: one process, one fresh artifact, one provenance record ------
export STUB_AGENT_MODE=write
run_review run acme/app "$LOCAL" --agent claude --json -- --model opus
assert_exit "launch exits 0 when the agent writes review.json" 0
assert_envelope "launch"
assert_json_field "launch reports the agent name" "claude" '.data.agent'
assert_json_field "launch reports the resolved binary" "$AGENTBIN/claude" '.data.binary'
assert_json_field "launch reports the agent's own exit" "0" '.data.exit'
assert_json_field "launch reports timed_out false" "false" '.data.timed_out'
assert_json_field "launch asserts a fresh artifact" "true" '.data.artifact_fresh'
assert_json_field "launch names the one artifact path" ".verdict/state/$LOCAL/review.json" '.data.artifact'
assert_json_field "launch names its provenance record" ".verdict/state/$LOCAL/launcher.json" '.data.record'
assert_json_field "launch hands off, it does not record" \
  "record the review with verdict record acme/app $LOCAL" '.next'
assert_json_field "launch's recorded argv replaces the directive" "true" \
  '(.data.argv | map(select(. == "<prompt>")) | length) == 1 and (.data.argv | index("--model")) == 2'
assert_eq "launch ran exactly one agent process" "1" "$(spawns)"
assert_file_grep "the agent got the shipped non-interactive flag" "$STUB_AGENT_LOG" '^arg\[0\]=-p$'
assert_file_grep "the agent got the operator's pass-through flags in order" "$STUB_AGENT_LOG" \
  '^arg\[1\]=--model$'
assert_file_grep "the operator's flag kept its operand" "$STUB_AGENT_LOG" '^arg\[2\]=opus$'
assert_file_grep "the directive arrived whole as the last argument" "$STUB_AGENT_LOG" \
  '^arg\[3\]=Review acme/app local change local-launch and write exactly one review file\.'
assert_file_grep "the directive named the one artifact to write" "$STUB_AGENT_LOG" \
  '^arg\[3\]=.*Write .verdict/state/local-launch/review.json with '
assert_file_grep "nothing follows the directive" "$STUB_AGENT_LOG" '^argc=4$'
assert_file_grep "the agent ran in the repository root" "$STUB_AGENT_LOG" "^cwd=$SANDBOX$"

assert_json_field "launcher.json carries its own schema_version" "1" '.schema_version' "$LAUNCHER"
assert_json_field "launcher.json records the agent name" "claude" '.agent' "$LAUNCHER"
assert_json_field "launcher.json records the freshness verdict" "true" '.artifact_fresh' "$LAUNCHER"
assert_json_field "launcher.json records the agent's exit" "0" '.exit' "$LAUNCHER"
assert_json_field "launcher.json records a duration" "true" '(.duration_ms | type) == "number"' "$LAUNCHER"
assert_json_field "launcher.json records the artifact size" "true" '.artifact_bytes > 0' "$LAUNCHER"
assert_file_nogrep "launcher.json never stores the directive body" "$LAUNCHER" \
  "write exactly one review file" "-F"
if [ -f "$LOCAL_DIR/.run.out" ]; then
  good "the child's own stream is kept in .run.out"
else
  bad ".run.out is missing (the operator lost the agent's output)"
fi
if [ ! -e "$LOCAL_DIR/findings.json" ] && [ ! -e "$LOCAL_DIR/record.json" ]; then
  good "the launcher recorded nothing: no findings.json, no record.json"
else
  bad "the launcher wrote findings.json or record.json (run must not record)"
fi
assert_stream_nogrep "the launcher itself made no host call" "$EVAL_STUBLOG" "stub-gh"

# --- the handoff: record reads the launched review unchanged -----------------
launched_sum="$(shasum "$LOCAL_DIR/review.json")"
run_review record acme/app "$LOCAL" --json
assert_exit "record accepts the review the launcher accepted" 0
assert_json_field "record stores exactly one open finding" "1" '.data.findings.open'
assert_json_field "record's item keeps the launcher's anchor" "notes.txt:2" '.data.items[0].locator'
if [ "$(shasum "$LOCAL_DIR/review.json")" = "$launched_sum" ] && [ -f "$LOCAL_DIR/record.json" ]; then
  good "record consumed the launched review byte-identically (record.json written)"
else
  bad "record rewrote review.json or wrote no record.json"
fi
assert_stream_nogrep "record made no host call either" "$EVAL_STUBLOG" "stub-gh"

# --- the PR handoff: post --dry-run accepts it, and nothing is submitted -----
run_review run acme/app "$PR" --agent claude --json
assert_exit "launch on a PR bundle exits 0" 0
assert_json_field "the PR launch hands off to post" \
  "post the review with verdict post acme/app $PR" '.next'
assert_stream_nogrep "the PR launch made no host call" "$EVAL_STUBLOG" "stub-gh"

run_review post acme/app "$PR" --dry-run --json
assert_exit "post --dry-run accepts the launched review" 0
assert_json_field "the dry run is a dry run" "true" '.data.dry_run'
assert_json_field "no --bind downgrades to COMMENT" "COMMENT" '.data.event'
assert_eq "the dry run paid exactly one intercepted head lookup" "1" \
  "$(grep -c 'stub-gh' "$EVAL_STUBLOG" 2>/dev/null || true)"
assert_file_nogrep "no review POST reached the host" "$EVAL_STUBLOG" "reviews"
if [ ! -e "$PR_DIR/post.json" ]; then
  good "the dry run wrote no post.json (run never posts)"
else
  bad "the dry run left a post.json behind"
fi

# --- typed refusals: every way the launch fails is named, and nothing runs ---
# The leftover is stamped in the past so the leg grades "older than the launch"
# on every filesystem, never a same-second race.
touch -t 202001010000 "$LOCAL_DIR/review.json"
export STUB_AGENT_MODE=none
run_review run acme/app "$LOCAL" --agent claude --json
assert_exit "a leftover review.json from an earlier run refuses" 1
assert_refusal "stale artifact" "stale-review-artifact"
assert_json_field "the stale refusal names the left-over file" "true" \
  '.error.message | contains("predates this run")'

rm -f "$LOCAL_DIR/review.json"
run_review run acme/app "$LOCAL" --agent claude --json
assert_exit "an agent that writes nothing refuses" 1
assert_refusal "no artifact" "no-review-artifact"
assert_json_field "the no-artifact refusal names the file it wanted" "true" \
  '.error.message | contains(".verdict/state/local-launch/review.json")'

export STUB_AGENT_MODE=fail
run_review run acme/app "$LOCAL" --agent claude --json
assert_exit "the agent's own exit code becomes the process exit code" 7
assert_refusal "agent failure" "agent-failed"
assert_json_field "the failure names the agent's exit" "true" \
  '.error.message | contains("exited 7")'

export STUB_AGENT_MODE=hang
run_review run acme/app "$LOCAL" --agent claude --timeout 2 --json
assert_exit "the watchdog kills an over-budget agent" 1
assert_refusal "watchdog" "agent-timeout"
assert_json_field "the timeout refusal names the watchdog" "true" \
  '.error.message | contains("watchdog")'
assert_json_field "the record marks the kill" "true" '.timed_out' "$LAUNCHER"

before_spawns="$(spawns)"
run_cli_path "$EVAL_STUBBIN:$MINBIN" run acme/app "$LOCAL" --agent claude --json
assert_exit "an agent binary that is not installed refuses" 1
assert_refusal "agent missing" "agent-missing"
assert_json_field "the missing-agent refusal names PATH" "true" \
  '.error.message | contains("not installed")'
run_review run acme/app "$LOCAL" --agent nope --json
assert_exit "an unknown agent name refuses" 1
assert_refusal "unknown agent" "unknown-agent"
assert_json_field "the unknown-agent refusal lists the shipped table" "true" \
  '.error.message | contains("claude, codex, cursor, opencode")'
run_review run acme/app "$LOCAL" --agent claude --timeout soon --json
assert_exit "a non-numeric timeout is a typed refusal" 1
assert_refusal "bad timeout" "bad-timeout"
run_review run acme/app 9999 --agent claude --json
assert_exit "a missing bundle refuses instead of fetching" 1
assert_refusal "missing bundle" "missing-bundle"
assert_eq "no refusal launched an agent process" "$before_spawns" "$(spawns)"

# --- the provenance record redacts secrets the child really received --------
export ANTHROPIC_API_KEY="$SECRET"
export STUB_AGENT_MODE=write
run_review run acme/app "$LOCAL" --agent claude --json \
  -- --api-key "$ANTHROPIC_API_KEY" --fast
assert_exit "a launch carrying a credential still succeeds" 0
assert_json_field "the recorded argv redacts the secret operand" "***REDACTED***" \
  '.argv[.argv | index("--api-key") + 1]' "$LAUNCHER"
assert_file_nogrep "the record carries no secret bytes" "$LAUNCHER" "$SECRET" "-F"
assert_json_field "a flag after the redacted operand is not swallowed" "true" \
  '(.argv | index("--fast")) != null' "$LAUNCHER"
assert_file_grep "the child received the real credential" "$STUB_AGENT_LOG" \
  "^arg\\[[0-9]+\\]=$SECRET$" "-E"
unset ANTHROPIC_API_KEY

# --- the same credential, echoed by the child, must not reach the record ----
# The leg above cannot catch the leak this case exists for: the stub wrote its
# argv to a log file, so the credential never entered the child's stream, and
# the value also sat in the environment where the old scrub found it. Here the
# credential is argument-only (no variable holds it) and the stub echoes its
# argv to stdout - a cooperating agent, a stray debug print, or a model
# repeating its prompt all do this. launcher.json is the provenance record, so
# the tail it carries must hide the operand; .run.out is the child's raw stream
# (docs/manual.md, "Child environment"), kept for debugging and written 0600
# because it can hold exactly these bytes, so the receipt proof lives there.
ARG_SECRET="sk-ant-eval-argonly-0002"
export STUB_AGENT_LOG=/dev/stdout
export STUB_AGENT_MODE=write
run_review run acme/app "$LOCAL" --agent claude --json \
  -- --api-key "$ARG_SECRET" --fast
export STUB_AGENT_LOG="$SANDBOX/agent-argv.log"
assert_exit "a child that echoes its own argv still launches" 0
assert_json_field "the record still marks the credential's position" "true" \
  '.output_tail | contains("arg[1]=--api-key") and contains("arg[2]=***REDACTED***")' "$LAUNCHER"
assert_file_nogrep "the record carries no credential the child printed" "$LAUNCHER" "$ARG_SECRET" "-F"
assert_file_grep "the child received the real credential (raw log)" "$LOCAL_DIR/.run.out" \
  "^arg\\[2\\]=$ARG_SECRET$" "-E"
assert_eq "the child's raw log is operator-only" "-rw-------" \
  "$(ls -l "$LOCAL_DIR/.run.out" | cut -c1-10)"

# --- the shipped recipe: docs claim no more than the launcher proves ---------
if [ -f "$RECIPE" ]; then
  good "docs/ci-and-tests.md exists"
else
  bail "docs/ci-and-tests.md is missing"
fi
for anchor in \
  "verdict run <repo> <pr|local-name> --agent <name>" \
  "review.json" \
  "launcher.json" \
  "not older than the launch" \
  "one-second" \
  "never posts" \
  "evals/fixtures/run-agent/agent-stub.sh" \
  "./evals/one.sh run-agent" \
  "no model" \
  "no secret"; do
  if grep -q -i -F -- "$anchor" "$RECIPE"; then
    good "the recipe names $anchor"
  else
    bad "the recipe misses $anchor"
  fi
done

# The lane that runs the case must prove it ran: a case deleted, renamed, or
# skipped would otherwise leave CI green about a recipe nobody exercised.
evals_job="$(awk '/^  evals:$/ { inside = 1 } inside && /^  evals-macos:$/ { exit } inside' "$WORKFLOW")"
printf '%s\n' "$evals_job" >"$SANDBOX/evals-job.txt"
assert_file_grep "CI proves the run --agent case passed" "$SANDBOX/evals-job.txt" \
  "evals/results/summary.json" "-F"
assert_file_grep "CI names the case it proves" "$SANDBOX/evals-job.txt" "run-agent" "-F"
assert_file_grep "CI names the committed stub the case launches" "$SANDBOX/evals-job.txt" \
  "evals/fixtures/run-agent/agent-stub.sh" "-F"
assert_file_nogrep "the evals lane needs no secret" "$SANDBOX/evals-job.txt" "secrets\\." "-E"
assert_file_nogrep "the evals lane needs no token" "$SANDBOX/evals-job.txt" \
  "GH_TOKEN|GITHUB_TOKEN" "-E"

eval_end
