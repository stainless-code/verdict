#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=evals/lib/common.sh
. "$EVAL_ROOT/evals/lib/common.sh"

eval_seed_base
run_review learn --rule "Never return nil without an error" --scope "*.go" --id nil-err --json >/dev/null
run_review learn --rule "Always close response bodies" --scope "*.go" --id close-body --json >/dev/null

# Leg 1: legacy bundle without kb-recall.json leaves recall columns unmeasured.
mkdir -p "$EVAL_SANDBOX/.verdict/state/401"
printf '[]\n' >"$EVAL_SANDBOX/.verdict/state/401/findings.json"

run_review learn --status --json
assert_exit "learn --status --json before recall manifests" 0
if [ "$(jq -r '.data.rules[] | select(.id == "nil-err") | "\(.recalled_in)|\(.cited_in)|\(.exposure)|\(.acceptance)"' <"$EVAL_OUT")" = "null|0|null|null" ]; then
  good "unmeasured repo reports null recalled_in and exposure in --json"
else
  bad "unexpected unmeasured --json row: $(cat "$EVAL_OUT")"
fi

run_review learn --status
assert_exit "learn --status human before recall manifests" 0
if matches 'nil-err \[active\] pointed hits=0 false_hits=0 judged=0 precision=n/a recalled_in=unmeasured cited_in=0 exposure=unmeasured acceptance=n/a' <"$EVAL_OUT"; then
  good "unmeasured repo renders recalled_in=unmeasured and exposure=unmeasured"
else
  bad "unexpected unmeasured text output: $(cat "$EVAL_OUT")"
fi

# Leg 2: measured bundles where nil-err is recalled twice and never cited, while close-body is recalled twice and cited once addressed.
cat >"$EVAL_SANDBOX/.verdict/state/401/kb-recall.json" <<'EOF'
[{"id":"nil-err"},{"id":"close-body"}]
EOF
cat >"$EVAL_SANDBOX/.verdict/state/401/findings.json" <<'EOF'
[{"kind":"inline","path":"main.go","side":"RIGHT","line":10,"anchored":true,"body":"Response body leaked on error.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","kb":["close-body"],"sig":"sig401","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"addressed","rationale":"","history":[]}]
EOF

mkdir -p "$EVAL_SANDBOX/.verdict/state/402"
cat >"$EVAL_SANDBOX/.verdict/state/402/kb-recall.json" <<'EOF'
[{"id":"nil-err"},{"id":"close-body"}]
EOF
printf '[]\n' >"$EVAL_SANDBOX/.verdict/state/402/findings.json"

run_review learn --status --json
assert_exit "learn --status --json with seeded bundles" 0
assert_envelope "learn --status envelope"
nil_row="$(jq -r '.data.rules[] | select(.id == "nil-err") | "\(.recalled_in)|\(.cited_in)|\(.exposure)|\(.acceptance)"' <"$EVAL_OUT")"
close_row="$(jq -r '.data.rules[] | select(.id == "close-body") | "\(.recalled_in)|\(.cited_in)|\(.exposure)|\(.acceptance)"' <"$EVAL_OUT")"
assert_eq "nil-err exposure row (never cited)" "2|0|0|null" "$nil_row"
assert_eq "close-body exposure row (cited once, addressed)" "2|1|0.5|1" "$close_row"

run_review learn --status
assert_exit "learn --status human with seeded bundles" 0
if matches 'nil-err \[active\] pointed hits=0 false_hits=0 judged=0 precision=n/a recalled_in=2 cited_in=0 exposure=0 acceptance=n/a' <"$EVAL_OUT" \
  && matches 'close-body \[active\] pointed hits=0 false_hits=0 judged=0 precision=n/a recalled_in=2 cited_in=1 exposure=0.5 acceptance=1' <"$EVAL_OUT"; then
  good "human learn --status renders per-rule exposure and acceptance"
else
  bad "unexpected human learn --status output: $(cat "$EVAL_OUT")"
fi

eval_end
