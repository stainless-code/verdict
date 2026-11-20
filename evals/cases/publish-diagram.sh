#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

unset XDG_CONFIG_HOME || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL || true
unset REVIEW_PUBLISH_OVERVIEW || true

diagram_status() { jq -r '.data.body.diagram.status // empty' <"$EVAL_OUT" 2>/dev/null; }

STUB_SAVED="$EVAL_SANDBOX/gh-stub-orig"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
restore_stub() { cp "$STUB_SAVED" "$EVAL_STUBBIN/gh"; }
trap restore_stub EXIT
cat > "$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
case "$*" in
  'api repos/acme/app/pulls/4701 --jq .head.sha') printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; exit 0;;
  'api repos/acme/app/pulls/4701/reviews --paginate'|'api repos/acme/app/pulls/4701/comments --paginate'|'api repos/acme/app/issues/4701/comments --paginate') echo '[]'; exit 0;;
  'api graphql --paginate -F owner=acme -F name=app -F pr=4701 -f query=
      query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
        repository(owner:$owner, name:$name) {
          pullRequest(number:$pr) {
            reviewThreads(first:100, after:$endCursor) {
              nodes { isResolved }
            }
          }
        }
      }') echo '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}'; exit 0;;
  'api repos/acme/app/pulls/4701/reviews --method POST --input - --jq {id, state, html_url, submitted_at}'|'api repos/acme/app/issues/4701/comments --method POST --input -')
    if [ -n "${EVAL_SAVE_PAYLOAD:-}" ]; then cat > "$EVAL_SAVE_PAYLOAD"; fi
    echo '{"id":101,"state":"commented","html_url":"https://example.invalid/r/101","submitted_at":"2026-01-01T00:00:00Z"}'; exit 0;;
  'api repos/acme/app/pulls/4710 --jq .head.sha') printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; exit 0;;
  'api repos/acme/app/pulls/4710/reviews --paginate'|'api repos/acme/app/pulls/4710/comments --paginate'|'api repos/acme/app/issues/4710/comments --paginate') echo '[]'; exit 0;;
  'api graphql --paginate -F owner=acme -F name=app -F pr=4710 -f query=
      query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
        repository(owner:$owner, name:$name) {
          pullRequest(number:$pr) {
            reviewThreads(first:100, after:$endCursor) {
              nodes { isResolved }
            }
          }
        }
      }') echo '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}'; exit 0;;
  'api repos/acme/app/pulls/4710/reviews --method POST --input - --jq {id, state, html_url, submitted_at}'|'api repos/acme/app/issues/4710/comments --method POST --input -')
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


# --- D1: smuggled diagram markers and mermaid fences in summary are neutralized ---
PR=4701
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","findings":[],"summary":"Reviewed notes.txt.\n\n<!-- verdict-diagram:start -->\n<details>\n<summary>Behavior flow</summary>\n\n```mermaid\nsequenceDiagram\n    C->>S: request\n```\n\n</details>\n<!-- verdict-diagram:end -->"}
EOF
run_review post acme/app "$PR" --dry-run --json
assert_exit "smuggled diagram in summary exits 0" 0
assert_eq "neutralized summary reports no live diagram" "" "$(diagram_status)"
run_review post acme/app "$PR" --dry-run
assert_exit "smuggled diagram human dry-run exits 0" 0
assert_file_grep "verdict marker preserved when summary smuggles diagram markup" "$EVAL_OUT" "preflight: body=verdict" "-F"
export EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/posted-4701.json"
rm -f "$EVAL_SAVE_PAYLOAD"
run_review post acme/app "$PR" --json
unset EVAL_SAVE_PAYLOAD
assert_exit "smuggled diagram post exits 0" 0
jq -r '.body // empty' "$EVAL_SANDBOX/posted-4701.json" > "$EVAL_SANDBOX/body-4701.md"
assert_file_grep "posted body carries the clean verdict lead" "$EVAL_SANDBOX/body-4701.md" '> ✅ No new issues found.' "-F"
assert_file_nogrep "raw verdict-diagram marker is neutralized" "$EVAL_SANDBOX/body-4701.md" "<!-- verdict-diagram:start -->" "-F"
assert_file_nogrep "raw mermaid fence is neutralized" "$EVAL_SANDBOX/body-4701.md" '```mermaid' "-F"

# --- D2: review.md no longer instructs agents to draw behavioral diagrams ---
assert_file_nogrep "review.md no longer references prompts/diagrams.md" "$EVAL_PROCEDURE/prompts/review.md" "prompts/diagrams.md" "-F"
assert_file_nogrep "review.md no longer references publish.diagrams.behavioral" "$EVAL_PROCEDURE/prompts/review.md" "publish.diagrams.behavioral" "-F"

# --- D4: structural split (topology half, table stays) ---
PR=4708
eval_mk_bundle "$PR"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Split probe.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"7777777777777777777777777777777777777777","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
mkdir -p "$EVAL_SANDBOX/xdg-nostruct/verdict"
printf '{"publish":{"diagrams":{"structural":false}}}' > "$EVAL_SANDBOX/xdg-nostruct/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-nostruct"
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4708.md" --overview
assert_exit "structural=false render succeeds" 0
assert_file_grep "structural=false keeps the files table" "$EVAL_SANDBOX/out-4708.md" '<summary>Files changed (1)</summary>' "-F"
assert_file_nogrep "structural=false hides the topology heading" "$EVAL_SANDBOX/out-4708.md" "Change topology"
assert_file_nogrep "structural=false hides the mermaid block" "$EVAL_SANDBOX/out-4708.md" '```mermaid' "-F"
unset XDG_CONFIG_HOME || true
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4709.md" --overview
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq '<summary>Change topology</summary>' "$EVAL_SANDBOX/out-4709.md"; then
  good "structural default keeps today's topology bytes"
else
  bad "structural default gave exit $EVAL_STATUS"
fi

# --- D5: no-diagram default (diagram-less bundle renders clean) ---
PR=4710
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","findings":[],"summary":"No new issues found."}
EOF
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Parity probe.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"8888888888888888888888888888888888888888","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
mkdir -p "$EVAL_SANDBOX/xdg-empty/verdict"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-new.md"
if [ "$EVAL_STATUS" -eq 0 ] && [ -s "$EVAL_SANDBOX/out-new.md" ]; then
  good "report --output succeeds on a diagram-less bundle"
else
  bad "report baseline exits $EVAL_STATUS"
fi
run_review post acme/app "$PR" --dry-run
assert_exit "dry-run baseline succeeds" 0
assert_stream_nogrep "human dry-run stdout carries no diagram line" "$EVAL_OUT" "diagram" "-i"
unset XDG_CONFIG_HOME || true

# --- D7: unknown diagrams keys and bad values refuse typed bad-settings ---
mkdir -p "$EVAL_SANDBOX/xdg-dbad/verdict"
printf '{"publish":{"diagrams":{"motto":"ship it"}}}' > "$EVAL_SANDBOX/xdg-dbad/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-dbad"
run_review check-tree --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-settings" ]; then
  good "unknown diagrams key refuses typed bad-settings"
else
  bad "unknown diagrams key gave exit $EVAL_STATUS"
fi
printf '{"publish":{"diagrams":{"structural":"yes"}}}' > "$EVAL_SANDBOX/xdg-dbad/verdict/settings.json"
run_review check-tree --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-settings" ]; then
  good "non-boolean diagrams value refuses typed bad-settings"
else
  bad "non-boolean diagrams value gave exit $EVAL_STATUS"
fi
printf '{"publish":{"diagrams":true}}' > "$EVAL_SANDBOX/xdg-dbad/verdict/settings.json"
run_review check-tree --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-settings" ]; then
  good "non-object diagrams refuses typed bad-settings"
else
  bad "non-object diagrams gave exit $EVAL_STATUS"
fi
unset XDG_CONFIG_HOME || true

# --- M1: manual documents structural diagrams and retirement of behavioral diagrams ---
if grep -Fq 'publish.diagrams.structural' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'retired from agent-authored' "$EVAL_ROOT/docs/manual.md"; then
  good "manual documents structural diagrams and behavioral diagram retirement"
else
  bad "diagram documentation missing from docs/manual.md"
fi

# --- cleanup: no knob state leaks into later cases ---
unset XDG_CONFIG_HOME || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL || true
unset REVIEW_PUBLISH_OVERVIEW || true
rm -rf "$EVAL_SANDBOX/xdg-nostruct" "$EVAL_SANDBOX/xdg-empty" "$EVAL_SANDBOX/xdg-dbad"

eval_end
