#!/usr/bin/env bash
# Case review-provenance: tool version, prompt-set hash, and model ride the
# post and calibration records, and calibrate/trend report them.
#
# Grades (all hermetic — crafted bundles, stub gh, no network):
#   C1  record capture: the record verb stamps record.json with the tool
#       version, the deterministic prompt-set hash, and the cost-record
#       model (null when no cost record names one).
#   C2  hash determinism: the stamped hash equals an independently computed
#       sha256 over the sorted prompt-set file names plus contents, and is
#       identical across verbs (record vs dry-run).
#   C3  dry-run surface: post --dry-run --json carries the same provenance
#       in data (the hermetic stand-in for post.json, which only real posts
#       write) and writes no post.json.
#   C4  legacy receipts: a pre-field post.json keeps reading exactly as
#       today — trend reports its provenance as unmarked, sums untouched.
#   C5  mixed-version reporting: trend names every tool version present, so
#       a mixed history never reads as one series (human line + --json).
#   C6  real-post capture: a stubbed real post writes post.json carrying the
#       same provenance plus the untouched receipt keys (stub overlay saved
#       and restored, per approve-recheck precedent).
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

HEADA="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
BASEB="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
EXPECTED_TV="$(jq -r .version "$EVAL_ROOT/package.json")"
EXPECTED_PH="$(for f in SKILL.md prompts/diagrams.md prompts/review.md prompts/system.md; do printf '== %s ==\n' "$f"; cat "$EVAL_ROOT/$f"; printf '\n'; done | shasum -a 256 | cut -d' ' -f1)"

mklocal() { # $1 = slug (without the local- prefix)
  local dir="$EVAL_SANDBOX/.review/local-$1"
  mkdir -p "$dir"
  cat > "$dir/source.json" <<EOF
{"head_sha":"$HEADA","base_sha":"$BASEB","head_ref":"feature"}
EOF
  cat > "$dir/full.diff" <<'EOF'
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
  cat > "$dir/review.json" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good overall — one nit below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.","rigor":"pointed","severity":"informational"}]}
EOF
}

# --- C1: record stamps provenance, model from the cost record ---
mklocal prov1
cat > "$EVAL_SANDBOX/.review/local-prov1/cost.json" <<'EOF'
{"schema_version":"1","effort":"standard","models":[{"name":"cost-model-1","input_tokens":100,"output_tokens":50}],"est_usd":0.05}
EOF
run_review record acme/app local-prov1 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.tool_version' "$EVAL_SANDBOX/.review/local-prov1/record.json" 2>/dev/null)" = "$EXPECTED_TV" ] \
  && [ "$(jq -r '.prompts_hash' "$EVAL_SANDBOX/.review/local-prov1/record.json" 2>/dev/null)" = "$EXPECTED_PH" ] \
  && [ "$(jq -r '.model' "$EVAL_SANDBOX/.review/local-prov1/record.json" 2>/dev/null)" = "cost-model-1" ] \
  && [ "$(jq -r '.schema_version' "$EVAL_SANDBOX/.review/local-prov1/record.json" 2>/dev/null)" = "1" ]; then
  good "record stamps tool version, prompt-set hash, and cost-record model"
else
  bad "record.json: $(cat "$EVAL_SANDBOX/.review/local-prov1/record.json" 2>/dev/null || echo MISSING) (exit $EVAL_STATUS)"
fi

# --- C1b: no cost record reads model null, never invented ---
mklocal prov2
run_review record acme/app local-prov2 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.tool_version' "$EVAL_SANDBOX/.review/local-prov2/record.json" 2>/dev/null)" = "$EXPECTED_TV" ] \
  && [ "$(jq -r '.prompts_hash' "$EVAL_SANDBOX/.review/local-prov2/record.json" 2>/dev/null)" = "$EXPECTED_PH" ] \
  && jq -e '.model == null' "$EVAL_SANDBOX/.review/local-prov2/record.json" >/dev/null 2>&1; then
  good "record without a cost record stamps model null with the same hash"
else
  bad "model-null record.json: $(cat "$EVAL_SANDBOX/.review/local-prov2/record.json" 2>/dev/null || echo MISSING) (exit $EVAL_STATUS)"
fi

# --- C3: dry-run envelope carries the same provenance, writes no post.json ---
eval_mk_bundle 4921
eval_write_review 4921 <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good overall — one nit below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.","rigor":"pointed","severity":"informational"}]}
EOF
cat > "$EVAL_SANDBOX/.review/4921/cost.json" <<'EOF'
{"schema_version":"1","effort":"standard","models":[{"name":"dry-model","input_tokens":10,"output_tokens":5}],"est_usd":0.01}
EOF
run_review post acme/app 4921 --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.provenance.tool_version' <"$EVAL_OUT" 2>/dev/null)" = "$EXPECTED_TV" ] \
  && [ "$(jq -r '.data.provenance.prompts_hash' <"$EVAL_OUT" 2>/dev/null)" = "$EXPECTED_PH" ] \
  && [ "$(jq -r '.data.provenance.model' <"$EVAL_OUT" 2>/dev/null)" = "dry-model" ] \
  && [ ! -f "$EVAL_SANDBOX/.review/4921/post.json" ]; then
  good "dry-run envelope carries record-identical provenance and writes no post.json"
else
  bad "dry-run provenance: $(jq -c '.data.provenance // "absent"' <"$EVAL_OUT" 2>/dev/null) (exit $EVAL_STATUS)"
fi
rm -rf "$EVAL_SANDBOX/.review/4921" "$EVAL_SANDBOX/.review/local-prov2"

# Cases share one sandbox: stash bundles other cases left behind so the
# trend rollup below grades exactly this case's fixtures (restored on exit,
# even on failure — later cases must see their own stores untouched).
# The stub overlay for C6 is saved/restored the same way.
HOLD="$EVAL_SANDBOX/.review-hold-prov"
STUB_SAVED="$EVAL_SANDBOX/gh-stub-saved-prov"
mkdir -p "$EVAL_SANDBOX/.review" "$HOLD"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
for e in "$EVAL_SANDBOX"/.review/*; do
  [ -e "$e" ] || continue
  mv "$e" "$HOLD/"
done
mv "$HOLD/local-prov1" "$EVAL_SANDBOX/.review/local-prov1"
restore_prov() {
  cp "$STUB_SAVED" "$EVAL_STUBBIN/gh" 2>/dev/null || true
  rm -f "$STUB_SAVED"
  for e in "$HOLD"/*; do
    [ -e "$e" ] || continue
    mv "$e" "$EVAL_SANDBOX/.review/" 2>/dev/null || true
  done
  rmdir "$HOLD" 2>/dev/null || true
}
trap restore_prov EXIT

# --- C4/C5 fixtures: legacy receipt (no fields) + older-version receipt ---
eval_mk_bundle 4922
eval_write_findings 4922 <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Consider clarifying this line.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"0123456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
cat > "$EVAL_SANDBOX/.review/4922/post.json" <<'EOF'
{"schema_version":"1","review":{"id":1},"head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","posted_at":"2026-01-01T00:00:00Z"}
EOF
mkdir -p "$EVAL_SANDBOX/.review/4923"
eval_write_findings 4923 <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Consider clarifying this line.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"0123456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"addressed","rationale":"","history":[]}]
EOF
cat > "$EVAL_SANDBOX/.review/4923/record.json" <<'EOF'
{"schema_version":"1","status":"local","head":"dirty","recorded_at":"2026-02-01T00:00:00Z","findings":1,"open":0,"tool_version":"0.1.0","prompts_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","model":"old-model"}
EOF

# --- C4: legacy receipt reads as today, provenance unmarked ---
run_review trend acme/app --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.per_bundle[] | select(.bundle == "4922") | .provenance.tool_version' <"$EVAL_OUT" 2>/dev/null)" = "null" ] \
  && [ "$(jq -r '.data.per_bundle[] | select(.bundle == "4922") | .provenance.model' <"$EVAL_OUT" 2>/dev/null)" = "null" ] \
  && [ "$(jq -r '.data.findings' <"$EVAL_OUT" 2>/dev/null)" = "3" ] \
  && [ "$(jq -r '.data.open' <"$EVAL_OUT" 2>/dev/null)" = "2" ]; then
  good "legacy receipt keeps today's sums with null (unmarked) provenance"
else
  bad "legacy trend: $(jq -c '.data.per_bundle // "absent"' <"$EVAL_OUT" 2>/dev/null) (exit $EVAL_STATUS)"
fi

# --- C5: mixed versions read as mixed, never one series ---
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.per_bundle[] | select(.bundle == "local-prov1") | .provenance.tool_version' <"$EVAL_OUT" 2>/dev/null)" = "$EXPECTED_TV" ] \
  && [ "$(jq -r '.data.per_bundle[] | select(.bundle == "4923") | .provenance.tool_version' <"$EVAL_OUT" 2>/dev/null)" = "0.1.0" ] \
  && [ "$(jq -r '.data.provenance.tools | length' <"$EVAL_OUT" 2>/dev/null)" = "3" ]; then
  good "per-bundle rows keep each run's own tool version in the rollup"
else
  bad "mixed rollup: $(jq -c '.data.provenance // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi
run_review trend acme/app
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq "provenance: tools=" "$EVAL_OUT" \
  && grep -Fq "$EXPECTED_TV" "$EVAL_OUT" \
  && grep -Fq "0.1.0" "$EVAL_OUT" \
  && grep -Fq "unmarked" "$EVAL_OUT"; then
  good "human trend names every tool version present, legacy included"
else
  bad "human trend exits $EVAL_STATUS: $(grep -h 'provenance:' "$EVAL_OUT" || echo MISSING)"
fi

# --- C6: a real (stubbed) post writes post.json with provenance intact ---
# Overlay a reviews-succeeding stub (dead stub answers nothing): the drift
# lookup and the reviews POST succeed, everything else still fails.
cat > "$EVAL_STUBBIN/gh" <<EOF
#!/usr/bin/env bash
echo "stub-gh \$*" >> "${EVAL_STUBLOG:-/dev/null}"
case "\$*" in
  *reviews*)
    printf '%s' '{"id":101,"state":"commented","html_url":"https://example.invalid/r/101","submitted_at":"2026-01-01T00:00:00Z"}'
    exit 0;;
  *pulls*)
    printf '%s' "$HEADA"; exit 0;;
esac
echo "evals-stub-gh: network disabled (\$*)" >&2
exit 1
EOF
chmod +x "$EVAL_STUBBIN/gh"
eval_mk_bundle 4924
eval_write_review 4924 <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good overall — one nit below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.","rigor":"pointed","severity":"informational"}]}
EOF
cat > "$EVAL_SANDBOX/.review/4924/cost.json" <<'EOF'
{"schema_version":"1","effort":"standard","models":[{"name":"post-model","input_tokens":10,"output_tokens":5}],"est_usd":0.01}
EOF
run_review post acme/app 4924 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.head' "$EVAL_SANDBOX/.review/4924/post.json" 2>/dev/null)" = "$HEADA" ] \
  && [ "$(jq -r '.review.id' "$EVAL_SANDBOX/.review/4924/post.json" 2>/dev/null)" = "101" ] \
  && [ "$(jq -r '.tool_version' "$EVAL_SANDBOX/.review/4924/post.json" 2>/dev/null)" = "$EXPECTED_TV" ] \
  && [ "$(jq -r '.prompts_hash' "$EVAL_SANDBOX/.review/4924/post.json" 2>/dev/null)" = "$EXPECTED_PH" ] \
  && [ "$(jq -r '.model' "$EVAL_SANDBOX/.review/4924/post.json" 2>/dev/null)" = "post-model" ] \
  && [ "$(jq -r '.data.provenance.model' <"$EVAL_OUT" 2>/dev/null)" = "post-model" ] \
  && [ "$(jq -r '.data.dry_run' <"$EVAL_OUT" 2>/dev/null)" = "false" ]; then
  good "real post writes post.json with provenance beside the receipt keys"
else
  bad "real post exits $EVAL_STATUS: $(cat "$EVAL_SANDBOX/.review/4924/post.json" 2>/dev/null || echo MISSING)"
fi

# --- cleanup: fixtures stay out of later cases, knob state goes ---
rm -rf "$EVAL_SANDBOX/.review/local-prov1" "$EVAL_SANDBOX/.review/4922" \
  "$EVAL_SANDBOX/.review/4923" "$EVAL_SANDBOX/.review/4924" "$EVAL_SANDBOX/xdg-empty"
unset XDG_CONFIG_HOME || true

if [ "$fails" -ne 0 ]; then echo "case review-provenance: FAIL ($fails)"; exit 1; fi
echo "case review-provenance: PASS"
