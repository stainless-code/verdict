#!/usr/bin/env bash
set -euo pipefail
prompt="${!#}"
case "$prompt" in
  *" FIRST with {"*)
    rest="${prompt#*Write }"
    artifact="${rest%% FIRST with \{*}"
    ;;
  *" in place:"*)
    rest="${prompt#*Update }"
    artifact="${rest%% in place:*}"
    ;;
  *)
    rest=""
    artifact=""
    ;;
esac
printf '%s\n' "$0" >>"$LOOP_LOG"
if [ "$LOOP_MODE" = empty ]; then
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
if [ "$LOOP_MODE" != stale ] && grep -q 'new line' notes.txt; then
  comments='[{"path":"notes.txt","line":2,"side":"RIGHT","claim":"Incorrect line.","quote":"new line","confidence":"high","severity":"important","rigor":"walked"}]'
  receipts='[]'
fi
printf '{"schema_version":"1","summary":"Reviewed the complete notes change.","findings":%s,"coverage":[{"file":"notes.txt","ranges":"1-3"}],"receipts":%s}\n' "$comments" "$receipts" >"$artifact"
if [ "$LOOP_MODE" = fix ] || [ "$LOOP_MODE" = stale ]; then
  printf 'top\nfixed line\nbottom\n' >notes.txt
fi
