#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

SLUG="local-dryrun"
LB="$EVAL_SANDBOX/.review/$SLUG"

mk_local() { # stdin becomes review.json
  unset EVAL_NO_SEED
  rm -rf "$LB"
  mkdir -p "$LB"
  cat > "$LB/source.json" <<'JSON'
{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}
JSON
  cat > "$LB/full.diff" <<'DIFF'
diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 top
-old line
+new line
 bottom
DIFF
  cat > "$LB/review.json"
  eval_seed_controls
  export EVAL_NO_SEED=1
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
  for artifact in findings.json record.json store.json hunks.tsv; do
    if [ -e "$LB/$artifact" ]; then
      bad "$label created $artifact"
    fi
  done
}

mk_local <<'JSON'
{"schema_version":"1","body":"> ✅ Dry run with a bad brief.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"A finding with enough substance.","rigor":"walked","brief":{"outcome":"Reject duplicate deliveries before anything is enqueued.","questions":"why"}}]}
JSON
BEFORE="$(snapshot)"
run_review record acme/app "$SLUG" --dry-run --json
assert_exit "brief.questions string refuses the dry run" 1
assert_refusal "brief.questions string" "bad-review-input"
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

unset EVAL_NO_SEED
rm -rf "$LB"
eval_end
