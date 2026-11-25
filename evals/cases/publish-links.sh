#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

SHA_A="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
SHA_B="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
SHA_F="ffffffffffffffffffffffffffffffffffffffff"

STUB_SAVED="$EVAL_SANDBOX/gh-stub-orig"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
restore_stub() { cp "$STUB_SAVED" "$EVAL_STUBBIN/gh"; }
trap restore_stub EXIT
cat > "$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
# post pins every call to its bundle's host; the routes below match the rest.
if [ "${1:-}" = api ] && [ "${2:-}" = --hostname ]; then set -- api "${@:4}"; fi
case "$*" in
  'api repos/acme/app/pulls/4522 --jq .head.sha') printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; exit 0;;
  'api repos/acme/app/pulls/4522/reviews --paginate'|'api repos/acme/app/pulls/4522/comments --paginate'|'api repos/acme/app/issues/4522/comments --paginate') echo '[]'; exit 0;;
  'api graphql --paginate -F owner=acme -F name=app -F pr=4522 -f query=
      query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
        repository(owner:$owner, name:$name) {
          pullRequest(number:$pr) {
            reviewThreads(first:100, after:$endCursor) {
              nodes { isResolved }
            }
          }
        }
      }') echo '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}'; exit 0;;
  'api repos/acme/app/pulls/4522/reviews --method POST --input - --jq {id, state, html_url, submitted_at}'|'api repos/acme/app/issues/4522/comments --method POST --input -')
    if [ -n "${EVAL_SAVE_PAYLOAD:-}" ]; then cat > "$EVAL_SAVE_PAYLOAD"; fi
    echo '{"id":101,"state":"commented","html_url":"https://example.invalid/r/101","submitted_at":"2026-01-01T00:00:00Z"}'; exit 0;;
  'api repos/acme/app/pulls/4525 --jq .head.sha') printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; exit 0;;
  'api repos/acme/app/pulls/4525/reviews --paginate'|'api repos/acme/app/pulls/4525/comments --paginate'|'api repos/acme/app/issues/4525/comments --paginate') echo '[]'; exit 0;;
  'api graphql --paginate -F owner=acme -F name=app -F pr=4525 -f query=
      query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
        repository(owner:$owner, name:$name) {
          pullRequest(number:$pr) {
            reviewThreads(first:100, after:$endCursor) {
              nodes { isResolved }
            }
          }
        }
      }') echo '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}'; exit 0;;
  'api repos/acme/app/pulls/4525/reviews --method POST --input - --jq {id, state, html_url, submitted_at}'|'api repos/acme/app/issues/4525/comments --method POST --input -')
    if [ -n "${EVAL_SAVE_PAYLOAD:-}" ]; then cat > "$EVAL_SAVE_PAYLOAD"; fi
    echo '{"id":101,"state":"commented","html_url":"https://example.invalid/r/101","submitted_at":"2026-01-01T00:00:00Z"}'; exit 0;;
  'api repos/acme/app/pulls/4526 --jq .head.sha') printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; exit 0;;
  'api repos/acme/app/pulls/4526/reviews --paginate'|'api repos/acme/app/pulls/4526/comments --paginate'|'api repos/acme/app/issues/4526/comments --paginate') echo '[]'; exit 0;;
  'api graphql --paginate -F owner=acme -F name=app -F pr=4526 -f query=
      query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
        repository(owner:$owner, name:$name) {
          pullRequest(number:$pr) {
            reviewThreads(first:100, after:$endCursor) {
              nodes { isResolved }
            }
          }
        }
      }') echo '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}'; exit 0;;
  'api repos/acme/app/pulls/4526/reviews --method POST --input - --jq {id, state, html_url, submitted_at}'|'api repos/acme/app/issues/4526/comments --method POST --input -')
    if [ -n "${EVAL_SAVE_PAYLOAD:-}" ]; then cat > "$EVAL_SAVE_PAYLOAD"; fi
    echo '{"id":101,"state":"commented","html_url":"https://example.invalid/r/101","submitted_at":"2026-01-01T00:00:00Z"}'; exit 0;;
  "api user --jq .login") printf someone-else; exit 0;;
esac
echo "evals-stub-gh: unregistered argv ($*)" >&2
exit 64
EOF
chmod +x "$EVAL_STUBBIN/gh"
status=0
"$EVAL_STUBBIN/gh" api repos/acme/app/pulls/4701/reviews --method DELETE >/dev/null 2>&1 || status=$?
assert_eq "unexpected review deletion is refused" 64 "$status"


PR=4521
dir="$EVAL_SANDBOX/.verdict/state/$PR"
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

PR=4522
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ No new issues found.\n> Confidence: 4/5 · Safety: not assessed\n\n**Reviewed changes**\n\n- `notes.txt` — one line changed\n\n```diff\n a\n b\n c\n d\n e\n```","approved":true,"comments":[]}
EOF
export EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/posted-4522.json"
rm -f "$EVAL_SAVE_PAYLOAD"
run_review post acme/app "$PR" --json
unset EVAL_SAVE_PAYLOAD
assert_exit "smuggled multi-line fence in summary exits 0" 0
jq -r '.body // empty' "$EVAL_SANDBOX/posted-4522.json" > "$EVAL_SANDBOX/body-4522.md"
assert_file_grep "posted body carries the canonical footer" "$EVAL_SANDBOX/body-4522.md" '<!-- verdict-footer -->' "-F"
assert_file_nogrep "summary fences are neutralized in the posted body" "$EVAL_SANDBOX/body-4522.md" '```diff' "-F"

PR=4523
eval_mk_bundle "$PR"
cat > "$EVAL_SANDBOX/.verdict/state/$PR/PR.json" <<EOF
{"schema_version":"1","provider":"gitlab","number":$PR,"title":"t","body":"","base_ref":"main","head_ref":"feat","base_sha":"$EVAL_FIXTURE_BASE","head_sha":"$SHA_B","author":"mate","url":"https://gitlab.example/acme/app/-/merge_requests/$PR","state":"open","draft":false,"files":[{"path":"notes.txt","status":"modified","additions":1,"deletions":1}],"additions":1,"deletions":1}
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
assert_exit "provider report renders" 0
assert_file_grep "provider report renders plain path:line" "$EVAL_SANDBOX/out-4523.md" 'notes.txt:2' "-F"
assert_file_nogrep "provider report carries no blob URLs" "$EVAL_SANDBOX/out-4523.md" 'blob/' "-F"

PR="local-t2"
eval_mk_bundle "$PR"
dir="$EVAL_SANDBOX/.verdict/state/$PR"
jq --arg h "$SHA_A" '.head_sha = $h | .head_ref = "HEAD" | .dirty = false' \
  "$dir/source.json" >"$dir/source.json.tmp"
mv "$dir/source.json.tmp" "$dir/source.json"
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
assert_exit "local report renders" 0
assert_file_grep "local report renders plain path:line" "$EVAL_SANDBOX/out-4524.md" 'notes.txt:2' "-F"
assert_file_nogrep "local report carries no blob URLs" "$EVAL_SANDBOX/out-4524.md" 'blob/' "-F"

PR=4525
eval_mk_bundle "$PR"
cat > "$EVAL_SANDBOX/.verdict/state/$PR/PR.json" <<EOF
{"state":"OPEN","headRefOid":"$SHA_A","baseRefOid":"$EVAL_FIXTURE_BASE","author":{"login":"teammate"},"url":"https://github.com/acme/app/pull/$PR","files":[{"path":"notes.txt","additions":1,"deletions":1}]}
EOF
eval_write_review "$PR" <<EOF
{"schema_version":"1","findings":[{"path":"notes.txt","line":2,"severity":"important","claim":"Off by one on the retry count.","quote":"new line","confidence":"high","rigor":"pointed"}],"summary":"Smuggled [notes.txt:2][ev-1] [ev-1]: https://github.com/acme/app/blob/$SHA_F/notes.txt#L2"}
EOF
export EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/posted-4525.json"
rm -f "$EVAL_SAVE_PAYLOAD"
run_review post acme/app "$PR" --json
unset EVAL_SAVE_PAYLOAD
assert_exit "binary-pinned permalink post succeeds" 0
jq -r '.body // empty' "$EVAL_SANDBOX/posted-4525.json" > "$EVAL_SANDBOX/body-4525.md"
assert_file_grep "finding renders reference-style [ev-1] link" "$EVAL_SANDBOX/body-4525.md" '[notes.txt:2][ev-1]' "-F"
assert_file_grep "permalink definition pins reviewed SHA" "$EVAL_SANDBOX/body-4525.md" "[ev-1]: https://github.com/acme/app/blob/$SHA_A/notes.txt#L2" "-F"
assert_file_nogrep "smuggled wrong-SHA definition cannot override [ev-1]" "$EVAL_SANDBOX/body-4525.md" "[ev-1]: https://github.com/acme/app/blob/$SHA_F/notes.txt#L2" "-F"
PR=4526
eval_mk_bundle "$PR"
eval_write_review "$PR" <<EOF
{"schema_version":"1","body":"> ✅ No new issues found.\n> Confidence: 4/5 · Safety: not assessed\n\n[notes.txt:2][ev-1]\n\n[ev-1]: https://github.com/acme/app/blob/$SHA_A/notes.txt#L2","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
assert_exit "correctly pinned link succeeds" 0
assert_stream_nogrep "correctly pinned link warns about no SHA drift" "$EVAL_ERR" "differs from the reviewed SHA"
assert_stream_nogrep "correctly pinned link warns about no line overflow" "$EVAL_ERR" "longer than 3 lines"

if grep -Fq 'pinned to the reviewed commit — never live HEAD' "$EVAL_ROOT/docs/manual.md"; then
  good "manual carries the permalink paragraph"
else
  bad "permalink paragraph missing from docs/manual.md"
fi

eval_end
