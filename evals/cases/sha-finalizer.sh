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
cp "$EVAL_SANDBOX/.verdict/state/$PR/post.json" "$EVAL_SANDBOX/post-before.json"
m="$(mark)"
run_review post acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "already-posted" ]; then
  good "same-SHA repost refused with already-posted (exit 1)"
else
  bad "same-SHA repost gave exit $EVAL_STATUS code '$code', want 1/already-posted"
fi
run_review post acme/app "$PR"
if [ "$EVAL_STATUS" -eq 1 ] && grep -q -- "--force" "$EVAL_ERR"; then
  good "refusal names the --force override"
else
  bad "refusal stderr: $(head -c 300 "$EVAL_ERR")"
fi
if cmp -s "$EVAL_SANDBOX/post-before.json" "$EVAL_SANDBOX/.verdict/state/$PR/post.json" \
  && [ "$(reviews_posts_since "$m")" -eq 0 ]; then
  good "refused repost posted nothing (no POST, post.json identical)"
else
  bad "refused repost posted or rewrote the record"
fi

FRESH="$EVAL_SANDBOX/fresh"
rm -rf "$FRESH"
mkdir -p "$FRESH/.verdict/state"
cp -R "$EVAL_SANDBOX/.verdict/state/$PR" "$FRESH/.verdict/state/$PR"
# A fresh checkout of the same repo carries the same controls the snapshot
# pinned; only the uncommitted receipts and artifacts are new.
for rel in config.json kb.jsonl kb-joins.jsonl learnings.md overrides; do
  [ -e "$EVAL_SANDBOX/.verdict/$rel" ] || continue
  cp -R "$EVAL_SANDBOX/.verdict/$rel" "$FRESH/.verdict/$rel"
done
cp "$EVAL_SANDBOX/review-$PR.json" "$FRESH/.verdict/state/$PR/review.json"
rm -f "$FRESH/.verdict/state/$PR/post.json" "$FRESH/.verdict/state/$PR"/.posted_*
posted_body="$(jq -r '.body // empty' "$EVAL_SANDBOX/posted-$PR.json" 2>/dev/null || true)"
jq -n --arg b "$posted_body" --arg h "$HEADA" \
  '[{id:901,body:$b,commit_id:$h,submitted_at:"2026-01-01T00:00:00Z"}]' \
  >"$FRESH/.verdict/state/$PR/reviews.json"
m="$(mark)"
run_review_in "$FRESH" post acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
msg="$(jq -r '.error.message // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "already-posted" ] \
  && [ "$(reviews_posts_since "$m")" -eq 0 ]; then
  good "fresh bundle refuses already-posted from reviews.json (no network POST)"
else
  bad "fresh bundle gave exit $EVAL_STATUS code '$code' calls $(reviews_posts_since "$m"), want 1/already-posted/0"
fi
if printf '%s' "$msg" | matches "re-fetch if the earlier review was deleted"; then
  good "fresh-bundle refusal names the stale-review re-fetch hint"
else
  bad "fresh-bundle refusal message: $(printf '%s' "$msg" | head -c 300)"
fi

jq --arg h "$HEADB" '.[0].commit_id = $h' "$FRESH/.verdict/state/$PR/reviews.json" \
  >"$FRESH/.verdict/state/$PR/reviews.json.new"
mv "$FRESH/.verdict/state/$PR/reviews.json.new" "$FRESH/.verdict/state/$PR/reviews.json"
m="$(mark)"
run_review_in "$FRESH" post acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "duplicate-payload" ] \
  && [ "$(reviews_posts_since "$m")" -eq 0 ]; then
  good "fresh bundle refuses duplicate-payload from the live marker (no network POST)"
else
  bad "fresh duplicate gave exit $EVAL_STATUS code '$code' calls $(reviews_posts_since "$m"), want 1/duplicate-payload/0"
fi
m="$(mark)"
run_review_in "$FRESH" post acme/app "$PR" --force --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(reviews_posts_since "$m")" -eq 1 ]; then
  good "--force posts through the live-review guard (exit 0, one POST)"
else
  bad "fresh --force gave exit $EVAL_STATUS calls $(reviews_posts_since "$m"), want 0/1"
fi

run_review post acme/app "$PR" --json
msg="$(jq -r '.error.message // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] \
  && printf '%s' "$msg" | matches "fetch acme/app $PR --base-sha ${HEADA:0:7}" \
  && printf '%s' "$msg" | matches -- "--force"; then
  good "--json error.message names the exact advance command + --force"
else
  bad "--json error.message: $(head -c 300 "$EVAL_OUT")"
fi
run_review post acme/app "$PR"
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "fetch acme/app $PR --base-sha ${HEADA:0:7}" "$EVAL_ERR" \
  && grep -q -- "--force" "$EVAL_ERR"; then
  good "text refusal names the exact advance command + --force"
else
  bad "text refusal stderr: $(head -c 300 "$EVAL_ERR")"
fi

run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && cmp -s "$EVAL_SANDBOX/post-before.json" "$EVAL_SANDBOX/.verdict/state/$PR/post.json"; then
  good "dry run after a completed post stays advisory and keeps the record"
else
  bad "dry run after completed post exits $EVAL_STATUS"
fi

jq --arg h "$HEADB" '.headRefOid = $h' "$EVAL_SANDBOX/.verdict/state/$PR/PR.json" \
  > "$EVAL_SANDBOX/.verdict/state/$PR/PR.json.new"
mv "$EVAL_SANDBOX/.verdict/state/$PR/PR.json.new" "$EVAL_SANDBOX/.verdict/state/$PR/PR.json"
run_review post acme/app "$PR" --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.head // empty' "$EVAL_SANDBOX/.verdict/state/$PR/post.json" 2>/dev/null)" = "$HEADB" ]; then
  good "new-SHA post proceeds and refreshes the record"
else
  bad "new-SHA post exits $EVAL_STATUS / head not refreshed"
fi

eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — forced repost on the same SHA.","approved":false,"comments":[]}
EOF
m="$(mark)"
run_review post acme/app "$PR" --force --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.head // empty' "$EVAL_SANDBOX/.verdict/state/$PR/post.json" 2>/dev/null)" = "$HEADB" ]; then
  good "--force overrides the same-SHA refusal"
else
  bad "forced repost exits $EVAL_STATUS, want 0"
fi
if [ "$(reviews_posts_since "$m")" -eq 1 ]; then
  good "forced repost made exactly one network POST"
else
  bad "forced repost made $(reviews_posts_since "$m") POST(s)"
fi

eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --bind --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "already-posted" ] && [ "$event" = "" ]; then
  good "APPROVE repost on the same SHA refused (no silent bind)"
else
  bad "APPROVE repost gave exit $EVAL_STATUS code '$code' event '$event'"
fi

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
assert_file_grep "provider stale-head names the plain provider fetch" "$EVAL_ERR" "fetch acme/app $PR --provider gitlab"
assert_stream_nogrep "provider stale-head keeps no --base-sha" "$EVAL_ERR" "--base-sha"
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
