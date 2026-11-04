#!/usr/bin/env bash
# Case never-prompts: machine modes never block on stdin.
#
# Grades (all hermetic — sandbox cwd, fake HOME, stub gh, stdin never a TTY):
#   H1  embedded skill install with stdin CLOSED applies the payload (exit 0)
#       without reading stdin and without dropping a verdict/ slot in any
#       detected agent dir.
#   H2  `skill install --json` with stdin at /dev/null reads keep (exit 0),
#       keeps the documented envelope, and never prompts.
#   H3  scaffolder overwrite with stdin at /dev/null refuses typed
#       needs-confirm (exit 1, try: hint) and overwrites nothing.
#   H4  scaffolder overwrite with stdin CLOSED under --json refuses typed
#       needs-confirm in the envelope, exit exactly 1.
#   H5  verification step: a verifyCmd that consumes stdin (`read -r line`)
#       completes promptly with stdin closed (exit 0, timedOut false, proof
#       recorded) — the spawned verification never waits on stdin either.
#   H6  hang-trap: `skill install --json` with stdin a held-open pipe (open
#       for read+write, never EOF, never data) still completes promptly. A
#       regressed bare `read` would block here, so the watchdog — never the
#       suite — eats the timeout and the leg FAILs.
#   H8  live pty: `skill install --json` and `init --json` with stdin a
#       pseudo-TTY (via script(1), macOS/Linux forms detected; SKIP where
#       unavailable, never a pass) never prompt; the installer
#       applies/keeps, the scaffolder refuses typed `needs-confirm` at exit 1.
#       A regressed
#       TTY-scoped prompt would block on the pty read, so the watchdog turns
#       it into a leg FAIL instead of a suite hang.
#   H7  offline: zero stub-gh intercepts across every leg.
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

# watchdog_review <secs> <stdin> -- <verdict args...>: runs $EVAL_CLI directly.
# <stdin> is "closed" (EBADF on read) or a path (/dev/null, fifo). Runs in
# $WD_DIR (default: sandbox root) with the stub gh first on PATH, captures to
# EVAL_OUT/EVAL_ERR, sets EVAL_STATUS/WD_STATUS/WD_TIMED_OUT. Always returns 0
# so callers under `set -e` can grade hangs as well as exits.
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

# --- sandbox roots: repo scope is the sandbox cwd; home scope is a fake HOME ---
OLD_HOME="$HOME"
export HOME="$EVAL_SANDBOX/fakehome"
mkdir -p "$EVAL_SANDBOX/.agents/skills" "$HOME/.agents/skills"
echo "foreign skill" > "$HOME/.agents/skills/foreign-skill.txt"
FOREIGN_SNAP="$EVAL_SANDBOX/foreign-snap.txt"
(cd "$EVAL_SANDBOX" && find .agents fakehome -type f | LC_ALL=C sort | xargs shasum) > "$FOREIGN_SNAP"
NEVER_SKILLS="$EVAL_SANDBOX/never-skills"

# --- scratch repo for the scaffolder overwrite path (own .review/, own tree) ---
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

# --- seed: standard preset applies cleanly (never prompts: nothing to overwrite) ---
WD_DIR="$EVAL_SANDBOX/np-repo"
watchdog_review "$LIMIT" "/dev/null" -- init --preset standard --yes
hung_ok "seed init --preset standard --yes"
if [ "$EVAL_STATUS" -eq 0 ] && [ -f "$EVAL_SANDBOX/np-repo/.review/kb.jsonl" ]; then
  good "seed writes the four control files"
else
  bad "seed exits $EVAL_STATUS"
fi
seed_sums="$(shasum "$EVAL_SANDBOX"/np-repo/.review/budget "$EVAL_SANDBOX"/np-repo/.review/panel "$EVAL_SANDBOX"/np-repo/.review/kb-policy "$EVAL_SANDBOX"/np-repo/.review/kb.jsonl)"

# --- H1: skill install, stdin closed, human mode ---
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

# --- H2: skill install, stdin /dev/null, machine mode ---
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

# --- H3: scaffolder overwrite, stdin /dev/null, human mode ---
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

# --- H4: scaffolder overwrite, stdin closed, machine mode ---
watchdog_review "$LIMIT" "closed" -- init --preset strict --json
hung_ok "init --json overwrite with stdin closed"
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "needs-confirm" ] \
  && grep -q "try:" "$EVAL_OUT"; then
  good "machine overwrite refusal is typed needs-confirm at exit 1 with a try: hint"
else
  bad "machine overwrite refusal: exit $EVAL_STATUS code $(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
fi

# --- H5: verification step — stdin-consuming verifyCmd, stdin closed ---
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

# --- H8: machine mode on a live pty never prompts ---
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
  # watchdog_pty <secs> -- <verdict args...>: like watchdog_review but the
  # child runs under script(1), so its stdin is a pty. Always returns 0 so
  # callers under `set -e` can grade hangs as well as exits.
  watchdog_pty() {
    local limit="$1"
    shift
    if [ "${1:-}" = "--" ]; then shift; fi
    local dir="${WD_DIR:-$EVAL_SANDBOX}"
    local pid="" elapsed=0 alive="" one="" q=""
    WD_STATUS=0
    WD_TIMED_OUT="false"
    set +e
    if [ "$(uname -s)" = "Darwin" ]; then
      ( cd "$dir" && PATH="$EVAL_STUBBIN:$PATH" script -q /dev/null "$EVAL_CLI" "$@" </dev/null >"$EVAL_OUT" 2>"$EVAL_ERR" ) & pid=$!
    else
      for one in "$@"; do
        q="$q '${one//\'/\'\\\'\'}'"
      done
      ( cd "$dir" && PATH="$EVAL_STUBBIN:$PATH" script -qec "'$EVAL_CLI'$q" /dev/null </dev/null >"$EVAL_OUT" 2>"$EVAL_ERR" ) & pid=$!
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

  # pty_envelope: the pty merges the child's stdout+stderr into one stream,
  # so the envelope cannot be read straight off EVAL_OUT. The fail() envelope
  # is pretty-printed jq: it opens on a line that is exactly `{` and closes
  # on a line that is exactly `}` (nested closings are indented; preview and
  # re-enter lines never form a lone brace). Print from the last such opener
  # through its closer, stripping pty CRs. Prints nothing when absent.
  pty_envelope() {
    awk '{ sub(/\r$/, "") } /^{$/ { buf=""; inenv=1 } inenv { buf=buf $0 "\n" } /^}$/ { if (inenv) { printf "%s", buf; inenv=0; buf="" } }' "$1"
  }

  # --- H8a: skill install under --json on a pty ---
  WD_DIR="$EVAL_SANDBOX"
  watchdog_pty "$LIMIT" -- skill install --dir "$NEVER_SKILLS" --json
  hung_ok "skill install --json on a pty"
  pty_env="$(pty_envelope "$EVAL_OUT")"
  if [ "$EVAL_STATUS" -eq 0 ] \
    && [ "$(jq -r '.error == null and .data.status == "keep"' <<<"$pty_env" 2>/dev/null)" = "true" ]; then
    good "pty machine install keeps at exit 0"
  else
    bad "pty machine install: exit $EVAL_STATUS status $(jq -r '.data.status // empty' <<<"$pty_env" 2>/dev/null || true)"
  fi
  assert_stream_nogrep "no prompt text on stdout under --json on a pty" "$EVAL_OUT" "Proceed?"
  assert_stream_nogrep "no prompt text on stderr under --json on a pty" "$EVAL_ERR" "Proceed?"
  if jq -e '.data != null and .error == null' <<<"$pty_env" >/dev/null 2>&1; then
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

  # --- H8b: scaffolder overwrite under --json on a pty ---
  # np-repo still carries the standard seed (H3/H4 refused), so a chill
  # preset wants overwrite and must take the confirm path.
  WD_DIR="$EVAL_SANDBOX/np-repo"
  watchdog_pty "$LIMIT" -- init --preset chill --json
  hung_ok "init --json overwrite on a pty"
  pty_env="$(pty_envelope "$EVAL_OUT")"
  if [ "$EVAL_STATUS" -eq 1 ] \
    && [ "$(jq -r '.error.code // empty' <<<"$pty_env" 2>/dev/null)" = "needs-confirm" ] \
    && grep -q "try:" <<<"$pty_env"; then
    good "pty machine overwrite refusal is typed needs-confirm at exit 1 with a try: hint"
  else
    bad "pty machine overwrite refusal: exit $EVAL_STATUS code $(jq -r '.error.code // empty' <<<"$pty_env" 2>/dev/null || true)"
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

# --- H7: offline across every leg ---
if [ "$(( $(wc -l <"$EVAL_STUBLOG" | tr -d ' ') - stub_before ))" = "0" ]; then
  good "zero stub-gh intercepts across never-prompts legs (offline)"
else
  bad "stub gh intercepted calls during never-prompts legs"
fi

# --- restore: fake HOME and scratch state stay in the sandbox ---
export HOME="$OLD_HOME"
rm -rf "$EVAL_SANDBOX/fakehome" "$EVAL_SANDBOX/.agents" "$EVAL_SANDBOX/np-repo" "$EVAL_SANDBOX/never-skills" "$EVAL_SANDBOX/foreign-snap.txt"
rm -rf "$EVAL_SANDBOX/.review/6101"

eval_end
