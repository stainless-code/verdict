#!/usr/bin/env bash
# Case chunk-cost: cost rollup across chunk runs (report --cost).
#
# Grades (all hermetic — crafted bundles, stub gh, no network):
#   C1  rolled-up totals: a bundle with a top-level cost record plus two
#       per-chunk records (one an array of two runs) reports one whole-review
#       figure — summed tokens, summed est_usd, summed runs — stratified by
#       the existing effort tiers, in both --json and human text.
#   C2  absent-record zeros: manifest chunks without a cost file contribute
#       zeros silently (exit 0, totals equal the present records, no warning).
#   C3  unreadable-record zeros: a corrupt per-chunk record warns, reads
#       zeros, and never fails; a chunk-only bundle (no top-level cost.json)
#       still rolls its valid chunks.
#   C4  non-chunked parity: a bundle with no per-chunk records reports exactly
#       today's rollup (unknown/missing effort still reads standard, a record
#       without models still counts as a run but not a with-cost run), and
#       report without --cost carries no cost key.
#   C5  manifest-unreadable fallback: a corrupt manifest still rolls the
#       on-disk per-chunk records via the glob (exit 0).
#
# Per-chunk convention (defined here, additive read-path only):
# <dir>/chunks/<name>.cost.json holds one cost record (or an array), same
# shape as cost.json, keyed by the manifest chunk name.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

# Deterministic surface: no settings file, no ambient REVIEW_* knobs.
mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_BUDGET_USD REVIEW_MODEL || true

mkcost() { # $1 = bundle dir; stdin = manifest chunk-name list (one per line)
  local dir="$1" names="$1/.chunk-names"
  mkdir -p "$dir/chunks"
  printf '%s\n' "$2" > "$names"
  jq -n --arg b "$dir" --argjson names "$(jq -R . "$names" | jq -s .)" \
    '{schema_version:"1",bundle:$b,head_sha:null,base_sha:null,
      cap:{bytes:512000,source:null},total_bytes:1,chunk_count:($names|length),
      chunks:([$names[]] | to_entries | map({index:(.key+1),name:.value,files:[],
        estimated_bytes:1,bytes:1,
        diff:"x.diff",toc:"x.toc.txt",roles:"x.roles.txt"}))}' \
    > "$dir/chunks/manifest.json"
  rm -f "$names"
}

# --- C1: top-level record + two chunk records (one a two-run array) ---
eval_mk_bundle 4901
printf '[]' > "$EVAL_SANDBOX/.review/4901/findings.json"
cat > "$EVAL_SANDBOX/.review/4901/cost.json" <<'EOF'
{"schema_version":"1","effort":"standard","models":[{"name":"m","input_tokens":100,"output_tokens":50}],"est_usd":0.05}
EOF
mkcost "$EVAL_SANDBOX/.review/4901" "chunk-01
chunk-02"
cat > "$EVAL_SANDBOX/.review/4901/chunks/chunk-01.cost.json" <<'EOF'
{"schema_version":"1","effort":"cheap","models":[{"name":"m","input_tokens":10,"output_tokens":5}],"est_usd":0.01}
EOF
cat > "$EVAL_SANDBOX/.review/4901/chunks/chunk-02.cost.json" <<'EOF'
[{"schema_version":"1","effort":"deep","models":[{"name":"m","input_tokens":1000,"output_tokens":200}],"est_usd":1.5},{"schema_version":"1","effort":"standard","models":[{"name":"m","input_tokens":20,"output_tokens":10}],"est_usd":0.02}]
EOF
run_review report acme/app 4901 --cost --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.cost.runs' <"$EVAL_OUT" 2>/dev/null)" = "4" ] \
  && [ "$(jq -r '.data.cost.runs_with_cost' <"$EVAL_OUT" 2>/dev/null)" = "4" ] \
  && [ "$(jq -r '.data.cost.input_tokens' <"$EVAL_OUT" 2>/dev/null)" = "1130" ] \
  && [ "$(jq -r '.data.cost.output_tokens' <"$EVAL_OUT" 2>/dev/null)" = "265" ] \
  && [ "$(jq -r '.data.cost.total_tokens' <"$EVAL_OUT" 2>/dev/null)" = "1395" ] \
  && [ "$(jq -r '.data.cost.est_usd' <"$EVAL_OUT" 2>/dev/null)" = "1.58" ]; then
  good "rolled-up totals sum the top-level plus both chunk records"
else
  bad "rollup totals: $(jq -c '.data.cost // "absent"' <"$EVAL_OUT" 2>/dev/null) (exit $EVAL_STATUS)"
fi
if [ "$(jq -c '.data.cost.by_tier.cheap' <"$EVAL_OUT" 2>/dev/null)" = '{"runs":1,"input_tokens":10,"output_tokens":5,"total_tokens":15}' ] \
  && [ "$(jq -c '.data.cost.by_tier.standard' <"$EVAL_OUT" 2>/dev/null)" = '{"runs":2,"input_tokens":120,"output_tokens":60,"total_tokens":180}' ] \
  && [ "$(jq -c '.data.cost.by_tier.deep' <"$EVAL_OUT" 2>/dev/null)" = '{"runs":1,"input_tokens":1000,"output_tokens":200,"total_tokens":1200}' ]; then
  good "existing tier stratification holds across the merged stream"
else
  bad "tier split: $(jq -c '.data.cost.by_tier // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi
run_review report acme/app 4901 --cost
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq 'cost: 4 run(s), 4 with records' "$EVAL_OUT" \
  && grep -Fq 'in=1130 out=265 total=1395 est_usd=1.58' "$EVAL_OUT" \
  && grep -Fq 'tiers: cheap=1 standard=2 deep=1' "$EVAL_OUT"; then
  good "human text carries the same rolled-up figure with the tier counts"
else
  bad "human text exits $EVAL_STATUS: $(grep -h 'cost:\|tiers:' "$EVAL_OUT" || echo MISSING)"
fi

# --- C2: absent per-chunk records read zeros, silently ---
eval_mk_bundle 4902
printf '[]' > "$EVAL_SANDBOX/.review/4902/findings.json"
cat > "$EVAL_SANDBOX/.review/4902/cost.json" <<'EOF'
{"schema_version":"1","effort":"standard","models":[{"name":"m","input_tokens":100,"output_tokens":50}],"est_usd":0.05}
EOF
mkcost "$EVAL_SANDBOX/.review/4902" "chunk-01
chunk-02
chunk-03"
cat > "$EVAL_SANDBOX/.review/4902/chunks/chunk-01.cost.json" <<'EOF'
{"schema_version":"1","effort":"cheap","models":[{"name":"m","input_tokens":10,"output_tokens":5}],"est_usd":0.01}
EOF
run_review report acme/app 4902 --cost --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.cost.runs' <"$EVAL_OUT" 2>/dev/null)" = "2" ] \
  && [ "$(jq -r '.data.cost.input_tokens' <"$EVAL_OUT" 2>/dev/null)" = "110" ] \
  && [ "$(jq -r '.data.cost.output_tokens' <"$EVAL_OUT" 2>/dev/null)" = "55" ] \
  && [ "$(jq -r '.data.cost.total_tokens' <"$EVAL_OUT" 2>/dev/null)" = "165" ] \
  && [ "$(jq -r '.data.cost.est_usd' <"$EVAL_OUT" 2>/dev/null)" = "0.06" ] \
  && ! grep -q "chunk cost" "$EVAL_ERR"; then
  good "two missing chunk records read zeros with no warning and no failure"
else
  bad "absent-grade exits $EVAL_STATUS: $(jq -c '.data.cost // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi

# --- C3: unreadable chunk record warns, reads zeros, never fails ---
eval_mk_bundle 4903
printf '[]' > "$EVAL_SANDBOX/.review/4903/findings.json"
mkcost "$EVAL_SANDBOX/.review/4903" "chunk-01
chunk-02"
cat > "$EVAL_SANDBOX/.review/4903/chunks/chunk-01.cost.json" <<'EOF'
{"schema_version":"1","effort":"deep","models":[{"name":"m","input_tokens":1000,"output_tokens":200}],"est_usd":1.5}
EOF
printf 'not json{{{corrupt' > "$EVAL_SANDBOX/.review/4903/chunks/chunk-02.cost.json"
run_review report acme/app 4903 --cost --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "chunk-02.cost.json unreadable" "$EVAL_ERR" \
  && [ "$(jq -r '.data.cost.runs' <"$EVAL_OUT" 2>/dev/null)" = "1" ] \
  && [ "$(jq -r '.data.cost.input_tokens' <"$EVAL_OUT" 2>/dev/null)" = "1000" ] \
  && [ "$(jq -r '.data.cost.output_tokens' <"$EVAL_OUT" 2>/dev/null)" = "200" ] \
  && [ "$(jq -r '.data.cost.est_usd' <"$EVAL_OUT" 2>/dev/null)" = "1.5" ] \
  && [ "$(jq -c '.data.cost.by_tier.deep' <"$EVAL_OUT" 2>/dev/null)" = '{"runs":1,"input_tokens":1000,"output_tokens":200,"total_tokens":1200}' ]; then
  good "corrupt chunk record warns, reads zeros, chunk-only bundle still rolls"
else
  bad "unreadable-grade exits $EVAL_STATUS: $(jq -c '.data.cost // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi

# --- C4: non-chunked parity (no per-chunk records, legacy shapes) ---
eval_mk_bundle 4904
printf '[]' > "$EVAL_SANDBOX/.review/4904/findings.json"
cat > "$EVAL_SANDBOX/.review/4904/cost.json" <<'EOF'
[{"schema_version":"1","effort":"standard","models":[{"name":"m","input_tokens":7,"output_tokens":3}],"est_usd":0.1},{"schema_version":"1","effort":"ULTRA","est_usd":0.2}]
EOF
run_review report acme/app 4904 --cost --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.cost.runs' <"$EVAL_OUT" 2>/dev/null)" = "2" ] \
  && [ "$(jq -r '.data.cost.runs_with_cost' <"$EVAL_OUT" 2>/dev/null)" = "1" ] \
  && [ "$(jq -r '.data.cost.input_tokens' <"$EVAL_OUT" 2>/dev/null)" = "7" ] \
  && [ "$(jq -r '.data.cost.output_tokens' <"$EVAL_OUT" 2>/dev/null)" = "3" ] \
  && [ "$(jq -r '.data.cost.total_tokens' <"$EVAL_OUT" 2>/dev/null)" = "10" ] \
  && [ "$(jq -r '.data.cost.est_usd' <"$EVAL_OUT" 2>/dev/null)" = "0.3" ] \
  && [ "$(jq -c '.data.cost.by_tier.standard' <"$EVAL_OUT" 2>/dev/null)" = '{"runs":2,"input_tokens":7,"output_tokens":3,"total_tokens":10}' ]; then
  good "no chunk records: rollup identical to today (unknown effort reads standard)"
else
  bad "parity-grade exits $EVAL_STATUS: $(jq -c '.data.cost // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi
run_review report acme/app 4904 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data | has("cost")' <"$EVAL_OUT" 2>/dev/null)" = "false" ]; then
  good "report without --cost still carries no cost key"
else
  bad "no-cost-flag exits $EVAL_STATUS"
fi

# --- C5: corrupt manifest falls back to the on-disk glob ---
eval_mk_bundle 4905
printf '[]' > "$EVAL_SANDBOX/.review/4905/findings.json"
mkdir -p "$EVAL_SANDBOX/.review/4905/chunks"
printf 'not json{{{corrupt manifest' > "$EVAL_SANDBOX/.review/4905/chunks/manifest.json"
cat > "$EVAL_SANDBOX/.review/4905/chunks/chunk-01.cost.json" <<'EOF'
{"schema_version":"1","effort":"cheap","models":[{"name":"m","input_tokens":10,"output_tokens":5}],"est_usd":0.01}
EOF
run_review report acme/app 4905 --cost --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.cost.runs' <"$EVAL_OUT" 2>/dev/null)" = "1" ] \
  && [ "$(jq -r '.data.cost.input_tokens' <"$EVAL_OUT" 2>/dev/null)" = "10" ] \
  && [ "$(jq -r '.data.cost.est_usd' <"$EVAL_OUT" 2>/dev/null)" = "0.01" ]; then
  good "unreadable manifest still rolls the on-disk chunk records"
else
  bad "manifest-fallback exits $EVAL_STATUS: $(jq -c '.data.cost // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi

# --- cleanup: fixtures stay out of later cases, knob state goes ---
rm -rf "$EVAL_SANDBOX/.review/4901" "$EVAL_SANDBOX/.review/4902" \
  "$EVAL_SANDBOX/.review/4903" "$EVAL_SANDBOX/.review/4904" \
  "$EVAL_SANDBOX/.review/4905" "$EVAL_SANDBOX/xdg-empty"
unset XDG_CONFIG_HOME || true

if [ "$fails" -ne 0 ]; then echo "case chunk-cost: FAIL ($fails)"; exit 1; fi
echo "case chunk-cost: PASS"
