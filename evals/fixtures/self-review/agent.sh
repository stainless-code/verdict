#!/usr/bin/env bash
set -euo pipefail
prompt="${!#}"
rest="${prompt#*Write }"
artifact="${rest%% with \{*}"
printf '%s\n' "$0" >>"$LOOP_LOG"
if [ "$LOOP_MODE" = empty ]; then
  printf '{"schema_version":"1","summary":"No review performed on this change.","findings":[],"coverage":[]}\n' >"$artifact"
  exit 0
fi
comments='[]'
if [ "$LOOP_MODE" != stale ] && grep -q 'new line' notes.txt; then
  comments='[{"path":"notes.txt","line":2,"side":"RIGHT","claim":"Incorrect line.","quote":"new line","confidence":"high","severity":"important","rigor":"walked"}]'
fi
printf '{"schema_version":"1","summary":"Reviewed the complete notes change.","findings":%s,"coverage":[{"file":"notes.txt","ranges":"1-3"}]}\n' "$comments" >"$artifact"
if [ "$LOOP_MODE" = fix ] || [ "$LOOP_MODE" = stale ]; then
  printf 'top\nfixed line\nbottom\n' >notes.txt
fi
