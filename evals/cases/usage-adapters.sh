#!/usr/bin/env bash
# Case usage-adapters: the optional harness converters outside the binary
# turn vendor-private session files into verdict usage records.
#
# Grades (all hermetic — checked-in fixtures plus crafted inputs, no gh):
#   A1  claude-to-usage.sh converts the checked-in transcript fixture to a
#       record that validates against schemas/usage.schema.json, preferring
#       the cost-state total and deduping the repeated request line.
#   A2  without a cost-state, per-request costs sum with repeats deduped.
#   A3  opencode-to-usage.sh converts the checked-in export fixture to a
#       validating record with the export window.
#   A4  without a session total, message costs sum.
#   A5  on-stop.sh turns a Stop-hook payload into a validating usage file.
#   A6  a timestamp-less transcript refuses with nothing written.
#   A7  tokens without a price still convert (unpriced, never measured).
#   A8  the adapters README documents both converters as optional.
#
# The converters need jq (>= 1.7 for the opencode epoch windows). A missing jq
# is a SKIP, not a pass: the case exits 77 and the runner refuses it unless
# EVAL_ALLOW_SKIP=1.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

command -v jq >/dev/null 2>&1 || skip_case "jq missing — the converters need jq"

AD="$EVAL_ROOT/examples/usage-adapters"
VAL="$EVAL_SKILL/schemas/validate.sh"
SCH="$EVAL_SKILL/schemas/usage.schema.json"
WORK="$EVAL_SANDBOX/adapter-out"
mkdir -p "$WORK"

check_valid() { # $1 = label, $2 = doc
  if "$VAL" "$SCH" "$2" >/dev/null 2>&1; then
    good "$1 validates against usage.schema.json"
  else
    bad "$1 fails schema validation"
  fi
}

# A1: the checked-in Claude transcript fixture.
"$AD/claude-code/claude-to-usage.sh" \
  "$AD/claude-code/fixtures/transcript.jsonl" "$WORK/claude.json" >/dev/null
check_valid "claude fixture record" "$WORK/claude.json"
if [ "$(jq -r '.usd' <"$WORK/claude.json")" = "2.5" ] \
  && [ "$(jq -r '.source' <"$WORK/claude.json")" = "claude-code" ] \
  && [ "$(jq -r '.model' <"$WORK/claude.json")" = "claude-sonnet-4-5" ] \
  && [ "$(jq -r '.started_at' <"$WORK/claude.json")" = "2026-09-01T10:00:00Z" ] \
  && [ "$(jq -r '.ended_at' <"$WORK/claude.json")" = "2026-09-01T10:02:00Z" ] \
  && [ "$(jq -r '.tokens.input' <"$WORK/claude.json")" = "3000" ] \
  && [ "$(jq -r '.tokens.cache_read' <"$WORK/claude.json")" = "800" ]; then
  good "claude fixture prefers the cost-state total and dedupes repeat lines"
else
  bad "claude fixture values: $(jq -c '.' <"$WORK/claude.json")"
fi

# A2: per-request costs sum with repeats deduped when no cost-state exists.
cat > "$WORK/nocost.jsonl" <<'EOF'
{"type":"assistant","sessionId":"s","timestamp":"2026-01-01T00:00:00Z","requestId":"r1","costUSD":0.4}
{"type":"assistant","sessionId":"s","timestamp":"2026-01-01T00:01:00Z","requestId":"r1","costUSD":0.4}
{"type":"assistant","sessionId":"s","timestamp":"2026-01-01T00:02:00Z","requestId":"r2","costUSD":1.7}
EOF
"$AD/claude-code/claude-to-usage.sh" "$WORK/nocost.jsonl" "$WORK/nocost.json" >/dev/null
check_valid "summed claude record" "$WORK/nocost.json"
if [ "$(jq -r '.usd' <"$WORK/nocost.json")" = "2.1" ]; then
  good "per-request costs sum to 2.1 with the repeat counted once"
else
  bad "summed usd = $(jq -r '.usd' <"$WORK/nocost.json" 2>/dev/null), want 2.1"
fi

# A3: the checked-in opencode export fixture (needs jq gmtime).
if jq -n '0 | gmtime' >/dev/null 2>&1; then
  "$AD/opencode/opencode-to-usage.sh" \
    "$AD/opencode/fixtures/export.json" "$WORK/opencode.json" >/dev/null
  check_valid "opencode fixture record" "$WORK/opencode.json"
  if [ "$(jq -r '.usd' <"$WORK/opencode.json")" = "2.5" ] \
    && [ "$(jq -r '.source' <"$WORK/opencode.json")" = "opencode" ] \
    && [ "$(jq -r '.started_at' <"$WORK/opencode.json")" = "2026-09-01T10:00:00Z" ] \
    && [ "$(jq -r '.ended_at' <"$WORK/opencode.json")" = "2026-09-01T10:01:00Z" ]; then
    good "opencode fixture keeps the session total and window"
  else
    bad "opencode fixture values: $(jq -c '.' <"$WORK/opencode.json")"
  fi

  # A4: message costs sum when the session total is absent.
  cat > "$WORK/msgs.json" <<'EOF'
{"info":{"id":"m","directory":"/work","time":{"created":1788256800000,"updated":1788256860000}},"messages":[{"info":{"cost":0.4}},{"info":{"cost":0.6}},{"info":{}}]}
EOF
  "$AD/opencode/opencode-to-usage.sh" "$WORK/msgs.json" "$WORK/msgs-out.json" >/dev/null
  if [ "$(jq -r '.usd' <"$WORK/msgs-out.json")" = "1" ]; then
    good "message costs sum to 1 without a session total"
  else
    bad "message-summed usd = $(jq -r '.usd' <"$WORK/msgs-out.json" 2>/dev/null), want 1"
  fi
else
  skip "opencode fixture leg (jq without gmtime)"
  skip "opencode message-sum leg (jq without gmtime)"
fi

# A5: the Stop hook converts the payload transcript into a usage file.
mkdir -p "$WORK/usage-log"
# shellcheck disable=SC2016
printf '{"session_id":"sess-e2e","transcript_path":"%s"}' \
  "$AD/claude-code/fixtures/transcript.jsonl" \
  | VERDICT_USAGE_DIR="$WORK/usage-log" "$AD/claude-code/on-stop.sh"
check_valid "hook-written record" "$WORK/usage-log/sess-e2e.json"
if [ "$(jq -r '.usd' <"$WORK/usage-log/sess-e2e.json" 2>/dev/null)" = "2.5" ]; then
  good "stop hook writes the priced session record"
else
  bad "hook record missing or unpriced"
fi

# A6: a transcript with no timestamps refuses with nothing written.
printf '{"type":"assistant","sessionId":"s"}\n' > "$WORK/notime.jsonl"
if "$AD/claude-code/claude-to-usage.sh" "$WORK/notime.jsonl" "$WORK/notime.json" >/dev/null 2>&1; then
  bad "timestamp-less transcript converted (want refusal)"
else
  if [ -e "$WORK/notime.json" ]; then
    bad "refused conversion still wrote an output file"
  else
    good "timestamp-less transcript refuses with nothing written"
  fi
fi

# A7: tokens without a price still convert, unpriced and validating.
printf '{"type":"assistant","sessionId":"s","timestamp":"2026-01-01T00:00:00Z","message":{"usage":{"input_tokens":5,"output_tokens":1}}}\n' > "$WORK/tokens.jsonl"
"$AD/claude-code/claude-to-usage.sh" "$WORK/tokens.jsonl" "$WORK/tokens.json" >/dev/null
check_valid "unpriced tokens record" "$WORK/tokens.json"
if jq -e 'has("usd") | not' <"$WORK/tokens.json" >/dev/null \
  && [ "$(jq -r '.tokens.input' <"$WORK/tokens.json")" = "5" ]; then
  good "unpriced tokens convert without a usd claim"
else
  bad "unpriced tokens record: $(jq -c '.' <"$WORK/tokens.json")"
fi

# A8: the adapters README documents both converters as optional.
if grep -Fq 'claude-to-usage.sh' "$AD/README.md" \
  && grep -Fq 'opencode-to-usage.sh' "$AD/README.md" \
  && grep -Fqi 'optional' "$AD/README.md"; then
  good "adapters README documents both converters as optional"
else
  bad "adapters README misses a converter or the optional framing"
fi

eval_end
