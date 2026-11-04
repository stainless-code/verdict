#!/usr/bin/env bash
#
#
# Watchdog: every CLI run below goes through watchdog_review, which backgrounds
# the command with explicit stdin and polls with kill -0 plus a ps zombie arm
# (a reaped-but-unwaited child reads alive under kill -0; without the ps arm a
# fast exit could false-fire the timeout). Portable to bash 3.2 and macOS: no
# `timeout(1)` (absent on macOS), no `wait -n` (bash 4+). A hang sets
# WD_TIMED_OUT and fails the leg — the suite keeps running.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

command -v ps >/dev/null 2>&1 || bail "ps(1) missing — the watchdog cannot rule out zombies"

LIMIT=15
WD_STATUS=0
WD_TIMED_OUT="false"
WD_DIR=""

watchdog_review() {
  local limit="$1" src="$2"
  shift 2
  if [ "${1:-}" = "--" ]; then shift; fi
  local dir="${WD_DIR:-$EVAL_SANDBOX}"
  local pid="" elapsed=0 alive=""
  WD_STATUS=0
  WD_TIMED_OUT="false"
  set +e
  if [ "$src" = "closed" ]; then
    ( cd "$dir" && PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" "$@" <&- >"$EVAL_OUT" 2>"$EVAL_ERR" ) & pid=$!
  else
    ( cd "$dir" && PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" "$@" <"$src" >"$EVAL_OUT" 2>"$EVAL_ERR" ) & pid=$!
  fi
  while :; do
    alive="1"
    kill -0 "$pid" 2>/dev/null || alive=""
    if [ -n "$alive" ] && ps -p "$pid" -o stat= 2>/dev/null | matches 'Z'; then alive=""; fi
    if [ -z "$alive" ]; then break; fi
    if [ "$elapsed" -ge "$limit" ]; then
      kill -9 "$pid" 2>/dev/null || true
      wait "$pid" >/dev/null 2>&1
      WD_STATUS=124
      WD_TIMED_OUT="true"
      EVAL_STATUS=124
      set -e
      return 0
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  wait "$pid"
  WD_STATUS=$?
  EVAL_STATUS=$WD_STATUS
  set -e
  return 0
}

hung_ok() { # $1 = label: watchdog silent is the whole point
  if [ "$WD_TIMED_OUT" = "false" ]; then
    good "$1 exited promptly (no watchdog fire)"
  else
    bad "$1 hung — watchdog fired at ${LIMIT}s (this leg fails, the suite continues)"
  fi
}

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

OLD_HOME="$HOME"
export HOME="$EVAL_SANDBOX/fakehome"
mkdir -p "$EVAL_SANDBOX/.agents/skills" "$HOME/.agents/skills"
echo "foreign skill" > "$HOME/.agents/skills/foreign-skill.txt"
FOREIGN_SNAP="$EVAL_SANDBOX/foreign-snap.txt"
(cd "$EVAL_SANDBOX" && find .agents fakehome -type f | LC_ALL=C sort | xargs shasum) > "$FOREIGN_SNAP"
NEVER_SKILLS="$EVAL_SANDBOX/never-skills"

mkdir -p "$EVAL_SANDBOX/np-repo"
(
  cd "$EVAL_SANDBOX/np-repo" || exit 99
  git init -q
  git config user.email "never-prompts@example.com"
  git config user.name "never prompts"
  echo "probe" > probe.txt
  git add -A
  git commit -qm probe
)

WD_DIR="$EVAL_SANDBOX/np-repo"
watchdog_review "$LIMIT" "/dev/null" -- init --preset standard --yes
hung_ok "seed init --preset standard --yes"
if [ "$EVAL_STATUS" -eq 0 ] && [ -f "$EVAL_SANDBOX/np-repo/.review/kb.jsonl" ]; then
  good "seed writes the four control files"
else
  bad "seed exits $EVAL_STATUS"
fi
seed_sums="$(shasum "$EVAL_SANDBOX"/np-repo/.review/budget "$EVAL_SANDBOX"/np-repo/.review/panel "$EVAL_SANDBOX"/np-repo/.review/kb-policy "$EVAL_SANDBOX"/np-repo/.review/kb.jsonl)"

WD_DIR="$EVAL_SANDBOX"
watchdog_review "$LIMIT" "closed" -- skill install --dir "$NEVER_SKILLS"
hung_ok "skill install with stdin closed"
if [ "$EVAL_STATUS" -eq 0 ] && [ -f "$NEVER_SKILLS/verdict/SKILL.md" ]; then
  good "skill install applies the embedded payload (exit 0)"
else
  bad "skill install exits $EVAL_STATUS without the payload"
fi
assert_stream_nogrep "skill install never prompts on stdout (no Proceed? text)" "$EVAL_OUT" "Proceed?"
assert_stream_nogrep "skill install never prompts on stderr (no Proceed? text)" "$EVAL_ERR" "Proceed?"
if (cd "$EVAL_SANDBOX" && shasum -c "$FOREIGN_SNAP" >/dev/null 2>&1) \
  && [ ! -e "$EVAL_SANDBOX/.agents/skills/verdict" ] \
  && [ ! -e "$HOME/.agents/skills/verdict" ]; then
  good "install left detected agent dirs and foreign bytes alone"
else
  bad "install touched foreign or detected agent dirs"
fi

watchdog_review "$LIMIT" "/dev/null" -- skill install --dir "$NEVER_SKILLS" --json
hung_ok "skill install --json with stdin at /dev/null"
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.error == null and .data.status == "keep"' <"$EVAL_OUT" 2>/dev/null)" = "true" ]; then
  good "machine keep is a success envelope at exit 0"
else
  bad "machine install: exit $EVAL_STATUS data $(jq -r '.data.status // empty' <"$EVAL_OUT" 2>/dev/null || true)"
fi
if [ "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null || true)" = "$EVAL_ENVELOPE_KEYS" ] \
  && jq -e '.data != null' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "keep keeps the one-doc envelope (documented keys, data present)"
else
  bad "keep breaks the envelope shape"
fi

WD_DIR="$EVAL_SANDBOX/np-repo"
watchdog_review "$LIMIT" "/dev/null" -- init --preset chill
hung_ok "init overwrite with stdin at /dev/null"
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "without confirm" "$EVAL_ERR" \
  && grep -q "try:" "$EVAL_ERR"; then
  good "overwrite without --yes refuses needs-confirm at exit 1 with a try: hint"
else
  bad "overwrite guard exits $EVAL_STATUS"
fi
if [ "$(shasum "$EVAL_SANDBOX"/np-repo/.review/budget "$EVAL_SANDBOX"/np-repo/.review/panel "$EVAL_SANDBOX"/np-repo/.review/kb-policy "$EVAL_SANDBOX"/np-repo/.review/kb.jsonl)" = "$seed_sums" ]; then
  good "refused overwrite left all four control files byte-identical"
else
  bad "refused overwrite mutated the control files"
fi

watchdog_review "$LIMIT" "closed" -- init --preset strict --json
hung_ok "init --json overwrite with stdin closed"
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "needs-confirm" ] \
  && grep -q "try:" "$EVAL_OUT"; then
  good "machine overwrite refusal is typed needs-confirm at exit 1 with a try: hint"
else
  bad "machine overwrite refusal: exit $EVAL_STATUS code $(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
fi

PR=6101
mkdir -p "$EVAL_SANDBOX/.review/$PR"
jq -n '{"verifyCmd":"read -r line || true"}' > "$EVAL_SANDBOX/.review/$PR/patch.json"
WD_DIR="$EVAL_SANDBOX"
watchdog_review 20 "closed" -- verify acme/app "$PR" --json --timeout 10
hung_ok "verify (stdin-consuming verifyCmd) with stdin closed"
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.timedOut' <"$EVAL_OUT" 2>/dev/null)" = "false" ]; then
  good "verification completes promptly (exit 0, timedOut false)"
else
  bad "verification exits $EVAL_STATUS (timedOut $(jq -r '.data.timedOut' <"$EVAL_OUT" 2>/dev/null || true))"
fi
if [ "$(jq -r '.verifyExit // empty' "$EVAL_SANDBOX/.review/$PR/patch.json" 2>/dev/null)" = "0" ]; then
  good "proof recorded into patch.json (verifyExit 0)"
else
  bad "proof missing from patch.json"
fi

# --- H6: hang-trap — held-open pipe as stdin (never EOF, never data) ---
mkfifo "$EVAL_SANDBOX/stdin-pipe"
# O_RDWR open never blocks and never delivers EOF: a regressed bare `read`
# would block here until the watchdog fires.
exec 9<> "$EVAL_SANDBOX/stdin-pipe"
watchdog_review "$LIMIT" "$EVAL_SANDBOX/stdin-pipe" -- skill install --dir "$NEVER_SKILLS" --json
hung_ok "skill install --json with a held-open pipe as stdin"
exec 9>&-
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.error == null' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "piped stdin completes without reading (keep envelope, exit 0)"
else
  bad "piped-stdin leg exits $EVAL_STATUS"
fi
rm -f "$EVAL_SANDBOX/stdin-pipe"

# script(1) gives the child a pty stdin (-t 0 true) while our own stdin stays
# /dev/null, so a regressed prompt would block on the pty read and the
# watchdog — never the suite — eats the timeout. macOS and Linux spell the
# invocation differently (script -q /dev/null cmd... vs script -qec "cmd"
# /dev/null); anything else, or no script(1) at all, reports SKIP — the leg did
# not run, so it must not count as a pass (test-audit F8).
if command -v script >/dev/null 2>&1; then
  case "$(uname -s 2>/dev/null || true)" in
    Darwin|Linux) pty_avail="true";;
    *) pty_avail="false";;
  esac
  # Restricted sandboxes can exist without pty allocation; skip there too
  # rather than grading an openpty failure as a CLI defect. Probe with the same
  # per-OS form the legs use: util-linux script(1) takes the command via -c and
  # rejects the BSD trailing-command form, which made Linux always SKIP.
  if [ "$pty_avail" = "true" ]; then
    if [ "$(uname -s)" = "Darwin" ]; then
      script -q /dev/null /bin/sh -c true </dev/null >/dev/null 2>&1 || pty_avail="false"
    else
      script -qec true /dev/null </dev/null >/dev/null 2>&1 || pty_avail="false"
    fi
  fi
else
  pty_avail="false"
fi
if [ "${pty_avail:-false}" = "true" ]; then
  # The pty merges the child's two streams and translates its newlines, so the
  # CLI writes them to plain files from inside the session: stdin stays the
  # terminal under test, and the graded envelope never passes through the pty.
  watchdog_pty() {
    local limit="$1"
    shift
    if [ "${1:-}" = "--" ]; then shift; fi
    local dir="${WD_DIR:-$EVAL_SANDBOX}"
    local pid="" elapsed=0 alive="" one="" q=""
    for one in "$EVAL_CLI" "$@"; do
      q="$q '${one//\'/\'\\\'\'}'"
    done
    q="$q >'${EVAL_OUT//\'/\'\\\'\'}' 2>'${EVAL_ERR//\'/\'\\\'\'}'"
    WD_STATUS=0
    WD_TIMED_OUT="false"
    set +e
    if [ "$(uname -s)" = "Darwin" ]; then
      ( cd "$dir" && PATH="$EVAL_STUBBIN:$PATH" script -q /dev/null /bin/sh -c "$q" </dev/null >/dev/null 2>&1 ) & pid=$!
    else
      ( cd "$dir" && PATH="$EVAL_STUBBIN:$PATH" script -qec "$q" /dev/null </dev/null >/dev/null 2>&1 ) & pid=$!
    fi
    while :; do
      alive="1"
      kill -0 "$pid" 2>/dev/null || alive=""
      if [ -n "$alive" ] && ps -p "$pid" -o stat= 2>/dev/null | matches 'Z'; then alive=""; fi
      if [ -z "$alive" ]; then break; fi
      if [ "$elapsed" -ge "$limit" ]; then
        kill -9 "$pid" 2>/dev/null || true
        wait "$pid" >/dev/null 2>&1
        WD_STATUS=124
        WD_TIMED_OUT="true"
        EVAL_STATUS=124
        set -e
        return 0
      fi
      sleep 1
      elapsed=$((elapsed + 1))
    done
    wait "$pid"
    WD_STATUS=$?
    EVAL_STATUS=$WD_STATUS
    set -e
    return 0
  }

  WD_DIR="$EVAL_SANDBOX"
  watchdog_pty "$LIMIT" -- skill install --dir "$NEVER_SKILLS" --json
  hung_ok "skill install --json on a pty"
  if [ "$EVAL_STATUS" -eq 0 ] \
    && jq -e '.error == null and .data.status == "keep"' <"$EVAL_OUT" >/dev/null 2>&1; then
    good "pty machine install keeps at exit 0"
  else
    bad "pty machine install: exit $EVAL_STATUS status $(jq -r '.data.status // empty' <"$EVAL_OUT" 2>/dev/null || true)"
  fi
  assert_stream_nogrep "no prompt text on stdout under --json on a pty" "$EVAL_OUT" "Proceed?"
  assert_stream_nogrep "no prompt text on stderr under --json on a pty" "$EVAL_ERR" "Proceed?"
  if jq -e '.data != null and .error == null' <"$EVAL_OUT" >/dev/null 2>&1; then
    good "pty keep keeps the envelope shape (data present, error null)"
  else
    bad "pty keep breaks the envelope shape"
  fi
  if (cd "$EVAL_SANDBOX" && shasum -c "$FOREIGN_SNAP" >/dev/null 2>&1) \
    && [ ! -e "$EVAL_SANDBOX/.agents/skills/verdict" ] \
    && [ ! -e "$HOME/.agents/skills/verdict" ]; then
    good "pty install left detected agent dirs and foreign bytes alone"
  else
    bad "pty install touched foreign or detected agent dirs"
  fi

  WD_DIR="$EVAL_SANDBOX/np-repo"
  watchdog_pty "$LIMIT" -- init --preset chill --json
  hung_ok "init --json overwrite on a pty"
  if [ "$EVAL_STATUS" -eq 1 ] \
    && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)" = "needs-confirm" ] \
    && grep -q "try:" "$EVAL_OUT"; then
    good "pty machine overwrite refusal is typed needs-confirm at exit 1 with a try: hint"
  else
    bad "pty machine overwrite refusal: exit $EVAL_STATUS code $(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
  fi
  assert_stream_nogrep "no prompt text on stdout under --json on a pty (init)" "$EVAL_OUT" "Proceed?"
  assert_stream_nogrep "no prompt text on stderr under --json on a pty (init)" "$EVAL_ERR" "Proceed?"
  if [ "$(shasum "$EVAL_SANDBOX"/np-repo/.review/budget "$EVAL_SANDBOX"/np-repo/.review/panel "$EVAL_SANDBOX"/np-repo/.review/kb-policy "$EVAL_SANDBOX"/np-repo/.review/kb.jsonl)" = "$seed_sums" ]; then
    good "pty refused overwrite left all four control files byte-identical"
  else
    bad "pty refused overwrite mutated the control files"
  fi
else
  skip "script(1) unavailable or unsupported OS — pty legs did not run"
fi

if [ "$(( $(wc -l <"$EVAL_STUBLOG" | tr -d ' ') - stub_before ))" = "0" ]; then
  good "zero stub-gh intercepts across never-prompts legs (offline)"
else
  bad "stub gh intercepted calls during never-prompts legs"
fi

export HOME="$OLD_HOME"
rm -rf "$EVAL_SANDBOX/fakehome" "$EVAL_SANDBOX/.agents" "$EVAL_SANDBOX/np-repo" "$EVAL_SANDBOX/never-skills" "$EVAL_SANDBOX/foreign-snap.txt"
rm -rf "$EVAL_SANDBOX/.review/6101"

eval_end
