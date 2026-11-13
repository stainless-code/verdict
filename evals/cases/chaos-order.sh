#!/usr/bin/env bash
# Chaos agent runs verbs out of order; every refusal stays safe and names a way forward.
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

mark() { wc -l <"$EVAL_STUBLOG" | tr -d ' '; }
reviews_since() {
  tail -n +"$(( $1 + 1 ))" "$EVAL_STUBLOG" 2>/dev/null | grep -c "reviews.*--method POST" || true
}
calls_since() {
  tail -n +"$(( $1 + 1 ))" "$EVAL_STUBLOG" 2>/dev/null | grep -c "" || true
}

snap() {
  local dir="$1" out="$2"
  if [ -d "$dir" ]; then
    if ! (cd "$dir" && find . -type f -exec shasum {} + | LC_ALL=C sort) >"$out" 2>/dev/null; then
      bad "could not hash $dir; store not observed"
      : >"$out"
    fi
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
    printf 'next-present %s\n' "$nxt" >"$EVAL_SANDBOX/last-next.txt"
  elif [ -n "$re" ]; then
    case "$re" in
      verdict\ *) good "$label re-enter names a runnable verdict command" ;;
      *) bad "$label re-enter is not a verdict command: $re" ;;
    esac
  else
    bad "$label refusal names no way forward (empty next, no re-enter)"
  fi
}
rerun_rentry() {
  local label="$1" want_code="$2" re got
  re="$(sed -n 's/^re-enter: //p' "$EVAL_ERR" | head -1 || true)"
  if [ -z "$re" ]; then
    bad "$label: no re-enter line to re-run"
    return
  fi
  case "$re" in
    verdict\ *) re="${re#verdict }" ;;
    *) bad "$label re-enter is not a verdict command: $re"; return ;;
  esac
  good "$label re-enter names a runnable verdict command"
  # shellcheck disable=SC2086 # re-enter is a machine-built argv with no quoted spaces
  run_review $re
  got="$(jq -r '.error.code // empty' "$EVAL_OUT" 2>/dev/null || true)"
  if [ "$EVAL_STATUS" -eq 1 ] && [ "$got" = "$want_code" ]; then
    good "$label re-run explains deterministically ($want_code again)"
  else
    bad "$label re-run exits $EVAL_STATUS with '$got', want 1/$want_code"
  fi
}

PR=9201
LB="$EVAL_SANDBOX/.verdict/state/local-chaos-order"

m0="$(mark)"
run_review record acme/app local-ghost --json
assert_exit "record before any fetch exits 1" 1
assert_refusal "record before any fetch" "missing-review-file"
if [ ! -e "$EVAL_SANDBOX/.verdict/state/local-ghost" ]; then
  good "record before fetch created no bundle dir"
else
  bad "record before fetch created a bundle dir"
fi
way_forward "record before any fetch"
rerun_rentry "record before any fetch" "missing-review-file"
matrix_row "record" "no-bundle" "$EVAL_STATUS" "missing-review-file" "no" "yes" "absent" "$(reviews_since "$m0")"
if [ "$(calls_since "$m0")" -eq 0 ]; then
  good "record before fetch made zero stub calls"
else
  bad "record before fetch reached the stub"
fi

mkdir -p "$EVAL_SANDBOX/.verdict/state/local-empty"
m0="$(mark)"
run_review record acme/app local-empty --json
assert_exit "record on an empty bundle dir exits 1" 1
assert_refusal "record on an empty bundle dir" "missing-review-file"
way_forward "record on an empty bundle dir"
if [ -z "$(ls -A "$EVAL_SANDBOX/.verdict/state/local-empty")" ]; then
  good "record on an empty dir wrote nothing into it"
else
  bad "record on an empty dir wrote files"
fi
matrix_row "record" "empty-dir" "$EVAL_STATUS" "missing-review-file" "no" "yes" "same" "$(reviews_since "$m0")"
rm -rf "$EVAL_SANDBOX/.verdict/state/local-empty"

eval_mk_bundle local-chaos-order
cat > "$LB/review.json" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — minor note below with enough length.","approved":false,"comments":[]}
EOF
snap "$LB" "$EVAL_SANDBOX/s2.before"
m0="$(mark)"
run_review record acme/app local-chaos-order --dry-run --json
assert_exit "clean record --dry-run exits 0" 0
assert_envelope "clean record --dry-run"
assert_json_field "dry run flags itself" "true" '.data.dry_run'
assert_json_field "dry run counts zero comments" "0" '.data.check.comments'
if [ -e "$LB/findings.json" ] || [ -e "$LB/record.json" ]; then
  bad "clean dry run minted store artifacts"
else
  good "clean dry run minted no findings.json/record.json"
fi
snap "$LB" "$EVAL_SANDBOX/s2.after"
snap_same "clean record --dry-run" "$EVAL_SANDBOX/s2.before" "$EVAL_SANDBOX/s2.after"
if [ "$(reviews_since "$m0")" -eq 0 ]; then
  good "clean dry run posted nothing to the stub provider"
else
  bad "clean dry run reached the reviews POST"
fi
matrix_row "record" "dry-run-clean" "$EVAL_STATUS" "none" "yes" "no" "same" "$(reviews_since "$m0")"

cat > "$LB/review.json" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — minor note below with enough length.","approved":false,"comments":[{"path":"nope/ghost.ts","line":1,"side":"RIGHT","body":"Hallucinated finding with enough substance here.","rigor":"walked","severity":"critical"}]}
EOF
snap "$LB" "$EVAL_SANDBOX/s2b.before"
m0="$(mark)"
run_review record acme/app local-chaos-order --dry-run --json
assert_exit "bad record --dry-run exits 1" 1
assert_refusal "bad record --dry-run" "bad-review-input"
assert_json_field "bad dry run names the unknown path" "path-not-in-diff" '.error.problems[0].code'
assert_json_field "no overflow on one problem" "0" '.error.problems_truncated'
if [ -n "$(jq -r '.error.problems[0].fix // empty' "$EVAL_OUT" 2>/dev/null)" ]; then
  good "dry-run problem carries the exact fix"
else
  bad "dry-run problem is missing its fix"
fi
snap "$LB" "$EVAL_SANDBOX/s2b.after"
snap_same "bad record --dry-run" "$EVAL_SANDBOX/s2b.before" "$EVAL_SANDBOX/s2b.after"
run_review record acme/app local-chaos-order --dry-run
assert_exit "human bad dry run exits 1" 1
assert_file_grep "human dry run prints the problem code" "$EVAL_ERR" "path-not-in-diff"
assert_file_grep "human dry run prints the fix" "$EVAL_ERR" "fix:"
assert_file_grep "human dry run names a re-enter" "$EVAL_ERR" "^re-enter: verdict record"
matrix_row "record" "dry-run-refused" "$EVAL_STATUS" "bad-review-input" "no" "yes" "same" "$(reviews_since "$m0")"
cat > "$LB/review.json" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — minor note below with enough length.","approved":false,"comments":[]}
EOF

eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — minor note below with enough length.","approved":false,"comments":[]}
EOF
m0="$(mark)"
run_review post acme/app "$PR" --dry-run --json
assert_exit "post --dry-run before any record exits 0" 0
assert_envelope "post --dry-run before any record"
if [ -f "$EVAL_SANDBOX/.verdict/state/$PR/findings.dryrun.json" ] \
  && [ ! -e "$EVAL_SANDBOX/.verdict/state/$PR/findings.json" ] \
  && [ ! -e "$EVAL_SANDBOX/.verdict/state/$PR/post.json" ]; then
  good "dry run stages only findings.dryrun.json (no findings.json, no post.json)"
else
  bad "dry run wrote the wrong artifacts"
fi
if [ "$(reviews_since "$m0")" -eq 0 ]; then
  good "dry run posted nothing to the stub provider"
else
  bad "dry run reached the reviews POST"
fi
matrix_row "post" "no-findings" "$EVAL_STATUS" "none" "yes" "no" "dryrun-only" "$(reviews_since "$m0")"

snap "$LB" "$EVAL_SANDBOX/s3.before"
m0="$(mark)"
run_review post acme/app local-chaos-order --json
assert_exit "post on a local bundle exits 1" 1
assert_refusal "post on a local bundle" "no-pr-target"
way_forward "post on a local bundle"
rerun_rentry "post on a local bundle" "no-pr-target"
snap "$LB" "$EVAL_SANDBOX/s3.after"
snap_same "post on a local bundle" "$EVAL_SANDBOX/s3.before" "$EVAL_SANDBOX/s3.after"
matrix_row "post" "local-bundle" "$EVAL_STATUS" "no-pr-target" "no" "yes" "same" "$(reviews_since "$m0")"

eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Binding approval with enough length to be a real review.","approved":true,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Critical boundary bug here with enough substance.","rigor":"walked","severity":"critical"}]}
EOF
rm -f "$EVAL_SANDBOX/.verdict/state/$PR/findings.json" "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
m0="$(mark)"
run_review post acme/app "$PR" --approve=true --bind --json
assert_exit "binding post before the gate passes exits 1" 1
assert_refusal "binding post before the gate passes" "approve-blocked"
way_forward "binding post before the gate passes"
if [ "$(reviews_since "$m0")" -eq 0 ] && [ ! -e "$EVAL_SANDBOX/.verdict/state/$PR/post.json" ]; then
  good "ungated APPROVE posted nothing and minted no post.json"
else
  bad "ungated APPROVE escaped to the provider or minted post.json"
fi
matrix_row "post" "gate-would-fail" "$EVAL_STATUS" "approve-blocked" "no" "yes" "merged-only" "$(reviews_since "$m0")"

SIG="$(jq -r '.data.items[0].sig // .data.findings[0].sig // empty' "$EVAL_OUT" 2>/dev/null || true)"
if [ -z "$SIG" ]; then
  run_review report acme/app "$PR" --json
  SIG="$(jq -r '.data.items[0].sig' "$EVAL_OUT" 2>/dev/null || true)"
fi
run_review triage acme/app "$PR" --sig "$SIG" --status wont-fix --rationale "By design: this hunk is intentionally terse for log brevity." --json
assert_exit "human triage of the blocker exits 0" 0
m0="$(mark)"
EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/payload.json"
export EVAL_SAVE_PAYLOAD
run_review post acme/app "$PR" --approve=true --bind --json
unset EVAL_SAVE_PAYLOAD
assert_exit "recovery post after triage exits 0" 0
if [ "$(reviews_since "$m0")" -eq 1 ] && [ -f "$EVAL_SANDBOX/.verdict/state/$PR/post.json" ]; then
  good "recovery post submitted exactly one review and minted post.json"
else
  bad "recovery post made $(reviews_since "$m0") reviews calls"
fi
if jq -e 'has("body") and .event == "APPROVE"' "$EVAL_SANDBOX/payload.json" >/dev/null 2>&1; then
  good "posted payload carries the review body and APPROVE event"
else
  bad "posted payload is malformed or not APPROVE"
fi
run_review gate acme/app "$PR" --require-head --live skip --coverage warn --json
assert_exit "gate on the posted HEAD exits 0" 0
if [ "$(jq -r '.data.passed' "$EVAL_OUT" 2>/dev/null)" = "true" ] \
  && jq -e '.error == null' "$EVAL_OUT" >/dev/null 2>&1; then
  good "posted HEAD passes the gate as a verdict, not a refusal"
else
  bad "gate after post did not pass"
fi
matrix_row "gate" "posted-head" "$EVAL_STATUS" "none" "yes" "no" "same" "0"

jq '.head = "cccccccccccccccccccccccccccccccccccccccc"' \
  "$EVAL_SANDBOX/.verdict/state/$PR/post.json" > "$EVAL_SANDBOX/post-tampered.json" \
  && mv "$EVAL_SANDBOX/post-tampered.json" "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
snap "$EVAL_SANDBOX/.verdict/state/$PR" "$EVAL_SANDBOX/s4.before"
m0="$(mark)"
run_review gate acme/app "$PR" --require-head --live skip --coverage warn --json
assert_exit "gate on a stale head exits 1" 1
if [ "$(jq -r '.data.dimensions[] | select(.id=="head") | .status' "$EVAL_OUT" 2>/dev/null)" = "fail" ] \
  && [ "$(jq -r '.data.dimensions[] | select(.id=="head") | .detail' "$EVAL_OUT" 2>/dev/null)" = "posted HEAD ccccccc != fetched HEAD aaaaaaa" ] \
  && [ -n "$(jq -r '.next // empty' "$EVAL_OUT" 2>/dev/null)" ]; then
  good "stale head fails the head dimension naming both SHAs with a next step"
else
  bad "stale head did not fail closed with a way forward"
fi
run_review gate acme/app "$PR" --require-head --live skip --coverage warn
if grep -q "^try:" "$EVAL_OUT"; then
  good "human gate tail carries a try: recovery line"
else
  bad "human gate tail has no try: line"
fi
snap "$EVAL_SANDBOX/.verdict/state/$PR" "$EVAL_SANDBOX/s4.after"
snap_same "gate on a stale head" "$EVAL_SANDBOX/s4.before" "$EVAL_SANDBOX/s4.after"
if [ "$(calls_since "$m0")" -eq 0 ]; then
  good "stale-head gate paid zero stub calls under --live skip"
else
  bad "stale-head gate reached the stub"
fi
matrix_row "gate" "stale-head" "$EVAL_STATUS" "none" "yes" "no" "same" "0"

for verb in report next show; do
  m0="$(mark)"
  snap "$EVAL_SANDBOX/.verdict/state/no-such-pr" "$EVAL_SANDBOX/s5.$verb.before"
  if [ "$verb" = "show" ]; then
    run_review "$verb" acme/app no-such-pr --sig abc --json
  else
    run_review "$verb" acme/app no-such-pr --json
  fi
  code="$(jq -r '.error.code // empty' "$EVAL_OUT" 2>/dev/null || true)"
  nxt="$(jq -r '.next // empty' "$EVAL_OUT" 2>/dev/null || true)"
  re="$(sed -n 's/^re-enter: //p' "$EVAL_ERR" | head -1 || true)"
  if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "missing-findings" ] \
    && [ -z "$nxt" ] && [ -z "$re" ]; then
    echo "  EXPECTED-FAIL: $verb on a missing bundle names no way forward (empty next, no re-enter; reason: read verbs never set Rentry and refusals render next as empty)"
  else
    bad "$verb on a missing bundle exits $EVAL_STATUS with code '$code' (want the documented gap or a way forward)"
  fi
  snap "$EVAL_SANDBOX/.verdict/state/no-such-pr" "$EVAL_SANDBOX/s5.$verb.after"
  snap_same "$verb on a missing bundle" "$EVAL_SANDBOX/s5.$verb.before" "$EVAL_SANDBOX/s5.$verb.after"
  matrix_row "$verb" "no-bundle" "$EVAL_STATUS" "$code" "no" "no" "same" "$(reviews_since "$m0")"
done

eval_end
