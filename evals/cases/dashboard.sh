#!/usr/bin/env bash
# `verdict dashboard`'s headless contract: --once draws one plain-text frame
# from a real bundle (the Overview by default, any tab with --tab; exit 0,
# 80x24 off a terminal, no escape byte), [repo] narrows the portfolio, the
# live view refuses without a terminal (dashboard-no-tty, as an envelope
# under --json), --once has no --json form, and no store moves.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

git -C "$EVAL_SANDBOX" init -q
eval_mk_bundle 4232
# One open critical finding and one addressed one: the totals the frame shows.
jq -cn '[range(2) as $i | {
  kind: "inline", path: "notes.txt", side: "RIGHT", line: ($i + 1), anchored: true,
  body: "A finding body with enough substance to stand.", suggestion: null,
  inBounds: true, rigor: "walked", severity: "critical", sig: ("c\($i)" + ("1" * 39)), sig_version: "1",
  firstSeen: "aaaaaaa", lastSeen: "aaaaaaa", status: (if $i == 0 then "open" else "addressed" end),
  rationale: "", history: []}]' | eval_write_findings 4232
state_sum() {
  (cd "$EVAL_SANDBOX/.verdict" && find . -type f -print0 | LC_ALL=C sort -z | xargs -0 cat) |
    { sha256sum 2>/dev/null || shasum -a 256; } | cut -d' ' -f1
}
BEFORE="$(state_sum)"

run_review dashboard --once
assert_exit "--once frame" 0
assert_eq "--once frame is 80x24" 24 "$(wc -l <"$EVAL_OUT" | tr -d ' ')"
if LC_ALL=C grep -q "$(printf '\033')" "$EVAL_OUT"; then
  bad "--once frame carries an escape byte"
else
  good "--once frame carries no escape byte"
fi
assert_eq "tab bar" " [1 Overview]  2 Queue   3 Detail   4 Diff   5 Compare   6 Cost   7 Precision" "$(sed -n 2p "$EVAL_OUT")"
assert_eq "portfolio totals" "   bundles 1   open 1   deferred 0   addressed 1   terminal 0" "$(sed -n 5p "$EVAL_OUT")"
if sed -n 8p "$EVAL_OUT" | matches -E '^   [0-9a-f]{12}  .* 1$'; then
  good "the served root lists its one bundle"
else
  bad "roots row is '$(sed -n 8p "$EVAL_OUT")'"
fi

run_review dashboard --once --tab detail
assert_exit "--once --tab detail" 0
assert_eq "detail tab bar" "  1 Overview   2 Queue  [3 Detail]  4 Diff   5 Compare   6 Cost   7 Precision" "$(sed -n 2p "$EVAL_OUT")"
assert_eq "detail verdict counts" "   open 1   deferred 0   addressed 1   terminal 0" "$(sed -n 5p "$EVAL_OUT")"
if matches -E '^   !!! open +[^ ]+ +notes.txt:1 ' <"$EVAL_OUT"; then
  good "detail lists the open critical finding"
else
  bad "detail rows: $(sed -n 8,9p "$EVAL_OUT")"
fi

run_review dashboard acme/elsewhere --once
assert_exit "[repo] filter" 0
assert_eq "a repo with no bundles totals zero" "   bundles 0   open 0   deferred 0   addressed 0   terminal 0" "$(sed -n 5p "$EVAL_OUT")"

run_review dashboard --once --tab inbox
assert_exit "unknown --tab" 2

run_review dashboard
assert_exit "live view without a terminal" 1
assert_eq "no-tty stderr" "error: dashboard: stdin and stdout must both be a terminal
try: run verdict dashboard in a terminal, or pass --once for a single plain-text frame" "$(cat "$EVAL_ERR")"
assert_eq "no-tty stdout" "" "$(cat "$EVAL_OUT")"

run_review dashboard --json
assert_exit "live view without a terminal under --json" 1
assert_refusal "no-tty envelope" dashboard-no-tty

run_review dashboard --once --json
assert_exit "--once has no --json form" 2

assert_eq "review stores unchanged" "$BEFORE" "$(state_sum)"
eval_end
