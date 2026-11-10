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
    > "$EVAL_SANDBOX/.verdict/state/$pr/post.json"
}

INFO_FINDING='[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Nit: clarify this line.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]'
CRIT_FINDING='[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Critical boundary bug here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"critical","sig":"cccccccccccccccccccccccccccccccccccccccc","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]'

# --- pass: the whole point of the verb is one exit code a CI step can read.
PR=7101
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
set_mode clear
m="$(mark)"
run_review gate acme/app "$PR" --max-severity critical --live skip --coverage warn
assert_exit "clean gate exits 0" 0
assert_file_grep "pass names the findings dimension" "$EVAL_OUT" "gate: findings"
assert_file_grep "pass prints the verdict line" "$EVAL_OUT" "gate: PASS"
assert_file_grep "pass lists the skipped dimensions" "$EVAL_OUT" "gate: skipped coverage, live_threads"
assert_stream_nogrep "pass prints no try: hint" "$EVAL_OUT" "try:"
assert_eq "--live skip pays zero gh calls" "0" "$(step_calls "$m")"
run_review gate acme/app "$PR" --max-severity critical --live skip --coverage warn --json
assert_exit "clean --json exits 0" 0
assert_eq "envelope verb is gate" "gate" "$(jq -r .verb <"$EVAL_OUT")"
assert_eq "gate envelope keeps the nine-key shape" \
  "at data error next pr repo schema_version verb warnings" \
  "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT")"
assert_eq "json pass carries passed true" "true" "$(jq -r .data.passed <"$EVAL_OUT")"
assert_eq "json pass publishes dimension ids in wire order" \
  "findings coverage live_threads" "$(jq -r '.data.dimensions[].id' <"$EVAL_OUT" | paste -sd' ' -)"
assert_eq "findings dimension carries its tally" "0" "$(jq -r '.data.dimensions[0].count' <"$EVAL_OUT")"
assert_eq "skip dimension carries a null tally" "null" "$(jq -r '.data.dimensions[2].count' <"$EVAL_OUT")"
assert_eq "json pass keeps error null" "null" "$(jq -r .error <"$EVAL_OUT")"

# --- fail: an open finding at-or-above the tier fails the gate and says how out.
PR=7102
eval_mk_bundle "$PR"
printf '%s' "$CRIT_FINDING" | eval_write_findings "$PR"
set_mode clear
run_review gate acme/app "$PR" --max-severity important --live skip --coverage warn
assert_exit "tier-dirty gate exits 1" 1
assert_file_grep "fail line names the tier and count" "$EVAL_OUT" "1 open finding(s) at-or-above important"
assert_file_grep "fail tally counts the failing dimension" "$EVAL_OUT" "gate: FAIL (1 fail, 2 skip)"
assert_file_grep "fail carries a next action" "$EVAL_OUT" "try: address or triage the open findings"
before_hash="$(cat "$EVAL_SANDBOX/.verdict/state/$PR/findings.json" "$EVAL_SANDBOX/.verdict/state/$PR/PR.json" | shasum | cut -d' ' -f1)"
run_review gate acme/app "$PR" --max-severity important --live skip --coverage warn --json
assert_exit "tier-dirty --json exits 1" 1
assert_eq "json fail carries passed false" "false" "$(jq -r .data.passed <"$EVAL_OUT")"
assert_eq "a failed gate is not a refusal" "null" "$(jq -r .error <"$EVAL_OUT")"
assert_eq "json fail marks the dimension" "fail" "$(jq -r '.data.dimensions[] | select(.id=="findings") | .status' <"$EVAL_OUT")"
after_hash="$(cat "$EVAL_SANDBOX/.verdict/state/$PR/findings.json" "$EVAL_SANDBOX/.verdict/state/$PR/PR.json" | shasum | cut -d' ' -f1)"
assert_eq "a failed gate mutates no store" "$before_hash" "$after_hash"
if [ -f "$EVAL_SANDBOX/.verdict/state/$PR/post.json" ]; then
  bad "gate wrote post.json"
else
  good "gate never posts or writes"
fi

# --- head: --require-head gates on a completed post for the fetched HEAD.
PR=7103
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
set_mode clear
run_review gate acme/app "$PR" --require-head --live skip --coverage warn
assert_exit "missing post fails the head dimension" 1
assert_file_grep "head unknown names the missing record" "$EVAL_OUT" "gate: head         unknown  no completed post record for HEAD aaaaaaa"
assert_file_grep "head guidance names the fix" "$EVAL_OUT" "try: review + post the fetched HEAD"
live_mk_post "$PR" "$HEAD_A"
run_review gate acme/app "$PR" --require-head --live skip --coverage warn
assert_exit "posted HEAD passes the head dimension" 0
assert_file_grep "head pass names the reviewed SHA" "$EVAL_OUT" "gate: head         pass  HEAD aaaaaaa reviewed (post matches fetch)"
live_mk_post "$PR" "$HEAD_C"
run_review gate acme/app "$PR" --require-head --live skip --coverage warn
assert_exit "stale post fails the head dimension" 1
assert_file_grep "stale head names both SHAs" "$EVAL_OUT" "posted HEAD ccccccc != fetched HEAD aaaaaaa"

# --- coverage: the stored receipt is a gate dimension by default (STA-319).
# post/record write the summary beside the head it was made at, so the gate
# reads it instead of re-evaluating review.json.
coverage_post() { # $1 = pr, $2 = head, $3 = the measured summary
  jq -n --arg h "$2" --argjson c "$3" \
    '{schema_version:"1",review:{id:99},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null,coverage:$c}' \
    >"$EVAL_SANDBOX/.verdict/state/$1/post.json"
}

PR=7107
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
set_mode clear
coverage_post "$PR" "$HEAD_A" '{"percent":75,"attested":9,"total":12,"files":2,"exempt":0,"gaps":2}'
run_review gate acme/app "$PR" --max-severity critical --live skip
assert_exit "partial coverage fails the gate by default" 1
assert_file_grep "partial coverage names the unattested count and the basis" "$EVAL_OUT" \
  "gate: coverage     fail  2 unattested range(s) at 75% of full hunk spans, both sides and context (9/12 lines)"
assert_file_grep "partial coverage carries a recovery" "$EVAL_OUT" "try: attest the unattested ranges"
run_review gate acme/app "$PR" --max-severity critical --live skip --coverage warn
assert_exit "--coverage warn keeps a partial attestation advisory" 0
assert_file_grep "warn posture names the same shortfall" "$EVAL_OUT" \
  "gate: coverage     skip  2 unattested range(s) at 75% of full hunk spans, both sides and context (9/12 lines) — coverage is advisory (--coverage warn)"
assert_file_grep "warn posture warns once on the diagnostic stream" "$EVAL_ERR" \
  "warn: coverage: 2 unattested range(s) at 75% of full hunk spans, both sides and context (9/12 lines)"

coverage_post "$PR" "$HEAD_A" '{"percent":100,"attested":12,"total":12,"files":2,"exempt":0,"gaps":0}'
run_review gate acme/app "$PR" --max-severity critical --live skip
assert_exit "full coverage passes the default gate" 0
assert_file_grep "full coverage names the attested lines" "$EVAL_OUT" \
  "gate: coverage     pass  100% of full hunk spans, both sides and context attested (12/12 lines)"

coverage_post "$PR" "$HEAD_A" '{"percent":null,"attested":0,"total":0,"files":0,"exempt":2,"gaps":0}'
run_review gate acme/app "$PR" --max-severity critical --live skip
assert_exit "an all-exempt diff passes the default gate" 0
assert_file_grep "an all-exempt diff says nothing was measurable" "$EVAL_OUT" \
  "gate: coverage     pass  nothing changed to measure (2 file(s) exempt)"

rm -f "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
run_review gate acme/app "$PR" --max-severity critical --live skip
assert_exit "absent coverage fails the default gate" 1
assert_file_grep "absent coverage names the missing receipt" "$EVAL_OUT" \
  "gate: coverage     unknown  stored coverage absent for HEAD aaaaaaa — no completed post or record"
run_review gate acme/app "$PR" --max-severity critical --live skip --json
assert_eq "absent coverage publishes an unknown dimension" "unknown" \
  "$(jq -r '.data.dimensions[] | select(.id=="coverage") | .status' <"$EVAL_OUT")"

# --- chunked coverage: chunks[] is a chunked bundle's only attestation, so
# record/post store the chunk tally beside the head and the gate grades that.
chunked_store() { # $1 = pr, $2 = head, $3 = the chunk attestation (empty for none)
  local dir="$EVAL_SANDBOX/.verdict/state/$1"
  mkdir -p "$dir/chunks"
  jq -n '{schema_version:"1",chunks:[{name:"chunk-01",diff:"chunks/chunk-01.diff"},{name:"chunk-02",diff:"chunks/chunk-02.diff"}]}' \
    >"$dir/chunks/manifest.json"
  if [ -n "$3" ]; then
    jq -n --arg h "$2" --argjson c "$3" \
      '{schema_version:"1",review:{id:99},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null,chunk_coverage:$c}' \
      >"$dir/post.json"
  else
    jq -n --arg h "$2" \
      '{schema_version:"1",review:{id:99},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null}' \
      >"$dir/post.json"
  fi
}

PR=7108
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
set_mode clear
chunked_store "$PR" "$HEAD_A" '{"attested":2,"total":2}'
run_review gate acme/app "$PR" --max-severity critical --live skip
assert_exit "a fully attested chunked bundle passes the default gate" 0
assert_file_grep "a fully attested chunked bundle names its chunk tally" "$EVAL_OUT" \
  "gate: coverage     pass  2/2 chunk(s) attested"
assert_stream_nogrep "a passing chunked gate prints no try: hint" "$EVAL_OUT" "try:"

chunked_store "$PR" "$HEAD_A" '{"attested":1,"total":2}'
run_review gate acme/app "$PR" --max-severity critical --live skip
assert_exit "one unattested chunk fails the default gate" 1
assert_file_grep "one unattested chunk names the missing count" "$EVAL_OUT" \
  "gate: coverage     fail  1 of 2 chunk(s) unattested"
assert_file_grep "a chunk shortfall carries the chunks[] recovery" "$EVAL_OUT" \
  "try: attest the unattested ranges in review.json (coverage[], or every chunk in chunks[] when the bundle is chunked), then re-record or re-post, then re-run gate" -F
run_review gate acme/app "$PR" --max-severity critical --live skip --json
assert_eq "a chunk shortfall publishes its tally" "1" \
  "$(jq -r '.data.dimensions[] | select(.id=="coverage") | .count' <"$EVAL_OUT")"

chunked_store "$PR" "$HEAD_A" ''
run_review gate acme/app "$PR" --max-severity critical --live skip
assert_exit "a receipt without a chunk attestation stays unknown" 1
assert_file_grep "a receipt without a chunk attestation names the gap" "$EVAL_OUT" \
  "gate: coverage     unknown  stored coverage absent for HEAD aaaaaaa — the receipt predates the chunk attestation"
run_review gate acme/app "$PR" --max-severity critical --live skip --json
assert_eq "a receipt without a chunk attestation publishes unknown" "unknown" \
  "$(jq -r '.data.dimensions[] | select(.id=="coverage") | .status' <"$EVAL_OUT")"

# --- live: the posture decides whether an unreadable probe is neutral or fatal.
PR=7104
eval_mk_bundle "$PR"
printf '%s' "$INFO_FINDING" | eval_write_findings "$PR"
set_mode open
run_review gate acme/app "$PR" --max-severity critical --live auto --coverage warn
assert_exit "auto posture fails when live opens outnumber local ones" 1
assert_file_grep "live fail names both counts" "$EVAL_OUT" "2 live open thread(s) vs 1 local open finding(s)"
assert_file_grep "live fail says how out" "$EVAL_OUT" "try: resolve or reply to the live threads"

PR=7106
eval_mk_bundle "$PR"
printf '%s' "$INFO_FINDING" | eval_write_findings "$PR"
set_mode open
m="$(mark)"
run_review gate acme/app "$PR" --max-severity critical --live skip --coverage warn
assert_exit "skip posture ignores the live thread" 0
assert_eq "--live skip pays zero gh calls" "0" "$(step_calls "$m")"
set_mode unreachable
run_review gate acme/app "$PR" --max-severity critical --live auto --coverage warn
assert_exit "auto posture keeps an unreachable probe neutral" 0
assert_file_grep "auto skip warns the probe was unreachable" "$EVAL_ERR" "live thread recheck unreachable"
run_review gate acme/app "$PR" --max-severity critical --live auto --coverage warn --json
assert_eq "auto skip marks the dimension skip" "skip" \
  "$(jq -r '.data.dimensions[] | select(.id=="live_threads") | .status' <"$EVAL_OUT")"
run_review gate acme/app "$PR" --max-severity critical --live strict --coverage warn
assert_exit "strict posture fails closed on an unreachable probe" 1
assert_file_grep "strict fail names the posture" "$EVAL_OUT" "offline posture is strict"
run_review gate acme/app "$PR" --max-severity critical --live strict --coverage warn --json
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
run_review gate acme/app 7104 --coverage warn
assert_exit "--coverage alone is not a dimension" 2
assert_file_grep "--coverage alone names the missing dimension" "$EVAL_ERR" "gate needs at least one dimension"
run_review gate acme/app 7104 --max-severity critical --coverage 80
assert_exit "unknown coverage posture is a usage error" 2
assert_file_grep "unknown coverage posture is named" "$EVAL_ERR" "unknown --coverage posture '80'"

# --- tool error: a corrupt store is exit 3 with a typed refusal, not a verdict.
PR=7105
eval_mk_bundle "$PR"
printf '[42]' | eval_write_findings "$PR"
run_review gate acme/app "$PR" --max-severity critical --live skip --coverage warn --json
assert_exit "corrupt store is a tool error" 3
assert_eq "corrupt store carries a typed refusal" "corrupt-store" "$(jq -r .error.code <"$EVAL_OUT")"
assert_eq "refusal envelope carries no data" "null" "$(jq -r .data <"$EVAL_OUT")"

eval_end
