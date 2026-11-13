#!/usr/bin/env bash
# Contract eval: post --summary comment|off (default off) via real CLI against fake gh.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

PR=4821
STATE_DIR="$EVAL_SANDBOX/.verdict/state/$PR"
ISSUES_FILE="$EVAL_SANDBOX/issue-comments.json"
printf '%s' '[{"id":301,"user":{"login":"stranger"},"html_url":"https://gh.example/acme/app/pull/4821#issuecomment-301","updated_at":"2026-01-01T00:00:00Z","body":"External summary\n\n<!-- verdict-summary head=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa -->"}]' > "$ISSUES_FILE"

STUB_SAVED="$EVAL_SANDBOX/gh-stub-orig"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
restore_stub() { cp "$STUB_SAVED" "$EVAL_STUBBIN/gh"; }
trap restore_stub EXIT

cat > "$EVAL_STUBBIN/gh" <<EOF
#!/usr/bin/env bash
set -euo pipefail
echo "stub-gh \$*" >> "\${EVAL_STUBLOG:-/dev/null}"
if [ "\${1:-}" = "api" ] && [ "\${2:-}" = "graphql" ]; then
  printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}'; exit 0
fi
case "\$*" in
  'api repos/'*'/issues/'*'/comments --paginate')
    cat "$ISSUES_FILE"; exit 0;;
  'api repos/'*'/issues/'*'/comments --method POST --input -')
    payload="\$(cat)"
    body="\$(printf '%s' "\$payload" | jq -r '.body')"
    new_entry="\$(jq -n --arg b "\$body" '{id:501,user:{login:"someone-else"},html_url:"https://gh.example/acme/app/pull/4821#issuecomment-501",updated_at:"2026-01-02T00:00:00Z",body:\$b}')"
    jq --argjson entry "\$new_entry" '. + [\$entry]' "$ISSUES_FILE" > "$ISSUES_FILE.tmp"
    mv "$ISSUES_FILE.tmp" "$ISSUES_FILE"
    printf '%s' "\$new_entry"; exit 0;;
  'api repos/'*'/issues/comments/501 --method PATCH --input -')
    payload="\$(cat)"
    body="\$(printf '%s' "\$payload" | jq -r '.body')"
    jq --arg b "\$body" 'map(if .id == 501 then .body = \$b | .updated_at = "2026-01-03T00:00:00Z" else . end)' "$ISSUES_FILE" > "$ISSUES_FILE.tmp"
    mv "$ISSUES_FILE.tmp" "$ISSUES_FILE"
    jq -c '.[] | select(.id == 501)' "$ISSUES_FILE"; exit 0;;
  'api repos/'*'/pulls/'*'/reviews/101/comments --paginate')
    printf '%s' '[{"path":"notes.txt","line":2,"body":"Consider clarifying this line.","html_url":"https://gh.example/acme/app/pull/4821#discussion_r88"}]'; exit 0;;
  'api repos/'*'/reviews --paginate'|'api repos/'*'/comments --paginate')
    printf '[]'; exit 0;;
  'api repos/'*'/pulls/'*'/reviews --method POST'*)
    cat >/dev/null
    printf '%s' '{"id":101,"state":"commented","html_url":"https://gh.example/acme/app/pull/4821#pullrequestreview-101","submitted_at":"2026-01-01T00:00:00Z"}'; exit 0;;
  'api repos/'*'/pulls/'*' --jq .head.sha')
    printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; exit 0;;
  'api user --jq .login')
    printf 'someone-else'; exit 0;;
esac
echo "evals-stub-gh: unexpected call (\$*)" >&2
exit 1
EOF
chmod +x "$EVAL_STUBBIN/gh"

eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,
 "comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.","rigor":"pointed","severity":"informational"}]}
EOF

mark() { wc -l <"$EVAL_STUBLOG" | tr -d ' '; }

m="$(mark)"
run_review post acme/app "$PR" --dry-run --json
assert_exit "dry-run with default --summary off succeeds" 0
assert_json_field "default dry-run reports summary action none" "none" '.data.summary.action'
if tail -n +"$(( m + 1 ))" "$EVAL_STUBLOG" | matches '/issues/'; then
  bad "--summary off dry-run queried issue comments"
else
  good "--summary off dry-run makes zero issue-comment calls"
fi

m="$(mark)"
run_review post acme/app "$PR" --summary comment --dry-run --json
assert_exit "dry-run --summary comment (no self comment) succeeds" 0
assert_json_field "dry-run plans create when only other author posted marker" "create" '.data.summary.action'
if tail -n +"$(( m + 1 ))" "$EVAL_STUBLOG" | matches -E -- '--method (POST|PATCH|PUT)'; then
  bad "dry-run wrote to host"
else
  good "dry-run --summary comment wrote nothing to host"
fi

run_review post acme/app "$PR" --summary comment --json
assert_exit "real post --summary comment (create) succeeds" 0
assert_json_field "real post reports summary action create" "create" '.data.summary.action'
assert_json_field "real post records summary comment_id 501" "501" '.data.summary.comment_id'
if [ "$(jq -r '.summary.action' <"$STATE_DIR/post.json")" = "create" ]; then
  good "post.json carries summary action create"
else
  bad "post.json summary action mismatch: $(cat "$STATE_DIR/post.json")"
fi
if [ "$(jq -r '[.[] | select(.id == 501)] | length' "$ISSUES_FILE")" -eq 1 ]; then
  good "created summary comment 501 in issue comments"
else
  bad "summary comment 501 missing from $ISSUES_FILE"
fi
created_body="$(jq -r '.[] | select(.id == 501) | .body' "$ISSUES_FILE")"
if printf '%s' "$created_body" | matches -F 'https://gh.example/acme/app/pull/4821#discussion_r88' \
  && printf '%s' "$created_body" | matches -F '<!-- verdict-summary head=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa -->'; then
  good "created summary comment carries inline link and summary marker"
else
  bad "created summary comment missing link or marker: $created_body"
fi

run_review post acme/app "$PR" --summary comment --dry-run --json
assert_exit "dry-run after summary creation succeeds" 0
assert_json_field "subsequent dry-run plans update in place" "update" '.data.summary.action'
assert_json_field "subsequent dry-run targets comment 501" "501" '.data.summary.comment_id'

m="$(mark)"
run_review post acme/app "$PR" --summary comment --force --json
assert_exit "subsequent real post --summary comment --force updates in place" 0
assert_json_field "subsequent post reports summary action update" "update" '.data.summary.action'
if tail -n +"$(( m + 1 ))" "$EVAL_STUBLOG" | matches "issues/${PR}/comments --method POST"; then
  bad "subsequent post created duplicate summary comment instead of PATCH"
elif tail -n +"$(( m + 1 ))" "$EVAL_STUBLOG" | matches "issues/comments/501 --method PATCH"; then
  good "subsequent post updated comment 501 in place via PATCH without creating a duplicate"
else
  bad "subsequent post did not PATCH comment 501"
fi
if [ "$(jq -r 'length' "$ISSUES_FILE")" -eq 2 ] \
  && [ "$(jq -r '.[] | select(.id == 301) | .body | split("\n")[0]' "$ISSUES_FILE")" = "External summary" ]; then
  good "updated in place while leaving stranger comment 301 untouched"
else
  bad "unexpected issue comments state: $(cat "$ISSUES_FILE")"
fi

run_review post acme/app "$PR" --summary bogus --json
assert_exit "invalid --summary exits non-zero" 1
assert_json_field "invalid --summary refuses bad-flags" "bad-flags" '.error.code'

eval_end
