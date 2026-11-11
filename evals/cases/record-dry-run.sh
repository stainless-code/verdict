#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

SLUG="local-dryrun"
LB="$EVAL_SANDBOX/.verdict/state/$SLUG"

mk_local() { # stdin becomes review.json
  eval_mk_bundle "$SLUG"
  cat > "$LB/review.json"
}

snapshot() {
  (cd "$LB" && find . -type f | LC_ALL=C sort | while IFS= read -r f; do
    printf '%s ' "$f"
    (shasum -a 256 "$f" 2>/dev/null || sha256sum "$f") | cut -d' ' -f1
  done)
}

check_untouched() { # $1 = label, $2 = snapshot before
  local label="$1" before="$2" after
  after="$(snapshot)"
  if [ "$before" = "$after" ]; then
    good "$label left the bundle byte-identical"
  else
    bad "$label changed the bundle"
  fi
  # store.json and hunks.tsv belong to the fetch that produced the bundle;
  # findings.json and record.json are the dry run's to withhold.
  for artifact in findings.json record.json; do
    if [ -e "$LB/$artifact" ]; then
      bad "$label created $artifact"
    fi
  done
}

mk_local <<'JSON'
{"schema_version":"1","body":"> ✅ Dry run with a bad brief.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"A finding with enough substance.","rigor":"walked","brief":{"outcome":"Reject duplicate deliveries before anything is enqueued.","questions":5}}]}
JSON
BEFORE="$(snapshot)"
run_review record acme/app "$SLUG" --dry-run --json
assert_exit "brief.questions number refuses the dry run" 1
assert_refusal "brief.questions number" "bad-review-input"
assert_json_field "one problem reported" "1" '.error.problems | length'
assert_json_field "problem path" '$.comments[0].brief.questions' '.error.problems[0].path'
assert_json_field "problem code" "wrong-type" '.error.problems[0].code'
if [ -n "$(jq -r '.error.problems[0].fix // empty' <"$EVAL_OUT")" ] \
  && [ -n "$(jq -r '.error.problems[0].example // empty' <"$EVAL_OUT")" ]; then
  good "problem carries the exact fix and an example"
else
  bad "problem is missing fix or example"
fi
assert_json_field "no overflow" "0" '.error.problems_truncated'
check_untouched "failed dry run" "$BEFORE"

mk_local <<'JSON'
{"schema_version":"1","body":"> ✅ Three independent problems.","approved":false,"comments":[{"path":7,"line":2,"body":"One.","rigor":"walked"},{"path":"notes.txt","line":2,"rigor":"walked"}],"resolved":[3]}
JSON
BEFORE="$(snapshot)"
run_review record acme/app "$SLUG" --dry-run --json
assert_exit "three errors refuse once" 1
assert_json_field "three problems in one run" "3" '.error.problems | length'
assert_json_field "document order first path" '$.comments[0].path' '.error.problems[0].path'
assert_json_field "document order second path" '$.comments[1].body' '.error.problems[1].path'
assert_json_field "document order third path" '$.resolved[0]' '.error.problems[2].path'
check_untouched "multi-error dry run" "$BEFORE"

mk_local <<'JSON'
{"schema_version":"1","body":"> ✅ Warning-only dry run.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"A finding with enough substance."}]}
JSON
BEFORE="$(snapshot)"
run_review record acme/app "$SLUG" --dry-run --json
assert_exit "warning-only dry run exits 0" 0
assert_envelope "warning-only dry run"
assert_json_field "dry run flag" "true" '.data.dry_run'
assert_json_field "comment count" "1" '.data.check.comments'
assert_json_field "section count" "0" '.data.check.sections'
assert_json_field "moved anchors" "0" '.data.check.anchors_moved_to_body'
if jq -e '.warnings | join(" ") | contains("rigor is unset")' <"$EVAL_OUT" >/dev/null; then
  good "warning rides the envelope"
else
  bad "warning missing from envelope"
fi
check_untouched "clean dry run" "$BEFORE"

run_review record acme/app "$SLUG" --dry-run --strict --json
assert_exit "--strict promotes the warning" 1
assert_json_field "strict problem level" "error" '.error.problems[0].level'
check_untouched "strict dry run" "$BEFORE"

jq '.strict_input = true' "$LB/controls.json" >"$LB/controls.tmp"
mv "$LB/controls.tmp" "$LB/controls.json"
run_review record acme/app "$SLUG" --dry-run --json
assert_exit "controls strict_input promotes the warning" 1
assert_json_field "controls strict problem level" "error" '.error.problems[0].level'
jq 'del(.strict_input)' "$LB/controls.json" >"$LB/controls.tmp"
mv "$LB/controls.tmp" "$LB/controls.json"

mk_local <<'JSON'
{"schema_version":"1","body":"> ✅ Summary counts.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Inline finding with substance.","rigor":"walked"},{"path":"notes.txt","line":99,"side":"RIGHT","body":"Outside hunk finding.","rigor":"walked"}],"sections":[{"heading":"### Security","severity":"important"}]}
JSON
BEFORE="$(snapshot)"
run_review record acme/app "$SLUG" --dry-run --json
assert_exit "clean dry run exits 0" 0
assert_json_field "summary comments" "2" '.data.check.comments'
assert_json_field "summary sections" "1" '.data.check.sections'
assert_json_field "summary moved anchors" "1" '.data.check.anchors_moved_to_body'
assert_json_field "would record both comments plus the moved body section" "3" '.data.findings.open'
check_untouched "summary dry run" "$BEFORE"

# --cost-names repairs attribution without touching a number: the dry run has
# to prove the repair write-free, and the real run has to land the panel's
# names in panel order while every count stays byte-identical.
mk_local <<'JSON'
{"schema_version":"1","body":"> ✅ Cost names.","approved":false,"comments":[]}
JSON
cat >"$LB/cost.json" <<'JSON'
{"schema_version":"1","models":[{"model":"a/one","role":"synthesizer","input_tokens":10,"output_tokens":2},{"model":"a/two","role":"lens","input_tokens":5,"output_tokens":1}]}
JSON
COUNTS="$(jq -c '[.models[] | [.input_tokens, .output_tokens]]' "$LB/cost.json")"
BEFORE="$(snapshot)"
run_review record acme/app "$SLUG" --dry-run --cost-names=lead,lens-1 --json
assert_exit "cost-name dry run exits 0" 0
assert_json_field "dry run reports the names it would write" "1" \
  '[.warnings[] | select(contains("would name 2"))] | length'
check_untouched "cost-name dry run" "$BEFORE"

run_review record acme/app "$SLUG" --cost-names=lead,lens-1 --json
assert_exit "cost-name repair exits 0" 0
assert_eq "panel order names the entries" "lead,lens-1" \
  "$(jq -r '[.models[].name] | join(",")' "$LB/cost.json")"
assert_eq "the repair changes no count" "$COUNTS" \
  "$(jq -c '[.models[] | [.input_tokens, .output_tokens]]' "$LB/cost.json")"

run_review record acme/app "$SLUG" --cost-names=lead,lens-1 --json
assert_exit "a second repair run exits 0" 0
assert_eq "a second repair run is a no-op" "lead,lens-1" \
  "$(jq -r '[.models[].name] | join(",")' "$LB/cost.json")"

# A record with more entries than the panel names cannot be named seat by
# seat, so it refuses and writes nothing.
cat >"$LB/cost.json" <<'JSON'
{"schema_version":"1","models":[{"model":"a/one","input_tokens":10},{"model":"a/two","input_tokens":5}]}
JSON
BEFORE="$(snapshot)"
run_review record acme/app "$SLUG" --cost-names=lead --json
assert_exit "a panel shorter than the record refuses" 1
assert_json_field "the short panel refuses typed" "cost-entry-unnamed" '.error.code'
assert_eq "the refusal wrote nothing" "$BEFORE" "$(snapshot)"

# Each record of an array document is its own set of seats, so a panel that
# matches the total but not each record refuses instead of splitting names
# across records.
cat >"$LB/cost.json" <<'JSON'
[{"schema_version":"1","models":[{"model":"a/one","input_tokens":10}]},{"schema_version":"1","models":[{"model":"a/two","input_tokens":5}]}]
JSON
BEFORE="$(snapshot)"
run_review record acme/app "$SLUG" --cost-names=lead,lens-1 --json
assert_exit "a panel split across records refuses" 1
assert_json_field "the split panel refuses typed" "cost-entry-unnamed" '.error.code'
assert_eq "the split-panel refusal wrote nothing" "$BEFORE" "$(snapshot)"

# A cost record whose models[] entry has no name still records and prices, but
# warns once per offending entry naming the file, the position, and the rule —
# on stderr and in warnings[], in both dry-run and real runs.
mk_local <<'JSON'
{"schema_version":"1","body":"> ✅ Nameless cost entry.","approved":false,"comments":[]}
JSON
cat >"$LB/cost.json" <<'JSON'
{"schema_version":"2","run":"dog","inputs_sha256":"deadbeef","models":[{"model":"claude-x","input_tokens":1200,"output_tokens":300}]}
JSON
BEFORE="$(snapshot)"
run_review record acme/app "$SLUG" --dry-run --json
assert_exit "nameless cost entry dry-runs 0" 0
assert_json_field "dry run warns the nameless entry" "1" \
  '[.warnings[] | select(contains("cost.json: models[0] has no name"))] | length'
if grep -Fq 'cost.json: models[0] has no name' "$EVAL_ERR"; then
  good "dry-run warning reaches stderr"
else
  bad "dry-run warning missing from stderr"
fi
check_untouched "nameless dry run" "$BEFORE"

COST_BEFORE="$(shasum -a 256 "$LB/cost.json" | cut -d' ' -f1)"
run_review record acme/app "$SLUG" --json
assert_exit "nameless cost entry records 0" 0
assert_json_field "the record warns the nameless entry" "1" \
  '[.warnings[] | select(contains("cost.json: models[0] has no name"))] | length'
if grep -Fq 'cost.json: models[0] has no name' "$EVAL_ERR"; then
  good "record warning reaches stderr"
else
  bad "record warning missing from stderr"
fi
if [ -e "$LB/record.json" ]; then
  good "the warned record is still written"
else
  bad "the warned record was not written"
fi
if [ "$(shasum -a 256 "$LB/cost.json" | cut -d' ' -f1)" = "$COST_BEFORE" ]; then
  good "the record never rewrites the cost file"
else
  bad "the record touched the cost file"
fi

run_review report acme/app "$SLUG" --cost --json
assert_exit "the warned record still prices" 0
assert_json_field "warned cost reads in" "1200" '.data.cost.input_tokens'
assert_json_field "warned cost reads out" "300" '.data.cost.output_tokens'

rm -rf "$LB"
eval_end
