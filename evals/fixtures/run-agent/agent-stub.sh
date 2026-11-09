#!/usr/bin/env bash
#
# Eval fixture: the agent `verdict run --agent` launches, with no model, no
# network, and no host. evals/cases/run-agent.sh copies this file onto PATH
# under a shipped agent name (claude), so the launcher resolves a real
# executable through its own table and the case grades the shipped path rather
# than a mock of it.
#
# The stub is a *cooperating* agent: it reads the single review path out of the
# launcher's directive (its last argument) and writes exactly that file, which
# is the one thing the artifact contract asks for. It never reads the bundle
# and never calls a host.
#
# STUB_AGENT_MODE picks the behaviour a leg grades:
#   write (default)  parse the directive, write one valid review.json, exit 0
#   none             exit 0 having written nothing (no-artifact / stale cases)
#   fail             exit 7 (a real non-zero agent exit)
#   hang             sleep past any sane watchdog budget
#
# STUB_AGENT_LOG, when set, receives the child's cwd, its argument count, and
# one line per argument (newlines flattened), so a case can prove what the
# launcher actually passed — and what it recorded instead.
set -euo pipefail

mode="${STUB_AGENT_MODE:-write}"

if [ -n "${STUB_AGENT_LOG:-}" ]; then
  {
    printf 'cwd=%s\n' "$PWD"
    printf 'argc=%s\n' "$#"
    i=0
    for arg in "$@"; do
      printf 'arg[%s]=%s\n' "$i" "$(printf '%s' "$arg" | tr '\n' ' ')"
      i=$((i + 1))
    done
  } >>"$STUB_AGENT_LOG"
fi

case "$mode" in
  none) exit 0 ;;
  fail) exit 7 ;;
  hang) sleep 30 ;;
esac

# The directive ends with: Write <artifact> with {...}. Take the last argument
# (the directive), then cut the artifact path out of that sentence: no side
# channel, so a directive that stopped naming its artifact fails here.
directive="${!#}"
rest="${directive#*Write }"
artifact="${rest%% with \{*}"
if [ -z "$artifact" ] || [ "$artifact" = "$directive" ]; then
  echo "agent-stub: the directive named no review path to write" >&2
  exit 2
fi
mkdir -p "$(dirname "$artifact")"
cat >"$artifact" <<'JSON'
{"schema_version":"1","body":"Nit: one note, nothing blocking.","approved":false,
 "comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Nit: rename this line.","severity":"informational","rigor":"walked"}]}
JSON
