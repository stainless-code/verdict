#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

STUB_SAVED="$EVAL_SANDBOX/gh-stub-orig"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
restore_stub() { cp "$STUB_SAVED" "$EVAL_STUBBIN/gh"; }
trap restore_stub EXIT
cat > "$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
if [ "${1:-}" = "api" ] && [ "${2:-}" = "graphql" ]; then
  mode="$(cat "${EVAL_SANDBOX:-/tmp}/recheck-mode" 2>/dev/null || echo unreachable)"
  case "$mode" in
    clear) printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}'; exit 0;;
    open) printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":false},{"isResolved":false},{"isResolved":true}]}}}}}'; exit 0;;
    *) echo "evals-stub-gh: threads unreachable" >&2; exit 1;;
  esac
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

graphql_since() {
  local n
  n=$(tail -n +"$(( $1 + 1 ))" "$EVAL_STUBLOG" 2>/dev/null | grep -c "graphql" || true)
  printf '%s' "$n"
}
mark() { wc -l <"$EVAL_STUBLOG" | tr -d ' '; }
set_mode() { printf '%s' "$1" > "$EVAL_SANDBOX/recheck-mode"; }

mk_clean() { # $1 = pr — clean bundle carrying an approving review
  eval_mk_bundle "$1"
  eval_write_review "$1" <<'EOF'
{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}
EOF
}

PR=4271
mk_clean "$PR"
set_mode clear
m="$(mark)"
run_review post acme/app "$PR" --bind --json
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$event" = "APPROVE" ]; then
  good "clear recheck binds APPROVE (exit 0)"
else
  bad "clear recheck gave exit $EVAL_STATUS event '$event', want 0/APPROVE"
fi
if [ -f "$EVAL_SANDBOX/.review/$PR/post.json" ]; then
  good "real APPROVE post wrote post.json"
else
  bad "real APPROVE post wrote no post.json"
fi
if [ "$(graphql_since "$m")" -ge 1 ]; then
  good "APPROVE path paid the recheck call"
else
  bad "APPROVE path made no recheck call"
fi

PR=4272
mk_clean "$PR"
set_mode open
run_review post acme/app "$PR" --bind --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "approve-blocked" ]; then
  good "unknown live threads refuse with approve-blocked (exit 1)"
else
  bad "unknown-live gave exit $EVAL_STATUS code '$code', want 1/approve-blocked"
fi
run_review post acme/app "$PR" --bind
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "2 live open" "$EVAL_ERR" \
  && grep -q "0 local open" "$EVAL_ERR" \
  && grep -q -- "--force" "$EVAL_ERR"; then
  good "refusal names the counts plus the override"
else
  bad "refusal stderr: $(head -c 300 "$EVAL_ERR")"
fi
if [ ! -f "$EVAL_SANDBOX/.review/$PR/post.json" ]; then
  good "refused APPROVE posted nothing"
else
  bad "refused APPROVE still wrote post.json"
fi

run_review post acme/app "$PR" --bind --force --json
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$event" = "APPROVE" ]; then
  good "--force overrides the live refusal"
else
  bad "forced run gave exit $EVAL_STATUS event '$event', want 0/APPROVE"
fi

PR=4273
mk_clean "$PR"
set_mode unreachable
export EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/posted-payload.json"
rm -f "$EVAL_SAVE_PAYLOAD"
run_review post acme/app "$PR" --bind --json
PAYLOAD_FILE="$EVAL_SAVE_PAYLOAD"
unset EVAL_SAVE_PAYLOAD
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$event" = "COMMENT" ]; then
  good "unreachable recheck lands as COMMENT (exit 0)"
else
  bad "unreachable gave exit $EVAL_STATUS event '$event', want 0/COMMENT"
fi
if grep -q "downgrading to COMMENT" "$EVAL_ERR" && grep -q "recheck unreachable" "$EVAL_ERR"; then
  good "downgrade warns with the recheck reason"
else
  bad "downgrade stderr: $(head -c 300 "$EVAL_ERR")"
fi
if jq -e '.warnings | join(" ") | contains("recheck unreachable")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "envelope warnings carry the recheck reason"
else
  bad "envelope warnings miss the reason: $(jq -c '.warnings' <"$EVAL_OUT" 2>/dev/null)"
fi
if grep -q "recheck unreachable" "$PAYLOAD_FILE" 2>/dev/null; then
  good "posted body carries the downgrade note"
else
  bad "posted payload misses the note: $(head -c 200 "$PAYLOAD_FILE" 2>/dev/null)"
fi

PR=4274
mk_clean "$PR"
set_mode clear
m="$(mark)"
run_review post acme/app "$PR" --json
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$event" = "COMMENT" ]; then
  good "unbound APPROVE still lands as COMMENT"
else
  bad "COMMENT run gave exit $EVAL_STATUS event '$event', want 0/COMMENT"
fi
if [ "$(graphql_since "$m")" -eq 0 ]; then
  good "COMMENT post made zero recheck calls"
else
  bad "COMMENT post paid $(graphql_since "$m") recheck call(s)"
fi

PR=4275
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> [!CAUTION] Blocking issue below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"This line is wrong.","rigor":"walked","severity":"critical"}]}
EOF
set_mode clear
m="$(mark)"
run_review post acme/app "$PR" --request-changes --bind --json
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$event" = "REQUEST_CHANGES" ]; then
  good "REQUEST_CHANGES binds with findings open"
else
  bad "REQUEST_CHANGES gave exit $EVAL_STATUS event '$event', want 0/REQUEST_CHANGES"
fi
if [ "$(graphql_since "$m")" -eq 0 ]; then
  good "REQUEST_CHANGES post made zero recheck calls"
else
  bad "REQUEST_CHANGES post paid $(graphql_since "$m") recheck call(s)"
fi

PR=4276
mk_clean "$PR"
set_mode clear
m="$(mark)"
run_review post acme/app "$PR" --bind --dry-run --json
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$event" = "APPROVE" ]; then
  good "dry run previews the bound verdict"
else
  bad "dry run gave exit $EVAL_STATUS event '$event', want 0/APPROVE"
fi
if [ "$(graphql_since "$m")" -eq 0 ] && [ ! -f "$EVAL_SANDBOX/.review/$PR/post.json" ]; then
  good "dry run made zero recheck calls and posted nothing"
else
  bad "dry run paid $(graphql_since "$m") recheck call(s)"
fi

eval_end
