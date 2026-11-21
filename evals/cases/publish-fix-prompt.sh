#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

unset XDG_CONFIG_HOME || true
unset REVIEW_PUBLISH_FIX_PROMPT || true

PR=4740
STUB_SAVED="$EVAL_SANDBOX/gh-stub-orig"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
restore_stub() { cp "$STUB_SAVED" "$EVAL_STUBBIN/gh"; }
trap restore_stub EXIT
cat > "$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
case "$*" in
  'api repos/acme/app/pulls/4740 --jq .head.sha') printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; exit 0;;
  'api repos/acme/app/pulls/4740/reviews --paginate'|'api repos/acme/app/pulls/4740/comments --paginate'|'api repos/acme/app/issues/4740/comments --paginate') echo '[]'; exit 0;;
  'api graphql --paginate -F owner=acme -F name=app -F pr=4740 -f query=
      query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
        repository(owner:$owner, name:$name) {
          pullRequest(number:$pr) {
            reviewThreads(first:100, after:$endCursor) {
              nodes { isResolved }
            }
          }
        }
      }') echo '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}'; exit 0;;
  'api repos/acme/app/pulls/4740/reviews --method POST --input - --jq {id, state, html_url, submitted_at}'|'api repos/acme/app/issues/4740/comments --method POST --input -')
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

# Four findings on one changed line: a critical and an important that carry a
# brief, an important with none, and an informational that carries one. Only the
# first two may render the copy-me block.
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ⚠️ Findings require attention.","approved":false,"comments":[
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Token check skips expiry validation when leeway is zero.","rigor":"ran","severity":"critical","brief":{"outcome":"Reject expired tokens </details> and ping @alice before merging.","approach":"Check now.After(claims.ExpiresAt.Add(leeway)) unconditionally.","questions":["Should clock skew default to 30s?"]}},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Retry loop swallows the audit sink error.","rigor":"walked","severity":"important","brief":{"sites":[{"path":"notes.txt","line":2}],"outcome":"Propagate the audit sink error instead of dropping it."}},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Defaults timeout stays at zero after removing the fallback.","rigor":"pointed","severity":"important"},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Helper name could be more descriptive.","rigor":"said","severity":"informational","brief":{"outcome":"Rename the helper so the name states its purpose."}}
]}
EOF

capture_body() { # $1 = destination body file, $2.. = extra post flags
  local dest="$1" payload="$EVAL_SANDBOX/capture-payload.json"
  shift
  export EVAL_SAVE_PAYLOAD="$payload"
  rm -f "$payload"
  run_review post acme/app "$PR" --json "$@"
  unset EVAL_SAVE_PAYLOAD
  if [ -f "$payload" ]; then jq -r '.body // empty' "$payload" >"$dest"; fi
}

# section_of prints the numbered finding block whose text carries the needle,
# so a prompt block can be attributed to the finding it belongs to.
section_of() { # $1 = body file, $2 = claim needle
  awk -v needle="$2" '
    /^[0-9]+\. / {
      if (found) { printf "%s", buf; exit }
      buf = $0 ORS; found = (index($0, needle) > 0)
      next
    }
    { buf = buf $0 ORS; if (index($0, needle) > 0) found = 1 }
    END { if (found) printf "%s", buf }
  ' "$1"
}

default_body="$EVAL_SANDBOX/body-default.md"
capture_body "$default_body"
assert_exit "default post exits 0" 0
assert_eq "default body carries exactly two prompt blocks" 2 \
  "$(grep -c '<summary>Prompt for AI agent</summary>' "$default_body" || true)"
assert_file_grep "critical prompt names the required outcome" "$default_body" \
  '   Required outcome: Reject expired tokens &lt;/details> and ping @​alice before merging.' "-F"
assert_file_grep "important prompt names its required outcome" "$default_body" \
  '   Required outcome: Propagate the audit sink error instead of dropping it.' "-F"
assert_file_grep "prompt anchor is a plain path:line location" "$default_body" \
  '   Location: `notes.txt:2`' "-F"
assert_file_nogrep "prompt body carries no ev reference" "$default_body" 'Location: [notes.txt:2][ev-' "-F"

no_brief="$(section_of "$default_body" "Defaults timeout stays at zero")"
if printf '%s' "$no_brief" | matches -F "Defaults timeout stays at zero"; then
  good "brief-less finding section located"
else
  bad "brief-less finding section not found"
fi
if printf '%s' "$no_brief" | matches -F "Prompt for AI agent"; then
  bad "brief-less important finding rendered a prompt block"
else
  good "brief-less important finding renders no prompt block"
fi
info="$(section_of "$default_body" "Helper name could be more descriptive")"
if printf '%s' "$info" | matches -F "Prompt for AI agent"; then
  bad "informational finding rendered a prompt block"
else
  good "informational finding renders no prompt block"
fi
if printf '%s' "$info" | matches -F "Technical details"; then
  good "informational finding still renders its own details block"
else
  bad "informational finding lost its details block"
fi

# The fenced prompt body is escaped data: no raw closing tag and no live
# mention can reach the posted text.
assert_file_grep "hostile closing tag is entity-escaped" "$default_body" '&lt;/details>' "-F"
assert_file_nogrep "mention stays neutralized in the prompt" "$default_body" '@alice' "-F"

# publish.fix_prompt: false omits the blocks, and the rest of the body is the
# default body with exactly those blocks subtracted (the review marker's
# payload hash necessarily moves with the body, so that one line is normalized).
mkdir -p "$EVAL_SANDBOX/xdg-off/verdict"
printf '{"publish":{"fix_prompt":false}}' >"$EVAL_SANDBOX/xdg-off/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-off"
off_body="$EVAL_SANDBOX/body-file-off.md"
capture_body "$off_body" --force
assert_exit "file-off post exits 0" 0
assert_eq "publish.fix_prompt=false renders no prompt block" 0 \
  "$(grep -c '<summary>Prompt for AI agent</summary>' "$off_body" || true)"

strip_prompt_blocks() { # $1 = body file
  awk '
    /<details><summary>Prompt for AI agent<\/summary>$/ { skipping = 1; if (n > 0 && lines[n] == "") n--; next }
    skipping { if ($0 ~ /^[[:space:]]*<\/details>$/) skipping = 0; next }
    { lines[++n] = $0 }
    END { for (i = 1; i <= n; i++) print lines[i] }
  ' "$1"
}

normalize_marker() {
  sed -E 's/^(<!-- verdict-review payload=)[0-9a-f]{40}( head=[0-9a-f]* -->)$/\1HASH\2/'
}

strip_prompt_blocks "$default_body" | normalize_marker >"$EVAL_SANDBOX/want-off.md"
normalize_marker <"$off_body" >"$EVAL_SANDBOX/got-off.md"
if cmp -s "$EVAL_SANDBOX/want-off.md" "$EVAL_SANDBOX/got-off.md"; then
  good "off body is the default body minus exactly the two prompt blocks"
else
  bad "off body differs beyond the removed blocks: $(diff "$EVAL_SANDBOX/want-off.md" "$EVAL_SANDBOX/got-off.md" | head -c 800)"
fi

capture_body "$EVAL_SANDBOX/body-flag-on.md" --force --fix-prompt
assert_exit "flag-on post exits 0" 0
assert_eq "--fix-prompt wins over a false file" 2 \
  "$(grep -c '<summary>Prompt for AI agent</summary>' "$EVAL_SANDBOX/body-flag-on.md" || true)"
unset XDG_CONFIG_HOME || true

REVIEW_PUBLISH_FIX_PROMPT="false"
export REVIEW_PUBLISH_FIX_PROMPT
capture_body "$EVAL_SANDBOX/body-env-off.md" --force
unset REVIEW_PUBLISH_FIX_PROMPT || true
assert_exit "env-off post exits 0" 0
assert_eq "REVIEW_PUBLISH_FIX_PROMPT=false renders no prompt block" 0 \
  "$(grep -c '<summary>Prompt for AI agent</summary>' "$EVAL_SANDBOX/body-env-off.md" || true)"

mkdir -p "$EVAL_SANDBOX/xdg-bad/verdict"
printf '{"publish":{"fix_prompt":"yes"}}' >"$EVAL_SANDBOX/xdg-bad/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-bad"
run_review check-tree --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-settings" ]; then
  good "non-boolean fix_prompt refuses typed bad-settings"
else
  bad "non-boolean fix_prompt gave exit $EVAL_STATUS"
fi
printf '{"publish":{"fixprompt":false}}' >"$EVAL_SANDBOX/xdg-bad/verdict/settings.json"
run_review check-tree --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-settings" ] \
  && grep -Fq 'fix_prompt' "$EVAL_OUT"; then
  good "unknown publish key refuses bad-settings and names the allow-list"
else
  bad "unknown publish key gave exit $EVAL_STATUS"
fi
unset XDG_CONFIG_HOME || true

if grep -Fq 'publish.fix_prompt' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'REVIEW_PUBLISH_FIX_PROMPT' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq -- '--no-fix-prompt' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'Prompt for AI agent' "$EVAL_ROOT/docs/manual.md"; then
  good "manual documents the knob, env, flags, and block"
else
  bad "prompt block missing from docs/manual.md"
fi

unset XDG_CONFIG_HOME || true
unset REVIEW_PUBLISH_FIX_PROMPT || true
rm -rf "$EVAL_SANDBOX/xdg-off" "$EVAL_SANDBOX/xdg-bad"

eval_end
