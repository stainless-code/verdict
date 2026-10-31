#!/usr/bin/env bash
# Case hunk-snapshot: anchors validate against the fetch-time snapshot.
#
# Grades (hermetic — crafted bundles, smart gh stub, no network):
#   H1  pinned snapshot wins: a mid-run push rewrites full.diff, but post
#       still validates against hunks.tsv — the kept anchor posts inline,
#       the snapshot-dropped anchor lands in the posted body as a named
#       note with its reason (exit 0, drops never fail).
#   H2  no-drop parity: a run with zero drops posts a body byte-identical
#       to the rebuilt-index run (pinning changes nothing on the kept path).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

# Smart gh stub: review POSTs succeed (payload saved when asked), the live
# SHA read returns a moved HEAD (bbbb…) so drift context triggers while
# validation must stay pinned to the snapshot.
STUB_SAVED="$EVAL_SANDBOX/gh-stub-orig"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
restore_stub() { cp "$STUB_SAVED" "$EVAL_STUBBIN/gh"; }
trap restore_stub EXIT
cat > "$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
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

# --- H1: mid-run push does not move the validation target ---
PR=4281
eval_mk_bundle "$PR"
# What fetch pinned before the push: notes.txt commentable on NEW 1-3 only.
printf 'notes.txt\tOLD\t1\t3\nnotes.txt\tNEW\t1\t3\n' > "$EVAL_SANDBOX/.review/$PR/hunks.tsv"
# The push rewrites full.diff (line 50 commentable live) — the snapshot
# must still decide.
{
  echo 'diff --git a/notes.txt b/notes.txt'
  echo 'index 1111111..2222222 100644'
  echo '--- a/notes.txt'
  echo '+++ b/notes.txt'
  echo '@@ -1,3 +1,51 @@'
  echo ' top'
  echo '-old line'
  echo '+new line'
  i=1; while [ "$i" -le 48 ]; do echo "+pad $i"; i=$((i + 1)); done
  echo ' bottom'
} > "$EVAL_SANDBOX/.review/$PR/full.diff"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Ship it.","approved":false,"comments":[
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Kept note on a snapshot-covered line.","rigor":"pointed","severity":"informational"},
{"path":"notes.txt","line":50,"side":"RIGHT","body":"Dropped note beyond the snapshot.","rigor":"pointed","severity":"informational"}]}
EOF
export EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/posted-4281.json"
rm -f "$EVAL_SAVE_PAYLOAD"
run_review post acme/app "$PR" --json
PAYLOAD_4281="$EVAL_SAVE_PAYLOAD"
unset EVAL_SAVE_PAYLOAD
event="$(jq -r '.data.event // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$event" = "COMMENT" ]; then
  good "snapshot run posts COMMENT (exit 0, drops never fail)"
else
  bad "snapshot run gave exit $EVAL_STATUS event '$event', want 0/COMMENT"
fi
if [ "$(jq -r '.data.anchors.kept // empty' <"$EVAL_OUT" 2>/dev/null)" = "1" ] \
  && [ "$(jq -r '.data.anchors.moved // empty' <"$EVAL_OUT" 2>/dev/null)" = "1" ]; then
  good "anchors partition 1 kept / 1 moved against the snapshot"
else
  bad "anchors partition: $(jq -c '.data.anchors' <"$EVAL_OUT" 2>/dev/null || echo unreadable)"
fi
if [ "$(jq '.comments | length' <"$PAYLOAD_4281" 2>/dev/null)" = "1" ] \
  && [ "$(jq -r '.comments[0].line' <"$PAYLOAD_4281" 2>/dev/null)" = "2" ]; then
  good "kept anchor posts inline, rebuilt-diff line stays out"
else
  bad "payload comments: $(jq -c '.comments' <"$PAYLOAD_4281" 2>/dev/null || echo unreadable)"
fi
if grep -q "Unanchored observations" "$PAYLOAD_4281" \
  && grep -Fq 'notes.txt:50-50' "$PAYLOAD_4281" \
  && grep -q "outside reviewed hunks" "$PAYLOAD_4281"; then
  good "dropped anchor lands in the body as a named note with its reason"
else
  bad "posted body misses the named drop note: $(head -c 400 "$PAYLOAD_4281" 2>/dev/null)"
fi
if grep -q "commits landed after this review's fetch" "$PAYLOAD_4281"; then
  good "moved HEAD surfaces as drift context in the body"
else
  bad "posted body misses the drift note"
fi
if [ "$(jq '[.[] | select(.anchored == true)] | length' <"$EVAL_SANDBOX/.review/$PR/findings.json" 2>/dev/null)" = "1" ] \
  && [ "$(jq '[.[] | select(.anchored == false)] | length' <"$EVAL_SANDBOX/.review/$PR/findings.json" 2>/dev/null)" = "1" ]; then
  good "findings store records the kept/dropped split"
else
  bad "findings anchored flags wrong"
fi

# --- H2: no-drop run is byte-identical with or without the snapshot ---
mk_kept() { # $1 = pr
  eval_mk_bundle "$1"
  eval_write_review "$1" <<'EOF'
{"schema_version":"1","body":"> ✅ Ship it.","approved":false,"comments":[
{"path":"notes.txt","line":2,"side":"RIGHT","body":"Kept note on a covered line.","rigor":"pointed","severity":"informational"}]}
EOF
}
PR=4282
mk_kept "$PR"
printf 'notes.txt\tOLD\t1\t3\nnotes.txt\tNEW\t1\t3\n' > "$EVAL_SANDBOX/.review/$PR/hunks.tsv"
export EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/posted-4282.json"
rm -f "$EVAL_SAVE_PAYLOAD"
run_review post acme/app "$PR" --json
unset EVAL_SAVE_PAYLOAD
cp "$EVAL_ERR" "$EVAL_SANDBOX/err-4282.txt"
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "pinned no-drop run posts (exit 0)"
else
  bad "pinned no-drop run exits $EVAL_STATUS"
fi
PR=4283
mk_kept "$PR"
export EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/posted-4283.json"
rm -f "$EVAL_SAVE_PAYLOAD"
run_review post acme/app "$PR" --json
unset EVAL_SAVE_PAYLOAD
cp "$EVAL_ERR" "$EVAL_SANDBOX/err-4283.txt"
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "rebuilt no-drop run posts (exit 0)"
else
  bad "rebuilt no-drop run exits $EVAL_STATUS"
fi
if cmp -s "$EVAL_SANDBOX/posted-4282.json" "$EVAL_SANDBOX/posted-4283.json"; then
  good "no-drop payload is byte-identical with a pinned or rebuilt index"
else
  bad "payloads differ between pinned and rebuilt runs"
fi
if grep -q "best-effort" "$EVAL_SANDBOX/err-4283.txt"; then
  good "missing snapshot warns that validation is best-effort"
else
  bad "rebuilt run stderr misses the best-effort warning"
fi
if grep -q "best-effort" "$EVAL_SANDBOX/err-4282.txt"; then
  bad "pinned run should not warn about a missing snapshot"
else
  good "pinned run warns about nothing snapshot-related"
fi

if [ "$fails" -ne 0 ]; then echo "case hunk-snapshot: FAIL ($fails)"; exit 1; fi
echo "case hunk-snapshot: PASS"
