#!/usr/bin/env bash
# Case publish-links: permalink-first evidence links (P2).
#
# Grades (all hermetic — crafted bundles, stub gh, no network):
#   L1  well-formed links on the script path: report --output on a GitHub
#       bundle renders reference-style [path:line][ev-N] locators with
#       blob/<reviewed-SHA> definitions pinned to the fetched HEAD.
#   L2  quote cap warn-and-keep: a >3-line fence outside collapsed details
#       exits 0 with a warning and keeps the body (footer still renders).
#   L3  provider fallback: record/report on a gitlab bundle renders plain
#       path:line text — never blob URLs.
#   L4  local fallback: record/report on a local bundle renders plain text.
#   L5  mismatch warn-and-keep: a blob link pinned to the wrong SHA exits 0
#       with a store-counts-style warning; a correctly pinned link stays
#       silent.
#   M1  the manual carries the permalink paragraph.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

SHA_A="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
SHA_B="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
SHA_F="ffffffffffffffffffffffffffffffffffffffff"

# --- L1: well-formed SHA-pinned reference-style links on report --output ---
PR=4521
dir="$EVAL_SANDBOX/.review/$PR"
mkdir -p "$dir"
cat > "$dir/PR.json" <<EOF
{"state":"OPEN","headRefOid":"$SHA_A","baseRefOid":"$SHA_B","author":{"login":"teammate"},"url":"https://github.com/acme/app/pull/$PR","files":[{"path":"notes.txt","additions":1,"deletions":1}]}
EOF
cat > "$dir/findings.json" <<'EOF'
[{"kind":"inline","path":"notes.txt","line":2,"anchored":true,"body":"Off by one on the retry count.","rigor":"pointed","severity":"important","sig":"cccccccccccccccccccccccccccccccccccccccc","status":"open"}]
EOF
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4521.md"
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "github report exits 0"
else
  bad "github report exits $EVAL_STATUS"
fi
if grep -Eq '\[notes.txt:2\]\[ev-1\]' "$EVAL_SANDBOX/out-4521.md" \
  && grep -Fq "[ev-1]: https://github.com/acme/app/blob/$SHA_A/notes.txt#L2" "$EVAL_SANDBOX/out-4521.md"; then
  good "locator is reference-style, definition pins blob/<reviewed-SHA>"
else
  bad "link shape wrong: $(grep -h 'notes.txt' "$EVAL_SANDBOX/out-4521.md" || echo MISSING)"
fi
if grep -Fq 'cost unrecorded · 1 file' "$EVAL_SANDBOX/out-4521.md"; then
  good "footer still renders beside the link definitions"
else
  bad "footer missing from link report"
fi

# --- L2: quote cap warns and keeps, never fails ---
PR=4522
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ No new issues found.\n> Confidence: 4/5 · Safety: not assessed\n\n**Reviewed changes**\n\n- `notes.txt` — one line changed\n\n```diff\n a\n b\n c\n d\n e\n```","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "longer than 3 lines outside collapsed details" "$EVAL_ERR" \
  && [ "$(jq -r '.data.body.footer // empty' <"$EVAL_OUT" 2>/dev/null | head -c 3)" = "---" ]; then
  good "long visible quote exits 0, warns, keeps the footer"
else
  bad "quote-cap path gave exit $EVAL_STATUS"
fi

# --- L3: provider bundle degrades to plain path:line ---
PR=4523
eval_mk_bundle "$PR"
cat > "$EVAL_SANDBOX/.review/$PR/PR.json" <<EOF
{"schema_version":"1","provider":"gitlab","number":$PR,"title":"t","body":"","base_ref":"main","head_ref":"feat","base_sha":"$SHA_B","head_sha":"$SHA_B","author":"mate","url":"https://gitlab.example/acme/app/-/merge_requests/$PR","state":"open","draft":false,"files":[{"path":"notes.txt","status":"modified","additions":1,"deletions":1}],"additions":1,"deletions":1}
EOF
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ No new issues found.\n> Confidence: 4/5 · Safety: not assessed","approved":true,"comments":[{"path":"notes.txt","line":2,"body":"Off by one on the retry count."}]}
EOF
run_review record acme/app "$PR" --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "provider record exits 0"
else
  bad "provider record exits $EVAL_STATUS"
fi
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4523.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq 'notes.txt:2' "$EVAL_SANDBOX/out-4523.md" \
  && ! grep -q 'blob/' "$EVAL_SANDBOX/out-4523.md"; then
  good "provider report renders plain path:line, no blob URLs"
else
  bad "provider fallback wrong (exit $EVAL_STATUS)"
fi

# --- L4: local bundle degrades to plain path:line ---
PR="local-t2"
dir="$EVAL_SANDBOX/.review/$PR"
mkdir -p "$dir"
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
cat > "$dir/source.json" <<EOF
{"schema_version":"1","kind":"worktree","head_sha":"$SHA_A","base_sha":"$SHA_B","head_ref":"HEAD","dirty":false}
EOF
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ No new issues found.\n> Confidence: 4/5 · Safety: not assessed","approved":true,"comments":[{"path":"notes.txt","line":2,"body":"Off by one on the retry count."}]}
EOF
run_review record acme/app "$PR" --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "local record exits 0"
else
  bad "local record exits $EVAL_STATUS"
fi
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4524.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq 'notes.txt:2' "$EVAL_SANDBOX/out-4524.md" \
  && ! grep -q 'blob/' "$EVAL_SANDBOX/out-4524.md"; then
  good "local report renders plain path:line, no blob URLs"
else
  bad "local fallback wrong (exit $EVAL_STATUS)"
fi

# --- L5: mismatch warns and keeps; correct pin stays silent ---
PR=4525
eval_mk_bundle "$PR"
eval_write_review "$PR" <<EOF
{"schema_version":"1","body":"> ✅ No new issues found.\n> Confidence: 4/5 · Safety: not assessed\n\n[notes.txt:2][ev-1]\n\n[ev-1]: https://github.com/acme/app/blob/$SHA_F/notes.txt#L2","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "differs from the reviewed SHA" "$EVAL_ERR" \
  && [ "$(jq -r '.data.body.footer // empty' <"$EVAL_OUT" 2>/dev/null | head -c 3)" = "---" ]; then
  good "wrong-SHA link exits 0, warns, keeps the footer"
else
  bad "mismatch path gave exit $EVAL_STATUS"
fi
PR=4526
eval_mk_bundle "$PR"
eval_write_review "$PR" <<EOF
{"schema_version":"1","body":"> ✅ No new issues found.\n> Confidence: 4/5 · Safety: not assessed\n\n[notes.txt:2][ev-1]\n\n[ev-1]: https://github.com/acme/app/blob/$SHA_A/notes.txt#L2","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && ! grep -q "differs from the reviewed SHA" "$EVAL_ERR" \
  && ! grep -q "longer than 3 lines" "$EVAL_ERR"; then
  good "correctly pinned link stays silent"
else
  bad "correct pin warned (exit $EVAL_STATUS)"
fi

# --- M1: manual carries the permalink paragraph ---
if grep -Fq 'pinned to the reviewed commit — never live HEAD' "$EVAL_ROOT/docs/manual.md"; then
  good "manual carries the permalink paragraph"
else
  bad "permalink paragraph missing from docs/manual.md"
fi

if [ "$fails" -ne 0 ]; then echo "case publish-links: FAIL ($fails)"; exit 1; fi
echo "case publish-links: PASS"
