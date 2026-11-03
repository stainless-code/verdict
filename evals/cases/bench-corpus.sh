#!/usr/bin/env bash
# Case bench-corpus: the marked corpus bundle kind and its measured rates.
#
# Grades (hermetic — checked-in fixtures assembled in the sandbox; the
# network-dead gh stub stays first on PATH throughout):
#   C1  assembly: every bench/corpus case x fake agent (8 cases, 2 models)
#       records through the existing record verb into .review/corpus-*;
#       findings.json lands and no gh call happens.
#   C2  calibrate accepts corpus-*: each bundle exits 0 offline and its data
#       (matched/false-positives/missed/rates/per-finding outcomes) equals
#       evals/fixtures/bench-corpus/calibrate.golden.jsonl exactly.
#   C3  trend reports the same ground truth per model and per panel:
#       data.corpus equals trend.golden.json exactly (overall 0.75/0.8333/
#       0.25, fixture-good 0.9/1/0.1, fixture-noisy 0.6/0.6667/0.4).
#   C4  additive-only: the normal rollup is byte-identical with and without
#       corpus bundles (corpus runs stay out of per_bundle), generated_at
#       aside; no corpus key appears when no corpus bundle exists.
#   C5  refusal boundary: local-* still refuses the live join (no-threads,
#       exit 1, zero gh calls) while corpus-* succeeds; a corrupt manifest
#       refuses typed on calibrate and warns/skips on trend.
#   C6  pure read: every .review byte is identical after all calibrate/trend
#       runs, and the stub log never moves.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

# Cases share one sandbox: stash bundles other cases left behind so these
# assertions grade exactly this case's fixtures (restored on exit).
HOLD="$EVAL_SANDBOX/.review-hold-bench"
mkdir -p "$EVAL_SANDBOX/.review" "$HOLD"
for e in "$EVAL_SANDBOX"/.review/*; do
  [ -e "$e" ] || continue
  mv "$e" "$HOLD/"
done
restore_bench() {
  rm -rf "$EVAL_SANDBOX/.review/corpus-"* "$EVAL_SANDBOX/.review/local-bench"
  for e in "$HOLD"/*; do
    [ -e "$e" ] || continue
    mv "$e" "$EVAL_SANDBOX/.review/"
  done
  rmdir "$HOLD" 2>/dev/null || true
}
trap restore_bench EXIT

GOLD="$EVAL_ROOT/evals/fixtures/bench-corpus"

# A normal bundle proves the corpus additions are additive to the rollup.
mkdir -p "$EVAL_SANDBOX/.review/9001"
cat > "$EVAL_SANDBOX/.review/9001/findings.json" <<'FINDINGS_EOF'
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

# --- C1: schemas accept every manifest, then assemble cases x fake agents ---
schema_bad=0
for case_dir in "$EVAL_ROOT"/bench/corpus/*/; do
  [ -f "$case_dir/case.json" ] || continue
  if ! "$EVAL_ROOT/schemas/validate.sh" "$EVAL_ROOT/schemas/corpus.schema.json" "$case_dir/case.json" >/dev/null 2>&1; then
    bad "corpus.schema.json rejects $case_dir/case.json"
    schema_bad=$((schema_bad + 1))
  fi
done
if [ "$schema_bad" -eq 0 ]; then
  good "every checked-in case.json validates against schemas/corpus.schema.json"
fi

# --- C1: assemble every case x fake agent through record ---
bundles=0
for case_dir in "$EVAL_ROOT"/bench/corpus/*/; do
  [ -d "$case_dir" ] || continue
  case_id="$(basename "$case_dir")"
  for agent in "$case_dir"agents/*.json; do
    [ -f "$agent" ] || continue
    model="$(jq -r '.model' "$agent")"
    slug="corpus-${case_id}-${model}"
    dir="$EVAL_SANDBOX/.review/$slug"
    mkdir -p "$dir"
    cp "$case_dir/case.json" "$dir/corpus.json"
    cp "$case_dir/full.diff" "$dir/full.diff"
    jq '.review' "$agent" >"$dir/review.json"
    jq -r '.panel[]' "$agent" >"$dir/panel"
    jq -n --arg m "$model" '{schema_version:"1",effort:"standard",models:[{name:$m,input_tokens:100,output_tokens:50}],est_usd:0.01}' >"$dir/cost.json"
    printf '{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}\n' >"$dir/source.json"
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

# Pure-read snapshot after assembly, before any calibrate/trend read.
before_hash="$(cd "$EVAL_SANDBOX" && find .review -type f | LC_ALL=C sort | xargs shasum -a 1)"

# --- C2: calibrate accepts every corpus bundle, exact numbers in goldens ---
actual_cal="$EVAL_SANDBOX/calibrate-actual.jsonl"
: >"$actual_cal"
for dir in "$EVAL_SANDBOX"/.review/corpus-*/; do
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

# --- C3: trend's per-model / per-panel corpus block ---
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

# --- C4: corpus runs stay out of the ordinary rollup ---
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

# --- C5: local-* refusal boundary + corrupt corpus manifest ---
mkdir -p "$EVAL_SANDBOX/.review/local-bench"
cp "$EVAL_SANDBOX/.review/corpus-off-by-one-fixture-good/findings.json" "$EVAL_SANDBOX/.review/local-bench/findings.json"
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
mkdir -p "$EVAL_SANDBOX/.review/corpus-broken"
cp "$EVAL_SANDBOX/.review/corpus-off-by-one-fixture-good/findings.json" "$EVAL_SANDBOX/.review/corpus-broken/findings.json"
echo '{"schema_version":"1","id":"broken","kind":"seeded"}' >"$EVAL_SANDBOX/.review/corpus-broken/corpus.json"
run_review calibrate acme/bench corpus-broken --json
if [ "$EVAL_STATUS" -eq 1 ] && [ "$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)" = "corpus-invalid" ]; then
  good "corrupt corpus manifest refuses typed on calibrate"
else
  bad "corrupt manifest calibrate: exit $EVAL_STATUS $(jq -c '.error // empty' <"$EVAL_OUT" 2>/dev/null)"
fi
run_review trend acme/bench --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.warnings | map(select(contains("trend skips corpus bundle .review/corpus-broken/"))) | length == 1' <"$EVAL_OUT" >/dev/null 2>&1 \
  && [ "$(jq -r '.data.corpus.bundles' <"$EVAL_OUT" 2>/dev/null)" = "16" ]; then
  good "trend warns and skips the corrupt corpus bundle, keeping the 16 measured"
else
  bad "corrupt manifest trend: exit $EVAL_STATUS $(jq -c '.warnings, .data.corpus.bundles' <"$EVAL_OUT" 2>/dev/null)"
fi

# --- C6: pure read + no network ---
# Remove only the two probe bundles added after the snapshot, then compare
# the measured tree byte-for-byte with the post-assembly snapshot.
rm -rf "$EVAL_SANDBOX/.review/corpus-broken" "$EVAL_SANDBOX/.review/local-bench"
after_hash="$(cd "$EVAL_SANDBOX" && find .review -type f | LC_ALL=C sort | xargs shasum -a 1)"
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

if [ "$fails" -ne 0 ]; then
  echo "  $fails assertion(s) failed"
  exit 1
fi
echo "  all bench-corpus assertions passed"
