#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

HEADA="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
HEADB="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

STUB_SAVED="$EVAL_SANDBOX/gh-stub-orig"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
restore_stub() { cp "$STUB_SAVED" "$EVAL_STUBBIN/gh"; }
trap restore_stub EXIT
cat > "$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
if [ "${1:-}" = "api" ] && [ "${2:-}" = "graphql" ]; then
  printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}'; exit 0
fi
case "$*" in
  *reviews*)
    if [ -f "${EVAL_SANDBOX:-/tmp}/post-fail" ]; then
      echo "evals-stub-gh: POST rejected" >&2; exit 1
    fi
    if [ -n "${EVAL_SAVE_PAYLOAD:-}" ]; then cat > "$EVAL_SAVE_PAYLOAD"; fi
    printf '%s' '{"id":101,"state":"commented","html_url":"https://example.invalid/r/101","submitted_at":"2026-01-01T00:00:00Z"}'
    exit 0;;
  *pulls*)
    printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; exit 0;;
  *user*)
    printf 'someone-else'; exit 0;;
esac
echo "evals-stub-gh: network disabled ($*)" >&2
exit 1
EOF
chmod +x "$EVAL_STUBBIN/gh"

reviews_posts_since() {
  local n
  n=$(tail -n +"$(( $1 + 1 ))" "$EVAL_STUBLOG" 2>/dev/null | grep -c "reviews" || true)
  printf '%s' "$n"
}
mark() { wc -l <"$EVAL_STUBLOG" | tr -d ' '; }

run_review_in() {
  local cwd="$1"
  shift
  set +e
  (
    cd "$cwd" || exit 99
    PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" "$@" >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  EVAL_STATUS=$?
  set -e
  return 0
}

mk_comment() { # $1 = pr — clean bundle carrying a non-binding review
  eval_mk_bundle "$1"
  eval_write_review "$1" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,"comments":[]}
EOF
}

PR=4295
mk_comment "$PR"
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] && [ ! -f "$EVAL_SANDBOX/.verdict/state/$PR/post.json" ]; then
  good "dry run posts nothing and writes no post.json"
else
  bad "dry run exits $EVAL_STATUS / post.json present"
fi

PR=4296
mk_comment "$PR"
cp "$EVAL_SANDBOX/.verdict/state/$PR/review.json" "$EVAL_SANDBOX/review-$PR.json"
export EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/posted-$PR.json"
rm -f "$EVAL_SAVE_PAYLOAD"
m="$(mark)"
run_review post acme/app "$PR" --json
unset EVAL_SAVE_PAYLOAD
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.head // empty' "$EVAL_SANDBOX/.verdict/state/$PR/post.json" 2>/dev/null)" = "$HEADA" ]; then
  good "first post completes and records the reviewed SHA"
else
  bad "first post exits $EVAL_STATUS / head not recorded"
fi
if [ "$(reviews_posts_since "$m")" -eq 1 ]; then
  good "first post made exactly one network POST"
else
  bad "first post made $(reviews_posts_since "$m") POST(s)"
fi

eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — reworded body, same SHA.","approved":false,"comments":[]}
EOF
export EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/reworded.json"
m="$(mark)"
run_review post acme/app "$PR" --json
unset EVAL_SAVE_PAYLOAD
assert_exit "new content on the same head posts" 0
assert_eq "new content makes one write" 1 "$(reviews_posts_since "$m")"
jq --arg h "$HEADA" '[{body:.body,commit_id:$h}]' "$EVAL_SANDBOX/reworded.json" \
  >"$EVAL_SANDBOX/.verdict/state/$PR/reviews.json"
m="$(mark)"
run_review post acme/app "$PR" --json
assert_exit "identical remote content suppresses publication" 1
assert_json_field "nothing missing names duplicate-payload" duplicate-payload '.error.code'
assert_eq "remote duplicate makes no write" 0 "$(reviews_posts_since "$m")"
jq --arg h "$HEADB" '.headRefOid = $h' "$EVAL_SANDBOX/.verdict/state/$PR/PR.json" \
  >"$EVAL_SANDBOX/.verdict/state/$PR/PR.json.new"
mv "$EVAL_SANDBOX/.verdict/state/$PR/PR.json.new" "$EVAL_SANDBOX/.verdict/state/$PR/PR.json"
run_review post acme/app "$PR" --json
assert_exit "identical content on a new head re-arms" 0
assert_json_field "new head is explicitly rearmed" true '.data.rearmed'
assert_json_field "new head names previous review head" "$HEADA" '.data.previous_head'
assert_json_field "receipt records rearm" true '.rearmed' "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
printf '[]\n' >"$EVAL_SANDBOX/.verdict/state/$PR/reviews.json"
m="$(mark)"
run_review post acme/app "$PR" --json
assert_exit "local receipts cannot override empty remote" 0
assert_eq "empty remote permits a new write" 1 "$(reviews_posts_since "$m")"

PR=4297
mk_comment "$PR"
touch "$EVAL_SANDBOX/post-fail"
run_review post acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "post-failed" ] \
  && [ ! -f "$EVAL_SANDBOX/.verdict/state/$PR/post.json" ]; then
  good "failed post writes no post.json"
else
  bad "failed post gave exit $EVAL_STATUS code '$code' / post.json present"
fi
rm -f "$EVAL_SANDBOX/post-fail"
run_review post acme/app "$PR" --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.head // empty' "$EVAL_SANDBOX/.verdict/state/$PR/post.json" 2>/dev/null)" = "$HEADA" ]; then
  good "retry after failure proceeds (nothing was armed to refuse it)"
else
  bad "retry after failure exits $EVAL_STATUS, want 0"
fi

PR=4298
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
jq -n --arg h "$HEADB" \
  '{schema_version:"1",review:{id:99},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null}' \
  > "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
run_review report acme/app "$PR" --completeness
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "stale-head" "$EVAL_ERR" \
  && grep -q "fetch acme/app $PR --base-sha ${HEADB:0:7}" "$EVAL_ERR"; then
  good "stale-head with a posted head names the --base-sha advance form"
else
  bad "stale-head stderr: $(head -c 300 "$EVAL_ERR")"
fi
PR=4299
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
run_review report acme/app "$PR" --completeness
assert_exit "missing-post (no head) fails the gate" 1
assert_file_grep "missing-post names the typed code" "$EVAL_ERR" "missing-post"
assert_file_grep "missing-post names the plain fetch" "$EVAL_ERR" "fetch acme/app $PR"
assert_stream_nogrep "missing-post keeps no --base-sha" "$EVAL_ERR" "--base-sha"

PR=4307
eval_mk_bundle "$PR"
cat > "$EVAL_SANDBOX/.verdict/state/$PR/PR.json" <<EOF
{"schema_version":"1","provider":"gitlab","number":$PR,"title":"t","body":"","base_ref":"main","head_ref":"feat","base_sha":"$HEADB","head_sha":"$HEADA","author":"mate","url":"https://gitlab.example/acme/app/-/merge_requests/$PR","state":"open","draft":false,"files":[],"additions":0,"deletions":0}
EOF
printf '[]' | eval_write_findings "$PR"
jq -n --arg h "$HEADB" \
  '{schema_version:"1",review:{id:99},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null}' \
  > "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
run_review report acme/app "$PR" --completeness
assert_exit "provider stale-head fails the gate" 1
assert_file_grep "provider stale-head names the typed code" "$EVAL_ERR" "stale-head"
assert_file_grep "provider stale-head names the provider and --base-sha advance" "$EVAL_ERR" "fetch acme/app $PR --provider gitlab --base-sha ${HEADB:0:7}"
assert_stream_nogrep "provider stale-head keeps no provider-less fetch" "$EVAL_ERR" "fetch acme/app $PR --base-sha"

PR=4309
eval_mk_bundle "$PR"
cat > "$EVAL_SANDBOX/.verdict/state/$PR/PR.json" <<EOF
{"state":"OPEN","provider":"github","headRefOid":"$HEADA","baseRefOid":"$HEADB","author":{"login":"mate"}}
EOF
printf '[]' | eval_write_findings "$PR"
jq -n --arg h "$HEADB" \
  '{schema_version:"1",review:{id:99},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null}' \
  > "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
run_review report acme/app "$PR" --completeness
assert_exit "github stale-head fails the gate" 1
assert_file_grep "github stale-head keeps the plain --base-sha advance" "$EVAL_ERR" "fetch acme/app $PR --base-sha ${HEADB:0:7}"
assert_stream_nogrep "github stale-head names no provider flag" "$EVAL_ERR" "--provider"

PR=4308
eval_mk_bundle "$PR"
cat > "$EVAL_SANDBOX/.verdict/state/$PR/PR.json" <<EOF
{"schema_version":"1","provider":"gitlab","number":$PR,"title":"t","body":"","base_ref":"main","head_ref":"feat","base_sha":"$HEADB","head_sha":"$HEADA","author":"mate","url":"https://gitlab.example/acme/app/-/merge_requests/$PR","state":"open","draft":false,"files":[],"additions":0,"deletions":0}
EOF
printf '[]' | eval_write_findings "$PR"
run_review report acme/app "$PR" --completeness
assert_exit "provider missing-post fails the gate" 1
assert_file_grep "provider missing-post names the typed code" "$EVAL_ERR" "missing-post"
assert_file_grep "provider missing-post names the plain provider fetch" "$EVAL_ERR" "fetch acme/app $PR --provider gitlab"
assert_stream_nogrep "provider missing-post keeps no --base-sha" "$EVAL_ERR" "--base-sha"

cat > "$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
if [ "${1:-}" = "api" ] && [ "${2:-}" = "graphql" ]; then
  printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}'; exit 0
fi
case "$*" in
  *reviews*)
    if [ -n "${EVAL_SAVE_PAYLOAD:-}" ]; then cat > "$EVAL_SAVE_PAYLOAD"; fi
    printf '%s' '{"id":101,"state":"commented","html_url":"https://example.invalid/r/101","submitted_at":"2026-01-01T00:00:00Z"}'
    exit 0;;
  *pulls*)
    printf 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'; exit 0;;
  *user*)
    printf 'someone-else'; exit 0;;
esac
echo "evals-stub-gh: network disabled ($*)" >&2
exit 1
EOF
chmod +x "$EVAL_STUBBIN/gh"

PR=4300
mk_comment "$PR"
export EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/posted-4300.json"
rm -f "$EVAL_SAVE_PAYLOAD"
m="$(mark)"
run_review post acme/app "$PR" --json
unset EVAL_SAVE_PAYLOAD
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$event" = "COMMENT" ] \
  && [ "$(reviews_posts_since "$m")" -eq 1 ]; then
  good "mid-run push posts once as COMMENT (exit 0, warn-and-keep, no refusal)"
else
  bad "mid-run push gave exit $EVAL_STATUS event '$event', want 0/COMMENT"
fi
if grep -q "HEAD moved since fetch" "$EVAL_ERR" \
  && grep -q "fetch acme/app $PR --base-sha ${HEADA:0:7}" "$EVAL_ERR"; then
  good "drift warn on stderr names the exact fetch --base-sha advance command"
else
  bad "drift warn stderr: $(head -c 300 "$EVAL_ERR")"
fi
if grep -q "⚠️" "$EVAL_SANDBOX/posted-4300.json" \
  && grep -q "fetch acme/app $PR --base-sha ${HEADA:0:7}" "$EVAL_SANDBOX/posted-4300.json"; then
  good "posted body carries the drift note naming the advance command"
else
  bad "posted payload: $(head -c 300 "$EVAL_SANDBOX/posted-4300.json" 2>/dev/null)"
fi

PR=4306
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}
EOF
export EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/posted-4306.json"
rm -f "$EVAL_SAVE_PAYLOAD"
m="$(mark)"
run_review post acme/app "$PR" --bind --approve=true --json
unset EVAL_SAVE_PAYLOAD
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$event" = "COMMENT" ] \
  && [ "$(reviews_posts_since "$m")" -eq 1 ]; then
  good "stale binding APPROVE downgraded to COMMENT (exit 0, posted once)"
else
  bad "stale APPROVE gave exit $EVAL_STATUS event '$event', want 0/COMMENT"
fi
if grep -q "downgraded to COMMENT" "$EVAL_ERR" \
  && grep -q "fetch acme/app $PR --base-sha ${HEADA:0:7}" "$EVAL_ERR"; then
  good "downgrade warn on stderr names the reason + advance command"
else
  bad "downgrade stderr: $(head -c 300 "$EVAL_ERR")"
fi
if grep -q "posting as COMMENT instead of APPROVE" "$EVAL_SANDBOX/posted-4306.json" \
  && [ "$(jq -r '.event // empty' "$EVAL_SANDBOX/posted-4306.json" 2>/dev/null)" = "COMMENT" ]; then
  good "posted payload carries the downgrade note and no APPROVE event"
else
  bad "downgraded payload: $(head -c 300 "$EVAL_SANDBOX/posted-4306.json" 2>/dev/null)"
fi

cat > "$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
if [ "${1:-}" = "api" ] && [ "${2:-}" = "graphql" ]; then
  printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}'; exit 0
fi
case "$*" in
  *reviews*)
    printf '%s' '{"id":101,"state":"commented","html_url":"https://example.invalid/r/101","submitted_at":"2026-01-01T00:00:00Z"}'
    exit 0;;
  *user*)
    printf 'someone-else'; exit 0;;
esac
echo "evals-stub-gh: network disabled ($*)" >&2
exit 1
EOF
chmod +x "$EVAL_STUBBIN/gh"

PR=4294
mk_comment "$PR"
m="$(mark)"
run_review post acme/app "$PR" --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "live SHA lookup failed" "$EVAL_ERR" \
  && [ "$(reviews_posts_since "$m")" -eq 1 ]; then
  good "unreachable live lookup warns and proceeds (exit 0, posted once)"
else
  bad "gh-down gave exit $EVAL_STATUS / stderr: $(head -c 300 "$EVAL_ERR")"
fi

PR=4293
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}
EOF
m="$(mark)"
run_review post acme/app "$PR" --bind --json
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$event" = "APPROVE" ] \
  && grep -q "live SHA lookup failed" "$EVAL_ERR" \
  && [ "$(reviews_posts_since "$m")" -eq 1 ]; then
  good "gh-down binding APPROVE warns and proceeds (exit 0, APPROVE, posted once)"
else
  bad "gh-down APPROVE gave exit $EVAL_STATUS event '$event' / stderr: $(head -c 300 "$EVAL_ERR")"
fi

eval_end
