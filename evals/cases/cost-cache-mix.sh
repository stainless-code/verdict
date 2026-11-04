#!/usr/bin/env bash
# Case cost-cache-mix: cached vs fresh input-token split in the cost rollup
# (report --cost).
#
# Grades (all hermetic — crafted bundles, stub gh, no network):
#   C1  cache-carrying record: per-model cache_read/cache_creation fields
#       roll up into a top-level cached_input_tokens scalar beside the
#       existing in/out/total line, in both --json and human text.
#   C2  legacy record (no cache keys): today's exact numbers — the scalar
#       sums to 0 and by_tier gains no new keys.
#   C3  explicit null cache fields: unknown, contribute nothing (scalar 0),
#       totals untouched — no fabricated precision.
#   C4  totals invariant: total == input + output on a mixed bundle (cached
#       tokens are a subset of input, never added on top).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

# Deterministic surface: no settings file, no ambient REVIEW_* knobs.
mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_BUDGET_USD REVIEW_MODEL || true

# --- C1: cache-carrying record splits cached vs fresh ---
eval_mk_bundle 4911
printf '[]' > "$EVAL_SANDBOX/.review/4911/findings.json"
cat > "$EVAL_SANDBOX/.review/4911/cost.json" <<'EOF'
{"schema_version":"1","effort":"standard","models":[{"name":"m","input_tokens":100,"output_tokens":50,"cache_read_input_tokens":30,"cache_creation_input_tokens":10},{"name":"n","input_tokens":20,"output_tokens":5}],"est_usd":0.05}
EOF
run_review report acme/app 4911 --cost --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.cost.input_tokens' <"$EVAL_OUT" 2>/dev/null)" = "120" ] \
  && [ "$(jq -r '.data.cost.output_tokens' <"$EVAL_OUT" 2>/dev/null)" = "55" ] \
  && [ "$(jq -r '.data.cost.total_tokens' <"$EVAL_OUT" 2>/dev/null)" = "175" ] \
  && [ "$(jq -r '.data.cost.cached_input_tokens' <"$EVAL_OUT" 2>/dev/null)" = "40" ]; then
  good "cache-carrying record rolls cached splits into cached_input_tokens"
else
  bad "cache split: $(jq -c '.data.cost // "absent"' <"$EVAL_OUT" 2>/dev/null) (exit $EVAL_STATUS)"
fi
run_review report acme/app 4911 --cost
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq 'in=120 out=55 total=175' "$EVAL_OUT" \
  && grep -Fq 'cached=40' "$EVAL_OUT"; then
  good "human text carries the cached split beside the in/out/total line"
else
  bad "human text exits $EVAL_STATUS: $(grep -h 'cost:' "$EVAL_OUT" || echo MISSING)"
fi

# --- C2: legacy record (no cache keys) keeps today's exact numbers ---
eval_mk_bundle 4912
printf '[]' > "$EVAL_SANDBOX/.review/4912/findings.json"
cat > "$EVAL_SANDBOX/.review/4912/cost.json" <<'EOF'
{"schema_version":"1","effort":"standard","models":[{"name":"m","input_tokens":100,"output_tokens":50}],"est_usd":0.05}
EOF
run_review report acme/app 4912 --cost --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.cost.input_tokens' <"$EVAL_OUT" 2>/dev/null)" = "100" ] \
  && [ "$(jq -r '.data.cost.output_tokens' <"$EVAL_OUT" 2>/dev/null)" = "50" ] \
  && [ "$(jq -r '.data.cost.total_tokens' <"$EVAL_OUT" 2>/dev/null)" = "150" ] \
  && [ "$(jq -r '.data.cost.cached_input_tokens' <"$EVAL_OUT" 2>/dev/null)" = "0" ] \
  && [ "$(jq -c '.data.cost.by_tier.standard' <"$EVAL_OUT" 2>/dev/null)" = '{"runs":1,"input_tokens":100,"output_tokens":50,"total_tokens":150}' ]; then
  good "legacy record rolls today's exact numbers with by_tier untouched"
else
  bad "legacy-grade exits $EVAL_STATUS: $(jq -c '.data.cost // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi

# --- C3: explicit null cache fields read unknown, contribute nothing ---
eval_mk_bundle 4913
printf '[]' > "$EVAL_SANDBOX/.review/4913/findings.json"
cat > "$EVAL_SANDBOX/.review/4913/cost.json" <<'EOF'
{"schema_version":"1","effort":"standard","models":[{"name":"m","input_tokens":100,"output_tokens":50,"cache_read_input_tokens":null,"cache_creation_input_tokens":null}],"est_usd":0.05}
EOF
run_review report acme/app 4913 --cost --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.cost.cached_input_tokens' <"$EVAL_OUT" 2>/dev/null)" = "0" ] \
  && [ "$(jq -r '.data.cost.total_tokens' <"$EVAL_OUT" 2>/dev/null)" = "150" ]; then
  good "null cache fields contribute nothing and leave totals alone"
else
  bad "null-grade exits $EVAL_STATUS: $(jq -c '.data.cost // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi

# --- C4: total == input + output on a mixed bundle (no double counting) ---
eval_mk_bundle 4914
printf '[]' > "$EVAL_SANDBOX/.review/4914/findings.json"
cat > "$EVAL_SANDBOX/.review/4914/cost.json" <<'EOF'
[{"schema_version":"1","effort":"cheap","models":[{"name":"m","input_tokens":1000,"output_tokens":200,"cache_read_input_tokens":900,"cache_creation_input_tokens":50}],"est_usd":1.5},{"schema_version":"1","effort":"deep","models":[{"name":"n","input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":4}],"est_usd":0.01}]
EOF
run_review report acme/app 4914 --cost --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.cost.input_tokens' <"$EVAL_OUT" 2>/dev/null)" = "1010" ] \
  && [ "$(jq -r '.data.cost.output_tokens' <"$EVAL_OUT" 2>/dev/null)" = "205" ] \
  && [ "$(jq -r '.data.cost.total_tokens' <"$EVAL_OUT" 2>/dev/null)" = "1215" ] \
  && [ "$(jq -r '.data.cost.cached_input_tokens' <"$EVAL_OUT" 2>/dev/null)" = "954" ] \
  && [ "$(jq -r '.data.cost.total_tokens == (.data.cost.input_tokens + .data.cost.output_tokens)' <"$EVAL_OUT" 2>/dev/null)" = "true" ]; then
  good "mixed bundle holds total == input + output with cached as a subset"
else
  bad "invariant-grade exits $EVAL_STATUS: $(jq -c '.data.cost // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi

# --- cleanup: fixtures stay out of later cases, knob state goes ---
rm -rf "$EVAL_SANDBOX/.review/4911" "$EVAL_SANDBOX/.review/4912" \
  "$EVAL_SANDBOX/.review/4913" "$EVAL_SANDBOX/.review/4914" "$EVAL_SANDBOX/xdg-empty"
unset XDG_CONFIG_HOME || true

eval_end
