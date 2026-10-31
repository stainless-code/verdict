#!/usr/bin/env bash
# Case publish-footer: footer receipt + leveled attribution + logo + divider + confidence basis.
#
# Grades (all hermetic — crafted bundles, stub gh, no network):
#   F1  footer fields on the script path: report --output carries the
#       golden default line (product-linked logo + linked bold name with
#       inline version + receipt fields), tier, summed cost, file count,
#       and the review link when a completed post record carries it.
#       No bracket/paren version anywhere (link-text collision guard).
#   F2  degraded reads: absent cost/post records render "unrecorded" and omit
#       the link (never blank, never fail); uncountable files read unrecorded.
#   F3  knob-off: publish.footer=false removes the block; attribution "off"
#       keeps the receipt without the sentence; --no-footer wins anywhere.
#       The day-old P1 attribution bool is refused (enum migration).
#   F4  precedence: flags beat file beat env on every publish knob.
#   F5  unknown publish keys refuse typed bad-settings (06 idiom).
#   F6  custom attribution_text ({version}/{tier}/{product_url} substituted)
#       and copyright render; unknown placeholders refuse typed.
#   A2  level matrix: off (receipt only) / product (default golden line,
#       no maker) / full (appends the maker mention).
#   A3  logo matrix: on (default asset) / off / custom URL / empty URL
#       (text-only, never a broken image); --no-logo flag wins.
#   A4  divider idempotency: machine comment separates content from footer;
#       report twice is byte-identical; a stale rendered block is
#       strip-and-replaced (same body chars as the clean body, one marker).
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
PRODUCT_URL="https://stainless-code.com/verdict"
PRODUCT_LINK="Reviewed with [**Verdict $VER**]($PRODUCT_URL)"
GOLDEN_LINE='> <a href="https://stainless-code.com/verdict"><img src="https://raw.githubusercontent.com/stainless-code/verdict/main/apps/docs/public/logo.svg" alt="Verdict" width="18" height="18"></a> '"$PRODUCT_LINK"' · standard tier · cost $0.05 · 4 files · [review `101`](https://example.invalid/r/101)'
unset XDG_CONFIG_HOME || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION || true
unset REVIEW_PUBLISH_ATTRIBUTION_TEXT REVIEW_PUBLISH_COPYRIGHT || true
unset REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL || true

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
if grep -Fq "$GOLDEN_LINE" "$EVAL_SANDBOX/out-4501.md" \
  && grep -Fq '<!-- verdict-footer -->' "$EVAL_SANDBOX/out-4501.md"; then
  good "footer carries the golden default line (logo, linked name, receipt, link)"
else
  bad "footer fields wrong: $(sed -n '/^---$/,$p' "$EVAL_SANDBOX/out-4501.md" | head -c 600)"
fi
if ! grep -Fq "Verdict [" "$EVAL_SANDBOX/out-4501.md" \
  && ! grep -Fq "Verdict (" "$EVAL_SANDBOX/out-4501.md"; then
  good "version stays inline space-separated, never brackets or parens"
else
  bad "bracket/paren version leaked into the footer"
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
  && ! grep -Fq '<!-- verdict-footer -->' "$EVAL_SANDBOX/out-4503.md" \
  && ! grep -Fq 'Reviewed with' "$EVAL_SANDBOX/out-4503.md" \
  && grep -Fq '> Basis:' "$EVAL_SANDBOX/out-4503.md"; then
  good "publish.footer=false removes the block, basis stays (lead, not footer)"
else
  bad "footer-off file exits $EVAL_STATUS"
fi
printf '{"publish":{"attribution":"off"}}' > "$EVAL_SANDBOX/xdg-off/verdict/settings.json"
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4504.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq '> standard tier · cost $0.05 · 4 files' "$EVAL_SANDBOX/out-4504.md" \
  && ! grep -Fq 'Reviewed with' "$EVAL_SANDBOX/out-4504.md" \
  && ! grep -Fq '<img' "$EVAL_SANDBOX/out-4504.md"; then
  good "publish.attribution=off keeps the receipt without the sentence or logo"
else
  bad "attribution-off file exits $EVAL_STATUS"
fi
printf '{"publish":{"attribution":false}}' > "$EVAL_SANDBOX/xdg-off/verdict/settings.json"
run_review check-tree --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-settings" ] \
  && grep -Fq 'off|product|full' "$EVAL_OUT"; then
  good "day-old P1 attribution bool refuses with the enum migration named"
else
  bad "bool migration gave exit $EVAL_STATUS"
fi
unset XDG_CONFIG_HOME || true
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4505.md" --no-footer
if [ "$EVAL_STATUS" -eq 0 ] && ! grep -Fq '<!-- verdict-footer -->' "$EVAL_SANDBOX/out-4505.md"; then
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
if [ "$EVAL_STATUS" -eq 0 ] && grep -Fq "$GOLDEN_LINE" "$EVAL_SANDBOX/out-4506.md"; then
  good "file true wins over env false"
else
  bad "file-over-env gave exit $EVAL_STATUS"
fi
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4507.md" --no-footer
if [ "$EVAL_STATUS" -eq 0 ] && ! grep -Fq '<!-- verdict-footer -->' "$EVAL_SANDBOX/out-4507.md"; then
  good "flag --no-footer wins over file true"
else
  bad "flag-over-file gave exit $EVAL_STATUS"
fi
unset XDG_CONFIG_HOME || true
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4508.md"
if [ "$EVAL_STATUS" -eq 0 ] && ! grep -Fq '<!-- verdict-footer -->' "$EVAL_SANDBOX/out-4508.md"; then
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
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4510.md" --attribution-text "Stamp {version} on {tier} via {product_url}"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq "Stamp $VER on standard via https://stainless-code.com/verdict" "$EVAL_SANDBOX/out-4510.md"; then
  good "attribution_text substitutes the {version}/{tier}/{product_url} inventory"
else
  bad "placeholder inventory exits $EVAL_STATUS"
fi
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4511.md" --attribution-text "Typo {bogus} here"
if [ "$EVAL_STATUS" -ne 0 ]; then
  good "unknown placeholder in flag text refuses instead of rendering literally"
else
  bad "unknown flag placeholder rendered (exit $EVAL_STATUS)"
fi
mkdir -p "$EVAL_SANDBOX/xdg-typo/verdict"
printf '{"publish":{"attribution_text":"Typo {bogus} here"}}' > "$EVAL_SANDBOX/xdg-typo/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-typo"
run_review check-tree --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-settings" ]; then
  good "unknown placeholder in file text refuses typed bad-settings"
else
  bad "unknown file placeholder gave exit $EVAL_STATUS"
fi
unset XDG_CONFIG_HOME || true

# --- A2: level matrix (off / product / full) ---
mkdir -p "$EVAL_SANDBOX/xdg-lvl/verdict"
printf '{"publish":{"attribution":"product"}}' > "$EVAL_SANDBOX/xdg-lvl/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-lvl"
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4520.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq "$GOLDEN_LINE" "$EVAL_SANDBOX/out-4520.md" \
  && ! grep -Fq 'Stainless Code](' "$EVAL_SANDBOX/out-4520.md"; then
  good "explicit product level renders the golden line with no maker"
else
  bad "product level gave exit $EVAL_STATUS"
fi
printf '{"publish":{"attribution":"full"}}' > "$EVAL_SANDBOX/xdg-lvl/verdict/settings.json"
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4521.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq "by [Stainless Code](https://stainless-code.com) · standard tier" "$EVAL_SANDBOX/out-4521.md" \
  && grep -Fq "$PRODUCT_LINK" "$EVAL_SANDBOX/out-4521.md"; then
  good "full level appends the maker mention after the linked name"
else
  bad "full level gave exit $EVAL_STATUS"
fi
unset XDG_CONFIG_HOME || true
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4522.md" --no-attribution
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq '> standard tier · cost $0.05' "$EVAL_SANDBOX/out-4522.md" \
  && ! grep -Fq 'Reviewed with' "$EVAL_SANDBOX/out-4522.md"; then
  good "--no-attribution flag keeps the receipt without the sentence"
else
  bad "--no-attribution gave exit $EVAL_STATUS"
fi
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4523.md" --attribution full
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq "by [Stainless Code](https://stainless-code.com)" "$EVAL_SANDBOX/out-4523.md"; then
  good "--attribution full flag reaches the maker level"
else
  bad "--attribution full gave exit $EVAL_STATUS"
fi

# --- A3: logo matrix (on / off / custom URL / empty URL) ---
mkdir -p "$EVAL_SANDBOX/xdg-logo/verdict"
printf '{"publish":{"logo":false}}' > "$EVAL_SANDBOX/xdg-logo/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-logo"
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4530.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && ! grep -Fq '<img' "$EVAL_SANDBOX/out-4530.md" \
  && grep -Fq "$PRODUCT_LINK" "$EVAL_SANDBOX/out-4530.md"; then
  good "publish.logo=false drops the image, keeps the linked sentence"
else
  bad "logo-off file exits $EVAL_STATUS"
fi
printf '{"publish":{"logo_url":"https://example.invalid/custom.svg"}}' > "$EVAL_SANDBOX/xdg-logo/verdict/settings.json"
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4531.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq '<img src="https://example.invalid/custom.svg"' "$EVAL_SANDBOX/out-4531.md"; then
  good "custom logo_url renders its own src"
else
  bad "custom logo exits $EVAL_STATUS"
fi
printf '{"publish":{"logo_url":""}}' > "$EVAL_SANDBOX/xdg-logo/verdict/settings.json"
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4532.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && ! grep -Fq '<img' "$EVAL_SANDBOX/out-4532.md" \
  && grep -Fq "$PRODUCT_LINK" "$EVAL_SANDBOX/out-4532.md"; then
  good "empty logo_url degrades to text-only, never a broken image"
else
  bad "empty logo exits $EVAL_STATUS"
fi
unset XDG_CONFIG_HOME || true
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4533.md" --no-logo
if [ "$EVAL_STATUS" -eq 0 ] \
  && ! grep -Fq '<img' "$EVAL_SANDBOX/out-4533.md" \
  && grep -Fq "$PRODUCT_LINK" "$EVAL_SANDBOX/out-4533.md"; then
  good "--no-logo flag drops the image"
else
  bad "--no-logo gave exit $EVAL_STATUS"
fi

# --- A4: divider idempotency (strip-and-replace, byte-identical re-render) ---
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4540a.md"
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4540b.md"
if cmp -s "$EVAL_SANDBOX/out-4540a.md" "$EVAL_SANDBOX/out-4540b.md" \
  && [ "$(grep -c '<!-- verdict-footer -->' "$EVAL_SANDBOX/out-4540a.md")" = "1" ]; then
  good "report renders twice byte-identical with exactly one divider"
else
  bad "report re-render drifted"
fi
PR=4513
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.","approved":false,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run
clean_chars=$(grep -o 'body chars: [0-9]*' "$EVAL_OUT" | grep -o '[0-9]*')
STALE_BODY='> ✅ Looks fine.

---

<!-- verdict-footer -->
> <a href="https://stainless-code.com/verdict"><img src="https://example.invalid/old.svg" alt="Verdict" width="18" height="18"></a> Reviewed with [**Verdict 9.9.9**](https://stainless-code.com/verdict) · standard tier · cost unrecorded · files unrecorded'
jq -n --arg b "$STALE_BODY" '{schema_version:"1",body:$b,approved:false,comments:[]}' \
  > "$EVAL_SANDBOX/.review/$PR/review.json"
run_review post acme/app "$PR" --dry-run
stale_chars=$(grep -o 'body chars: [0-9]*' "$EVAL_OUT" | grep -o '[0-9]*')
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ -n "$clean_chars" ] && [ "$clean_chars" = "$stale_chars" ] \
  && [ "$(grep -c '<!-- verdict-footer -->' "$EVAL_OUT")" = "1" ] \
  && grep -Fq "Verdict $VER" "$EVAL_OUT" \
  && ! grep -Fq 'Verdict 9.9.9' "$EVAL_OUT" \
  && grep -Fq 'replacing it with the canonical block' "$EVAL_ERR"; then
  good "stale rendered block is strip-and-replaced (same body chars, one marker, fresh version)"
else
  bad "divider idempotency broke (clean=$clean_chars stale=$stale_chars exit $EVAL_STATUS)"
fi

# --- B2: agent-path mismatch warns and keeps, never fails ---
PR=4510
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.\n> Basis: 9 reproduced · 9 ran · 9 walked · 9 pointed · 9 said · 9 rules cited · 9 verified fixes","approved":false,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "basis line counts (9 9 9 9 9 9 9) differ from this review's comments (0 0 0 0 0 0 0)" "$EVAL_ERR" \
  && [ "$(jq -r '.data.body.footer // empty' <"$EVAL_OUT" 2>/dev/null | head -c 3)" = "---" ]; then
  good "wrong basis exits 0, warns with review counts, keeps the footer"
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

# --- B3: re-review with carried-over findings never warns on its own counts ---
# Store holds 2 open pointed findings from a prior run; the current review
# carries 1 pointed comment (rigor omitted, default pointed) with a matching
# Basis line. The validator compares against the review's own comments, so the
# carried-over pair stays silent. The control miscounts its own comment and warns.
PR=4515
eval_mk_bundle "$PR"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Carried-over one.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},{"kind":"inline","path":"notes.txt","side":"RIGHT","line":3,"anchored":true,"body":"Carried-over two.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.\n> Basis: 0 reproduced · 0 ran · 0 walked · 1 pointed · 0 said · 0 rules cited · 0 verified fixes","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Fresh probe comment."}]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] && ! grep -q "basis line counts" "$EVAL_ERR"; then
  good "re-review matching its own comment stays silent despite carried-over store"
else
  bad "re-review warned (exit $EVAL_STATUS)"
fi
PR=4516
eval_mk_bundle "$PR"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Carried-over one.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},{"kind":"inline","path":"notes.txt","side":"RIGHT","line":3,"anchored":true,"body":"Carried-over two.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.\n> Basis: 0 reproduced · 0 ran · 0 walked · 2 pointed · 0 said · 0 rules cited · 0 verified fixes","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Fresh probe comment."}]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "basis line counts (0 0 0 2 0 0 0) differ from this review's comments (0 0 0 1 0 0 0)" "$EVAL_ERR"; then
  good "re-review miscounting its own comment warns with review counts"
else
  bad "re-review control gave exit $EVAL_STATUS"
fi

# --- F7: hand-written footer (legacy + v2) warns and keeps, never stacks ---
# Skip is observable in human dry-run preflight body-chars: skipped means the
# payload body is byte-identical to the agent body, so chars equal printf length.
L1='> ✅ Looks fine.'
L2='> Basis: 0 reproduced · 0 ran · 0 walked · 0 pointed · 0 said · 0 rules cited · 0 verified fixes'
PR=4513
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.\n> Basis: 0 reproduced · 0 ran · 0 walked · 0 pointed · 0 said · 0 rules cited · 0 verified fixes\n> [<img src=\"https://example.invalid/logo.svg\" width=\"18\" height=\"18\" alt=\"Verdict\">](https://stainless-code.com/verdict) Reviewed with [**Verdict 0.3.0**](https://stainless-code.com/verdict) · standard tier · cost unrecorded","approved":false,"comments":[]}
EOF
L3='> [<img src="https://example.invalid/logo.svg" width="18" height="18" alt="Verdict">](https://stainless-code.com/verdict) Reviewed with [**Verdict 0.3.0**](https://stainless-code.com/verdict) · standard tier · cost unrecorded'
WANT="$(printf '%s\n%s\n%s' "$L1" "$L2" "$L3" | wc -c | tr -d ' ')"
run_review post acme/app "$PR" --dry-run
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "already carries a footer receipt" "$EVAL_ERR" \
  && grep -Fq "($WANT chars)" "$EVAL_OUT"; then
  good "hand-written v2 footer warns and keeps, payload unstacked"
else
  bad "v2 hand-written path gave exit $EVAL_STATUS"
fi
PR=4514
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.\n> Basis: 0 reproduced · 0 ran · 0 walked · 0 pointed · 0 said · 0 rules cited · 0 verified fixes\n> verdict `abc1234` · standard tier · cost unrecorded","approved":false,"comments":[]}
EOF
L3='> verdict `abc1234` · standard tier · cost unrecorded'
WANT="$(printf '%s\n%s\n%s' "$L1" "$L2" "$L3" | wc -c | tr -d ' ')"
run_review post acme/app "$PR" --dry-run
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "already carries a footer receipt" "$EVAL_ERR" \
  && grep -Fq "($WANT chars)" "$EVAL_OUT"; then
  good "hand-written legacy footer warns and keeps, payload unstacked"
else
  bad "legacy hand-written path gave exit $EVAL_STATUS"
fi

# --- B4: this-run flips (triage addressed + cited rule) count exactly ---
# Store holds 1 open pointed finding; the review cites a kb rule and flips it
# addressed via triage in the same run. Silence proves the ver branch (this-run
# addressed history at now) and the distinct-cites path — not just the rigor row.
PR=4517
eval_mk_bundle "$PR"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Fresh finding to flip.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"cccccccccccccccccccccccccccccccccccccccc","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.\n> Basis: 0 reproduced · 0 ran · 0 walked · 1 pointed · 0 said · 1 rules cited · 1 verified fixes","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Recheck cite.","kb":["b4-rule"]}],"triage":[{"sig":"cccccccccccccccccccccccccccccccccccccccc","status":"addressed","rationale":"verified the fix path end to end with evidence"}]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] && ! grep -q "basis line counts" "$EVAL_ERR"; then
  good "triage-addressed + cited rule count exactly, no warn"
else
  bad "B4 flip-count gave exit $EVAL_STATUS"
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
  && [ "$(jq -r '.data.body.footer // empty' <"$EVAL_OUT" 2>/dev/null | grep -c 'Reviewed with')" = "1" ]; then
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
unset REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL || true
rm -rf "$EVAL_SANDBOX/xdg-off" "$EVAL_SANDBOX/xdg-on" "$EVAL_SANDBOX/xdg-bad" \
  "$EVAL_SANDBOX/xdg-typo" "$EVAL_SANDBOX/xdg-lvl" "$EVAL_SANDBOX/xdg-logo"

if [ "$fails" -ne 0 ]; then echo "case publish-footer: FAIL ($fails)"; exit 1; fi
echo "case publish-footer: PASS"
