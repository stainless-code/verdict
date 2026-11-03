#!/usr/bin/env bash
# Case real-usage-cost: the spend budget gates on measured harness usage
# (local-only session files) with the self-reported cost record as fallback.
#
# Grades (all hermetic — crafted bundles, canned gh, local session fixtures):
#   U1  measured over budget while the self-report was under: the gate
#       refuses, and the refusal names the measured basis (so a guessed
#       number never silently blocks).
#   U2  measured under budget while the self-report was over: the gate
#       passes and data.estimate carries basis/harness/measured_runs
#       (so a guessed number never silently blesses either).
#   U3  no harness data: today's byte-surface — no basis key, the legacy
#       estimated line, and the self-reported decision.
#   U4  opencode export via VERDICT_HARNESS_USAGE feeds the same join.
#   U5  tokens-only session (no priced total): honest fallback to the
#       self-report, never reported as measured.
#   U6  a consented over-budget measured run records the basis in the
#       budget.json receipt.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

# Normalize the sandbox path: mktemp can leave a double slash when TMPDIR
# ends in one, while the CLI's cwd (and Claude Code's project slug) does not.
EVAL_SANDBOX="$(cd "$EVAL_SANDBOX" && pwd -P)"

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
unset REVIEW_PANEL REVIEW_MODEL VERDICT_HARNESS_USAGE || true

# Canned provider responses: the gate runs before any gh call, but the
# within-budget legs continue into the normal fetch.
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

# The measured history anchor: one past run whose self-report stays low.
mkdir -p "$EVAL_SANDBOX/.review/1"
cat > "$EVAL_SANDBOX/.review/1/cost.json" <<'EOF'
{"schema_version":"1","effort":"standard","models":[],"est_usd":0.10}
EOF

# Claude Code transcript fixtures, discovered under CLAUDE_CONFIG_DIR. The
# project slug replaces every non-alphanumeric byte with a dash.
CLAUDE_HOME="$EVAL_SANDBOX/claude"
SLUG="$(printf '%s' "$EVAL_SANDBOX" | sed 's/[^A-Za-z0-9-]/-/g')"
PROJECT="$CLAUDE_HOME/projects/$SLUG"
mkdir -p "$PROJECT" "$EVAL_SANDBOX/no-harness"
NOW_MS=$(( $(date +%s) * 1000 ))
START_MS=$(( NOW_MS - 600000 ))
cat > "$PROJECT/session.jsonl" <<EOF
{"type":"assistant","sessionId":"sess-a","cwd":"$EVAL_SANDBOX","timestamp":"2000-01-01T00:00:00Z","requestId":"r1","costUSD":2.50}
{"type":"assistant","sessionId":"sess-a","cwd":"$EVAL_SANDBOX","timestamp":"2099-01-01T00:00:00Z"}
{"type":"cost-state","sessionId":"sess-a","cwd":"$EVAL_SANDBOX","totalCostUSD":2.50,"startTime":$START_MS}
EOF
TOKENS_ONLY="$EVAL_SANDBOX/tokens-project"
TOKENS_PROJECT="$TOKENS_ONLY/projects/$SLUG"
mkdir -p "$TOKENS_PROJECT"
cat > "$TOKENS_PROJECT/tokens-only.jsonl" <<EOF
{"type":"assistant","sessionId":"sess-t","cwd":"$EVAL_SANDBOX","timestamp":"2000-01-01T00:00:00Z","message":{"id":"m1","usage":{"input_tokens":1000,"output_tokens":50}}}
{"type":"assistant","sessionId":"sess-t","cwd":"$EVAL_SANDBOX","timestamp":"2099-01-01T00:00:00Z","message":{"id":"m2","usage":{"input_tokens":1000,"output_tokens":50}}}
EOF

# An opencode `opencode export` fixture: the session total plus its window.
cat > "$EVAL_SANDBOX/opencode-session.json" <<EOF
{"info":{"id":"ses_oc","directory":"$EVAL_SANDBOX","cost":2.50,
  "time":{"created":0,"updated":4102444800000}},"messages":[]}
EOF

# U1: self-report says $0.10, measured session says $2.50, budget $1.
export CLAUDE_CONFIG_DIR="$CLAUDE_HOME"
export REVIEW_BUDGET_USD=1.00
fetch_pr 4701 --json
msg="$(jq -r '.error.message // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "budget-exceeded" ] \
  && grep -Fq '[measured from 1 harness session(s)]' "$EVAL_ERR" \
  && printf '%s' "$msg" | grep -Fq '[measured from 1 harness session(s)]'; then
  good "measured spend refuses a run the self-report would have blessed, and says measured"
else
  bad "measured refusal: exit $EVAL_STATUS msg=$(printf '%s' "$msg" | head -1) err=$(head -2 "$EVAL_ERR" | tr '\n' ' ')"
fi

# U2: self-report says $5.00, measured session says $2.50, budget $4.
cat > "$EVAL_SANDBOX/.review/1/cost.json" <<'EOF'
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
  good "measured spend blesses a run the self-report would have blocked, flagged in envelope and text"
else
  bad "measured pass: exit $EVAL_STATUS estimate=$(jq -c '.data.estimate // "null"' <"$EVAL_OUT" 2>/dev/null)"
fi

# U3: no harness data at all: the legacy estimated surface is byte-identical
# (no basis/harness/measured_runs keys) and the self-report decides.
cat > "$EVAL_SANDBOX/.review/1/cost.json" <<'EOF'
{"schema_version":"1","effort":"standard","models":[],"est_usd":0.10}
EOF
export CLAUDE_CONFIG_DIR="$EVAL_SANDBOX/no-harness"
export REVIEW_BUDGET_USD=1.00
fetch_pr 4703 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.estimate.projected_usd' <"$EVAL_OUT" 2>/dev/null)" = "0.1" ] \
  && [ "$(jq -r '.data.estimate | has("basis") or has("harness") or has("measured_runs")' <"$EVAL_OUT" 2>/dev/null)" = "false" ] \
  && grep -Fq '» estimate: tier standard ~$0.1/run from 1 comparable run(s) — within budget $1.00 (REVIEW_BUDGET_USD)' "$EVAL_ERR"; then
  good "no harness degrades to today's estimated surface with no measured claim"
else
  bad "estimated fallback: exit $EVAL_STATUS estimate=$(jq -c '.data.estimate // "null"' <"$EVAL_OUT" 2>/dev/null)"
fi

# U4: an explicit opencode export feeds the same join.
export CLAUDE_CONFIG_DIR="$EVAL_SANDBOX/no-harness"
export VERDICT_HARNESS_USAGE="$EVAL_SANDBOX/opencode-session.json"
export REVIEW_BUDGET_USD=4.00
fetch_pr 4704 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.estimate.basis' <"$EVAL_OUT" 2>/dev/null)" = "measured" ] \
  && [ "$(jq -c '.data.estimate.harness' <"$EVAL_OUT" 2>/dev/null)" = '["opencode"]' ]; then
  good "opencode session export joins and is named as the measured source"
else
  bad "opencode join: exit $EVAL_STATUS estimate=$(jq -c '.data.estimate // "null"' <"$EVAL_OUT" 2>/dev/null)"
fi
unset VERDICT_HARNESS_USAGE

# U5: tokens without a priced total fall back honestly, never as measured.
export CLAUDE_CONFIG_DIR="$TOKENS_ONLY"
export REVIEW_BUDGET_USD=1.00
fetch_pr 4705 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.estimate.projected_usd' <"$EVAL_OUT" 2>/dev/null)" = "0.1" ] \
  && [ "$(jq -r '.data.estimate | has("basis")' <"$EVAL_OUT" 2>/dev/null)" = "false" ]; then
  good "tokens-only harness data degrades to the self-reported estimate, never measured"
else
  bad "tokens-only: exit $EVAL_STATUS estimate=$(jq -c '.data.estimate // "null"' <"$EVAL_OUT" 2>/dev/null)"
fi

# U6: consent over a measured projection records the measured basis.
cat > "$EVAL_SANDBOX/.review/1/cost.json" <<'EOF'
{"schema_version":"1","effort":"standard","models":[],"est_usd":0.10}
EOF
export CLAUDE_CONFIG_DIR="$CLAUDE_HOME"
export REVIEW_BUDGET_USD=1.00
fetch_pr 4706 --consent-over-budget --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.estimate.basis' <"$EVAL_OUT" 2>/dev/null)" = "measured" ] \
  && [ "$(jq -r '.[0].basis' "$EVAL_SANDBOX/.review/4706/budget.json" 2>/dev/null)" = "measured" ] \
  && [ "$(jq -r '.[0].measured_runs' "$EVAL_SANDBOX/.review/4706/budget.json" 2>/dev/null)" = "1" ]; then
  good "consented over-budget receipt preserves the measured basis"
else
  bad "receipt basis: exit $EVAL_STATUS receipt=$(jq -c '.' "$EVAL_SANDBOX/.review/4706/budget.json" 2>/dev/null || echo missing)"
fi

exit "$fails"
