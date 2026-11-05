#!/bin/sh
# Fixture agent: plays a checked-in review so the benchmark loop runs without a
# model, a network, or a bill. It is a plumbing stub, not a reviewer: it never
# reads the prompt, so a run through it proves the runner, never judgment.
set -eu

: "${VERDICT_BENCH_CASE:?the runner sets VERDICT_BENCH_CASE}"
: "${VERDICT_BENCH_OUT:?the runner sets VERDICT_BENCH_OUT}"

fixture=${BENCH_FIXTURE:-fixture-good}
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd -P)

if [ -n "${BENCH_FIXTURE_LOOP:-}" ]; then
	src="$root/corpus/$VERDICT_BENCH_CASE/agents/$BENCH_FIXTURE_LOOP.json"
	if [ ! -f "$src" ]; then
		printf 'fixture agent: no %s for case %s\n' "$BENCH_FIXTURE_LOOP" "$VERDICT_BENCH_CASE" >&2
		exit 2
	fi
	round=${VERDICT_BENCH_ROUND:-1}
	idx=$((round - 1))
	len=$(jq -r '.reviews | length' "$src")
	if [ "$idx" -ge "$len" ]; then
		idx=$((len - 1))
	fi
	jq -e ".reviews[$idx]" "$src" > "$VERDICT_BENCH_OUT"
	if [ -n "${VERDICT_BENCH_COST:-}" ] && jq -e 'has("cost")' "$src" > /dev/null; then
		jq '.cost' "$src" > "$VERDICT_BENCH_COST"
	fi
	exit 0
fi

src="$root/corpus/$VERDICT_BENCH_CASE/agents/$fixture.json"

if [ ! -f "$src" ]; then
	printf 'fixture agent: no %s for case %s\n' "$fixture" "$VERDICT_BENCH_CASE" >&2
	exit 2
fi

jq -e '.review' "$src" > "$VERDICT_BENCH_OUT"

if [ -n "${VERDICT_BENCH_COST:-}" ] && jq -e 'has("cost")' "$src" > /dev/null; then
	jq '.cost' "$src" > "$VERDICT_BENCH_COST"
fi
