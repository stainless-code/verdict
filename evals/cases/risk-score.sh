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

eval_end
