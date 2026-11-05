#!/bin/sh
set -eu

: "${VERDICT_BENCH_PROMPT:?the runner sets VERDICT_BENCH_PROMPT}"
: "${VERDICT_BENCH_OUT:?the runner sets VERDICT_BENCH_OUT}"
: "${VERDICT_BENCH_MODEL:?the runner sets VERDICT_BENCH_MODEL}"

pattern=${BENCH_PI_MODEL:-deepseek/deepseek-flash:high}
case $pattern in
*:*)
	model=${pattern%:*}
	thinking=${pattern##*:}
	;;
*)
	model=$pattern
	thinking=medium
	;;
esac
if [ "$model" != "$VERDICT_BENCH_MODEL" ]; then
	printf 'pi agent: %s resolves to model %s, but the run is attributed to %s\n' \
		"$pattern" "$model" "$VERDICT_BENCH_MODEL" >&2
	exit 2
fi

bin=${BENCH_PI_BIN:-}
if [ -z "$bin" ]; then
	bin=$(command -v pi || true)
fi
if [ -z "$bin" ]; then
	printf 'pi agent: no pi on PATH (set BENCH_PI_BIN)\n' >&2
	exit 2
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/verdict-bench-pi.XXXXXX")
trap 'rm -rf "$work"' EXIT INT TERM

# The shipped prompt set is written for a harness with tools and subagents. This
# adapter grants neither, so the system prompt has to say so: pi's default coding
# persona advertises tools that --no-tools removed, and models answer that lie
# with literal tool-call markup instead of a review.
system='You are the reviewer under test for the verdict benchmark. You have no tools: no shell, no file access, no network, no subagents, and no repository. Everything you may read is in the user message. Reply with one JSON document and nothing else.'
instruction="Your final message must be the complete review.json object for this change: one JSON document, no prose, no code fence, no tool call. The harness writes it to $VERDICT_BENCH_OUT."

status=0
"$bin" --mode json -p --no-session --no-tools --no-mcp --no-skills \
	--no-prompt-templates --no-context-files --system-prompt "$system" \
	--thinking "$thinking" --model "$model" \
	@"$VERDICT_BENCH_PROMPT" "$instruction" >"$work/events.jsonl" 2>"$work/stderr.log" || status=$?
cat "$work/events.jsonl"
write_cost() {
	if [ -n "${VERDICT_BENCH_COST:-}" ] && [ -f "$work/events.jsonl" ]; then
		jq -R -s --arg model "$VERDICT_BENCH_MODEL" '
			[split("\n")[] | select(length > 0) | fromjson? | select(.type == "message_end" and .message.role == "assistant") | .message.usage // empty]
			| {
				schema_version: "1",
				effort: "standard",
				models: [{
					name: $model,
					input_tokens: (map(.input // 0) | add // 0),
					output_tokens: (map(.output // 0) | add // 0),
					cache_read_input_tokens: (map(.cacheRead // 0) | add // 0),
					cache_creation_input_tokens: (map(.cacheWrite // 0) | add // 0)
				}],
				est_usd: (map(.cost.total // 0) | add // 0)
			}
			| if .est_usd > 0 then . else del(.est_usd) end
		' "$work/events.jsonl" >"$work/cost.tmp" || true
		if [ -f "$work/cost.tmp" ]; then
			if jq -e . "$work/events.jsonl" >/dev/null 2>&1; then
				mv "$work/cost.tmp" "$VERDICT_BENCH_COST"
			else
				jq '. + {incomplete: true}' "$work/cost.tmp" >"$VERDICT_BENCH_COST" || mv "$work/cost.tmp" "$VERDICT_BENCH_COST"
			fi
		fi
	fi
}
write_cost || true
if [ "$status" -ne 0 ]; then
	printf 'pi agent: pi exited %d\n' "$status" >&2
	sed -n '1,20p' "$work/stderr.log" >&2 || true
	write_cost || true
	exit 1
fi

jq -R -s -r '[split("\n")[] | select(length > 0) | fromjson? | select(.type == "message_end" and .message.role == "assistant")] | last // {} | [.message.content[]? | select(type == "object" and .type == "text") | .text] | join("")' \
	"$work/events.jsonl" >"$work/review.json"

if ! jq -e . "$work/review.json" >/dev/null 2>&1; then
	if ! grep -q '```' "$work/review.json"; then
		printf 'pi agent: final message is not JSON: %s\n' "$(tr -d '\n' <"$work/review.json" | cut -c1-240)" >&2
		write_cost || true
		exit 1
	fi
	sed -e '1{/^[[:space:]]*```/d;}' -e '$ s/^[[:space:]]*```[[:space:]]*$//' "$work/review.json" >"$work/fenced.json"
	if ! jq -e . "$work/fenced.json" >/dev/null 2>&1; then
		printf 'pi agent: final message is not JSON minus its fence: %s\n' "$(tr -d '\n' <"$work/review.json" | cut -c1-240)" >&2
		write_cost || true
		exit 1
	fi
	mv "$work/fenced.json" "$work/review.json"
fi

cp "$work/review.json" "$VERDICT_BENCH_OUT"

write_cost

if [ -n "${VERDICT_BENCH_COST:-}" ]; then
	printf 'pi agent: %s wrote %s\n' "$pattern" "$VERDICT_BENCH_OUT"
	jq -r '"pi agent: input \(.models[0].input_tokens), cache read \(.models[0].cache_read_input_tokens), output \(.models[0].output_tokens), est_usd \(.est_usd // "unknown")"' \
		"$VERDICT_BENCH_COST"
fi
