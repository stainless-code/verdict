#!/usr/bin/env bash
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

cd "$EVAL_ROOT"
report="$EVAL_SANDBOX/benchmark.json"
agent="sh '$EVAL_ROOT/bench/agents/fixture.sh'"
if BENCH_FIXTURE=fixture-good go run ./bench/runner -model fixture-good -agent "$agent" -panel panel-alpha \
  -cases off-by-one,control-comment -repeats 3 -k 3 -verdict "$EVAL_CLI" \
  -workdir "$EVAL_SANDBOX/benchmark-run" -out "$report" >"$EVAL_SANDBOX/bench.log" 2>&1; then
  good "real runner records six samples through the shipped CLI"
else
  cat "$EVAL_SANDBOX/bench.log"
  bail "benchmark runner failed"
fi
if jq -e '.k == 3 and .repeats == 3 and .any_of_k_recall.value == 1 and
  .any_of_k_recall.denominator == 2 and .seeded == true and .citable == false and
  (.corpus.commit | length > 0) and (.prompts_hash | length > 0) and
  (.tool_version | length > 0) and (.commands | length > 0) and
  .trend_corpus.metrics.precision.value == 0.6667 and
  .trend_corpus.metrics.control_fp_rate.denominator == 3 and
  .trend_corpus.metrics.severity_weighted_pooled_f1.value == 0.9836 and
  .trend_corpus.metrics.cost_usd == null and .trend_corpus.metrics.latency_n == 6' "$report" >/dev/null; then
  good "k=3, precision 6/9, control FP 0/3, weighted F1 180/183 and metadata match"
else
  bad "benchmark measurements or metadata drifted"
fi
if go run ./bench/runner -render "$report" >"$EVAL_SANDBOX/benchmark-render.md" \
  && cmp -s "$EVAL_SANDBOX/benchmark-render.md" "$EVAL_SANDBOX/benchmark.md" \
  && grep -q 'not a model-quality claim' "$EVAL_SANDBOX/benchmark-render.md"; then
  good "static Markdown is deterministic and labels seeded evidence"
else
  bad "Markdown render differs from the published artifact"
fi

# Adjudication recomputes the report from its own sigs: strict figures stay,
# the one recorded finding moves adjudicated precision by exactly 1 of 9.
sig="$(jq -r 'first(.bundles[] | select(.case=="off-by-one") | .calibrate.outcomes[]
  | select(.result=="false-positive") | .sig)' "$report")"
record="$EVAL_SANDBOX/adjudication.json"
jq -n --arg run "$(jq -r .run_id "$report")" --arg sig "$sig" '{
  schema_version: "1", run_id: $run,
  entries: [{case: "off-by-one", sample: 1, sig: $sig, verdict: "real-unlisted",
    adjudicator: "eval", rationale: "`renderRows` shadows the package rows slice the file still builds; the call site reads the wrong one."}]}' \
  >"$record"
adjudicated="$EVAL_SANDBOX/benchmark-adjudicated.json"
if go run ./bench/runner -render "$report" -adjudication "$record" -out "$adjudicated" \
  >"$EVAL_SANDBOX/benchmark-adjudicated.md" 2>"$EVAL_SANDBOX/adjudication.log"; then
  good "an adjudication record recomputes the publication without a model"
else
  cat "$EVAL_SANDBOX/adjudication.log"
  bail "adjudicated render failed"
fi
assert_json_field "strict precision is untouched by adjudication" "0.6667" '.trend_corpus.precision' "$adjudicated"
assert_json_field "strict recall is untouched by adjudication" "1" '.trend_corpus.recall' "$adjudicated"
assert_json_field "one real-unlisted finding raises adjudicated precision by 1/9" "0.7778" '.adjudication.precision_adjudicated' "$adjudicated"
assert_json_field "adjudicated recall keeps the strict bug count" "1" '.adjudication.recall_adjudicated' "$adjudicated"
assert_json_field "the record's digest is published" "64" '.adjudication.sha256 | length' "$adjudicated"
assert_file_grep "the Markdown shows strict and adjudicated precision side by side" \
  "$EVAL_SANDBOX/benchmark-adjudicated.md" '^| precision | 0.6667 | 0.7778 |$'
assert_file_grep "the Markdown shows strict and adjudicated recall side by side" \
  "$EVAL_SANDBOX/benchmark-adjudicated.md" '^| recall | 1.0000 | 1.0000 |$'
assert_json_field "the strict publication carries no adjudication block" "absent" '.adjudication.precision_adjudicated // "absent"' "$report"
eval_end
