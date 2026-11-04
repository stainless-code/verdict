#!/bin/sh
# on-stop.sh — Claude Code Stop hook: convert the finished session transcript
# to a verdict usage record for the budget gate.
#
# Setup: copy hook.json into .claude/settings.json (fixing the script path),
# then export VERDICT_USAGE_DIR for the hook and pass the same directory as
# VERDICT_USAGE on later fetches. Reads the hook JSON from stdin.
#
# Best-effort by design: a hook failure must never break the session, so
# every failure path exits 0 after (at most) one stderr line.
set -eu

prog="verdict-on-stop"
[ -n "${VERDICT_USAGE_DIR:-}" ] || exit 0
command -v jq >/dev/null 2>&1 || { echo "$prog: jq not on PATH, skipping usage write" >&2; exit 0; }
input="$(cat)"
transcript="$(printf '%s' "$input" | jq -r '.transcript_path // empty')"
[ -n "$transcript" ] || exit 0
[ -f "$transcript" ] || exit 0
session="$(printf '%s' "$input" | jq -r '.session_id // empty')"
case "$session" in
  ""|*/*) session="session-$(date +%Y%m%dT%H%M%SZ)" ;;
esac
mkdir -p "$VERDICT_USAGE_DIR" || exit 0
here="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
if ! "$here/claude-to-usage.sh" "$transcript" "$VERDICT_USAGE_DIR/$session.json" >/dev/null 2>&1; then
  echo "$prog: converter refused the transcript, wrote nothing" >&2
  exit 0
fi
