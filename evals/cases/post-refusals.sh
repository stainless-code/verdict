#!/usr/bin/env bash
# Case post-refusals: typed refusals on the post path (R1: crashes become envelopes).
#
# Grades (hermetic — crafted bundles, smart gh stub, no network):
#   P1  non-object prior findings elements refuse corrupt-store on the post
#       path (exit 1, envelope) with the seeded findings.json byte-identical
#       and no post.json — the merge never starts fresh over history. The
#       same shape refuses identically on the record path.
#   P2  a POST that returns garbage (exit 0, non-JSON) refuses post-failed
#       typed (exit 1, envelope) with no marker file and no post.json —
#       decoding it would crash jq with no envelope and wedge the bundle
#       behind the touched marker.
#   P3  a POST that returns empty (exit 0, no body) refuses post-failed typed
#       with no marker file and no post.json.
#   P4  a missing review.json refuses missing-review-file typed (exit 1)
#       with nothing posted and no post.json.
#   P5  a bad --approve value refuses bad-approve typed (exit 1) naming the value.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

# reviews_posts_since <mark> — reviews POST calls logged after <mark>.
reviews_posts_since() {
  local n
  n=$(tail -n +"$(( $1 + 1 ))" "$EVAL_STUBLOG" 2>/dev/null | grep -c "reviews" || true)
  printf '%s' "$n"
}
mark() { wc -l <"$EVAL_STUBLOG" | tr -d ' '; }

# Overlay a smart gh stub: pulls/user answer locally, the reviews POST
# behavior follows $EVAL_SANDBOX/post-mode (garbage|empty, else success).
# Restored on exit so sibling cases keep the dead stub.
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

# --- P1: non-object prior elements refuse corrupt-store, never start fresh ---
PR=4311
mk_clean "$PR"
printf '%s' '[5]' > "$EVAL_SANDBOX/.review/$PR/findings.json"
before="$(shasum -a 256 "$EVAL_SANDBOX/.review/$PR/findings.json" | cut -d' ' -f1)"
m="$(mark)"
run_review post acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "corrupt-store" ]; then
  good "post refuses non-object prior elements with corrupt-store (exit 1)"
else
  bad "post on [5] prior gave exit $EVAL_STATUS code '$code', want 1/corrupt-store"
fi
after_p1="$(shasum -a 256 "$EVAL_SANDBOX/.review/$PR/findings.json" | cut -d' ' -f1)"
if [ "$before" = "$after_p1" ] \
  && [ ! -e "$EVAL_SANDBOX/.review/$PR/post.json" ] \
  && [ "$(reviews_posts_since "$m")" -eq 0 ]; then
  good "refused post kept the seeded findings byte-identical (no post.json, no POST)"
else
  bad "refused post rewrote artifacts or posted"
fi

# The same shape refuses identically on the record path.
LB="$EVAL_SANDBOX/.review/local-p1"
rm -rf "$LB"; mkdir -p "$LB"
cat > "$LB/source.json" <<'EOF'
{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}
EOF
cat > "$LB/full.diff" <<'EOF'
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

# Null elements refuse too: they read as no prior for the read verbs, but the
# merge indexes elements by sig ($byNew[null] crashes), so the write path
# refuses rather than silently starting fresh.
mk_clean "$PR"
printf '%s' '[null]' > "$EVAL_SANDBOX/.review/$PR/findings.json"
run_review post acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "corrupt-store" ]; then
  good "null prior elements refuse corrupt-store on the write path (exit 1)"
else
  bad "null prior gave exit $EVAL_STATUS code '$code', want 1/corrupt-store"
fi

# --- P2: garbage POST response refuses typed, arms nothing ---
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
if [ ! -e "$EVAL_SANDBOX/.review/$PR/post.json" ] \
  && [ -z "$(ls "$EVAL_SANDBOX/.review/$PR"/.posted_* 2>/dev/null || true)" ]; then
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

# --- P3: empty POST response refuses typed, arms nothing ---
PR=4313
mk_clean "$PR"
printf '%s' 'empty' > "$EVAL_SANDBOX/post-mode"
run_review post acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "post-failed" ] \
  && [ ! -e "$EVAL_SANDBOX/.review/$PR/post.json" ] \
  && [ -z "$(ls "$EVAL_SANDBOX/.review/$PR"/.posted_* 2>/dev/null || true)" ]; then
  good "empty POST response refuses post-failed (arms nothing)"
else
  bad "empty POST gave exit $EVAL_STATUS code '$code'"
fi
rm -f "$EVAL_SANDBOX/post-mode"

# --- P4: missing review.json refuses typed, posts nothing ---
PR=4314
eval_mk_bundle "$PR"
m="$(mark)"
run_review post acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "missing-review-file" ] \
  && [ ! -e "$EVAL_SANDBOX/.review/$PR/post.json" ] \
  && [ "$(reviews_posts_since "$m")" -eq 0 ]; then
  good "missing review.json refuses missing-review-file (no post.json, no POST)"
else
  bad "missing review gave exit $EVAL_STATUS code '$code'"
fi

# --- P5: bad --approve value refuses typed naming the value ---
PR=4315
mk_clean "$PR"
run_review post acme/app "$PR" --approve=yes --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
msg="$(jq -r '.error.message // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "bad-approve" ] \
  && printf '%s' "$msg" | grep -q "got 'yes'"; then
  good "bad --approve refuses bad-approve naming the value"
else
  bad "bad --approve gave exit $EVAL_STATUS code '$code'"
fi

# --- P6: malformed live threads downgrade to COMMENT (never a silent APPROVE) ---
# The probe err file carries jq's own stderr text (version-dependent), so
# this grades the behavior only: exit 0, COMMENT, reason in warnings and in
# the posted body, posted exactly once.
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

if [ "$fails" -ne 0 ]; then echo "case post-refusals: FAIL ($fails)"; exit 1; fi
echo "case post-refusals: PASS"
