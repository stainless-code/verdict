#!/usr/bin/env bash
#
# Eval fixture: the repair-loop agent `verdict run --agent --rounds N`
# launches. Like agent-stub.sh it reads the single review path out of the
# launcher's directive (its last argument) and writes exactly that file, with
# no model, no network, and no host. Unlike agent-stub.sh it changes its
# answer per invocation, counted in STUB_REPAIR_COUNT, so a case can grade the
# loop: an invalid first round, a valid second one, or never valid at all.
#
# STUB_REPAIR_MODE picks the behaviour a leg grades:
#   fix-on-second  first call writes invalid bytes, later calls a valid review
#   always-invalid every call writes distinct invalid bytes (the call number
#                  keeps them distinct, so the no-progress stop never fires)
#   valid          every call writes a valid review (the one-shot default leg)
#
# STUB_REPAIR_COUNT names the counter file (created on first use).
# STUB_AGENT_LOG, when set, receives the child's cwd, its argument count, and
# one line per argument (newlines flattened), so a case can count spawns.
set -euo pipefail

mode="${STUB_REPAIR_MODE:-fix-on-second}"
count_file="${STUB_REPAIR_COUNT:-/dev/null}"

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

call=1
if [ "$count_file" != "/dev/null" ]; then
  if [ -f "$count_file" ]; then
    call="$(cat "$count_file")"
    case "$call" in ''|*[!0-9]*) call=0 ;; esac
    call=$((call + 1))
  fi
  printf '%s\n' "$call" >"$count_file"
fi

# The directive ends with: Write <artifact> with {...}. Take the last argument
# (the directive), then cut the artifact path out of that sentence: no side
# channel, so a directive that stopped naming its artifact fails here.
directive="${!#}"
rest="${directive#*Write }"
artifact="${rest%% with \{*}"
if [ -z "$artifact" ] || [ "$artifact" = "$directive" ]; then
  echo "repair-stub: the directive named no review path to write" >&2
  exit 2
fi
mkdir -p "$(dirname "$artifact")"

case "$mode" in
  fix-on-second)
    if [ "$call" -eq 1 ]; then
      printf 'not json first attempt' >"$artifact"
      exit 0
    fi
    ;;
  always-invalid)
    printf 'not json attempt %s' "$call" >"$artifact"
    exit 0
    ;;
  valid) ;;
  *)
    echo "repair-stub: unknown STUB_REPAIR_MODE '$mode'" >&2
    exit 2
    ;;
esac

cat >"$artifact" <<'JSON'
{"schema_version":"1","body":"> ✅ Reviewed the change; one inline note.","approved":false,
 "comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Nit: rename this line.","severity":"informational","rigor":"walked"}]}
JSON
