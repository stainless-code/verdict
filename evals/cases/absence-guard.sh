#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

CASES="$EVAL_ROOT/evals/cases"
COMMON="$EVAL_ROOT/evals/lib/common.sh"

bare_neg="$(grep -rn '! grep' "$CASES"/*.sh | grep -v "/$(basename "${0%.sh}").sh:" || true)"
if [ -z "$bare_neg" ]; then
  good "no case negates grep directly (absence goes through the guarded helpers)"
else
  bad "bare negated grep(s); use assert_file_nogrep or assert_stream_nogrep:
$bare_neg"
fi

inverted="$(awk 'FNR == 1 { prev = "" }
  /^[[:space:]]*if grep / { pending = FILENAME ":" FNR ": " $0; next }
  { if (pending != "" && $0 ~ /^[[:space:]]*bad /) { print pending; pending = "" } else if ($0 !~ /^[[:space:]]*$/) { pending = "" } }' \
  "$CASES"/*.sh || true)"
if [ -z "$inverted" ]; then
  good "no case states absence as an inverted positive grep"
else
  bad "inverted absence grep(s); use assert_file_nogrep or assert_stream_nogrep:
$inverted"
fi

private_skip="$(grep -rn -E '(^|[[:space:]])(skips=[0-9]|skip\(\))' "$CASES"/*.sh || true)"
if [ -z "$private_skip" ]; then
  good "no case keeps a private skip tally (skipped legs reach the runner as SKIP)"
else
  bad "private skip tally in a case; use the shared skip/skip_case helpers:
$private_skip"
fi

defined="$(grep -oE '^[a-z_]+\(\)' "$COMMON" | sed 's/()$//')"
called="$(grep -rhoE '\bassert_[a-z_]+' "$CASES"/*.sh | sort -u)"
unknown=""
for fn in $called; do
  if printf '%s\n' "$defined" | matches -x "$fn"; then continue; fi
  unknown="$unknown $fn"
done
if [ -z "$unknown" ]; then
  good "every assert_* helper a case calls is defined in evals/lib/common.sh"
else
  bad "case(s) call undefined helper(s):$unknown"
fi

eval_end
