#!/usr/bin/env bash
#
# backtest fixtures: the offline known-defect scorer, pinned through the binary.
# No model, no network, no host CLI.
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

mkdir -p "$EVAL_SANDBOX/bench"
cp -R "$EVAL_ROOT/bench/quality" "$EVAL_SANDBOX/bench/quality"

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

run_review backtest fixtures
assert_exit "fixtures exits 0" 0

cat >"$EVAL_SANDBOX/expected-human.txt" <<'EOF'
backtest fixtures  (2 fixtures, 7 recorded reviews, 7 scored)
fx1  STA-440 part 2, first build (291c83ae5..98d8becd9)  (6 recordings, 6 scored)
  recall per run: 0.0000 0.0000 0.2000 1.0000 0.2000 0.2000  mean: 0.2667  union: 1.0000 (N=6, 5 known defects)
  bonus union: 2/2  false green: 3/5 (1 unknown gate outcome)
  hallucination: n/a (stubbed; 5 gating findings, 0 checked)  review-insufficient: 0
fx2  STA-351 PR A (d69957f9c..475310826)  (1 recording, 1 scored)
  recall per run: 0.1667  mean: 0.1667  union: 0.1667 (N=1, 6 known defects)
  bonus union: 0/0  false green: 0/1 (0 unknown gate outcomes)
  hallucination: n/a (stubbed; 1 gating findings, 0 checked)  review-insufficient: 0
calibration: 7/7 rows reproduced
known defects from fixture manifests; rates describe recorded reviews, not a model claim
EOF
assert_file_eq "human block" "$EVAL_SANDBOX/expected-human.txt" "$EVAL_OUT"

run_review backtest fixtures --json
assert_exit "fixtures --json exits 0" 0
assert_envelope "fixtures envelope"
assert_json_field "verb" "backtest" '.verb'
assert_json_field "mode" "fixtures" '.data.mode'
assert_json_field "recordings" "7" '.data.recordings'
assert_json_field "scored" "7" '.data.scored'
assert_json_field "calibration rows" "7" '.data.calibration.rows'
assert_json_field "calibration reproduced" "true" '.data.calibration.reproduced'
assert_json_field "fx1 mean" "0.2667" '.data.fixtures[0].recall.mean'
assert_json_field "fx1 union" "1" '.data.fixtures[0].recall.union'
assert_json_field "fx1 false green" "0.6" '.data.fixtures[0].false_green.rate'
assert_json_field "fx1 unknown gate" "1" '.data.fixtures[0].false_green.unknown'
assert_json_field "fx1 bonus union" "2" '.data.fixtures[0].bonus_union.count'
assert_json_field "fx2 recall" "0.1667" '.data.fixtures[1].recall.per_run[0]'
assert_json_field "hallucination stays null" "null" '.data.fixtures[0].hallucination.rate'
assert_json_field "hallucination status" "stubbed" '.data.fixtures[0].hallucination.status'
assert_json_field "review-insufficient" "0" '.data.fixtures[0].review_insufficient'

REPORT="$EVAL_SANDBOX/.verdict/backtest/fixtures/report.json"
if ! jq -e --slurpfile env "$EVAL_OUT" '. == $env[0].data' "$REPORT" >/dev/null 2>&1; then
  bad "report.json is not the envelope data"
else
  good "report.json is the envelope data"
fi
cp "$REPORT" "$EVAL_SANDBOX/report.first.json"
run_review backtest fixtures --json
assert_exit "re-score exits 0" 0
assert_file_eq "report.json is byte-identical on re-score" "$EVAL_SANDBOX/report.first.json" "$REPORT"

run_review backtest fixtures --fixture fx2 --json
assert_exit "filtered fixtures exits 0" 0
assert_json_field "one fixture" "1" '.data.fixtures | length'
assert_json_field "selected id" "fx2" '.data.fixtures[0].id'
assert_json_field "calibration still covers all runs" "7" '.data.calibration.rows'
cp "$REPORT" "$EVAL_SANDBOX/report.filtered.json"

run_review backtest fixtures --fixture fx1-typo --json
assert_refusal "unknown fixture" fixtures-invalid
assert_file_grep "unknown fixture names valid ids" "$EVAL_OUT" "valid ids: fx1, fx2"
assert_file_eq "unknown fixture leaves report untouched" "$EVAL_SANDBOX/report.filtered.json" "$REPORT"

# An infra error makes a recording unscored: it leaves every rate denominator.
jq '.infra_error = "agent failed to start"' \
  "$EVAL_SANDBOX/bench/quality/fx1/recordings/vr-sta440b-haiku.meta.json" \
  >"$EVAL_SANDBOX/infra-meta.json"
cp "$EVAL_SANDBOX/infra-meta.json" \
  "$EVAL_SANDBOX/bench/quality/fx1/recordings/vr-sta440b-haiku.meta.json"
run_review backtest fixtures --json
assert_exit "infra-error run exits 0" 0
assert_json_field "one run unscored" "6" '.data.scored'
assert_json_field "fx1 scored" "5" '.data.fixtures[0].scored'
assert_json_field "fx1 union excludes the run" "0.4" '.data.fixtures[0].recall.union'
assert_json_field "fx1 measured gates" "4" '.data.fixtures[0].false_green.measured'

run_review backtest fixtures --run x --json
assert_exit "--run is a usage error" 2
assert_docs "--run prints no envelope" 0
assert_file_grep "--run names the mode" "$EVAL_ERR" "unknown flag '--run' for backtest fixtures"

stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
assert_eq "zero host CLI calls" "$stub_before" "$stub_after"

eval_end
