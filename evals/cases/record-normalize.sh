#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

SLUG="local-norm"
LB="$EVAL_SANDBOX/.verdict/state/$SLUG"

mk_local() { # $1 = slug dir; stdin becomes review.json
  local lb
  eval_mk_bundle "$1"
  lb="$EVAL_SANDBOX/.verdict/state/$1"
  eval_write_review_at "$lb/review.json"
}

V1_REVIEW='{"schema_version":"1","body":"> ✅ v1 review is refused with no shim.","approved":false,"comments":[]}'
eval_mk_bundle "$SLUG"
printf '%s' "$V1_REVIEW" >"$LB/review.json"
BEFORE="$(cat "$LB/review.json")"
run_review record acme/app "$SLUG" --dry-run --json
assert_exit "the old shape refuses the dry run" 1
assert_refusal "old shape" "bad-review-input"
assert_json_field "old shape problem code" "unknown-field" '.error.problems[0].code'
assert_json_field "old shape problem path" '$.body' '.error.problems[0].path'
assert_json_field "old shape problem level" "error" '.error.problems[0].level'
if [ "$(cat "$LB/review.json")" = "$BEFORE" ]; then
  good "the dry run never rewrites review.json"
else
  bad "the dry run rewrote review.json"
fi

FINDINGS_REVIEW='{"schema_version":"1","summary":"A clean v2 review.","findings":[{"path":"notes.txt","line":2,"side":"RIGHT","claim":"A finding with enough substance.","quote":"new line","severity":"important","confidence":"high","rigor":"walked","brief":{"outcome":"Reject duplicate deliveries before anything is enqueued.","questions":["Why is this safe?"]}}]}'
printf '%s' "$FINDINGS_REVIEW" | mk_local "$SLUG"
run_review record acme/app "$SLUG" --dry-run --json
assert_exit "a clean findings review passes the dry run" 0
assert_envelope "findings dry run"
assert_json_field "claim is stored as the finding body" "A finding with enough substance." '.data.normalized.findings[0].claim'
assert_json_field "finder confidence keeps its store name" "high" '.data.normalized.findings[0].confidence'
assert_json_field "finding carries the quoted code" "new line" '.data.normalized.findings[0].quote'
assert_json_field "brief questions stay an array" "Why is this safe?" '.data.normalized.findings[0].brief.questions[0]'

run_review record acme/app "$SLUG" --json
assert_exit "a clean findings review records" 0
if [ "$(jq -r '.[0].verification.quote' "$LB/findings.json" 2>/dev/null)" = "unchecked" ]; then
  good "findings.json reserves the quote verification field"
else
  bad "findings.json verification: $(cat "$LB/findings.json" 2>/dev/null || echo MISSING)"
fi
if [ "$(jq -r '.[0].rigor' "$LB/findings.json" 2>/dev/null)" = "walked" ]; then
  good "finding records the walked rung"
else
  bad "finding misses rigor"
fi

eval_mk_bundle local-strictfetch
WARN_REVIEW='{"schema_version":"1","summary":"A warning-only findings review.","findings":[{"path":"notes.txt","line":2,"side":"RIGHT","claim":"A finding with enough substance.","quote":"new line","severity":"important","confidence":"high","category":"nope"}]}'
printf '%s' "$WARN_REVIEW" | eval_write_review_at "$EVAL_SANDBOX/.verdict/state/local-strictfetch/review.json"
run_review record acme/app local-strictfetch --dry-run --json
assert_exit "a bad category only warns" 0
if jq -e '.warnings | join(" ") | contains("nope")' <"$EVAL_OUT" >/dev/null; then
  good "the category warning rides the envelope"
else
  bad "the category warning is missing"
fi
run_review record acme/app local-strictfetch --dry-run --strict --json
assert_exit "--strict promotes the warning" 1
assert_json_field "strict problem level" "error" '.error.problems[0].level'


eval_end
