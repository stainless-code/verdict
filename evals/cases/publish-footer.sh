#!/usr/bin/env bash
# Case publish-footer: footer receipt + attribution knobs + confidence basis.
#
# Grades (all hermetic — crafted bundles, stub gh, no network):
#   F1  footer fields on the script path: report --output carries tool
#       version, effort tier, summed cost, file count, and the review link
#       when a completed post record carries it.
#   F2  degraded reads: absent cost/post records render "unrecorded" and omit
#       the link (never blank, never fail); uncountable files read unrecorded.
#   F3  knob-off: publish.footer=false removes the block; =false attribution
#       keeps the receipt without the sentence; --no-footer wins anywhere.
#   F4  precedence: flags beat file beat env on every publish knob.
#   F5  unknown publish keys refuse typed bad-settings (06 idiom).
#   F6  custom attribution_text ({version} substituted) and copyright render.
#   B1  basis exactness on the script path: report --output Basis counts equal
#       the findings-store derivation (rigor/cited/verified).
#   B2  agent-path mismatch warns and keeps: a wrong Basis in review.json
#       exits 0 with a store-counts warning; a right one stays silent.
#   M1  the manual carries the one-sentence confidence metric definition.
#   P0  absent-file parity: no settings file keeps the gates byte-identical
#       (degenerate-body still refuses marker-less shorts, marker-first still
#       passes) while the rendered bytes gain the default footer block.
# Parity choice (one line): the footer is a new default surface, so parity
# means gate shape unchanged, not bytes — documented in docs/manual.md.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

VER="$(jq -r .version "$EVAL_ROOT/package.json")"
unset XDG_CONFIG_HOME || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION || true
unset REVIEW_PUBLISH_ATTRIBUTION_TEXT REVIEW_PUBLISH_COPYRIGHT || true

# --- F1 + B1: full store, exact footer and basis on the script path ---
PR=4501
dir="$EVAL_SANDBOX/.review/$PR"
mkdir -p "$dir"
cat > "$dir/PR.json" <<'EOF'
{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"teammate"},"files":[{"path":"a.txt","additions":10,"deletions":2},{"path":"b.txt","additions":3,"deletions":0},{"path":"c.txt","additions":0,"deletions":5},{"path":"d.txt","additions":1,"deletions":1}]}
EOF
cat > "$dir/full.diff" <<'EOF'
diff --git a/a.txt b/a.txt
index 1111111..2222222 100644
--- a/a.txt
+++ b/a.txt
@@ -1 +1,2 @@
 top
+line
EOF
cat > "$dir/findings.json" <<'EOF'
[{"kind":"inline","path":"a.txt","side":"RIGHT","line":2,"anchored":true,"body":"Repro-level breakage.","suggestion":null,"inBounds":true,"rigor":"reproduced","severity":"critical","kb":["check-error-paths"],"sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},{"kind":"inline","path":"b.txt","side":"RIGHT","line":2,"anchored":true,"body":"Ran once, failed once.","suggestion":null,"inBounds":true,"rigor":"reproduced","severity":"important","kb":["check-error-paths","verify-before-claim"],"sig":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"addressed","rationale":"","history":[]},{"kind":"inline","path":"c.txt","side":"RIGHT","line":1,"anchored":true,"body":"Ran clean.","suggestion":null,"inBounds":true,"rigor":"ran","severity":"informational","kb":[],"sig":"cccccccccccccccccccccccccccccccccccccccc","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},{"kind":"inline","path":"d.txt","side":"RIGHT","line":1,"anchored":true,"body":"Walked the trace.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"informational","kb":[],"sig":"dddddddddddddddddddddddddddddddddddddddd","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},{"kind":"inline","path":"a.txt","side":"RIGHT","line":3,"anchored":true,"body":"Pointed at the line.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},{"kind":"section","heading":"### note","anchored":"body","body":"### note","severity":null,"kb":[],"sig":"ffffffffffffffffffffffffffffffffffffffff","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
cat > "$dir/cost.json" <<'EOF'
{"schema_version":"1","effort":"standard","models":[{"name":"m","input_tokens":100,"output_tokens":50}],"est_usd":0.05}
EOF
jq -n --arg h "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" \
  '{schema_version:"1",review:{id:101,html_url:"https://example.invalid/r/101"},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:4,security_summary:"None."}' \
  > "$dir/post.json"
# sections carry no rigor key, so the store default (pointed) counts them too
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4501.md"
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "full-store report exits 0"
else
  bad "full-store report exits $EVAL_STATUS"
fi
if grep -Fq 'verdict `'"$VER"'` · standard tier · cost $0.05 · 4 files · [review `101`](https://example.invalid/r/101)' "$EVAL_SANDBOX/out-4501.md" \
  && grep -Fq 'Reviewed with verdict '"$VER" "$EVAL_SANDBOX/out-4501.md"; then
  good "footer carries version, tier, cost, files, review link, attribution"
else
  bad "footer fields wrong: $(sed -n '/^---$/,$p' "$EVAL_SANDBOX/out-4501.md" | head -c 400)"
fi
if grep -Fq '> Basis: 2 reproduced · 1 ran · 1 walked · 2 pointed · 0 said · 2 rules cited · 1 verified fixes' "$EVAL_SANDBOX/out-4501.md"; then
  good "basis counts exact on the script path"
else
  bad "basis line wrong: $(grep -h '^> Basis' "$EVAL_SANDBOX/out-4501.md" || echo MISSING)"
fi
if [ "$(grep -c '^> Basis' "$EVAL_SANDBOX/out-4501.md")" = "1" ] \
  && [ "$(sed -n '5p' "$EVAL_SANDBOX/out-4501.md" | cut -c1-8)" = "> Basis:" ]; then
  good "basis sits directly under the confidence lead"
else
  bad "basis placement drifted"
fi

# --- F2: degraded reads (no cost, no post, no file list, no diff) ---
PR=4502
dir="$EVAL_SANDBOX/.review/$PR"
mkdir -p "$dir"
printf '[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Degraded probe.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"1111111111111111111111111111111111111111","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]' > "$dir/findings.json"
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4502.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq 'cost unrecorded · files unrecorded' "$EVAL_SANDBOX/out-4502.md" \
  && ! grep -Fq '[review' "$EVAL_SANDBOX/out-4502.md" \
  && grep -Fq '> Basis: 0 reproduced · 0 ran · 0 walked · 1 pointed · 0 said · 0 rules cited · 0 verified fixes' "$EVAL_SANDBOX/out-4502.md"; then
  good "absent records degrade to unrecorded with no link, never fail"
else
  bad "degraded footer wrong (exit $EVAL_STATUS)"
fi

# --- F3: knob-off (file) ---
mkdir -p "$EVAL_SANDBOX/xdg-off/verdict"
printf '{"publish":{"footer":false}}' > "$EVAL_SANDBOX/xdg-off/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-off"
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4503.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && ! grep -Fq 'verdict `'"$VER"'`' "$EVAL_SANDBOX/out-4503.md" \
  && ! grep -Fq 'Reviewed with verdict' "$EVAL_SANDBOX/out-4503.md" \
  && grep -Fq '> Basis:' "$EVAL_SANDBOX/out-4503.md"; then
  good "publish.footer=false removes the block, basis stays (lead, not footer)"
else
  bad "footer-off file exits $EVAL_STATUS"
fi
printf '{"publish":{"attribution":false}}' > "$EVAL_SANDBOX/xdg-off/verdict/settings.json"
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4504.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq 'verdict `'"$VER"'` · standard tier' "$EVAL_SANDBOX/out-4504.md" \
  && ! grep -Fq 'Reviewed with verdict' "$EVAL_SANDBOX/out-4504.md"; then
  good "publish.attribution=false keeps the receipt without the sentence"
else
  bad "attribution-off file exits $EVAL_STATUS"
fi
unset XDG_CONFIG_HOME || true
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4505.md" --no-footer
if [ "$EVAL_STATUS" -eq 0 ] && ! grep -Fq 'verdict `'"$VER"'`' "$EVAL_SANDBOX/out-4505.md"; then
  good "--no-footer flag kills the block with no file present"
else
  bad "--no-footer exits $EVAL_STATUS"
fi

# --- F4: precedence flags > file > env ---
mkdir -p "$EVAL_SANDBOX/xdg-on/verdict"
printf '{"publish":{"footer":true}}' > "$EVAL_SANDBOX/xdg-on/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-on"
# shellcheck disable=SC2034
REVIEW_PUBLISH_FOOTER="false"
export REVIEW_PUBLISH_FOOTER
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4506.md"
if [ "$EVAL_STATUS" -eq 0 ] && grep -Fq 'verdict `'"$VER"'`' "$EVAL_SANDBOX/out-4506.md"; then
  good "file true wins over env false"
else
  bad "file-over-env gave exit $EVAL_STATUS"
fi
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4507.md" --no-footer
if [ "$EVAL_STATUS" -eq 0 ] && ! grep -Fq 'verdict `'"$VER"'`' "$EVAL_SANDBOX/out-4507.md"; then
  good "flag --no-footer wins over file true"
else
  bad "flag-over-file gave exit $EVAL_STATUS"
fi
unset XDG_CONFIG_HOME || true
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4508.md"
if [ "$EVAL_STATUS" -eq 0 ] && ! grep -Fq 'verdict `'"$VER"'`' "$EVAL_SANDBOX/out-4508.md"; then
  good "env false alone (no file) turns the footer off"
else
  bad "env alone gave exit $EVAL_STATUS"
fi
unset REVIEW_PUBLISH_FOOTER || true

# --- F5: unknown publish keys refuse (06 idiom) ---
mkdir -p "$EVAL_SANDBOX/xdg-bad/verdict"
printf '{"publish":{"motto":"ship it"}}' > "$EVAL_SANDBOX/xdg-bad/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-bad"
run_review check-tree --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-settings" ]; then
  good "unknown publish key refuses typed bad-settings"
else
  bad "unknown publish key gave exit $EVAL_STATUS"
fi
unset XDG_CONFIG_HOME || true

# --- F6: custom attribution text + copyright ---
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4509.md" --attribution-text "Custom stamped {version} words" --copyright "Acme Corp"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq "Custom stamped $VER words" "$EVAL_SANDBOX/out-4509.md" \
  && grep -Fq '> Acme Corp' "$EVAL_SANDBOX/out-4509.md"; then
  good "custom attribution_text substitutes {version}, copyright appends"
else
  bad "custom text exits $EVAL_STATUS"
fi

# --- B2: agent-path mismatch warns and keeps, never fails ---
PR=4510
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.\n> Basis: 9 reproduced · 9 ran · 9 walked · 9 pointed · 9 said · 9 rules cited · 9 verified fixes","approved":false,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "basis line counts (9 9 9 9 9 9 9) differ from the findings store (0 0 0 0 0 0 0)" "$EVAL_ERR" \
  && [ "$(jq -r '.data.body.footer // empty' <"$EVAL_OUT" 2>/dev/null | head -c 3)" = "---" ]; then
  good "wrong basis exits 0, warns with store counts, keeps the footer"
else
  bad "mismatch path gave exit $EVAL_STATUS"
fi
PR=4511
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.\n> Basis: 0 reproduced · 0 ran · 0 walked · 0 pointed · 0 said · 0 rules cited · 0 verified fixes","approved":false,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] && ! grep -q "basis" "$EVAL_ERR"; then
  good "right basis stays silent"
else
  bad "right basis warned (exit $EVAL_STATUS)"
fi

# --- M1: manual carries the one-sentence metric definition ---
if grep -Fq 'Confidence is the agent'"'"'s "would I merge on this review" judgment, informed by evidence depth, coverage, and rule support.' "$EVAL_ROOT/docs/manual.md"; then
  good "manual carries the confidence metric definition"
else
  bad "metric definition missing from docs/manual.md"
fi

# --- P0: absent-file parity (gates identical, bytes gain the default block) ---
unset XDG_CONFIG_HOME || true
PR=4512
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ No new issues found.","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.body.footer // empty' <"$EVAL_OUT" 2>/dev/null | grep -c 'Reviewed with verdict')" = "1" ]; then
  good "no settings file: marker-first passes and the default footer renders"
else
  bad "absent-file default gave exit $EVAL_STATUS"
fi
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"placeholder","approved":false,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "degenerate-body" ]; then
  good "no settings file: marker-less shorts still refuse as degenerate-body"
else
  bad "degenerate-body gate moved (exit $EVAL_STATUS)"
fi

# --- cleanup: no knob state leaks into later cases ---
unset XDG_CONFIG_HOME || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION || true
unset REVIEW_PUBLISH_ATTRIBUTION_TEXT REVIEW_PUBLISH_COPYRIGHT || true
rm -rf "$EVAL_SANDBOX/xdg-off" "$EVAL_SANDBOX/xdg-on" "$EVAL_SANDBOX/xdg-bad"

if [ "$fails" -ne 0 ]; then echo "case publish-footer: FAIL ($fails)"; exit 1; fi
echo "case publish-footer: PASS"
