#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

EVAL_SANDBOX="$(cd "$EVAL_SANDBOX" && pwd -P)"

mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_PANEL REVIEW_MODEL VERDICT_USAGE || true

cat > "$EVAL_SANDBOX/canned-PR.json" <<'EOF'
{"number":4701,"title":"canned","body":"","baseRefName":"main","headRefName":"feat","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"teammate"},"url":"https://example.invalid/4701","commits":[{"oid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","messageHeadline":"canned"}],"files":[{"path":"notes.txt","additions":1,"deletions":1}],"additions":1,"deletions":1,"isDraft":false,"reviewDecision":null,"state":"OPEN"}
EOF
cat > "$EVAL_SANDBOX/canned.diff" <<'EOF'
diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 top
-old line
+new line
 bottom
EOF
mkdir -p "$EVAL_SANDBOX/fakebin"
cat > "$EVAL_SANDBOX/fakebin/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
case "$*" in
  *--jq*) echo "notes.txt (+1/-1)" ;;
  "pr view "*) cat "$EVAL_SANDBOX/canned-PR.json" ;;
  "pr diff "*) cat "$EVAL_SANDBOX/canned.diff" ;;
  "api "*) echo "[]" ;;
  *) echo "canned-gh: unhandled call ($*)" >&2; exit 1 ;;
esac
EOF
chmod +x "$EVAL_SANDBOX/fakebin/gh"
fetch_pr() { # $1 = PR number, rest = extra args
  local pr="$1"
  shift
  EVAL_STUBBIN="$EVAL_SANDBOX/fakebin:$EVAL_STUBBIN" run_review fetch acme/app "$pr" "$@"
}

mkdir -p "$EVAL_SANDBOX/.verdict/state/1"
cat > "$EVAL_SANDBOX/.verdict/state/1/cost.json" <<'EOF'
{"schema_version":"1","effort":"standard","models":[],"est_usd":0.10}
EOF

cat > "$EVAL_SANDBOX/usage.json" <<'EOF'
{"schema_version":"1","source":"claude-code","model":"sonnet","tokens":{"input":3000,"output":170},"usd":2.50,"started_at":"2000-01-01T00:00:00Z","ended_at":"2099-01-01T00:00:00Z"}
EOF
mkdir -p "$EVAL_SANDBOX/usage-dir"
cat > "$EVAL_SANDBOX/usage-dir/opencode.json" <<'EOF'
{"schema_version":"1","source":"opencode","usd":2.50,"started_at":"2000-01-01T00:00:00Z","ended_at":"2099-01-01T00:00:00Z"}
EOF
cat > "$EVAL_SANDBOX/usage-tokens.json" <<'EOF'
{"schema_version":"1","source":"bench-harness","tokens":{"input":5000,"output":200},"started_at":"2000-01-01T00:00:00Z","ended_at":"2099-01-01T00:00:00Z"}
EOF

export VERDICT_USAGE="$EVAL_SANDBOX/usage.json"
export REVIEW_BUDGET_USD=1.00
fetch_pr 4701 --json
msg="$(jq -r '.error.message // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "budget-exceeded" ] \
  && grep -Fq '[measured from 1 harness session(s)]' "$EVAL_ERR" \
  && printf '%s' "$msg" | matches -F '[measured from 1 harness session(s)]'; then
  good "measured spend refuses a run the self-report would have blessed, and says measured"
else
  bad "measured refusal: exit $EVAL_STATUS msg=$(printf '%s' "$msg" | head -1) err=$(head -2 "$EVAL_ERR" | tr '\n' ' ')"
fi

cat > "$EVAL_SANDBOX/.verdict/state/1/cost.json" <<'EOF'
{"schema_version":"1","effort":"standard","models":[],"est_usd":5.00}
EOF
export REVIEW_BUDGET_USD=4.00
fetch_pr 4702 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.estimate.projected_usd' <"$EVAL_OUT" 2>/dev/null)" = "2.5" ] \
  && [ "$(jq -r '.data.estimate.basis' <"$EVAL_OUT" 2>/dev/null)" = "measured" ] \
  && [ "$(jq -c '.data.estimate.harness' <"$EVAL_OUT" 2>/dev/null)" = '["claude-code"]' ] \
  && [ "$(jq -r '.data.estimate.measured_runs' <"$EVAL_OUT" 2>/dev/null)" = "1" ] \
  && grep -Fq '[measured from 1 harness session(s)]' "$EVAL_ERR"; then
  good "handed usage blesses a run the self-report would have blocked, flagged in envelope and text"
else
  bad "measured pass: exit $EVAL_STATUS estimate=$(jq -c '.data.estimate // "null"' <"$EVAL_OUT" 2>/dev/null)"
fi

cat > "$EVAL_SANDBOX/.verdict/state/1/cost.json" <<'EOF'
{"schema_version":"1","effort":"standard","models":[],"est_usd":0.10}
EOF
unset VERDICT_USAGE
export REVIEW_BUDGET_USD=1.00
fetch_pr 4703 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.estimate.projected_usd' <"$EVAL_OUT" 2>/dev/null)" = "0.1" ] \
  && [ "$(jq -r '.data.estimate | has("basis") or has("harness") or has("measured_runs")' <"$EVAL_OUT" 2>/dev/null)" = "false" ] \
  && grep -Fq '» estimate: tier standard ~$0.1/run from 1 comparable run(s) — within budget $1.00 (REVIEW_BUDGET_USD)' "$EVAL_ERR"; then
  good "no handoff degrades to today's estimated surface with no measured claim"
else
  bad "estimated fallback: exit $EVAL_STATUS estimate=$(jq -c '.data.estimate // "null"' <"$EVAL_OUT" 2>/dev/null)"
fi

export VERDICT_USAGE="$EVAL_SANDBOX/usage-dir"
export REVIEW_BUDGET_USD=4.00
fetch_pr 4704 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.estimate.basis' <"$EVAL_OUT" 2>/dev/null)" = "measured" ] \
  && [ "$(jq -c '.data.estimate.harness' <"$EVAL_OUT" 2>/dev/null)" = '["opencode"]' ]; then
  good "handed directory joins and is named as the measured source"
else
  bad "directory join: exit $EVAL_STATUS estimate=$(jq -c '.data.estimate // "null"' <"$EVAL_OUT" 2>/dev/null)"
fi

export VERDICT_USAGE="$EVAL_SANDBOX/usage-tokens.json"
export REVIEW_BUDGET_USD=1.00
fetch_pr 4705 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.estimate.projected_usd' <"$EVAL_OUT" 2>/dev/null)" = "0.1" ] \
  && [ "$(jq -r '.data.estimate | has("basis")' <"$EVAL_OUT" 2>/dev/null)" = "false" ]; then
  good "unpriced usage degrades to the self-reported estimate, never measured"
else
  bad "tokens-only: exit $EVAL_STATUS estimate=$(jq -c '.data.estimate // "null"' <"$EVAL_OUT" 2>/dev/null)"
fi

cat > "$EVAL_SANDBOX/.verdict/state/1/cost.json" <<'EOF'
{"schema_version":"1","effort":"standard","models":[],"est_usd":0.10}
EOF
export VERDICT_USAGE="$EVAL_SANDBOX/usage.json"
export REVIEW_BUDGET_USD=1.00
fetch_pr 4706 --consent-over-budget --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.estimate.basis' <"$EVAL_OUT" 2>/dev/null)" = "measured" ] \
  && [ "$(jq -r '.[0].basis' "$EVAL_SANDBOX/.verdict/state/4706/budget.json" 2>/dev/null)" = "measured" ] \
  && [ "$(jq -r '.[0].measured_runs' "$EVAL_SANDBOX/.verdict/state/4706/budget.json" 2>/dev/null)" = "1" ]; then
  good "consented over-budget receipt preserves the measured basis"
else
  bad "receipt basis: exit $EVAL_STATUS receipt=$(jq -c '.' "$EVAL_SANDBOX/.verdict/state/4706/budget.json" 2>/dev/null || echo missing)"
fi

printf '{"schema_version":"1","source":"","usd":-2}' > "$EVAL_SANDBOX/usage-bad.json"
export VERDICT_USAGE="$EVAL_SANDBOX/usage-bad.json"
export REVIEW_BUDGET_USD=1.00
fetch_pr 4707 --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-usage" ] \
  && [ ! -e "$EVAL_SANDBOX/.verdict/state/4707" ] \
  && jq -e '.error.message | contains("try:")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "malformed handoff refuses typed with nothing written"
else
  bad "bad-usage refusal: exit $EVAL_STATUS code=$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)"
fi

export VERDICT_USAGE="$EVAL_SANDBOX/usage-absent.json"
export REVIEW_BUDGET_USD=1.00
fetch_pr 4708 --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-usage" ] \
  && [ ! -e "$EVAL_SANDBOX/.verdict/state/4708" ]; then
  good "missing handoff path refuses typed with nothing written"
else
  bad "missing-path refusal: exit $EVAL_STATUS code=$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)"
fi
unset VERDICT_USAGE

eval_end
