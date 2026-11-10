#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

stub_mark() { wc -l <"$EVAL_STUBLOG" | tr -d ' '; }
reviews_posts_since() {
  local n
  n=$(tail -n +"$(( $1 + 1 ))" "$EVAL_STUBLOG" 2>/dev/null | grep -c "reviews" || true)
  printf '%s' "$n"
}

# A live stub: the refusal leg must prove zero calls, so every argv is logged
# and the review POST answers a plausible payload.
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
    cat >/dev/null 2>&1 || true
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

# One AWS key id in a finding body: the fixture value is fake and the shape is
# what the scan recognizes.
PR=4411
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,
 "comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line. AKIAIOSFODNN7EXAMPLE","rigor":"pointed","severity":"informational"}]}
EOF

run_review post acme/app "$PR" --dry-run --json
assert_exit "a dry run over a secret-shaped finding exits 0" 0
assert_json_field "the dry run warns with the refusal code" "1" \
  '[.warnings[] | select(test("post-secret-shaped"))] | length'
if jq -r '.warnings[]' <"$EVAL_OUT" | grep -q "finding 1 (aws-access-key-id)"; then
  good "the warning locates the site and the shape"
else
  bad "the warning misses the location: $(jq -r '.warnings[]' <"$EVAL_OUT" | head -c 300)"
fi
if jq -r '.warnings[]' <"$EVAL_OUT" | grep -q -- "--allow-secret-values"; then
  good "the warning names the override flag"
else
  bad "the warning does not name --allow-secret-values"
fi
if jq -r '.warnings[]' <"$EVAL_OUT" | grep -q "AKIAIOSFODNN7EXAMPLE"; then
  bad "the warning republished the secret value"
else
  good "the warning never repeats the value"
fi

run_review post acme/app "$PR" --dry-run
assert_exit "the human dry run over a secret-shaped finding exits 0" 0
if grep -q "warn:" "$EVAL_ERR" && grep -q "aws-access-key-id" "$EVAL_ERR"; then
  good "the human dry run warns on stderr with the shape"
else
  bad "human dry-run stderr: $(head -c 300 "$EVAL_ERR")"
fi

m="$(stub_mark)"
run_review post acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
msg="$(jq -r '.error.message // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "post-secret-shaped" ]; then
  good "a real post refuses post-secret-shaped (exit 1)"
else
  bad "secret-shaped post gave exit $EVAL_STATUS code '$code', want 1/post-secret-shaped"
fi
if printf '%s' "$msg" | matches -F -- "finding 1 (aws-access-key-id)" \
  && printf '%s' "$msg" | matches -F -- "--allow-secret-values"; then
  good "the refusal locates the site and the shape and names the override"
else
  bad "refusal message: $(printf '%s' "$msg" | head -c 300)"
fi
if printf '%s' "$msg" | matches -qF -- "AKIAIOSFODNN7EXAMPLE"; then
  bad "the refusal republished the secret value"
else
  good "the refusal never repeats the value"
fi
if [ "$(stub_mark)" -eq "$m" ] && [ ! -e "$EVAL_SANDBOX/.verdict/state/$PR/post.json" ]; then
  good "the refusal made zero host calls and wrote no post.json"
else
  bad "the refused post called the host or wrote post.json: $(tail -n +"$(( m + 1 ))" "$EVAL_STUBLOG")"
fi

m="$(stub_mark)"
run_review post acme/app "$PR" --allow-secret-values --json
assert_exit "--allow-secret-values publishes the review" 0
assert_json_field "the overridden post is a real post, not a preview" "false" '.data.dry_run'
assert_json_field "post.json records the shape the override let through" "aws-access-key-id" \
  '.secret_shapes_allowed | join(",")' "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
assert_eq "the overridden post reached the host once" "1" "$(reviews_posts_since "$m")"

# A review with no secret shape is untouched: no warning, no receipt field.
PR=4412
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,
 "comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.","rigor":"pointed","severity":"informational"}]}
EOF
run_review post acme/app "$PR" --json
assert_exit "a review with no secret shape posts" 0
assert_json_field "a clean review warns nothing about secret shapes" "0" \
  '[.warnings[]? | select(test("secret-shaped"))] | length'
assert_json_field "a clean receipt carries no secret_shapes_allowed" "false" \
  'has("secret_shapes_allowed")' "$EVAL_SANDBOX/.verdict/state/$PR/post.json"

eval_end
