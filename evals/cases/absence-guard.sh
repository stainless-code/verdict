#!/usr/bin/env bash
# Case absence-guard: the suite's own assertion hygiene, graded statically.
#
# Grades (no CLI, no network — reads the case sources):
#   G1  every case states absence through `assert_file_nogrep` /
#       `assert_stream_nogrep`, never a bare negated grep. `! grep -q X file`
#       returns 2 on a missing file, which `!` turns into a pass, so an
#       unwritten output used to satisfy every "omits X" check (test-audit
#       F5). The helpers assert existence first.
#   G2  the inverse shape is banned too: a positive grep whose `then` branch
#       is `bad` passes for the same wrong reason when the file never landed.
#   G3  no case keeps a private skip tally: a skipped leg must reach the
#       runner as SKIP (exit 77), so the case uses the shared `skip` /
#       `skip_case` vocabulary instead of printing an `ok:` line.
#   G4  every assertion helper a case calls exists in evals/lib/common.sh, so
#       a typo cannot turn an assertion into a silent no-op.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

CASES="$EVAL_ROOT/evals/cases"
COMMON="$EVAL_ROOT/evals/lib/common.sh"

# G1: no bare negated grep in a case (this file excluded: it names the shape).
bare_neg="$(grep -rn '! grep' "$CASES"/*.sh | grep -v "/$(basename "${0%.sh}").sh:" || true)"
if [ -z "$bare_neg" ]; then
  good "no case negates grep directly (absence goes through the guarded helpers)"
else
  bad "bare negated grep(s); use assert_file_nogrep or assert_stream_nogrep:
$bare_neg"
fi

# G2: no inverted negative — a grep whose next line is `bad` passes when the
# target file is missing. awk keeps it one pass over every case.
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

# G3: the skip vocabulary stays shared.
private_skip="$(grep -rn -E '(^|[[:space:]])(skips=[0-9]|skip\(\))' "$CASES"/*.sh || true)"
if [ -z "$private_skip" ]; then
  good "no case keeps a private skip tally (skipped legs reach the runner as SKIP)"
else
  bad "private skip tally in a case; use the shared skip/skip_case helpers:
$private_skip"
fi

# G4: every helper a case calls is defined in common.sh.
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
