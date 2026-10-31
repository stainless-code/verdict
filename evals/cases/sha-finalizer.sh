#!/usr/bin/env bash
# Case sha-finalizer: SHA-keyed single-review finality.
#
# Grades (hermetic — crafted bundles, smart gh stub, no network):
#   F1  same-SHA repost refused: a second completed post on the same reviewed
#       SHA (even with different content) exits 1 with the already-posted
#       typed error naming --force, and posts nothing (no new network POST,
#       post.json byte-identical).
#   F2  new-SHA re-arms: retargeting the bundle at a new HEAD posts cleanly
#       and refreshes the post.json head with no manual reset.
#   F3  override: --force reposts on the wedged SHA (exit 0, record refreshed);
#       the APPROVE path is finalizer-bound too (clear recheck, SHA refuses).
#   F4  dry-run never arms: dry runs write no post.json on a fresh bundle, and
#       stay advisory (never refused, record untouched) after a completed post.
#   F5  failed post never arms: a POST failure writes no post.json, and the
#       retry on the same SHA proceeds (nothing was armed to refuse it).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

HEADA="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
HEADB="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

# Overlay a smart gh stub: pulls/user answer locally, reviews POST succeeds,
# graphql always clear (zero live opens). Touching $EVAL_SANDBOX/post-fail
# flips the reviews POST to failure for the failed-post case. Restored on
# exit so sibling cases keep the dead stub.
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

# --- F4a: dry-run never arms (fresh bundle) ---
PR=4295
mk_comment "$PR"
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] && [ ! -f "$EVAL_SANDBOX/.review/$PR/post.json" ]; then
  good "dry run posts nothing and writes no post.json"
else
  bad "dry run exits $EVAL_STATUS / post.json present"
fi

# --- F1: first post arms, same-SHA repost refused ---
PR=4296
mk_comment "$PR"
m="$(mark)"
run_review post acme/app "$PR" --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.head // empty' "$EVAL_SANDBOX/.review/$PR/post.json" 2>/dev/null)" = "$HEADA" ]; then
  good "first post completes and records the reviewed SHA"
else
  bad "first post exits $EVAL_STATUS / head not recorded"
fi
if [ "$(reviews_posts_since "$m")" -eq 1 ]; then
  good "first post made exactly one network POST"
else
  bad "first post made $(reviews_posts_since "$m") POST(s)"
fi

# Second post, different content, same SHA — the coarse guard refuses.
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — reworded body, same SHA.","approved":false,"comments":[]}
EOF
cp "$EVAL_SANDBOX/.review/$PR/post.json" "$EVAL_SANDBOX/post-before.json"
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
if cmp -s "$EVAL_SANDBOX/post-before.json" "$EVAL_SANDBOX/.review/$PR/post.json" \
  && [ "$(reviews_posts_since "$m")" -eq 0 ]; then
  good "refused repost posted nothing (no POST, post.json identical)"
else
  bad "refused repost posted or rewrote the record"
fi

# --- F4b: dry run after a completed post stays advisory ---
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && cmp -s "$EVAL_SANDBOX/post-before.json" "$EVAL_SANDBOX/.review/$PR/post.json"; then
  good "dry run after a completed post stays advisory and keeps the record"
else
  bad "dry run after completed post exits $EVAL_STATUS"
fi

# --- F2: new SHA re-arms ---
jq --arg h "$HEADB" '.headRefOid = $h' "$EVAL_SANDBOX/.review/$PR/PR.json" \
  > "$EVAL_SANDBOX/.review/$PR/PR.json.new"
mv "$EVAL_SANDBOX/.review/$PR/PR.json.new" "$EVAL_SANDBOX/.review/$PR/PR.json"
run_review post acme/app "$PR" --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.head // empty' "$EVAL_SANDBOX/.review/$PR/post.json" 2>/dev/null)" = "$HEADB" ]; then
  good "new-SHA post proceeds and refreshes the record"
else
  bad "new-SHA post exits $EVAL_STATUS / head not refreshed"
fi

# --- F3: --force overrides the wedged SHA ---
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — forced repost on the same SHA.","approved":false,"comments":[]}
EOF
m="$(mark)"
run_review post acme/app "$PR" --force --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.head // empty' "$EVAL_SANDBOX/.review/$PR/post.json" 2>/dev/null)" = "$HEADB" ]; then
  good "--force overrides the same-SHA refusal"
else
  bad "forced repost exits $EVAL_STATUS, want 0"
fi
if [ "$(reviews_posts_since "$m")" -eq 1 ]; then
  good "forced repost made exactly one network POST"
else
  bad "forced repost made $(reviews_posts_since "$m") POST(s)"
fi

# The APPROVE path is finalizer-bound too: clear recheck, same SHA refuses.
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

# --- F5: failed post never arms ---
PR=4297
mk_comment "$PR"
touch "$EVAL_SANDBOX/post-fail"
run_review post acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "post-failed" ] \
  && [ ! -f "$EVAL_SANDBOX/.review/$PR/post.json" ]; then
  good "failed post writes no post.json"
else
  bad "failed post gave exit $EVAL_STATUS code '$code' / post.json present"
fi
rm -f "$EVAL_SANDBOX/post-fail"
run_review post acme/app "$PR" --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.head // empty' "$EVAL_SANDBOX/.review/$PR/post.json" 2>/dev/null)" = "$HEADA" ]; then
  good "retry after failure proceeds (nothing was armed to refuse it)"
else
  bad "retry after failure exits $EVAL_STATUS, want 0"
fi

if [ "$fails" -ne 0 ]; then echo "case sha-finalizer: FAIL ($fails)"; exit 1; fi
echo "case sha-finalizer: PASS"
