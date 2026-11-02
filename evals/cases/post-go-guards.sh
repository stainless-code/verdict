#!/usr/bin/env bash
# Case post-go-guards: bounded post guards (audit 02 + audit 21).
#
# Grades (hermetic — crafted bundles, smart gh stub, no network):
#   G1  secondary rate limit retries with bounded backoff: a 429 carrying a
#       retry-after hint retries and posts exactly once (exit 0, two reviews
#       calls, one review created).
#   G2  a persistent 429 fails fast after three attempts (exit 1, post-failed,
#       three reviews calls, no post.json) — bounded, never hanging the run.
#   G3  oversized bodies trim with a notice: a body past the 65,536-char
#       hosting limit posts once with the notice and an intact footer instead
#       of submitting a doomed review.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

HEAD="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

# Overlay a smart gh stub: pulls/user answer locally, the reviews POST fails
# with a 429 carrying retry-after while $EVAL_SANDBOX/rate-count stays below
# $EVAL_SANDBOX/rate-budget, then succeeds. Restored on exit.
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
    if [ -f "${EVAL_SANDBOX:-/tmp}/rate-budget" ]; then
      count="$(cat "${EVAL_SANDBOX:-/tmp}/rate-count" 2>/dev/null || echo 0)"
      budget="$(cat "${EVAL_SANDBOX:-/tmp}/rate-budget")"
      printf '%s' "$(( count + 1 ))" > "${EVAL_SANDBOX:-/tmp}/rate-count"
      if [ "$count" -lt "$budget" ]; then
        echo "HTTP 429: You have exceeded a secondary rate limit. retry-after: 0, slow down" >&2
        exit 1
      fi
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

# reviews_posts_since <mark> — reviews POST calls logged after <mark>.
reviews_posts_since() {
  local n
  n=$(tail -n +"$(( $1 + 1 ))" "$EVAL_STUBLOG" 2>/dev/null | grep -c "reviews" || true)
  printf '%s' "$n"
}
mark() { wc -l <"$EVAL_STUBLOG" | tr -d ' '; }

mk_comment() { # $1 = pr — clean bundle carrying a non-binding review
  eval_mk_bundle "$1"
  eval_write_review "$1" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,"comments":[]}
EOF
}

# --- G1: one 429 retries and posts exactly once ---
PR=4471
mk_comment "$PR"
printf '%s' '1' > "$EVAL_SANDBOX/rate-budget"
rm -f "$EVAL_SANDBOX/rate-count"
m="$(mark)"
run_review post acme/app "$PR" --json
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$event" = "COMMENT" ] \
  && [ "$(reviews_posts_since "$m")" -eq 2 ]; then
  good "one 429 retries and posts exactly once (exit 0, two attempts)"
else
  bad "rate retry gave exit $EVAL_STATUS event '$event' calls $(reviews_posts_since "$m"), want 0/COMMENT/2"
fi
if grep -q "transient GitHub error — retry 1/2" "$EVAL_ERR"; then
  good "retry warns naming the attempt"
else
  bad "retry stderr: $(head -c 200 "$EVAL_ERR")"
fi
if [ "$(jq -r '.head // empty' "$EVAL_SANDBOX/.review/$PR/post.json" 2>/dev/null)" = "$HEAD" ]; then
  good "retried post records the reviewed SHA"
else
  bad "retried post wrote no post.json"
fi
rm -f "$EVAL_SANDBOX/rate-budget" "$EVAL_SANDBOX/rate-count"

# --- G2: persistent 429 fails after three attempts, arms nothing ---
PR=4472
mk_comment "$PR"
printf '%s' '99' > "$EVAL_SANDBOX/rate-budget"
rm -f "$EVAL_SANDBOX/rate-count"
m="$(mark)"
run_review post acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "post-failed" ] \
  && [ "$(reviews_posts_since "$m")" -eq 3 ]; then
  good "persistent 429 fails after three attempts (exit 1, post-failed)"
else
  bad "persistent 429 gave exit $EVAL_STATUS code '$code' calls $(reviews_posts_since "$m"), want 1/post-failed/3"
fi
if [ ! -e "$EVAL_SANDBOX/.review/$PR/post.json" ] \
  && [ -z "$(ls "$EVAL_SANDBOX/.review/$PR"/.posted_* 2>/dev/null || true)" ]; then
  good "bounded failure arms nothing (no post.json, no marker)"
else
  bad "bounded failure armed the record"
fi
rm -f "$EVAL_SANDBOX/rate-budget" "$EVAL_SANDBOX/rate-count"

# --- G3: oversized body trims with a notice, footer intact ---
PR=4473
eval_mk_bundle "$PR"
python3 -c "print('{\"schema_version\":\"1\",\"body\":\"> ✅ ' + 'x' * 70000 + '\",\"approved\":false,\"comments\":[]}')" \
  | eval_write_review "$PR"
export EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/posted-4473.json"
rm -f "$EVAL_SAVE_PAYLOAD"
m="$(mark)"
run_review post acme/app "$PR" --json
unset EVAL_SAVE_PAYLOAD
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$event" = "COMMENT" ] \
  && [ "$(reviews_posts_since "$m")" -eq 1 ]; then
  good "oversized body posts once as COMMENT (exit 0)"
else
  bad "oversized body gave exit $EVAL_STATUS event '$event', want 0/COMMENT"
fi
if grep -q "trimmed with a notice" "$EVAL_ERR" \
  && grep -q "exceeded the 65,536-char hosting limit" "$EVAL_SANDBOX/posted-4473.json" \
  && grep -q "verdict-footer" "$EVAL_SANDBOX/posted-4473.json"; then
  good "posted body carries the trim notice with the footer intact"
else
  bad "trim stderr/payload: $(head -c 200 "$EVAL_ERR")"
fi
if python3 -c "import json,sys; sys.exit(0 if len(json.load(open('$EVAL_SANDBOX/posted-4473.json'))['body']) <= 65536 else 1)"; then
  good "posted body fits the hosting limit"
else
  bad "posted body still exceeds the hosting limit"
fi

if [ "$fails" -ne 0 ]; then echo "case post-go-guards: FAIL ($fails)"; exit 1; fi
echo "case post-go-guards: PASS"
