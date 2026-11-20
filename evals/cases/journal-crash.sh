#!/usr/bin/env bash
#
# STA-447: crash vs cancellation semantics in the external bundle journal.
# - Leg 1 (SIGKILL mid-record): leaves an unpaired start in unfinished[] naming
#   `record` with started_at and no ended_at, while the last completed step is
#   `run` and chain_breaks is empty.
# - Leg 2 (SIGINT mid-record): a caught signal is NOT a crash — unfinished[] is
#   empty and the end line records exit 3 with refusal "canceled".
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

STUB_SRC="$EVAL_ROOT/evals/fixtures/run-agent/agent-stub.sh"
if [ ! -f "$STUB_SRC" ]; then
  bail "missing stub agent fixture: $STUB_SRC"
fi

REAL_GIT="$(command -v git)"
SANDBOX="$(cd "$EVAL_SANDBOX" && pwd)"

AGENTBIN="$SANDBOX/agentbin"
mkdir -p "$AGENTBIN"
cp "$STUB_SRC" "$AGENTBIN/claude"
chmod +x "$AGENTBIN/claude"

BLOCKBIN="$SANDBOX/blockbin"
mkdir -p "$BLOCKBIN"
cat >"$BLOCKBIN/git" <<EOF
#!/usr/bin/env bash
set -euo pipefail
if [ "\${1:-}" = "rev-parse" ] && [ "\${2:-}" = "--show-toplevel" ]; then
  exec "$REAL_GIT" "\$@"
fi
if [ -n "\${VERDICT_TEST_READY:-}" ]; then
  : >"\$VERDICT_TEST_READY"
fi
exec cat "\$VERDICT_TEST_FIFO"
EOF
chmod +x "$BLOCKBIN/git"

journal_path_for() {
  local repo="$1" bundle="$2" canon key
  canon="$(cd "$repo" && pwd -P)"
  if command -v sha256sum >/dev/null 2>&1; then
    key="$(printf '%s' "$canon" | sha256sum | cut -c1-16)"
  else
    key="$(printf '%s' "$canon" | shasum -a 256 | cut -c1-16)"
  fi
  printf '%s/verdict/journal/%s/%s.jsonl' "$XDG_STATE_HOME" "$key" "$bundle"
}

run_in() {
  local dir="$1"
  shift
  set +e
  (
    cd "$dir" || exit 99
    PATH="$AGENTBIN:$EVAL_STUBBIN:$PATH" "$EVAL_CLI" "$@" >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  # shellcheck disable=SC2034 # EVAL_STATUS is read by assert_exit
  EVAL_STATUS=$?
  set -e
  return 0
}

wait_for_start() {
  local journal="$1" ready="$2" tries=0
  while [ "$tries" -lt 200 ]; do
    if [ -f "$ready" ] && grep -q '"phase":"start".*"verb":"record"' "$journal" 2>/dev/null; then
      return 0
    fi
    sleep 0.05
    tries=$((tries + 1))
  done
  return 1
}

seed_bundle() {
  local repo="$1" slug="$2"
  eval_fixture_repo "$repo"
  run_in "$repo" fetch-local acme/app "$slug" --json
  assert_exit "$slug fetch-local succeeds" 0
  run_in "$repo" run acme/app "local-$slug" --agent claude --finders 1 --rounds 1 --json
  assert_exit "$slug run succeeds" 0
}

# --- Leg 1: SIGKILL mid-record (crash) --------------------------------------
REPO_KILL="$SANDBOX/repo-kill"
SLUG_KILL="crash"
BUNDLE_KILL="local-$SLUG_KILL"
FIFO_KILL="$SANDBOX/kill.fifo"
READY_KILL="$SANDBOX/kill.ready"
mkfifo "$FIFO_KILL"

seed_bundle "$REPO_KILL" "$SLUG_KILL"
JOURNAL_KILL="$(journal_path_for "$REPO_KILL" "$BUNDLE_KILL")"

(
  cd "$REPO_KILL" || exit 99
  VERDICT_TEST_FIFO="$FIFO_KILL" VERDICT_TEST_READY="$READY_KILL" \
    PATH="$BLOCKBIN:$EVAL_STUBBIN:$PATH" \
    exec "$EVAL_CLI" record acme/app "$BUNDLE_KILL" --json \
    >"$SANDBOX/kill.out" 2>"$SANDBOX/kill.err"
) &
KILL_PID=$!

if ! wait_for_start "$JOURNAL_KILL" "$READY_KILL"; then
  kill -9 "$KILL_PID" 2>/dev/null || true
  bail "record did not write start line and reach blocking git stub"
fi

# Simulate hard crash (SIGKILL).
kill -9 "$KILL_PID" 2>/dev/null || true
wait "$KILL_PID" 2>/dev/null || true
# Unblock any orphaned blocking git stub reading the FIFO so the sandbox cleans up.
( : >"$FIFO_KILL" ) >/dev/null 2>&1 &
UNBLOCK_PID=$!
sleep 0.1
kill "$UNBLOCK_PID" 2>/dev/null || true
wait "$UNBLOCK_PID" 2>/dev/null || true

run_in "$REPO_KILL" journal acme/app "$BUNDLE_KILL" --json
assert_exit "journal after SIGKILL succeeds" 0
assert_envelope "journal after SIGKILL"
cp "$EVAL_OUT" "$SANDBOX/journal-kill.json"

assert_json_field "SIGKILL leaves 2 completed steps" "2" '.data.steps | length' "$SANDBOX/journal-kill.json"
assert_json_field "last completed step before crash is run" "run" '.data.steps[-1].verb' "$SANDBOX/journal-kill.json"
assert_json_field "SIGKILL leaves 1 unfinished step" "1" '.data.unfinished | length' "$SANDBOX/journal-kill.json"
assert_json_field "unfinished step verb is record" "record" '.data.unfinished[0].verb' "$SANDBOX/journal-kill.json"
assert_json_field "unfinished step has started_at" "true" '(.data.unfinished[0].started_at | length) > 0' "$SANDBOX/journal-kill.json"
assert_json_field "unfinished step has no ended_at" "false" '.data.unfinished[0] | has("ended_at")' "$SANDBOX/journal-kill.json"
assert_json_field "SIGKILL leaves zero chain breaks" "0" '.data.chain_breaks | length' "$SANDBOX/journal-kill.json"

# --- Leg 2: SIGINT mid-record (caught cancellation, NOT a crash) ------------
REPO_INT="$SANDBOX/repo-int"
SLUG_INT="cancel"
BUNDLE_INT="local-$SLUG_INT"
FIFO_INT="$SANDBOX/int.fifo"
READY_INT="$SANDBOX/int.ready"
mkfifo "$FIFO_INT"

seed_bundle "$REPO_INT" "$SLUG_INT"
JOURNAL_INT="$(journal_path_for "$REPO_INT" "$BUNDLE_INT")"

(
  cd "$REPO_INT" || exit 99
  VERDICT_TEST_FIFO="$FIFO_INT" VERDICT_TEST_READY="$READY_INT" \
    PATH="$BLOCKBIN:$EVAL_STUBBIN:$PATH" \
    exec "$EVAL_CLI" record acme/app "$BUNDLE_INT" --json \
    >"$SANDBOX/int.out" 2>"$SANDBOX/int.err"
) &
INT_PID=$!

if ! wait_for_start "$JOURNAL_INT" "$READY_INT"; then
  kill -9 "$INT_PID" 2>/dev/null || true
  bail "record (SIGINT leg) did not write start line and reach blocking git stub"
fi

kill -INT "$INT_PID" 2>/dev/null || true
set +e
wait "$INT_PID"
INT_CODE=$?
set -e
assert_eq "SIGINT record exits 3 (canceled)" "3" "$INT_CODE"

run_in "$REPO_INT" journal acme/app "$BUNDLE_INT" --json
assert_exit "journal after SIGINT succeeds" 0
assert_envelope "journal after SIGINT"
cp "$EVAL_OUT" "$SANDBOX/journal-int.json"

assert_json_field "SIGINT leaves zero unfinished steps" "0" '.data.unfinished | length' "$SANDBOX/journal-int.json"
assert_json_field "SIGINT records 3 completed steps" "3" '.data.steps | length' "$SANDBOX/journal-int.json"
assert_json_field "SIGINT step verb is record" "record" '.data.steps[2].verb' "$SANDBOX/journal-int.json"
assert_json_field "SIGINT step exit is 3" "3" '.data.steps[2].exit' "$SANDBOX/journal-int.json"
assert_json_field "SIGINT step refusal is canceled" "canceled" '.data.steps[2].refusal' "$SANDBOX/journal-int.json"
assert_json_field "SIGINT leaves zero chain breaks" "0" '.data.chain_breaks | length' "$SANDBOX/journal-int.json"

eval_end
