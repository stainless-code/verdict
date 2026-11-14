#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

BODY='> ✅ Clean review summary with enough length to satisfy record validation.'

# 1) Unassessed bundle: never claims low, stays unknown under complete coverage,
# and fails closed under --max-risk.
eval_mk_bundle local-unassessed
eval_write_review local-unassessed <<EOF
{
  "schema_version": "1",
  "body": "$BODY",
  "approved": true,
  "coverage": [{"file": "notes.txt", "ranges": "all"}],
  "comments": []
}
EOF
run_review record acme/app local-unassessed --json
assert_exit "record without risk succeeds" 0
run_review report acme/app local-unassessed --risk --json
assert_exit "report --risk succeeds on unassessed bundle" 0
assert_json_field "unassessed claimed is unknown" "unknown" '.data.risk.claimed'
assert_json_field "unassessed floor is low" "low" '.data.risk.floor'
assert_json_field "unassessed reported stays unknown" "unknown" '.data.risk.reported'
run_review report acme/app local-unassessed --max-risk critical --json
assert_exit "--max-risk fails closed on unknown risk" 1
assert_json_field "unknown risk gate reason" "risk-unknown" '.data.risk.gate_reason'

# 2) Valid low-risk claim + complete coverage passes --max-risk low.
eval_mk_bundle local-low
eval_write_review local-low <<EOF
{
  "schema_version": "1",
  "body": "$BODY",
  "approved": true,
  "coverage": [{"file": "notes.txt", "ranges": "all"}],
  "risk": {
    "level": "low",
    "rationale": "Single-line note update with complete file coverage.",
    "assessed_head": "$EVAL_FIXTURE_HEAD"
  },
  "comments": []
}
EOF
run_review record acme/app local-low --json
assert_exit "record with valid risk succeeds" 0
assert_json_field "record response carries risk level" "low" '.data.risk.level'
run_review report acme/app local-low --risk --json
assert_exit "report --risk succeeds on low bundle" 0
assert_json_field "low claimed" "low" '.data.risk.claimed'
assert_json_field "low floor" "low" '.data.risk.floor'
assert_json_field "low reported" "low" '.data.risk.reported'
run_review report acme/app local-low --max-risk low --json
assert_exit "--max-risk low passes when reported=low" 0
assert_json_field "low max-risk exceeded is false" "false" '.data.risk.exceeded'

# 3) Optimistic low claim overridden by open critical finding -> critical, exits 1 under --max-risk high.
eval_mk_bundle local-overridden
eval_write_review local-overridden <<EOF
{
  "schema_version": "1",
  "body": "> [!IMPORTANT]\n> Review surfaced one critical bug in notes handling.",
  "approved": false,
  "coverage": [{"file": "notes.txt", "ranges": "all"}],
  "risk": {
    "level": "low",
    "rationale": "Optimistic initial estimate before reviewing findings.",
    "assessed_head": "$EVAL_FIXTURE_HEAD"
  },
  "comments": [
    {
      "path": "notes.txt",
      "line": 2,
      "side": "RIGHT",
      "body": "Critical logic error in changed line requiring immediate fix.",
      "rigor": "walked",
      "severity": "critical"
    }
  ]
}
EOF
run_review record acme/app local-overridden --json
assert_exit "record with overridden risk succeeds" 0
run_review report acme/app local-overridden --risk --json
assert_exit "report --risk succeeds on overridden bundle" 0
assert_json_field "overridden keeps claimed low" "low" '.data.risk.claimed'
assert_json_field "overridden raises floor to critical" "critical" '.data.risk.floor'
assert_json_field "overridden reports critical" "critical" '.data.risk.reported'
assert_json_field "overridden counts open_critical=1" "1" '.data.risk.inputs.open_critical'
run_review report acme/app local-overridden --max-risk high --json
assert_exit "--max-risk high fails on critical floor" 1
assert_json_field "exceeded gate reason" "risk-exceeded" '.data.risk.gate_reason'

# 4) Usage error (exit 2) for invalid --max-risk and tool error (exit 3) for missing store.
run_review report acme/app local-low --max-risk extreme
assert_exit "invalid --max-risk returns exit 2" 2
run_review report acme/app local-does-not-exist --max-risk low
assert_exit "missing bundle with --max-risk returns exit 3" 3

# 5) Trend groups all three bundles into their recomputed risk bands.
run_review trend acme/app --json
assert_exit "trend --json succeeds" 0
assert_json_field "trend low band has 1 bundle" "1" '.data.by_risk.low.bundles'
assert_json_field "trend critical band has 1 bundle" "1" '.data.by_risk.critical.bundles'
assert_json_field "trend critical band tracks open=1" "1" '.data.by_risk.critical.open'
assert_json_field "trend unknown band has 1 bundle" "1" '.data.by_risk.unknown.bundles'

# 6) The score on a bundle whose impact.json a real index answered. The index
# leg builds the store from the same fixture commit, so reach is the producer's
# own symbol and caller counts rather than a hand-written artifact.
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q -b main
  git config user.email "risk-score@example.com"
  git config user.name "risk score"
  printf 'module example.com/m\n\ngo 1.27\n' >go.mod
  mkdir -p src app
  printf 'package src\n\nfunc Save() {}\n' >src/save.go
  printf 'package app\n\nimport "example.com/m/src"\n\nfunc Use() {\n\tsrc.Save()\n}\n' >app/use.go
  grep -qxF '.verdict/state/' .gitignore 2>/dev/null || echo ".verdict/state/" >>.gitignore
  git add -- .gitignore go.mod src app
  GIT_AUTHOR_DATE=2024-05-01T00:00:00Z GIT_COMMITTER_DATE=2024-05-01T00:00:00Z \
    git commit -qm 'base'
  printf 'package src\n\nfunc Save() { _ = 1 }\n' >src/save.go
)
run_review index build --json
assert_exit "index build for the scored bundle exits 0" 0
run_review fetch-local acme/app impact-scored --index HEAD --json
assert_exit "fetch-local --index exits 0" 0
assert_json_field "the scored bundle carries impact.json" \
  ".verdict/state/local-impact-scored/impact.json" '.data.artifacts["impact.json"]'
eval_write_review local-impact-scored <<EOF
{
  "schema_version": "1",
  "body": "> [!IMPORTANT]\n> The changed line drops the saved value.",
  "approved": false,
  "coverage": [{"file": "src/save.go", "ranges": "all"}],
  "comments": [
    {
      "path": "src/save.go",
      "line": 3,
      "side": "RIGHT",
      "body": "The assignment discards the saved value on every call.",
      "rigor": "walked",
      "severity": "critical",
      "category": "security"
    }
  ]
}
EOF
run_review record acme/app local-impact-scored --json
assert_exit "record the scored bundle exits 0" 0
run_review report acme/app local-impact-scored --risk --json
assert_exit "report --risk on the scored bundle exits 0" 0
assert_json_field "score value is the published integer formula" "37" '.data.risk.score.value'
assert_json_field "all four components measured" "4" '.data.risk.score.measured'
assert_json_field "reach counts the changed declaration and its caller" "2" \
  '.data.risk.score.reach.symbols + .data.risk.score.reach.callers'
assert_json_field "reach points at the 1-3 bucket" "25" '.data.risk.score.reach.points'
assert_json_field "reach weight" "35" '.data.risk.score.reach.weight'
assert_json_field "reach measured" "true" '.data.risk.score.reach.measured'
assert_json_field "churn counts the replaced line" "1" '.data.risk.score.churn.additions'
assert_json_field "churn counts the removed line" "1" '.data.risk.score.churn.deletions'
assert_json_field "churn points for two changed lines" "0" '.data.risk.score.churn.points'
assert_json_field "churn weight" "25" '.data.risk.score.churn.weight'
assert_json_field "category picks the open security finding" "security" '.data.risk.score.category.worst'
assert_json_field "category points" "100" '.data.risk.score.category.points'
assert_json_field "category weight" "25" '.data.risk.score.category.weight'
assert_json_field "category counts only open findings" "1" '.data.risk.score.category.open'
assert_json_field "rung is the walked finding" "walked" '.data.risk.score.rung.weakest'
assert_json_field "rung points" "25" '.data.risk.score.rung.points'
assert_json_field "rung weight" "15" '.data.risk.score.rung.weight'
assert_json_field "rung measured" "true" '.data.risk.score.rung.measured'
run_review report acme/app local-impact-scored --risk
assert_exit "human report --risk on the scored bundle exits 0" 0
assert_file_grep "human mode prints the score after the floor inputs" "$EVAL_OUT" \
  '^risk score: 37/100 (reach 25w35 churn 0w25 category 100w25 rung 25w15, measured 4/4)$'
run_review report acme/app local-impact-scored --json
assert_exit "report without --risk exits 0" 0
assert_json_field "report without --risk carries no risk block" "false" '.data | has("risk")'
run_review report acme/app local-impact-scored --max-risk critical --json
assert_exit "--max-risk still gates on the reported band" 0
assert_json_field "--max-risk publishes no score" "false" '.data.risk | has("score")'

# 7) No impact.json: reach reads unmeasured at 0 points and the other three
# components stay measured, so an un-indexed bundle never looks like reach 0.
run_review report acme/app local-unassessed --risk --json
assert_exit "report --risk without impact.json exits 0" 0
assert_json_field "no impact.json leaves reach unmeasured" "false" '.data.risk.score.reach.measured'
assert_json_field "unmeasured reach scores zero points" "0" '.data.risk.score.reach.points'
assert_json_field "unmeasured reach publishes no symbol count" "null" '.data.risk.score.reach.symbols'
assert_json_field "churn, category, and rung stay measured" "3" '.data.risk.score.measured'

eval_end
