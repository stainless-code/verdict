#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

HOLD="$EVAL_SANDBOX/.verdict-hold-bench"
mkdir -p "$EVAL_SANDBOX/.verdict" "$HOLD"
for e in "$EVAL_SANDBOX"/.verdict/state/*; do
  [ -e "$e" ] || continue
  mv "$e" "$HOLD/"
done
restore_bench() {
  rm -rf "$EVAL_SANDBOX/.verdict/state/corpus-"* "$EVAL_SANDBOX/.verdict/state/local-bench"
  for e in "$HOLD"/*; do
    [ -e "$e" ] || continue
    mv "$e" "$EVAL_SANDBOX/.verdict/state/"
  done
  rmdir "$HOLD" 2>/dev/null || true
}
trap restore_bench EXIT

GOLD="$EVAL_ROOT/evals/fixtures/bench-corpus"

mkdir -p "$EVAL_SANDBOX/.verdict/state/9001"
cat > "$EVAL_SANDBOX/.verdict/state/9001/findings.json" <<'FINDINGS_EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Ordinary portfolio finding here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","kb":[],"sig":"9001000000000000000000000000000000000000","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
FINDINGS_EOF
run_review trend acme/bench --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(jq -r 'has("corpus")' <"$EVAL_OUT" 2>/dev/null)" = "false" ]; then
  good "trend without corpus bundles carries no corpus key"
else
  bad "empty-corpus trend: exit $EVAL_STATUS $(jq -c '.data // .error' <"$EVAL_OUT" 2>/dev/null)"
fi
jq -S '.data | del(.generated_at)' <"$EVAL_OUT" >"$EVAL_SANDBOX/baseline-trend.json"

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

schema_bad=0
for case_dir in "$EVAL_ROOT"/bench/corpus/*/; do
  [ -f "$case_dir/case.json" ] || continue
  if ! "$EVAL_SKILL/schemas/validate.sh" "$EVAL_SKILL/schemas/corpus.schema.json" "$case_dir/case.json" >/dev/null 2>&1; then
    bad "corpus.schema.json rejects $case_dir/case.json"
    schema_bad=$((schema_bad + 1))
  fi
done
if [ "$schema_bad" -eq 0 ]; then
  good "every checked-in case.json validates against schemas/corpus.schema.json"
fi

bundles=0
for case_dir in "$EVAL_ROOT"/bench/corpus/*/; do
  [ -d "$case_dir" ] || continue
  case_id="$(basename "$case_dir")"
  [ "$(jq -r .kind "$case_dir/case.json")" != control ] || continue
  for agent in "$case_dir"agents/*.json; do
    [ -f "$agent" ] || continue
    model="$(jq -r '.model' "$agent")"
    slug="corpus-${case_id}-${model}"
    dir="$EVAL_SANDBOX/.verdict/state/$slug"
    # The corpus bundle is a typed capture like any other: the producer
    # pins the controls and run evidence, the case supplies the artifacts.
    eval_mk_bundle "$slug"
    jq --arg r "acme/bench" '.repo = $r' "$dir/source.json" >"$dir/source.json.tmp"
    mv "$dir/source.json.tmp" "$dir/source.json"
    cp "$case_dir/case.json" "$dir/corpus.json"
    cp "$case_dir/full.diff" "$dir/full.diff"
    # The generic producer's hunk cache describes notes.txt, not this corpus patch.
    rm -f "$dir/hunks.tsv"
    jq '.review' "$agent" >"$dir/review.json"
    jq -r '.panel[]' "$agent" >"$dir/panel"
    jq -n --arg m "$model" '{schema_version:"1",effort:"standard",models:[{name:$m,input_tokens:100,output_tokens:50}],est_usd:0.01}' >"$dir/cost.json"
    run_review record acme/bench "$slug" --json
    if [ "$EVAL_STATUS" -eq 0 ] \
      && jq -e '(.data.findings.open + .data.findings.deferred + .data.findings.addressed) >= 1' <"$EVAL_OUT" >/dev/null 2>&1 \
      && [ -f "$dir/findings.json" ]; then
      bundles=$((bundles + 1))
    else
      bad "record $slug: exit $EVAL_STATUS $(jq -c '.error // empty' <"$EVAL_OUT" 2>/dev/null)"
    fi
  done
done
if [ "$bundles" -eq 16 ]; then
  good "16 corpus bundles recorded (8 cases x 2 fake models)"
else
  bad "recorded $bundles corpus bundles, want 16"
fi

before_hash="$(cd "$EVAL_SANDBOX" && find .verdict -type f | LC_ALL=C sort | xargs shasum -a 1)"

actual_cal="$EVAL_SANDBOX/calibrate-actual.jsonl"
: >"$actual_cal"
for dir in "$EVAL_SANDBOX"/.verdict/state/corpus-*/; do
  [ -d "$dir" ] || continue
  slug="$(basename "$dir")"
  run_review calibrate acme/bench "$slug" --json
  if [ "$EVAL_STATUS" -eq 0 ] && jq -e '.data.bundle == $b' --arg b "$slug" <"$EVAL_OUT" >/dev/null 2>&1; then
    jq -cS '.data' <"$EVAL_OUT" >>"$actual_cal"
  else
    bad "calibrate $slug: exit $EVAL_STATUS $(jq -c '.error // .data' <"$EVAL_OUT" 2>/dev/null)"
  fi
done
sort "$actual_cal" -o "$actual_cal"
if diff -u "$GOLD/calibrate.golden.jsonl" "$actual_cal" >"$EVAL_SANDBOX/cal.diff" 2>&1; then
  good "every corpus bundle's calibrate data equals the checked-in golden"
else
  bad "calibrate goldens drifted:"; sed -n '1,20p' "$EVAL_SANDBOX/cal.diff"
fi

run_review trend acme/bench --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  jq -S '.data.corpus' <"$EVAL_OUT" >"$EVAL_SANDBOX/trend-corpus.json"
  if diff -u "$GOLD/trend.golden.json" "$EVAL_SANDBOX/trend-corpus.json" >"$EVAL_SANDBOX/trend.diff" 2>&1; then
    good "trend corpus block equals the checked-in golden (models and panels)"
  else
    bad "trend goldens drifted:"; sed -n '1,30p' "$EVAL_SANDBOX/trend.diff"
  fi
else
  bad "trend with corpus bundles: exit $EVAL_STATUS $(jq -c '.error // empty' <"$EVAL_OUT" 2>/dev/null)"
fi
rate_line="corpus rates: precision=0.7500 recall=0.8333 false-positive-rate=0.2500"
if run_review trend acme/bench && grep -q "$rate_line" <"$EVAL_OUT"; then
  good "human trend prints the exact overall corpus rates"
else
  bad "human trend missing '$rate_line'"
fi

run_review trend acme/bench --json
jq -S '.data | del(.generated_at) | del(.corpus)' <"$EVAL_OUT" >"$EVAL_SANDBOX/after-trend.json"
if diff -u "$EVAL_SANDBOX/baseline-trend.json" "$EVAL_SANDBOX/after-trend.json" >"$EVAL_SANDBOX/add.diff" 2>&1; then
  good "normal rollup is identical with corpus bundles present (additive-only)"
else
  bad "corpus bundles changed the ordinary rollup:"; sed -n '1,20p' "$EVAL_SANDBOX/add.diff"
fi
if jq -e '.data.per_bundle | map(select(.bundle | startswith("corpus-"))) | length == 0' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "no corpus bundle leaks into per_bundle"
else
  bad "corpus bundle leaked into the portfolio rows"
fi

mkdir -p "$EVAL_SANDBOX/.verdict/state/local-bench"
cp "$EVAL_SANDBOX/.verdict/state/corpus-off-by-one-fixture-good/findings.json" "$EVAL_SANDBOX/.verdict/state/local-bench/findings.json"
stub_local_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
run_review calibrate acme/bench local-bench --json
stub_local_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)" = "no-threads" ] \
  && [ "$(jq -r '.data' <"$EVAL_OUT" 2>/dev/null)" = "null" ] \
  && [ "$stub_local_before" = "$stub_local_after" ]; then
  good "local-* still refuses the live join (no-threads, no network)"
else
  bad "local refusal boundary: exit $EVAL_STATUS $(jq -c '.error // empty' <"$EVAL_OUT" 2>/dev/null)"
fi
mkdir -p "$EVAL_SANDBOX/.verdict/state/corpus-broken"
cp "$EVAL_SANDBOX/.verdict/state/corpus-off-by-one-fixture-good/findings.json" "$EVAL_SANDBOX/.verdict/state/corpus-broken/findings.json"
echo '{"schema_version":"1","id":"broken","kind":"seeded"}' >"$EVAL_SANDBOX/.verdict/state/corpus-broken/corpus.json"
run_review calibrate acme/bench corpus-broken --json
if [ "$EVAL_STATUS" -eq 1 ] && [ "$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)" = "corpus-invalid" ]; then
  good "corrupt corpus manifest refuses typed on calibrate"
else
  bad "corrupt manifest calibrate: exit $EVAL_STATUS $(jq -c '.error // empty' <"$EVAL_OUT" 2>/dev/null)"
fi
run_review trend acme/bench --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.warnings | map(select(contains("trend skips corpus bundle .verdict/state/corpus-broken/"))) | length == 1' <"$EVAL_OUT" >/dev/null 2>&1 \
  && [ "$(jq -r '.data.corpus.bundles' <"$EVAL_OUT" 2>/dev/null)" = "16" ]; then
  good "trend warns and skips the corrupt corpus bundle, keeping the 16 measured"
else
  bad "corrupt manifest trend: exit $EVAL_STATUS $(jq -c '.warnings, .data.corpus.bundles' <"$EVAL_OUT" 2>/dev/null)"
fi

rm -rf "$EVAL_SANDBOX/.verdict/state/corpus-broken" "$EVAL_SANDBOX/.verdict/state/local-bench"
after_hash="$(cd "$EVAL_SANDBOX" && find .verdict -type f | LC_ALL=C sort | xargs shasum -a 1)"
if [ "$before_hash" = "$after_hash" ]; then
  good "corpus calibrate/trend wrote nothing (every fixture byte unchanged)"
else
  bad "corpus reads mutated the fixture tree"
fi
stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
if [ "$stub_before" = "$stub_after" ]; then
  good "zero gh interceptions across assembly, calibrate, and trend"
else
  bad "corpus path touched the network ($stub_before -> $stub_after)"
fi

for case_id in control-comment control-rename; do
  slug="corpus-$case_id"
  dir="$EVAL_SANDBOX/.verdict/state/$slug"
  eval_mk_bundle "$slug"
  cp "$EVAL_ROOT/bench/corpus/$case_id/case.json" "$dir/corpus.json"
  cp "$EVAL_ROOT/bench/corpus/$case_id/full.diff" "$dir/full.diff"
  rm -f "$dir/hunks.tsv"
  jq '.review' "$EVAL_ROOT/bench/corpus/$case_id/agents/fixture-good.json" >"$dir/review.json"
  run_review record acme/bench "$slug" --json
  if [ "$EVAL_STATUS" -eq 0 ]; then
    run_review calibrate acme/bench "$slug" --json
    if [ "$EVAL_STATUS" -eq 0 ] && jq -e '.data | .ground_truth == 0 and .reported == 0 and .false_positives == 0 and .recall == null' <"$EVAL_OUT" >/dev/null; then
      good "$case_id: clean review has zero findings and undefined recall"
    else
      bad "$case_id: clean control calibration failed"
    fi
  else
    bad "$case_id: clean review record failed"
  fi
  rm -rf "$dir"
done

eval_end
