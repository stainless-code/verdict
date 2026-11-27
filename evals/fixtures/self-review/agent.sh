#!/usr/bin/env bash
#
# Eval fixture: the agent `verdict loop` spawns, with no model, no network, and
# no host. evals/cases/self-review.sh copies it onto PATH under each agent name
# it grades. It tells the two spawns apart by their directive (the last
# argument): run's review directive names the review path ("Write <path> FIRST
# with {"), and the loop's fix directive names the findings file.
#
# LOOP_MODE picks the behaviour a leg grades:
#   fix    review reports the changed line until it is fixed; the fix rewrites
#          it and declares a verify command in patch.json
#   never  review keeps reporting the line; the fix changes nothing
#   empty  review attests no coverage (record --require-coverage refuses)
#   stale  the working tree changes during the review (coverage-stale): the
#          review runs in its disposable snapshot, so the edit comes from
#          outside it, as an editor saving would, at $LOOP_REPO
#   fail   the review spawn exits 7
#   slow   the review spawn sleeps past a short --timeout
#   cost   like never, plus a priced cost.json beside the review, in the
#          review prompt's own schema-2 shape (undated, total at the root)
#
# LOOP_MEASURED=1 makes every spawn print the json result object a measured
# `claude -p --output-format json` run ends with, carrying its total_cost_usd.
#
# LOOP_LOG receives one "<name> review|fix" line per spawn it can write.
set -euo pipefail
prompt="${!#}"
name="$(basename "$0")"
mode="${LOOP_MODE:-fix}"
# measured prints the CLI's own result object when the loop asked for it.
measured() {
  case " $* " in
    *" --output-format json "*)
      [ "${LOOP_MEASURED:-}" = 1 ] &&
        printf '{"type":"result","subtype":"success","is_error":false,"num_turns":2,"result":"done","session_id":"s","total_cost_usd":0.2}\n'
      ;;
  esac
  return 0
}

case "$prompt" in
  *"Findings file: "*)
    printf '%s fix\n' "$name" >>"$LOOP_LOG"
    patch="${prompt##*patch.json: }"
    if [ "$mode" = fix ]; then
      printf 'top\nfixed line\nbottom\n' >notes.txt
      printf '{"verifyCmd":"grep -q fixed notes.txt"}\n' >"$patch"
    fi
    measured "$@"
    exit 0
    ;;
  *" FIRST with {"*)
    rest="${prompt#*Write }"
    artifact="${rest%% FIRST with \{*}"
    ;;
  *)
    echo "self-review agent: unrecognized directive" >&2
    exit 2
    ;;
esac
# A review agent with no host path its sandbox lets it write (opencode, whose
# data home is ephemeral) cannot log; the leg checks its receipts instead.
printf '%s review\n' "$name" >>"$LOOP_LOG" 2>/dev/null || true
measured "$@"
case "$mode" in
  fail) exit 7 ;;
  slow) sleep 5 ;;
esac
if [ "$mode" = empty ]; then
  printf '{"schema_version":"1","summary":"No review performed on this change.","findings":[],"coverage":[]}\n' >"$artifact"
  exit 0
fi
# The recording verb approves only a review whose every changed hunk carries a
# matched reading receipt, so this fixture attests them the way an agent reading
# the fetched diff does. eval_hunk_receipts is the harness's own hunk-to-quote
# mapping: reusing it keeps the fixture's receipts checkable by construction.
# shellcheck source=../../lib/common.sh
. "$EVAL_ROOT/evals/lib/common.sh"
receipts="$(eval_hunk_receipts "$(dirname "$artifact")")"
comments='[]'
if grep -q 'new line' notes.txt; then
  comments='[{"path":"notes.txt","line":2,"side":"RIGHT","claim":"Incorrect line.","quote":"new line","confidence":"high","severity":"important","rigor":"walked"}]'
  receipts='[]'
fi
printf '{"schema_version":"1","summary":"Reviewed the complete notes change.","findings":%s,"coverage":[{"file":"notes.txt","ranges":"1-3"}],"receipts":%s}\n' "$comments" "$receipts" >"$artifact"
if [ "$mode" = cost ]; then
  printf '{"schema_version":"2","effort":"standard","run":"loop","models":[],"lenses":[],"group_costs":[],"est_usd":0.6}\n' \
    >"$(dirname "$artifact")/cost.json"
fi
if [ "$mode" = stale ]; then
  printf 'top\nedited during review\nbottom\n' >"$LOOP_REPO/notes.txt"
fi
