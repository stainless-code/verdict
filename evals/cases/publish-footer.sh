#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

VER="$(jq -r .version "$EVAL_ROOT/package.json")"
PRODUCT_URL="https://stainless-code.com/verdict"
PRODUCT_LINK="Reviewed with [**Verdict $VER**]($PRODUCT_URL)"
GOLDEN_LINE='> <a href="https://stainless-code.com/verdict"><img src="https://raw.githubusercontent.com/stainless-code/verdict/main/apps/docs/public/logo.svg" alt="Verdict" width="18" height="18"></a> '"$PRODUCT_LINK"' · standard tier · cost $0.05 · 4 files · [review `101`](https://example.invalid/r/101)'
unset XDG_CONFIG_HOME || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION || true
unset REVIEW_PUBLISH_ATTRIBUTION_TEXT REVIEW_PUBLISH_COPYRIGHT || true
unset REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL || true

PR=4501
dir="$EVAL_SANDBOX/.verdict/state/$PR"
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
jq -n --arg h "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" \
  '{schema_version:"1",review:{id:101,html_url:"https://example.invalid/r/101"},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:4,security_summary:"None."}' \
  > "$dir/post.json"
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
assert_file_nogrep "version stays inline, never bracketed" "$EVAL_SANDBOX/out-4501.md" "Verdict [" "-F"
assert_file_nogrep "version stays inline, never parenthesized" "$EVAL_SANDBOX/out-4501.md" "Verdict (" "-F"
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

PR=4524
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.","approved":false,"comments":[]}
EOF
jq -n --arg h "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" \
  '{schema_version:"1",review:{id:101,html_url:"https://example.invalid/r/101"},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:4,security_summary:"None."}' \
  > "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
run_review post acme/app "$PR" --dry-run --json
repost_footer="$(jq -r '.data.body.footer // empty' <"$EVAL_OUT" 2>/dev/null)"
printf '%s' "$repost_footer" > "$EVAL_SANDBOX/repost-footer.txt"
assert_exit "same-head repost succeeds" 0
assert_file_nogrep "same-head repost omits the stale link" "$EVAL_SANDBOX/repost-footer.txt" '[review' "-F"
assert_file_grep "same-head repost keeps the receipt" "$EVAL_SANDBOX/repost-footer.txt" 'standard tier' "-F"
PR=4525
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.","approved":false,"comments":[]}
EOF
jq -n --arg h "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" \
  '{schema_version:"1",review:{id:101,html_url:"https://example.invalid/r/101"},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:4,security_summary:"None."}' \
  > "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq '[review `101`](https://example.invalid/r/101)' <<<"$(jq -r '.data.body.footer // empty' <"$EVAL_OUT" 2>/dev/null)"; then
  good "differing-head post keeps today's link"
else
  bad "head-differs control gave exit $EVAL_STATUS"
fi
PR=4526
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.","approved":false,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
absent_footer="$(jq -r '.data.body.footer // empty' <"$EVAL_OUT" 2>/dev/null)"
printf '%s' "$absent_footer" > "$EVAL_SANDBOX/absent-footer.txt"
assert_exit "absent post.json render succeeds" 0
assert_file_nogrep "absent post.json keeps today's unlinked render" "$EVAL_SANDBOX/absent-footer.txt" '[review' "-F"
if [ "$absent_footer" = "$repost_footer" ]; then
  good "omit-branch footer byte-identical to fresh unlinked render"
else
  bad "omit-branch footer differs from fresh render"
fi
PR=4527
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.","approved":false,"comments":[]}
EOF
jq -n --arg h "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" \
  '{schema_version:"1",review:{id:101},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:4,security_summary:"None."}' \
  > "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
run_review post acme/app "$PR" --dry-run --json
nourl_footer="$(jq -r '.data.body.footer // empty' <"$EVAL_OUT" 2>/dev/null)"
assert_exit "no-URL fallback render succeeds" 0
if [ -n "$nourl_footer" ]; then
  good "no-URL fallback emits a footer"
else
  bad "no-URL fallback emitted no footer to scan"
fi
printf '%s' "$nourl_footer" > "$EVAL_SANDBOX/nourl-footer.txt"
assert_file_nogrep "same-head omit swallows the plain-text no-URL fallback too" "$EVAL_SANDBOX/nourl-footer.txt" 'review `' "-F"

PR=4502
dir="$EVAL_SANDBOX/.verdict/state/$PR"
mkdir -p "$dir"
printf '[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Degraded probe.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"1111111111111111111111111111111111111111","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]' > "$dir/findings.json"
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4502.md"
assert_exit "degraded footer renders" 0
assert_file_grep "absent records degrade the receipt" "$EVAL_SANDBOX/out-4502.md" 'cost unrecorded · files unrecorded' "-F"
assert_file_nogrep "absent records carry no review link" "$EVAL_SANDBOX/out-4502.md" '[review' "-F"
assert_file_grep "degraded footer keeps the exact basis line" "$EVAL_SANDBOX/out-4502.md" '> Basis: 0 reproduced · 0 ran · 0 walked · 1 pointed · 0 said · 0 rules cited · 0 verified fixes' "-F"

mkdir -p "$EVAL_SANDBOX/xdg-off/verdict"
printf '{"publish":{"footer":false}}' > "$EVAL_SANDBOX/xdg-off/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-off"
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4503.md"
assert_exit "footer-off file render succeeds" 0
assert_file_nogrep "publish.footer=false removes the marker" "$EVAL_SANDBOX/out-4503.md" '<!-- verdict-footer -->' "-F"
assert_file_nogrep "publish.footer=false removes the sentence" "$EVAL_SANDBOX/out-4503.md" 'Reviewed with' "-F"
assert_file_grep "publish.footer=false keeps the basis line" "$EVAL_SANDBOX/out-4503.md" '> Basis:' "-F"
printf '{"publish":{"attribution":"off"}}' > "$EVAL_SANDBOX/xdg-off/verdict/settings.json"
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4504.md"
assert_exit "attribution-off file render succeeds" 0
assert_file_grep "attribution-off keeps the receipt" "$EVAL_SANDBOX/out-4504.md" '> standard tier · cost $0.05 · 4 files' "-F"
assert_file_nogrep "attribution-off drops the sentence" "$EVAL_SANDBOX/out-4504.md" 'Reviewed with' "-F"
assert_file_nogrep "attribution-off drops the logo" "$EVAL_SANDBOX/out-4504.md" '<img' "-F"
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
assert_exit "--no-footer render succeeds" 0
assert_file_nogrep "--no-footer flag kills the block with no file present" "$EVAL_SANDBOX/out-4505.md" '<!-- verdict-footer -->' "-F"

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
assert_exit "flag-over-file render succeeds" 0
assert_file_nogrep "flag --no-footer wins over file true" "$EVAL_SANDBOX/out-4507.md" '<!-- verdict-footer -->' "-F"
unset XDG_CONFIG_HOME || true
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4508.md"
assert_exit "env-off render succeeds" 0
assert_file_nogrep "env false alone (no file) turns the footer off" "$EVAL_SANDBOX/out-4508.md" '<!-- verdict-footer -->' "-F"
unset REVIEW_PUBLISH_FOOTER || true

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

mkdir -p "$EVAL_SANDBOX/xdg-lvl/verdict"
printf '{"publish":{"attribution":"product"}}' > "$EVAL_SANDBOX/xdg-lvl/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-lvl"
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4520.md"
assert_exit "product level render succeeds" 0
assert_file_grep "product level renders the golden line" "$EVAL_SANDBOX/out-4520.md" "$GOLDEN_LINE" "-F"
assert_file_nogrep "product level names no maker" "$EVAL_SANDBOX/out-4520.md" 'Stainless Code](' "-F"
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
assert_exit "--no-attribution render succeeds" 0
assert_file_grep "--no-attribution keeps the receipt" "$EVAL_SANDBOX/out-4522.md" '> standard tier · cost $0.05' "-F"
assert_file_nogrep "--no-attribution drops the sentence" "$EVAL_SANDBOX/out-4522.md" 'Reviewed with' "-F"
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4523.md" --attribution full
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq "by [Stainless Code](https://stainless-code.com)" "$EVAL_SANDBOX/out-4523.md"; then
  good "--attribution full flag reaches the maker level"
else
  bad "--attribution full gave exit $EVAL_STATUS"
fi

mkdir -p "$EVAL_SANDBOX/xdg-logo/verdict"
printf '{"publish":{"logo":false}}' > "$EVAL_SANDBOX/xdg-logo/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-logo"
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4530.md"
assert_exit "logo-off file render succeeds" 0
assert_file_nogrep "publish.logo=false drops the image" "$EVAL_SANDBOX/out-4530.md" '<img' "-F"
assert_file_grep "publish.logo=false keeps the linked sentence" "$EVAL_SANDBOX/out-4530.md" "$PRODUCT_LINK" "-F"
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
assert_exit "empty logo_url render succeeds" 0
assert_file_nogrep "empty logo_url renders no image" "$EVAL_SANDBOX/out-4532.md" '<img' "-F"
assert_file_grep "empty logo_url keeps the linked sentence" "$EVAL_SANDBOX/out-4532.md" "$PRODUCT_LINK" "-F"
unset XDG_CONFIG_HOME || true
run_review report acme/app 4501 --output "$EVAL_SANDBOX/out-4533.md" --no-logo
assert_exit "--no-logo render succeeds" 0
assert_file_nogrep "--no-logo flag drops the image" "$EVAL_SANDBOX/out-4533.md" '<img' "-F"
assert_file_grep "--no-logo keeps the linked sentence" "$EVAL_SANDBOX/out-4533.md" "$PRODUCT_LINK" "-F"

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
  > "$EVAL_SANDBOX/.verdict/state/$PR/review.json"
run_review post acme/app "$PR" --dry-run
stale_chars=$(grep -o 'body chars: [0-9]*' "$EVAL_OUT" | grep -o '[0-9]*')
assert_exit "stale-block repost succeeds" 0
assert_eq "stale block keeps the body char count" "$clean_chars" "$stale_chars"
assert_eq "stale block leaves exactly one marker" "1" "$(grep -c '<!-- verdict-footer -->' "$EVAL_OUT")"
assert_file_grep "stale block is replaced with the fresh version" "$EVAL_OUT" "Verdict $VER" "-F"
assert_file_nogrep "stale block drops the old version" "$EVAL_OUT" 'Verdict 9.9.9' "-F"
assert_file_grep "stale block warns it is replacing" "$EVAL_ERR" 'replacing it with the canonical block' "-F"

PR=4510
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.\n> Basis: 9 reproduced · 9 ran · 9 walked · 9 pointed · 9 said · 9 rules cited · 9 verified fixes","approved":false,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "basis line counts (9 9 9 9 9 9 9) differ from the computed counts (0 0 0 0 0 0 0) — replaced it (basis-replaced)" "$EVAL_ERR" \
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
assert_exit "right basis render succeeds" 0
assert_stream_nogrep "right basis stays silent" "$EVAL_ERR" "basis"

PR=4515
eval_mk_bundle "$PR"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Carried-over one.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},{"kind":"inline","path":"notes.txt","side":"RIGHT","line":3,"anchored":true,"body":"Carried-over two.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.\n> Basis: 0 reproduced · 0 ran · 0 walked · 0 pointed · 1 said · 0 rules cited · 0 verified fixes","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Fresh probe comment."}]}
EOF
run_review post acme/app "$PR" --dry-run --json
assert_exit "re-review matching its own comment succeeds" 0
assert_stream_nogrep "re-review stays silent despite the carried-over store" "$EVAL_ERR" "basis line counts"
PR=4516
eval_mk_bundle "$PR"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Carried-over one.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},{"kind":"inline","path":"notes.txt","side":"RIGHT","line":3,"anchored":true,"body":"Carried-over two.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.\n> Basis: 0 reproduced · 0 ran · 0 walked · 0 pointed · 2 said · 0 rules cited · 0 verified fixes","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Fresh probe comment."}]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "basis line counts (0 0 0 0 2 0 0) differ from the computed counts (0 0 0 0 1 0 0) — replaced it (basis-replaced)" "$EVAL_ERR"; then
  good "re-review miscounting its own comment warns with review counts"
else
  bad "re-review control gave exit $EVAL_STATUS"
fi

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
  good "hand-written footer warns and keeps, payload unstacked"
else
  bad "hand-written footer path gave exit $EVAL_STATUS"
fi

PR=4517
# The rule the review cites has to be in the base the producer pins, not a
# file written after the capture.
printf '%s\n' '{"id":"b4-rule","status":"active"}' > "$EVAL_SANDBOX/.verdict/kb.jsonl"
eval_mk_bundle "$PR"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Fresh finding to flip.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"cccccccccccccccccccccccccccccccccccccccc","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.\n> Basis: 0 reproduced · 0 ran · 0 walked · 0 pointed · 1 said · 1 rules cited · 1 verified fixes","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Recheck cite.","kb":["b4-rule"]}],"triage":[{"sig":"cccccccccccccccccccccccccccccccccccccccc","status":"addressed","rationale":"verified the fix path end to end with evidence"}]}
EOF
run_review post acme/app "$PR" --dry-run --json
assert_exit "triage-addressed flip succeeds" 0
assert_stream_nogrep "triage-addressed + cited rule count exactly, no warn" "$EVAL_ERR" "basis line counts"
rm -f "$EVAL_SANDBOX/.verdict/kb.jsonl"

if grep -Fq 'Confidence is the agent'"'"'s "would I merge on this review" judgment, informed by evidence depth, coverage, and rule support.' "$EVAL_ROOT/docs/manual.md"; then
  good "manual carries the confidence metric definition"
else
  bad "metric definition missing from docs/manual.md"
fi

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

unset XDG_CONFIG_HOME || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION || true
unset REVIEW_PUBLISH_ATTRIBUTION_TEXT REVIEW_PUBLISH_COPYRIGHT || true
unset REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL || true
rm -rf "$EVAL_SANDBOX/xdg-off" "$EVAL_SANDBOX/xdg-on" "$EVAL_SANDBOX/xdg-bad" \
  "$EVAL_SANDBOX/xdg-typo" "$EVAL_SANDBOX/xdg-lvl" "$EVAL_SANDBOX/xdg-logo"

eval_end
