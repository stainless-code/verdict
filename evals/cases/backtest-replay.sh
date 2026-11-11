#!/usr/bin/env bash
#
# backtest replay: one mined candidate reviewed by a stubbed agent at its own
# head, priced, recorded, and resumed — no model, no network, no host CLI.
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

# The same hermetic git the mine case pins, so the fixture ids are the Go
# golden fixture's rather than the machine's.
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

# The agent stub lives inside .verdict, which the store's own .gitignore
# already excludes, so it never shows up in the bundle's untracked warning.
mkdir -p "$EVAL_SANDBOX/.verdict/agentbin"
export EVAL_AGENT_LOG="$EVAL_SANDBOX/agent.log"
cat >"$EVAL_SANDBOX/.verdict/agentbin/claude" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
directive="${!#}"
rest="${directive#*Write }"
artifact="${rest%% with \{*}"
printf '%s\n' "$PWD" >>"$EVAL_AGENT_LOG"
mkdir -p "$(dirname "$artifact")"
# One inline finding on the historical line, with the Basis line the record
# pass computes, so the recorded review warns about nothing.
cat >"$artifact" <<'JSON'
{"schema_version":"1",
 "body":"> Basis: 0 0 1 0 0 0 0\n\n### One note\n\nThis line deserves a second look.",
 "approved":false,
 "comments":[{"path":"main.go","line":6,"side":"RIGHT",
              "body":"Nit: the removal can fail here.","severity":"informational","rigor":"walked"}]}
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
assert_json_field "candidate count" "1" '.data.mine.candidates | length'

EVAL_AGENT_PRICE=0.6 run_review backtest replay --run rel --agent claude --json
assert_exit "replay exits 0" 0
assert_envelope "replay envelope"
assert_json_field "verb" "backtest" '.verb'
assert_json_field "mode" "replay" '.data.mode'
assert_json_field "run id" "rel" '.data.run'
assert_json_field "agent" "claude" '.data.agent'
assert_json_field "max reviews" "10" '.data.budgets.max_reviews'
assert_json_field "budget cap" "null" '.data.budgets.budget_usd'
assert_json_field "timeout" "300" '.data.budgets.timeout_s'
assert_json_field "spent" "0.6" '.data.budgets.spent_usd'
assert_json_field "unpriced" "0" '.data.budgets.unpriced_reviews'
assert_json_field "stop reason" "complete" '.data.stop_reason'
assert_json_field "row count" "1" '.data.prs | length'
assert_json_field "row n" "1" '.data.prs[0].n'
assert_json_field "row pr" "2" '.data.prs[0].pr'
assert_json_field "row status" "ok" '.data.prs[0].status'
assert_json_field "row bundle" "local-bt-rel-1" '.data.prs[0].bundle'
assert_json_field "row cost" "0.6" '.data.prs[0].cost_usd'

BUNDLE="$EVAL_SANDBOX/.verdict/state/local-bt-rel-1"
# The review was recorded against the historical diff, which is the PR's own
# change: the fix that landed after it is not in the bundle.
assert_file_grep "findings recorded" "$BUNDLE/findings.json" '"path": "main.go"'
assert_file_grep "basis line rewritten" "$BUNDLE/review.json" 'Basis: 0 0 1 0 0 0 0'
assert_file_nogrep "historical diff, not today's fix" "$BUNDLE/full.diff" 'panic(err)'
assert_file_grep "marker mode" "$BUNDLE/backtest.json" '"mode": "replay"'
assert_file_grep "resume record" "$EVAL_SANDBOX/.verdict/backtest/rel/prs/001/replay.json" '"status": "ok"'
# The agent ran in a detached checkout of the historical head, and the
# checkout is gone again.
assert_file_grep "agent ran in the checkout" "$EVAL_AGENT_LOG" '.worktree-bt-rel-1$'
if [ -e "$EVAL_SANDBOX/.verdict/state/.worktree-bt-rel-1" ]; then
  bad "the checkout was removed after the review"
else
  good "the checkout was removed after the review"
fi
worktrees="$(git -C "$EVAL_SANDBOX" worktree list --porcelain)"
if matches 'worktree .*\.worktree-bt-' <<<"$worktrees"; then
  bad "git kept no replay worktree"
else
  good "git kept no replay worktree"
fi

agent_runs="$(wc -l <"$EVAL_AGENT_LOG" | tr -d ' ')"
EVAL_AGENT_PRICE=0.6 run_review backtest replay --run rel --agent claude --json
assert_exit "rerun exits 0" 0
assert_json_field "rerun spent" "0.6" '.data.budgets.spent_usd'
assert_eq "resume launched no agent" "$agent_runs" "$(wc -l <"$EVAL_AGENT_LOG" | tr -d ' ')"

EVAL_AGENT_PRICE=0.6 run_review backtest replay --run rel --agent codex --json
assert_exit "another agent refuses" 1
assert_refusal "another agent" "run-mismatch"

cat >"$EVAL_SANDBOX/expected-human.txt" <<EOF
backtest replay rel  (agent claude, max 10 reviews, no budget, timeout 300s)
  1  #2  local-bt-rel-1  ok  \$0.60
stopped: complete
spend: \$0.60 over 1 review
not citable — SZZ-derived: a replay grades a blame-derived guess, not a verified defect
EOF
EVAL_AGENT_PRICE=0.6 run_review backtest replay --run rel --agent claude
assert_exit "human mode exits 0" 0
assert_file_eq "human output matches the fixed fixture" "$EVAL_SANDBOX/expected-human.txt" "$EVAL_OUT"

# An agent that reports no price is unknown spend on the wire, never zero.
run_review backtest mine --since "$BASE" --run rel2 --json
assert_exit "second mine exits 0" 0
run_review backtest replay --run rel2 --agent claude --json
assert_exit "unpriced replay exits 0" 0
assert_json_field "unpriced row cost" "null" '.data.prs[0].cost_usd'
assert_json_field "unpriced total" "null" '.data.budgets.spent_usd'
assert_json_field "unpriced count" "1" '.data.budgets.unpriced_reviews'
assert_file_grep "unknown spend footer" "$EVAL_ERR" 'spend: unknown over 1 review (all unpriced)'

run_review backtest replay --run missing --agent claude --json
assert_exit "missing run refuses" 1
assert_refusal "missing run" "missing-bundle"

stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
assert_eq "zero host CLI calls" "$stub_before" "$stub_after"

eval_end
