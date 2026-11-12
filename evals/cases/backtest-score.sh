#!/usr/bin/env bash
#
# backtest score: the pure read over a replay's bundles, and the composed
# mine -> replay -> score run — no model, no network, no host CLI.
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

# The same hermetic git the mine and replay cases pin, so the fixture ids are
# the Go golden fixture's rather than the machine's.
export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0=commit.gpgsign
export GIT_CONFIG_VALUE_0=false
export GIT_AUTHOR_NAME=verdict-test
export GIT_AUTHOR_EMAIL=test@verdict.invalid
export GIT_COMMITTER_NAME=verdict-test
export GIT_COMMITTER_EMAIL=test@verdict.invalid
export HOME="$EVAL_SANDBOX/home"
mkdir -p "$HOME"

dated() {
  local date="$1"
  shift
  GIT_AUTHOR_DATE="$date" GIT_COMMITTER_DATE="$date" git "$@"
}

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q -b main
  printf 'package main\n\nimport "os"\n\nfunc main() {\n\tos.Remove("out.txt")\n}\n' >main.go
  dated 2024-01-01T00:00:00Z add main.go
  dated 2024-01-01T00:00:00Z commit -qm 'feat: base'
  git switch -qc feature
  printf 'package main\n\nimport "os"\n\nfunc main() {\n\tos.Remove("missing")\n}\n' >main.go
  dated 2024-01-02T00:00:00Z add main.go
  dated 2024-01-02T00:00:00Z commit -qm 'feat: remove the output'
  git switch -q main
  dated 2024-01-03T00:00:00Z merge --no-ff feature -m 'Merge pull request #2 from acme/feature' >/dev/null
  printf 'package main\n\nimport "os"\n\nfunc main() {\n\tif err := os.Remove("missing"); err != nil {\n\t\tpanic(err)\n\t}\n}\n' >main.go
  dated 2024-01-04T00:00:00Z add main.go
  dated 2024-01-04T00:00:00Z commit -qm 'fix: guard the removal'
)

BASE=84b31730499aa237889660981350fda2b3edc91d

# The agent stub names the missing error check on the bug line, so the score
# has a true positive to measure. It lives inside .verdict, which the store's
# own .gitignore already excludes.
mkdir -p "$EVAL_SANDBOX/.verdict/agentbin"
cat >"$EVAL_SANDBOX/.verdict/agentbin/claude" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
directive="${!#}"
rest="${directive#*Write }"
artifact="${rest%% with \{*}"
mkdir -p "$(dirname "$artifact")"
cat >"$artifact" <<'JSON'
{"schema_version":"1",
 "body":"> Basis: 0 0 1 0 0 0 0",
 "approved":false,
 "comments":[{"path":"main.go","line":6,"side":"RIGHT",
              "body":"The removal is unchecked; the error is dropped.","severity":"important","rigor":"walked"}]}
JSON
if [ -n "${EVAL_AGENT_PRICE:-}" ]; then
  printf '{"schema_version":"2","est_usd":%s,"models":[{"name":"stub","input_tokens":10,"output_tokens":5}]}\n' \
    "$EVAL_AGENT_PRICE" >"$(dirname "$artifact")/cost.json"
fi
STUB
chmod +x "$EVAL_SANDBOX/.verdict/agentbin/claude"
export PATH="$EVAL_SANDBOX/.verdict/agentbin:$PATH"

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

run_review backtest mine --since "$BASE" --run rel --json
assert_exit "mine exits 0" 0
EVAL_AGENT_PRICE=0.6 run_review backtest replay --run rel --agent claude --json
assert_exit "replay exits 0" 0

run_review backtest score --run rel --json
assert_exit "score exits 0" 0
assert_envelope "score envelope"
assert_json_field "verb" "backtest" '.verb'
assert_json_field "mode" "score" '.data.mode'
assert_json_field "run id" "rel" '.data.run'
assert_json_field "prs" "1" '.data.prs'
assert_json_field "not replayed" "0" '.data.not_replayed'
assert_json_field "bugs" "1" '.data.bugs'
assert_json_field "reported" "1" '.data.reported'
assert_json_field "matched" "1" '.data.matched'
assert_json_field "unclassified" "0" '.data.unclassified'
assert_json_field "catch rate" "1" '.data.catch_rate'
assert_json_field "off-target per PR" "0" '.data.off_target_per_pr'
assert_json_field "cost per review" "0.6" '.data.cost_per_review_usd'
assert_json_field "spend" "0.6" '.data.spend.usd'
assert_json_field "priced reviews" "1" '.data.spend.priced_reviews'
assert_json_field "unpriced reviews" "0" '.data.spend.unpriced_reviews'
assert_json_field "signal rows" "2" '.data.signals | length'
assert_json_field "fix signal" "fix" '.data.signals[0].signal'
assert_json_field "fix bugs" "1" '.data.signals[0].bugs'
assert_json_field "fix matched" "1" '.data.signals[0].matched'
assert_json_field "fix rate" "1" '.data.signals[0].catch_rate'
assert_json_field "revert signal" "revert" '.data.signals[1].signal'
assert_json_field "unmeasured revert rate" "null" '.data.signals[1].catch_rate'

REPORT="$EVAL_SANDBOX/.verdict/backtest/rel/report.json"
assert_file_grep "report artifact" "$REPORT" '"mode": "score"'
assert_file_grep "report catch rate" "$REPORT" '"catch_rate": 1'
cp "$REPORT" "$EVAL_SANDBOX/report.first.json"
run_review backtest score --run rel --json
assert_exit "rescore exits 0" 0
assert_file_eq "report.json is byte-identical on re-score" "$EVAL_SANDBOX/report.first.json" "$REPORT"

cat >"$EVAL_SANDBOX/expected-human.txt" <<EOF
backtest score rel  (1 PR, 1 bug)
  catch rate: 1.0000 (1/1 bugs)
  off-target: 0.0000 per PR (1 reported, 1 matched, 1 PR)
  cost: \$0.60 per review (\$0.60 over 1 priced review)
  unclassified: 0 of 1 bugs (kind matched no rule)
  signal fix: 1.0000 (1/1)
  signal revert: n/a (0/0)
not citable — SZZ-derived: a score grades a blame-derived guess, not a verified defect
EOF
run_review backtest score --run rel
assert_exit "human mode exits 0" 0
assert_file_eq "human output matches the fixed fixture" "$EVAL_SANDBOX/expected-human.txt" "$EVAL_OUT"

run_review backtest score --json
assert_exit "score without --run refuses" 1
assert_refusal "score without --run" "bad-flags"
assert_file_grep "refusal names --run" "$EVAL_OUT" "--run <id>"

run_review backtest mine --since "$BASE" --run unreplayed --json
assert_exit "unreplayed mine exits 0" 0
run_review backtest score --run unreplayed --json
assert_exit "unreplayed score refuses" 1
assert_refusal "unreplayed score" "missing-bundle"
assert_file_grep "refusal names the replay step" "$EVAL_OUT" "no replayed reviews"

# The composed form runs mine, replay, and score off one window and one agent.
EVAL_AGENT_PRICE=0.6 run_review backtest --since "$BASE" --run all --agent claude --json
assert_exit "composed exits 0" 0
assert_envelope "composed envelope"
assert_json_field "composed verb" "backtest" '.verb'
assert_json_field "composed mode" "score" '.data.mode'
assert_json_field "composed run id" "all" '.data.run'
assert_json_field "composed prs" "1" '.data.prs'
assert_json_field "composed catch rate" "1" '.data.catch_rate'
assert_file_grep "composed report" "$EVAL_SANDBOX/.verdict/backtest/all/report.json" '"run": "all"'

run_review backtest --since "$BASE" --json
assert_exit "composed without --agent refuses" 1
assert_refusal "composed without --agent" "bad-flags"
assert_file_grep "composed refusal names --agent" "$EVAL_OUT" "--agent <name>"

stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
assert_eq "zero host CLI calls" "$stub_before" "$stub_after"

eval_end
