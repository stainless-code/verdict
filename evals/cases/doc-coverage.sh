#!/usr/bin/env bash
#
#
#   D3  no case is documented twice.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

README="$EVAL_ROOT/evals/README.md"
CASES="$EVAL_ROOT/evals/cases"
SELF="$(basename "${0%.sh}")"

if [ ! -f "$README" ]; then
  bail "evals/README.md is missing"
fi

doc_rows="$(grep -oE '^\| `[a-z0-9-]+`' "$README" | sed -E 's/^\| `//; s/`$//' | LC_ALL=C sort)"
case_names="$(ls "$CASES"/*.sh | sed -E 's#.*/##; s/\.sh$//' | LC_ALL=C sort)"

undocumented=""
for c in $case_names; do
  if ! printf '%s\n' "$doc_rows" | matches -x "$c"; then
    undocumented="$undocumented $c"
  fi
done
if [ -z "$undocumented" ]; then
  good "every case under evals/cases/ has a row in evals/README.md ($(printf '%s\n' "$case_names" | wc -l | tr -d ' ') cases)"
else
  bad "case(s) with no README row:$undocumented"
fi

stale=""
for d in $doc_rows; do
  if ! printf '%s\n' "$case_names" | matches -x "$d"; then
    stale="$stale $d"
  fi
done
if [ -z "$stale" ]; then
  good "every documented case name has an evals/cases/<name>.sh"
else
  bad "README row(s) naming a case that does not exist:$stale"
fi

dupes="$(printf '%s\n' "$doc_rows" | uniq -d | tr '\n' ' ')"
dupes="${dupes% }"
if [ -z "$dupes" ]; then
  good "no case is documented twice"
else
  bad "duplicate README row(s):$dupes"
fi

assert_file_grep "this check's own row is in the table" "$README" "^| \`$SELF\`[[:space:]]*|"

eval_end
