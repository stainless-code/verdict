#!/usr/bin/env bash
# Chaos agent works from stale state; verdict pins the fetch-time snapshot and refuses typed.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

mark() { wc -l <"$EVAL_STUBLOG" | tr -d ' '; }
reviews_since() {
  tail -n +"$(( $1 + 1 ))" "$EVAL_STUBLOG" 2>/dev/null | grep -c "reviews" || true
}
calls_since() {
  tail -n +"$(( $1 + 1 ))" "$EVAL_STUBLOG" 2>/dev/null | grep -c "" || true
}

snap() {
  local dir="$1" out="$2"
  if [ -d "$dir" ]; then
    (cd "$dir" && find . -type f -exec shasum {} + | LC_ALL=C sort) >"$out" 2>/dev/null || : >"$out"
  else
    printf 'ABSENT %s\n' "$dir" >"$out"
  fi
}
snap_same() {
  local label="$1" before="$2" after="$3"
  if cmp -s "$before" "$after"; then
    good "$label store byte-identical"
  else
    bad "$label store changed"
  fi
}
matrix_row() {
  printf 'matrix: verb=%s prior=%s exit=%s code=%s next=%s reenter=%s store=%s stub_reviews=%s\n' \
    "$1" "$2" "$3" "$4" "$5" "$6" "$7" "$8"
}
way_forward() {
  local label="$1" re nxt
  nxt="$(jq -r '.next // empty' "$EVAL_OUT" 2>/dev/null || true)"
  re="$(sed -n 's/^re-enter: //p' "$EVAL_ERR" | head -1 || true)"
  if [ -n "$nxt" ]; then
    good "$label envelope carries a next step"
  elif [ -n "$re" ]; then
    case "$re" in
      verdict\ *) good "$label re-enter names a runnable verdict command" ;;
      *) bad "$label re-enter is not a verdict command: $re" ;;
    esac
  else
    bad "$label refusal names no way forward (empty next, no re-enter)"
  fi
}

git -C "$EVAL_SANDBOX" init -q 2>/dev/null || true
git -C "$EVAL_SANDBOX" config user.email chaos@example.com
git -C "$EVAL_SANDBOX" config user.name chaos
printf 'top\nold line\nbottom\n' > "$EVAL_SANDBOX/notes.txt"
printf '{"id":"check-error-paths","rule":"Check error paths on every await","scope":"*.txt","provenance":"manual","rigor":"walked","hits":0,"false_hits":0,"judged":0,"status":"active"}\n' \
  > "$EVAL_SANDBOX/.review-kb.jsonl"
mkdir -p "$EVAL_SANDBOX/.review"
cp "$EVAL_SANDBOX/.review-kb.jsonl" "$EVAL_SANDBOX/.review/kb.jsonl"
(cd "$EVAL_SANDBOX" && git add -A && git -c commit.gpgsign=false commit -qm chaos-base)
printf 'top\nnew line\nbottom\nextra line\n' > "$EVAL_SANDBOX/notes.txt"

LB="$EVAL_SANDBOX/.review/local-chaos-stale"

m0="$(mark)"
run_review fetch-local acme/app chaos-stale --json
assert_exit "fetch-local baseline exits 0" 0
assert_envelope "fetch-local baseline"
if [ "$(calls_since "$m0")" -eq 0 ]; then
  good "fetch-local made zero stub calls"
else
  bad "fetch-local reached the stub"
fi
matrix_row "fetch-local" "clean-tree" "$EVAL_STATUS" "none" "yes" "no" "created" "$(reviews_since "$m0")"

cp "$LB/full.diff" "$EVAL_SANDBOX/full-fetched.diff"

cat > "$LB/full.diff" <<'EOF'
diff --git a/totally-different.txt b/totally-different.txt
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/totally-different.txt
@@ -0,0 +1,3 @@
+brand
+new
+file
EOF
cat > "$LB/review.json" <<'EOF'
{"schema_version":"1","body":"> ✅ Re-review after the push with enough length to pass the guard.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Still about notes.txt with enough substance.","rigor":"pointed","severity":"informational"}]}
EOF
snap "$LB" "$EVAL_SANDBOX/s1.base"
m0="$(mark)"
run_review record acme/app chaos-stale --json
assert_refusal "record after the diff moved" "bad-review-input"
assert_json_field "moved anchor refuses as an unknown path" "path-not-in-diff" '.error.problems[0].code'
assert_json_field "refusal carries the exact fix" "anchor the comment on a changed file or move it into the body" '.error.problems[0].fix'
way_forward "record after the diff moved"
if cmp -s "$LB/full.diff" "$EVAL_SANDBOX/full-fetched.diff"; then
  bad "record repaired the rewritten full.diff (must keep the drift visible)"
else
  good "record left the rewritten full.diff alone (drift stays visible)"
fi
snap "$LB" "$EVAL_SANDBOX/s1.snap"
if cmp -s "$EVAL_SANDBOX/s1.base" "$EVAL_SANDBOX/s1.snap"; then
  good "refused record merged nothing (no findings.json/record.json)"
else
  bad "refused record wrote into the store"
fi
matrix_row "record" "stale-diff" "$EVAL_STATUS" "bad-review-input" "no" "yes" "same" "$(reviews_since "$m0")"

cp "$EVAL_SANDBOX/full-fetched.diff" "$LB/full.diff"
m0="$(mark)"
run_review record acme/app chaos-stale --json
assert_exit "record after restoring the fetched diff exits 0" 0
if [ -f "$LB/findings.json" ] && [ -f "$LB/record.json" ]; then
  good "restored diff merges findings.json + record.json"
else
  bad "restored diff merged nothing"
fi
matrix_row "record" "restored-diff" "$EVAL_STATUS" "none" "yes" "no" "merged" "$(reviews_since "$m0")"

echo "evil edit under the snapshot" >> "$LB/controls/kb.jsonl"
cat > "$LB/review.json" <<'EOF'
{"schema_version":"1","body":"> ✅ Another round with enough length to pass the guard here.","approved":false,"comments":[]}
EOF
snap "$LB" "$EVAL_SANDBOX/s2.before"
m0="$(mark)"
run_review record acme/app chaos-stale --json
assert_exit "record on a tampered snapshot exits 1" 1
assert_refusal "record on a tampered snapshot" "controls-stale"
way_forward "record on a tampered snapshot"
snap "$LB" "$EVAL_SANDBOX/s2.after"
snap_same "record on a tampered snapshot" "$EVAL_SANDBOX/s2.before" "$EVAL_SANDBOX/s2.after"
if [ "$(calls_since "$m0")" -eq 0 ]; then
  good "controls-stale refusal made zero stub calls"
else
  bad "controls-stale refusal reached the stub"
fi
matrix_row "record" "tampered-controls" "$EVAL_STATUS" "controls-stale" "no" "yes" "same" "$(reviews_since "$m0")"

git -C "$EVAL_SANDBOX" show HEAD:.review/kb.jsonl > "$LB/controls/kb.jsonl"
norm_store() {
  jq -S '[.[] | {sig, status}] | sort_by(.sig)' "$LB/findings.json" 2>/dev/null || true
}
norm_store > "$EVAL_SANDBOX/n.r2"
m0="$(mark)"
run_review record acme/app chaos-stale --json
assert_exit "record after restoring the snapshot exits 0" 0
way_re="$(sed -n 's/^re-enter: //p' "$EVAL_ERR" | head -1 || true)"
if [ -z "$way_re" ]; then
  good "success run prints no re-enter line"
else
  bad "success run prints a re-enter line"
fi
norm_store > "$EVAL_SANDBOX/n.r2.after"
if cmp -s "$EVAL_SANDBOX/n.r2" "$EVAL_SANDBOX/n.r2.after" \
  && [ -f "$LB/record.json" ] && [ -f "$LB/findings.json" ] \
  && jq -e '.schema_version == "1"' "$LB/record.json" >/dev/null 2>&1; then
  good "restored record advanced to exit 0 with the store intact (recorded_at may tick; sig set + statuses stable)"
else
  bad "restored record left the bundle in a bad state"
fi
matrix_row "record" "restored-controls" "$EVAL_STATUS" "none" "yes" "no" "same" "$(reviews_since "$m0")"

cat > "$LB/review.json" <<'EOF'
{"schema_version":"1","body":"> ✅ Off-hunk line below with enough length to pass the guard.","approved":false,"comments":[{"path":"notes.txt","line":99,"side":"RIGHT","body":"Off-hunk line with enough substance here.","rigor":"walked","severity":"important"}]}
EOF
m0="$(mark)"
run_review record acme/app chaos-stale --json
assert_exit "record of an off-hunk anchor exits 0" 0
if grep -q "moved to body" "$EVAL_ERR"; then
  good "off-hunk anchor moves to the body with a named reason (kept, never refused)"
else
  bad "off-hunk anchor was not partitioned: $(head -c 300 "$EVAL_ERR")"
fi
if [ "$(reviews_since "$m0")" -eq 0 ]; then
  good "off-hunk record posted nothing"
else
  bad "off-hunk record reached the stub"
fi
matrix_row "record" "off-hunk" "$EVAL_STATUS" "none" "yes" "no" "merged" "$(reviews_since "$m0")"

cat > "$LB/review.json" <<'EOF'
{"schema_version":"1","body":"> ✅ Hallucinated finding below with enough length to pass the guard.","approved":false,"comments":[{"path":"nope/ghost.ts","line":999,"side":"RIGHT","body":"Hallucinated finding with enough substance here.","rigor":"walked","severity":"critical"}]}
EOF
snap "$LB" "$EVAL_SANDBOX/s3h.before"
m0="$(mark)"
run_review record acme/app chaos-stale --json
assert_exit "record of a hallucinated path exits 1" 1
assert_refusal "record of a hallucinated path" "bad-review-input"
assert_json_field "hallucinated path refuses as unknown" "path-not-in-diff" '.error.problems[0].code'
if [ "$(reviews_since "$m0")" -eq 0 ]; then
  good "hallucinated record posted nothing"
else
  bad "hallucinated record reached the stub"
fi
snap "$LB" "$EVAL_SANDBOX/s3h.after"
snap_same "hallucinated path" "$EVAL_SANDBOX/s3h.before" "$EVAL_SANDBOX/s3h.after"
way_forward "hallucinated path"
matrix_row "record" "hallucinated" "$EVAL_STATUS" "bad-review-input" "no" "yes" "same" "$(reviews_since "$m0")"

cat > "$LB/review.json" <<'EOF'
{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[5]}
EOF
snap "$LB" "$EVAL_SANDBOX/s3.before"
for i in 1 2 3; do
  m0="$(mark)"
  run_review record acme/app chaos-stale --json
  assert_exit "identical invalid review run $i exits 1" 1
  assert_refusal "identical invalid review run $i" "bad-review-input"
  jq -S 'del(.at)' "$EVAL_OUT" > "$EVAL_SANDBOX/env.$i.json"
  if [ "$(reviews_since "$m0")" -eq 0 ]; then
    good "identical invalid review run $i posted nothing"
  else
    bad "identical invalid review run $i reached the stub"
  fi
done
if cmp -s "$EVAL_SANDBOX/env.1.json" "$EVAL_SANDBOX/env.2.json" \
  && cmp -s "$EVAL_SANDBOX/env.2.json" "$EVAL_SANDBOX/env.3.json"; then
  good "identical invalid input gives the identical refusal 3 times (modulo at)"
else
  bad "identical invalid input refused non-deterministically"
fi
snap "$LB" "$EVAL_SANDBOX/s3.after"
snap_same "identical invalid review x3" "$EVAL_SANDBOX/s3.before" "$EVAL_SANDBOX/s3.after"
way_forward "identical invalid review"
matrix_row "record" "repeat-invalid" "$EVAL_STATUS" "bad-review-input" "no" "yes" "same" "0"

snap "$LB" "$EVAL_SANDBOX/s3d.before"
for i in 1 2 3; do
  m0="$(mark)"
  run_review record acme/app chaos-stale --dry-run --json
  assert_exit "identical invalid dry run $i exits 1" 1
  assert_refusal "identical invalid dry run $i" "bad-review-input"
  jq -S '.error.problems' "$EVAL_OUT" > "$EVAL_SANDBOX/dry.$i.json"
  if [ "$(reviews_since "$m0")" -eq 0 ]; then
    good "identical invalid dry run $i posted nothing"
  else
    bad "identical invalid dry run $i reached the stub"
  fi
done
if cmp -s "$EVAL_SANDBOX/dry.1.json" "$EVAL_SANDBOX/dry.2.json" \
  && cmp -s "$EVAL_SANDBOX/dry.2.json" "$EVAL_SANDBOX/dry.3.json"; then
  good "identical invalid dry run gives byte-identical problems 3 times"
else
  bad "identical invalid dry run refused non-deterministically"
fi
snap "$LB" "$EVAL_SANDBOX/s3d.after"
snap_same "identical invalid dry run x3" "$EVAL_SANDBOX/s3d.before" "$EVAL_SANDBOX/s3d.after"
matrix_row "record" "repeat-invalid-dry-run" "$EVAL_STATUS" "bad-review-input" "no" "yes" "same" "0"

cat > "$LB/review.json" <<'EOF'
{"schema_version":"1","body":"> ✅ Warning-only review with enough length to pass the guard.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"A finding with enough substance here."}]}
EOF
snap "$LB" "$EVAL_SANDBOX/s3s.before"
m0="$(mark)"
run_review record acme/app chaos-stale --dry-run --json
assert_exit "warning-only dry run exits 0" 0
assert_envelope "warning-only dry run"
assert_json_field "warning-only dry run flags itself" "true" '.data.dry_run'
assert_json_field "warning-only dry run counts the comment" "1" '.data.check.comments'
if jq -e '.warnings | join(" ") | contains("rigor is unset")' "$EVAL_OUT" >/dev/null; then
  good "rigor-unset rides the envelope as a warning"
else
  bad "rigor-unset warning missing from the envelope"
fi
m0s="$(mark)"
run_review record acme/app chaos-stale --dry-run --strict --json
assert_exit "strict dry run exits 1" 1
assert_refusal "strict dry run" "bad-review-input"
assert_json_field "strict promotes the warning to an error" "error" '.error.problems[0].level'
assert_json_field "strict names the rigor code" "rigor-unset" '.error.problems[0].code'
if [ "$(reviews_since "$m0s")" -eq 0 ]; then
  good "strict refusal posted nothing"
else
  bad "strict refusal reached the stub"
fi
snap "$LB" "$EVAL_SANDBOX/s3s.after"
snap_same "strict dry run" "$EVAL_SANDBOX/s3s.before" "$EVAL_SANDBOX/s3s.after"
matrix_row "record" "strict-dry-run" "$EVAL_STATUS" "bad-review-input" "no" "yes" "same" "0"

cat > "$LB/review.json" <<'EOF'
{"schema_version":"1","body":"> ✅ Clean review with enough length to pass the guard here.","approved":false,"comments":[]}
EOF
MCP_OUT="$EVAL_SANDBOX/mcp-out.jsonl"
FRAMES="$EVAL_SANDBOX/mcp-frames.jsonl"
cat > "$FRAMES" <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"record","arguments":{"repo":"acme/app","pr":"local-chaos-stale","dry_run":true}}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"record","arguments":{"repo":"acme/app","pr":"local-chaos-stale"}}}
EOF
snap "$LB" "$EVAL_SANDBOX/s4.before"
set +e
(
  cd "$EVAL_SANDBOX" || exit 99
  PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" mcp --read-only >"$MCP_OUT" 2>"$EVAL_SANDBOX/mcp-err.txt"
) <"$FRAMES"
mcp_status=$?
set -e
if [ "$mcp_status" -eq 0 ]; then
  good "read-only MCP session exits 0"
else
  bad "read-only MCP session exits $mcp_status"
fi
if jq -e 'select(.id == 2).result | (.isError == false) and (.structuredContent.dry_run == true) and (.structuredContent.check != null)' \
  "$MCP_OUT" >/dev/null 2>&1; then
  good "read-only MCP serves the dry-run record with its check"
else
  bad "read-only MCP did not serve the dry-run record"
fi
if jq -e 'select(.id == 3).result | (.isError == true) and (.structuredContent.code == "bad-flags") and (.structuredContent.message | contains("dry_run"))' \
  "$MCP_OUT" >/dev/null 2>&1; then
  good "read-only MCP refuses a non-dry record as bad-flags naming dry_run"
else
  bad "read-only MCP did not refuse the non-dry record"
fi
if [ "$(jq -s 'length' <"$MCP_OUT" 2>/dev/null || echo 0)" -ge 2 ] \
  && jq -s -e 'all(.[]; .jsonrpc == "2.0")' "$MCP_OUT" >/dev/null 2>&1; then
  good "MCP stdout stays protocol frames only"
else
  bad "MCP stdout broke the frame contract"
fi
snap "$LB" "$EVAL_SANDBOX/s4.after"
snap_same "read-only MCP record attempt" "$EVAL_SANDBOX/s4.before" "$EVAL_SANDBOX/s4.after"
matrix_row "mcp-record" "read-only" "$mcp_status" "-32602" "no" "no" "same" "0"

eval_end
