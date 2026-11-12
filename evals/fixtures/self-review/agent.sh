#!/usr/bin/env bash
set -euo pipefail
prompt="${!#}"
rest="${prompt#*Write }"
artifact="${rest%% with \{*}"
printf '%s\n' "$0" >>"$LOOP_LOG"
comments='[]'
if grep -q 'new line' notes.txt; then
  comments='[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Incorrect line.","severity":"important","rigor":"walked"}]'
fi
printf '{"schema_version":"1","body":"Reviewed the complete notes change.","approved":false,"comments":%s,"coverage":[{"file":"notes.txt","ranges":"1-3"}]}\n' "$comments" >"$artifact"
if [ "$LOOP_MODE" = fix ]; then
  printf 'top\nfixed line\nbottom\n' >notes.txt
fi
