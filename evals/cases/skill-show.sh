#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

run_review skill show prompts/review.md
if [ "$EVAL_STATUS" -eq 0 ] && cmp -s "$EVAL_OUT" "$EVAL_SKILL/prompts/review.md"; then
  good "show prints one payload file byte-identically"
else
  bad "show prompts/review.md exits $EVAL_STATUS or differs from the source"
fi

run_review skill show scripts/verdict
if [ "$EVAL_STATUS" -eq 0 ] && cmp -s "$EVAL_OUT" "$EVAL_SKILL/scripts/verdict"; then
  good "show prints the shim byte-identically"
else
  bad "show scripts/verdict exits $EVAL_STATUS or differs from the source"
fi

run_review skill show
if [ "$EVAL_STATUS" -eq 0 ] && cmp -s "$EVAL_OUT" "$EVAL_SKILL/SKILL.md"; then
  good "bare show still prints SKILL.md"
else
  bad "bare show exits $EVAL_STATUS or differs from SKILL.md"
fi

run_review skill show prompts/review.md --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.path' <"$EVAL_OUT")" = "prompts/review.md" ] \
  && jq -e --rawfile want "$EVAL_SKILL/prompts/review.md" '.data.content == $want' <"$EVAL_OUT" >/dev/null; then
  good "show <path> --json carries the same bytes in one envelope"
else
  bad "show <path> --json exits $EVAL_STATUS or loses the bytes"
fi

for path in "../SKILL.md" "/etc/passwd" "prompts/../SKILL.md" "no-such-file.md"; do
  run_review skill show "$path" --json
  assert_exit "traversal or unknown path refuses ($path)" 1
  assert_refusal "path refusal ($path)" "skill-path-not-inventory"
done
assert_file_grep "the path refusal names skill files" "$EVAL_OUT" "verdict skill files" "-F"

if [ ! -s "$EVAL_STUBLOG" ]; then
  good "show makes zero host calls (offline)"
else
  bad "show called gh"
fi

eval_end
