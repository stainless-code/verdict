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
  mode="$(cat "${EVAL_SANDBOX:-/tmp}/gate-mode" 2>/dev/null || echo unreachable)"
  case "$mode" in
    clear) printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":true}]}}}}}'; exit 0;;
    open) printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":false},{"isResolved":false},{"isResolved":true}]}}}}}'; exit 0;;
    *) echo "evals-stub-gh: threads unreachable" >&2; exit 1;;
  esac
fi
echo "evals-stub-gh: network disabled ($*)" >&2
exit 1
EOF
chmod +x "$EVAL_STUBBIN/gh"

mark() { wc -l <"$EVAL_STUBLOG" | tr -d ' '; }
step_calls() { tail -n +"$(( $1 + 1 ))" "$EVAL_STUBLOG" 2>/dev/null | grep -c "" || true; }
set_mode() { printf '%s' "$1" > "$EVAL_SANDBOX/gate-mode"; }

HEAD_A="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
HEAD_C="cccccccccccccccccccccccccccccccccccccccc"

live_mk_post() {
  local pr="$1" head="$2"
  jq -n --arg h "$head" \
    '{schema_version:"1",review:{id:99},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null}' \
    > "$EVAL_SANDBOX/.review/$pr/post.json"
}

INFO_FINDING='[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Nit: clarify this line.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]'
CRIT_FINDING='[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Critical boundary bug here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"critical","sig":"cccccccccccccccccccccccccccccccccccccccc","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]'

# --- pass: the whole point of the verb is one exit code a CI step can read.
PR=7101
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
set_mode clear
m="$(mark)"
run_review gate acme/app "$PR" --max-severity critical --live skip
assert_exit "clean gate exits 0" 0
assert_file_grep "pass names the findings dimension" "$EVAL_OUT" "gate: findings"
assert_file_grep "pass prints the verdict line" "$EVAL_OUT" "gate: PASS"
assert_file_grep "pass lists the skipped dimension" "$EVAL_OUT" "gate: skipped live_threads"
assert_stream_nogrep "pass prints no try: hint" "$EVAL_OUT" "try:"
assert_eq "--live skip pays zero gh calls" "0" "$(step_calls "$m")"
run_review gate acme/app "$PR" --max-severity critical --live skip --json
assert_exit "clean --json exits 0" 0
assert_eq "envelope verb is gate" "gate" "$(jq -r .verb <"$EVAL_OUT")"
assert_eq "gate envelope keeps the nine-key shape" \
  "at data error next pr repo schema_version verb warnings" \
  "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT")"
assert_eq "json pass carries passed true" "true" "$(jq -r .data.passed <"$EVAL_OUT")"
assert_eq "json pass publishes dimension ids in wire order" \
  "findings live_threads" "$(jq -r '.data.dimensions[].id' <"$EVAL_OUT" | paste -sd' ' -)"
assert_eq "findings dimension carries its tally" "0" "$(jq -r '.data.dimensions[0].count' <"$EVAL_OUT")"
assert_eq "skip dimension carries a null tally" "null" "$(jq -r '.data.dimensions[1].count' <"$EVAL_OUT")"
assert_eq "json pass keeps error null" "null" "$(jq -r .error <"$EVAL_OUT")"

# --- fail: an open finding at-or-above the tier fails the gate and says how out.
PR=7102
eval_mk_bundle "$PR"
printf '%s' "$CRIT_FINDING" | eval_write_findings "$PR"
set_mode clear
run_review gate acme/app "$PR" --max-severity important --live skip
assert_exit "tier-dirty gate exits 1" 1
assert_file_grep "fail line names the tier and count" "$EVAL_OUT" "1 open finding(s) at-or-above important"
assert_file_grep "fail tally counts the failing dimension" "$EVAL_OUT" "gate: FAIL (1 fail, 1 skip)"
assert_file_grep "fail carries a next action" "$EVAL_OUT" "try: address or triage the open findings"
before_hash="$(cat "$EVAL_SANDBOX/.review/$PR/findings.json" "$EVAL_SANDBOX/.review/$PR/PR.json" | shasum | cut -d' ' -f1)"
run_review gate acme/app "$PR" --max-severity important --live skip --json
assert_exit "tier-dirty --json exits 1" 1
assert_eq "json fail carries passed false" "false" "$(jq -r .data.passed <"$EVAL_OUT")"
assert_eq "a failed gate is not a refusal" "null" "$(jq -r .error <"$EVAL_OUT")"
assert_eq "json fail marks the dimension" "fail" "$(jq -r '.data.dimensions[] | select(.id=="findings") | .status' <"$EVAL_OUT")"
after_hash="$(cat "$EVAL_SANDBOX/.review/$PR/findings.json" "$EVAL_SANDBOX/.review/$PR/PR.json" | shasum | cut -d' ' -f1)"
assert_eq "a failed gate mutates no store" "$before_hash" "$after_hash"
if [ -f "$EVAL_SANDBOX/.review/$PR/post.json" ]; then
  bad "gate wrote post.json"
else
  good "gate never posts or writes"
fi

# --- head: --require-head gates on a completed post for the fetched HEAD.
PR=7103
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
set_mode clear
run_review gate acme/app "$PR" --require-head --live skip
assert_exit "missing post fails the head dimension" 1
assert_file_grep "head unknown names the missing record" "$EVAL_OUT" "gate: head         unknown  no completed post record for HEAD aaaaaaa"
assert_file_grep "head guidance names the fix" "$EVAL_OUT" "try: review + post the fetched HEAD"
live_mk_post "$PR" "$HEAD_A"
run_review gate acme/app "$PR" --require-head --live skip
assert_exit "posted HEAD passes the head dimension" 0
assert_file_grep "head pass names the reviewed SHA" "$EVAL_OUT" "gate: head         pass  HEAD aaaaaaa reviewed (post matches fetch)"
live_mk_post "$PR" "$HEAD_C"
run_review gate acme/app "$PR" --require-head --live skip
assert_exit "stale post fails the head dimension" 1
assert_file_grep "stale head names both SHAs" "$EVAL_OUT" "posted HEAD ccccccc != fetched HEAD aaaaaaa"

# --- live: the posture decides whether an unreadable probe is neutral or fatal.
PR=7104
eval_mk_bundle "$PR"
printf '%s' "$INFO_FINDING" | eval_write_findings "$PR"
set_mode open
run_review gate acme/app "$PR" --max-severity critical --live auto
assert_exit "auto posture fails when live opens outnumber local ones" 1
assert_file_grep "live fail names both counts" "$EVAL_OUT" "2 live open thread(s) vs 1 local open finding(s)"
assert_file_grep "live fail says how out" "$EVAL_OUT" "try: resolve or reply to the live threads"

PR=7106
eval_mk_bundle "$PR"
printf '%s' "$INFO_FINDING" | eval_write_findings "$PR"
set_mode open
m="$(mark)"
run_review gate acme/app "$PR" --max-severity critical --live skip
assert_exit "skip posture ignores the live thread" 0
assert_eq "--live skip pays zero gh calls" "0" "$(step_calls "$m")"
set_mode unreachable
run_review gate acme/app "$PR" --max-severity critical --live auto
assert_exit "auto posture keeps an unreachable probe neutral" 0
assert_file_grep "auto skip warns the probe was unreachable" "$EVAL_ERR" "live thread recheck unreachable"
run_review gate acme/app "$PR" --max-severity critical --live auto --json
assert_eq "auto skip marks the dimension skip" "skip" \
  "$(jq -r '.data.dimensions[] | select(.id=="live_threads") | .status' <"$EVAL_OUT")"
run_review gate acme/app "$PR" --max-severity critical --live strict
assert_exit "strict posture fails closed on an unreachable probe" 1
assert_file_grep "strict fail names the posture" "$EVAL_OUT" "offline posture is strict"
run_review gate acme/app "$PR" --max-severity critical --live strict --json
assert_eq "strict json marks the dimension unknown" "unknown" \
  "$(jq -r '.data.dimensions[] | select(.id=="live_threads") | .status' <"$EVAL_OUT")"

# --- usage: a gate with no dimension is a usage error, never a silent pass.
run_review gate acme/app 7104
assert_exit "bare gate is a usage error" 2
assert_file_grep "usage names the dimensions" "$EVAL_ERR" "gate needs at least one dimension"
assert_stream_nogrep "usage prints no envelope" "$EVAL_OUT" "schema_version"
run_review gate acme/app 7104 --live skip
assert_exit "--live alone is not a dimension" 2
run_review gate acme/app 7104 --max-severity severe
assert_exit "unknown tier is a usage error" 2
assert_file_grep "unknown tier is named" "$EVAL_ERR" "unknown --max-severity tier 'severe'"
run_review gate acme/app 7104 --max-risk high
assert_exit "unknown flag is a usage error" 2
assert_file_grep "unknown flag is named" "$EVAL_ERR" "unknown flag '--max-risk'"

# --- tool error: a corrupt store is exit 3 with a typed refusal, not a verdict.
PR=7105
eval_mk_bundle "$PR"
printf '[42]' | eval_write_findings "$PR"
run_review gate acme/app "$PR" --max-severity critical --live skip --json
assert_exit "corrupt store is a tool error" 3
assert_eq "corrupt store carries a typed refusal" "corrupt-store" "$(jq -r .error.code <"$EVAL_OUT")"
assert_eq "refusal envelope carries no data" "null" "$(jq -r .data <"$EVAL_OUT")"

eval_end
