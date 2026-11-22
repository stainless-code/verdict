#!/usr/bin/env bash
#
# `report --compare` (STA-483): four buckets over stable finding signatures.
# The case grades the classification rules that matter — a missing finding
# reads resolved only on proven coverage or a provider PR's full diff losing
# the location — plus the human block, the --json shape, and every typed
# baseline refusal.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_BUDGET_USD REVIEW_MODEL || true

BASE_SHA="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
OLD_SHA="cccccccccccccccccccccccccccccccccccccccc"

# compare_diff writes the four-file reviewed diff: seen (attested), kept
# (persisting), excluded (withheld by excluded.json), uncovered (unattested).
# src/removed.ts is absent: the fix reverted it to base.
compare_diff() { # $1 = bundle dir
  local dir="$1" file
  for file in excluded kept seen uncovered; do
    cat >>"$dir/full.diff" <<EOF
diff --git a/src/$file.ts b/src/$file.ts
index 1111111..2222222 100644
--- a/src/$file.ts
+++ b/src/$file.ts
@@ -1,3 +1,3 @@
 a
-b
+B
 c
EOF
  done
}

# baseline_findings is the prior run: one finding per bucket rule, plus a
# pathless section finding that can never prove coverage.
baseline_findings() {
  cat <<EOF
[
 {"kind":"inline","path":"src/kept.ts","side":"RIGHT","line":2,"body":"kept bug","severity":"important","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa01","status":"open"},
 {"kind":"inline","path":"src/seen.ts","side":"RIGHT","line":2,"body":"seen bug","severity":"critical","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02","status":"open"},
 {"kind":"inline","path":"src/removed.ts","side":"RIGHT","line":2,"body":"removed bug","severity":"important","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa03","status":"open"},
 {"kind":"inline","path":"src/excluded.ts","side":"RIGHT","line":2,"body":"excluded bug","severity":"important","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa04","status":"open"},
 {"kind":"inline","path":"src/uncovered.ts","side":"RIGHT","line":2,"body":"uncovered bug","severity":"important","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa05","status":"open"},
 {"kind":"section","heading":"Missing migration note","body":"no location","severity":"informational","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa06","status":"open"}
]
EOF
}

# target_findings is the reported run: the kept bug persists with a severity
# and status move, and one finding is new.
target_findings() {
  cat <<EOF
[
 {"kind":"inline","path":"src/kept.ts","side":"RIGHT","line":2,"body":"kept bug","severity":"critical","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa01","status":"addressed"},
 {"kind":"inline","path":"src/new.ts","side":"RIGHT","line":9,"body":"new bug","severity":"important","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa07","status":"open"}
]
EOF
}

# mkbaseline <name> prepares the prior local bundle.
mkbaseline() { # $1 = local slug
  local dir="$EVAL_SANDBOX/.verdict/state/local-$1"
  eval_mk_bundle "local-$1"
  baseline_findings >"$dir/findings.json"
  jq --arg h "$BASE_SHA" --arg b "$OLD_SHA" '.head_sha = $h | .base_sha = $b' \
    "$dir/source.json" >"$dir/source.json.tmp"
  mv "$dir/source.json.tmp" "$dir/source.json"
  printf '{"schema_version":"1","status":"local","head":"%s","recorded_at":"2026-10-01T00:00:00Z"}\n' \
    "$BASE_SHA" >"$dir/record.json"
}

# mktarget <pr> prepares the reported provider bundle.
mktarget() { # $1 = pr
  local dir="$EVAL_SANDBOX/.verdict/state/$1"
  eval_mk_bundle "$1"
  # A real fetched PR bundle carries its review URL; the comparison folds the
  # URL's project path against the local source.json repo, so this is the pair
  # that must match.
  jq --arg url "https://github.com/acme/app/pull/$1" '.url = $url' \
    "$dir/PR.json" >"$dir/PR.json.tmp"
  mv "$dir/PR.json.tmp" "$dir/PR.json"
  # The producer's hunk snapshot indexes its own fixture diff; the case
  # replaces the diff, so the snapshot goes and the comparison rebuilds it.
  rm -f "$dir/hunks.tsv"
  : >"$dir/full.diff"
  compare_diff "$dir"
  target_findings >"$dir/findings.json"
  printf '{"schema_version":"1","coverage":[{"file":"src/seen.ts","ranges":"1-3"}]}\n' >"$dir/review.json"
  printf '{"schema_version":"1","excluded":[{"path":"src/excluded.ts","rule_id":"builtin:secret","pattern":"*","reason":"default","bytes":120}]}\n' \
    >"$dir/excluded.json"
}

mkbaseline prior
mktarget 7201

# --- 1. human block: four buckets, counts, and reasons ---------------------
run_review report acme/app 7201 --compare local-prior
assert_exit "report --compare exits 0 (advisory, no gate)" 0
assert_file_grep "the summary line carries all four counts" "$EVAL_OUT" \
  '^compare: .verdict/state/local-prior @ bbbbbbb — new=1 persisting=1 resolved=2 not-reviewed=3$'
assert_file_grep "the new bucket is printed" "$EVAL_OUT" '^new (1)$'
assert_file_grep "the persisting bucket is printed" "$EVAL_OUT" '^persisting (1)$'
assert_file_grep "the resolved bucket is printed" "$EVAL_OUT" '^resolved (2)$'
assert_file_grep "the not-reviewed bucket is printed" "$EVAL_OUT" '^not reviewed (3)$'
assert_file_grep "a reverted hunk reads removed-from-diff" "$EVAL_OUT" \
  '^- \[open\] src/removed.ts:2 (aaaaaaaa, important) — removed-from-diff — removed bug$'
assert_file_grep "an attested hunk reads covered-and-absent" "$EVAL_OUT" \
  '^- \[open\] src/seen.ts:2 (aaaaaaaa, critical) — covered-and-absent — seen bug$'
assert_file_grep "an excluded path reads excluded" "$EVAL_OUT" \
  '^- \[open\] src/excluded.ts:2 (aaaaaaaa, important) — excluded — excluded bug$'
assert_file_grep "an unattested hunk reads uncovered-hunk" "$EVAL_OUT" \
  '^- \[open\] src/uncovered.ts:2 (aaaaaaaa, important) — uncovered-hunk — uncovered bug$'
assert_file_grep "a pathless finding reads no-location" "$EVAL_OUT" \
  '^- \[open\] [?] (aaaaaaaa, informational) — no-location — no location$'
assert_file_grep "a persisting finding names both moves" "$EVAL_OUT" \
  'severity: important→critical, status: open→addressed'

# --- 2. --json: counts, sorted arrays, baseline metadata -------------------
run_review report acme/app 7201 --compare local-prior --json
assert_exit "report --compare --json exits 0" 0
assert_envelope "compare envelope"
assert_json_field "counts.new" 1 '.data.compare.counts.new'
assert_json_field "counts.persisting" 1 '.data.compare.counts.persisting'
assert_json_field "counts.resolved" 2 '.data.compare.counts.resolved'
assert_json_field "counts.not_reviewed" 3 '.data.compare.counts.not_reviewed'
assert_json_field "baseline head" "$BASE_SHA" '.data.compare.baseline.head_sha'
assert_json_field "baseline base" "$OLD_SHA" '.data.compare.baseline.base_sha'
assert_json_field "baseline dir" '.verdict/state/local-prior' '.data.compare.baseline.dir'
assert_json_field "baseline name" 'local-prior' '.data.compare.baseline.name'
assert_json_field "baseline recorded_at" '2026-10-01T00:00:00Z' '.data.compare.baseline.recorded_at'
assert_json_field "baseline findings count" 6 '.data.compare.baseline.findings'
assert_json_field "resolved rows are sorted by path" 'removed-from-diff covered-and-absent' \
  '[.data.compare.resolved[].reason] | join(" ")'
assert_json_field "not-reviewed rows are sorted with the pathless first" \
  'no-location excluded uncovered-hunk' '[.data.compare.not_reviewed[].reason] | join(" ")'
assert_json_field "persisting carries the severity move" 'severity: important→critical' \
  '.data.compare.persisting[0].changes[0]'
assert_json_field "persisting carries the status move" 'status: open→addressed' \
  '.data.compare.persisting[0].changes[1]'

# --- 3. without the flag no compare object rides the envelope --------------
run_review report acme/app 7201 --json
assert_exit "plain report --json exits 0" 0
assert_json_field "no compare key without --compare" 'false' '.data | has("compare")'

# --- 4. an incremental delta never resolves --------------------------------
cat >"$EVAL_SANDBOX/.verdict/state/7201/incremental.diff" <<'EOF'
diff --git a/src/kept.ts b/src/kept.ts
index 1111111..2222222 100644
--- a/src/kept.ts
+++ b/src/kept.ts
@@ -1,3 +1,3 @@
 a
-b
+B
 c
EOF
# The delta also touches src/seen.ts, but only at line 100: the file is in the
# delta while the finding's line is not, which must stay not-in-incremental-diff.
cat >>"$EVAL_SANDBOX/.verdict/state/7201/incremental.diff" <<'EOF'
diff --git a/src/seen.ts b/src/seen.ts
--- a/src/seen.ts
+++ b/src/seen.ts
@@ -100,3 +100,3 @@
 a
-b
+B
 c
EOF
run_review report acme/app 7201 --compare local-prior --json
assert_exit "incremental compare exits 0" 0
assert_json_field "an incremental run resolves nothing" 0 '.data.compare.counts.resolved'
assert_json_field "a file outside the delta reads not-in-incremental-diff" 'not-in-incremental-diff' \
  '.data.compare.not_reviewed[] | select(.sig | startswith("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02")) | .reason'
assert_json_field "a reverted file outside the delta reads not-in-incremental-diff" 'not-in-incremental-diff' \
  '.data.compare.not_reviewed[] | select(.sig | startswith("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa03")) | .reason'
rm -f "$EVAL_SANDBOX/.verdict/state/7201/incremental.diff"

# --- 5. a local bundle's narrowed diff never reads removed-from-diff -------
eval_mk_bundle local-head
HEAD_DIR="$EVAL_SANDBOX/.verdict/state/local-head"
rm -f "$HEAD_DIR/hunks.tsv"
: >"$HEAD_DIR/full.diff"
compare_diff "$HEAD_DIR"
target_findings >"$HEAD_DIR/findings.json"
printf '{"schema_version":"1","coverage":[{"file":"src/seen.ts","ranges":"1-3"}]}\n' >"$HEAD_DIR/review.json"
printf '{"schema_version":"1","excluded":[{"path":"src/excluded.ts","rule_id":"builtin:secret","pattern":"*","reason":"default","bytes":120}]}\n' \
  >"$HEAD_DIR/excluded.json"
run_review report acme/app local-head --compare local-prior --json
assert_exit "local compare exits 0" 0
assert_json_field "a local bundle proves only attested coverage" 1 '.data.compare.counts.resolved'
assert_json_field "an absent local file reads partial-review" 'partial-review' \
  '.data.compare.not_reviewed[] | select(.sig | startswith("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa03")) | .reason'

# --- 6. every typed baseline refusal --------------------------------------
mkdir -p "$EVAL_SANDBOX/.verdict/state/local-unrecorded"
printf '{"repo":"acme/app"}\n' >"$EVAL_SANDBOX/.verdict/state/local-unrecorded/source.json"
mkdir -p "$EVAL_SANDBOX/.verdict/state/local-corrupt"
printf '{not json\n' >"$EVAL_SANDBOX/.verdict/state/local-corrupt/findings.json"
ln -sfn "$EVAL_SANDBOX/.verdict/state/local-prior" "$EVAL_SANDBOX/.verdict/state/local-link"
mkdir -p "$EVAL_SANDBOX/.verdict/state/local-other"
printf '{"repo":"other/repo"}\n' >"$EVAL_SANDBOX/.verdict/state/local-other/source.json"
printf '[]\n' >"$EVAL_SANDBOX/.verdict/state/local-other/findings.json"

compare_refusal() { # $1 = label, $2 = baseline arg, $3 = code
  run_review report acme/app 7201 --compare "$2" --json
  assert_exit "$1 refuses" 1
  assert_refusal "$1 keeps the envelope contract" "$3"
}

compare_refusal "a missing identifier" local-absent missing-compare-bundle
compare_refusal "a missing path" no/such/bundle missing-compare-bundle
compare_refusal "a symlinked baseline" local-link unsafe-compare-bundle
compare_refusal "a symlinked baseline with a trailing slash" local-link/ unsafe-compare-bundle
compare_refusal "an unrecorded baseline" local-unrecorded unrecorded-compare-bundle
compare_refusal "a corrupt baseline" local-corrupt corrupt-compare-bundle
compare_refusal "another repository" local-other bad-compare
compare_refusal "the reported bundle itself" 7201 bad-compare

run_review report acme/app 7201 --compare "" --json
assert_exit "an empty --compare value refuses" 1
assert_refusal "an empty --compare value is typed" bad-compare

run_review report acme/app 7201 --compare
assert_exit "--compare with no operand is a usage error" 2
assert_file_grep "the usage error names the operand" "$EVAL_ERR" '(want <bundle-dir|pr|slug>)'

# --- 7. the stub is the only gh, and the case made no host call -----------
assert_eq "the harness stub is the only gh on PATH" "$EVAL_STUBBIN/gh" \
  "$(PATH="$EVAL_STUBBIN:$PATH" command -v gh)"
if [ -s "$EVAL_STUBLOG" ]; then
  bad "report --compare made a host call: $(head -n 3 "$EVAL_STUBLOG" | tr '\n' ' ')"
else
  good "report --compare made zero host calls"
fi

eval_end
