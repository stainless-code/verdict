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

eval_end
