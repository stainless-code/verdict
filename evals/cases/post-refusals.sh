#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

reviews_posts_since() {
  local n
  n=$(tail -n +"$(( $1 + 1 ))" "$EVAL_STUBLOG" 2>/dev/null | grep -c "reviews.*--method POST" || true)
  printf '%s' "$n"
}
mark() { wc -l <"$EVAL_STUBLOG" | tr -d ' '; }

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
  'api repos/'*'/reviews --paginate'|'api repos/'*'/comments --paginate')
    part="${2#*/pulls/}"
    pr="${part%%/*}"
    file="${2##*/}"
    if [ "$file" = comments ]; then file=inline; fi
    snapshot=".verdict/state/$pr/$file.json"
    if [ -f "$snapshot" ]; then cat "$snapshot"; else echo '[]'; fi
    exit 0;;

  *reviews*)
    mode="$(cat "${EVAL_SANDBOX:-/tmp}/post-mode" 2>/dev/null || echo ok)"
    case "$mode" in
      garbage) printf '%s' 'not json{{{'; exit 0;;
      empty) printf ''; exit 0;;
    esac
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

mk_clean() { # $1 = pr — clean bundle carrying a non-binding review
  eval_mk_bundle "$1"
  eval_write_review "$1" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,"comments":[]}
EOF
}

PR=4311
mk_clean "$PR"
printf '%s' '[5]' > "$EVAL_SANDBOX/.verdict/state/$PR/findings.json"
before="$(shasum -a 256 "$EVAL_SANDBOX/.verdict/state/$PR/findings.json" | cut -d' ' -f1)"
m="$(mark)"
run_review post acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "corrupt-store" ]; then
  good "post refuses non-object prior elements with corrupt-store (exit 1)"
else
  bad "post on [5] prior gave exit $EVAL_STATUS code '$code', want 1/corrupt-store"
fi
after_p1="$(shasum -a 256 "$EVAL_SANDBOX/.verdict/state/$PR/findings.json" | cut -d' ' -f1)"
if [ "$before" = "$after_p1" ] \
  && [ ! -e "$EVAL_SANDBOX/.verdict/state/$PR/post.json" ] \
  && [ "$(reviews_posts_since "$m")" -eq 0 ]; then
  good "refused post kept the seeded findings byte-identical (no post.json, no POST)"
else
  bad "refused post rewrote artifacts or posted"
fi

LB="$EVAL_SANDBOX/.verdict/state/local-p1"
eval_mk_bundle "local-p1"
eval_write_review "local-p1" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,"comments":[]}
EOF
printf '%s' '[5]' > "$LB/findings.json"
before="$(shasum -a 256 "$LB/findings.json" | cut -d' ' -f1)"
run_review record acme/app "local-p1" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "corrupt-store" ] \
  && [ "$(shasum -a 256 "$LB/findings.json" | cut -d' ' -f1)" = "$before" ]; then
  good "record refuses non-object prior elements with corrupt-store (kept)"
else
  bad "record on [5] prior gave exit $EVAL_STATUS code '$code'"
fi

mk_clean "$PR"
printf '%s' '[null]' > "$EVAL_SANDBOX/.verdict/state/$PR/findings.json"
run_review post acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "corrupt-store" ]; then
  good "null prior elements refuse corrupt-store on the write path (exit 1)"
else
  bad "null prior gave exit $EVAL_STATUS code '$code', want 1/corrupt-store"
fi

PR=4312
mk_clean "$PR"
printf '%s' 'garbage' > "$EVAL_SANDBOX/post-mode"
m="$(mark)"
run_review post acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "post-failed" ]; then
  good "garbage POST response refuses post-failed (exit 1)"
else
  bad "garbage POST gave exit $EVAL_STATUS code '$code', want 1/post-failed"
fi
if [ ! -e "$EVAL_SANDBOX/.verdict/state/$PR/post.json" ] \
  && [ -z "$(ls "$EVAL_SANDBOX/.verdict/state/$PR"/.posted_* 2>/dev/null || true)" ]; then
  good "garbage POST armed nothing (no post.json, no marker)"
else
  bad "garbage POST left post.json or a marker behind"
fi
run_review post acme/app "$PR"
if [ "$EVAL_STATUS" -eq 1 ] && grep -q "post failed" "$EVAL_ERR" \
  && grep -q "re-enter: verdict post acme/app $PR" "$EVAL_ERR"; then
  good "text refusal names post failed plus the re-enter command"
else
  bad "garbage text stderr: $(head -c 300 "$EVAL_ERR")"
fi

PR=4313
mk_clean "$PR"
printf '%s' 'empty' > "$EVAL_SANDBOX/post-mode"
run_review post acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "post-failed" ] \
  && [ ! -e "$EVAL_SANDBOX/.verdict/state/$PR/post.json" ] \
  && [ -z "$(ls "$EVAL_SANDBOX/.verdict/state/$PR"/.posted_* 2>/dev/null || true)" ]; then
  good "empty POST response refuses post-failed (arms nothing)"
else
  bad "empty POST gave exit $EVAL_STATUS code '$code'"
fi
rm -f "$EVAL_SANDBOX/post-mode"

PR=4314
eval_mk_bundle "$PR"
m="$(mark)"
run_review post acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "missing-review-file" ] \
  && [ ! -e "$EVAL_SANDBOX/.verdict/state/$PR/post.json" ] \
  && [ "$(reviews_posts_since "$m")" -eq 0 ]; then
  good "missing review.json refuses missing-review-file (no post.json, no POST)"
else
  bad "missing review gave exit $EVAL_STATUS code '$code'"
fi

PR=4315
mk_clean "$PR"
run_review post acme/app "$PR" --approve=yes --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
msg="$(jq -r '.error.message // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "bad-approve" ] \
  && printf '%s' "$msg" | matches "got 'yes'"; then
  good "bad --approve refuses bad-approve naming the value"
else
  bad "bad --approve gave exit $EVAL_STATUS code '$code'"
fi

PR=4316
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}
EOF
cat > "$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
if [ "${1:-}" = "api" ] && [ "${2:-}" = "graphql" ]; then
  printf '%s' 'not json{{{'; exit 0
fi
case "$*" in
  'api repos/'*'/reviews --paginate'|'api repos/'*'/comments --paginate')
    part="${2#*/pulls/}"
    pr="${part%%/*}"
    file="${2##*/}"
    if [ "$file" = comments ]; then file=inline; fi
    snapshot=".verdict/state/$pr/$file.json"
    if [ -f "$snapshot" ]; then cat "$snapshot"; else echo '[]'; fi
    exit 0;;

  *reviews*)
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
export EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/posted-4316.json"
rm -f "$EVAL_SAVE_PAYLOAD"
m="$(mark)"
run_review post acme/app "$PR" --bind --json
unset EVAL_SAVE_PAYLOAD
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$event" = "COMMENT" ] \
  && [ "$(reviews_posts_since "$m")" -eq 1 ]; then
  good "malformed live threads downgrade to COMMENT (exit 0, posted once)"
else
  bad "malformed threads gave exit $EVAL_STATUS event '$event', want 0/COMMENT"
fi
if grep -q "downgrading to COMMENT" "$EVAL_ERR" \
  && grep -q "recheck unreachable" "$EVAL_ERR" \
  && grep -q "recheck unreachable" "$EVAL_SANDBOX/posted-4316.json"; then
  good "downgrade warns with the recheck reason in stderr and posted body"
else
  bad "downgrade stderr: $(head -c 300 "$EVAL_ERR")"
fi

LB="$EVAL_SANDBOX/.verdict/state/local-p7"
eval_mk_bundle "local-p7"
eval_write_review "local-p7" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,"comments":[]}
EOF
m="$(mark)"
run_review post acme/app local-p7 --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "no-pr-target" ] \
  && [ ! -e "$LB/post.json" ] && [ "$(reviews_posts_since "$m")" -eq 0 ]; then
  good "post refuses a local bundle with no-pr-target (no post.json, no POST)"
else
  bad "post on a local bundle gave exit $EVAL_STATUS code '$code', want 1/no-pr-target"
fi

post_help="$("$EVAL_CLI" post --help 2>/dev/null || true)"
trend_help="$("$EVAL_CLI" trend --help 2>/dev/null || true)"
if printf '%s' "$post_help" | matches -q "local bundles refuse typed (no-pr-target)"; then
  good "post --help names no-pr-target for local bundles"
else
  bad "post --help does not name no-pr-target for local bundles"
fi
if printf '%s' "$trend_help" | matches -q "local bundles and non-GitHub providers refuse"; then
  bad "trend --help still claims local bundles refuse"
else
  good "trend --help makes no local-bundle refusal claim (it rolls them up)"
fi

# A captured clause blocks approval, while comments keep the finding visible.
mkdir -p "$EVAL_SANDBOX/.verdict"
cat > "$EVAL_SANDBOX/.verdict/config.json" <<'EOF'
{"schema_version":"1","requirements":{"block_clauses":[{"name":"critical-block","threshold":"critical","paths":["**"]}],"require_checks":["no-open-above-block"]}}
EOF
PR=4399
mk_clean "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"Blocking issue found.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"This breaks the invariant.","category":"logic","rigor":"pointed","severity":"critical"}]}
EOF
m="$(mark)"
run_review post acme/app "$PR" --dry-run --json
assert_exit "a clause-meeting finding can be posted as a comment" 0
assert_json_field "the blocking finding does not promote the comment" "COMMENT" '.data.event'
assert_json_field "the comment payload retains the blocking finding" "1" '.data.findings.open'
run_review post acme/app "$PR" --approve true --bind --force --dry-run --json
assert_exit "the same finding refuses binding approval even with force" 1
assert_json_field "the approval names the configured requirement" "requirement-blocked" '.error.code'
run_review post acme/app "$PR" --request-changes --bind --dry-run --json
assert_exit "the same finding permits a negative binding verdict" 0
assert_json_field "negative verdict stays negative" "REQUEST_CHANGES" '.data.event'
assert_eq "dry-run policy probes never call the reviews endpoint" "0" "$(reviews_posts_since "$m")"

eval_end
