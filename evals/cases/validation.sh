#!/usr/bin/env bash
#
# Rigor-gated validation pass (STA-94 D14). A `validation.json` beside
# review.json lets one trusted pass confirm, reject, or downgrade candidates;
# `rigor_policy` in the trusted-base configuration floors each severity tier.
# Advisory is the default: the finding posts and the receipt records what the
# pass decided. Gate mode withholds a rejected finding through D2's receipt.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_BUDGET_USD REVIEW_MODEL || true

HEDGED="This might be a race because the guard could be skipped."
POINTED="The parser drops the error and returns a zero value."
WALKED="The retry loop resets the counter on every pass."

# mkvalid <pr> <mode|none> — a bundle with a hedged `said` critical, a
# `pointed` critical, and a `walked` important. <mode> writes the trusted-base
# rigor policy the probe commits before the bundle is captured.
mkvalid() { # $1 = pr, $2 = base rigor mode or "none"
  if [ "$2" = "none" ]; then rm -f "$EVAL_SANDBOX/.verdict/config.json"
  else printf '{"schema_version":"1","rigor_policy":{"mode":"%s"}}\n' "$2" >"$EVAL_SANDBOX/.verdict/config.json"; fi
  eval_mk_bundle "$1"
  eval_write_review "$1" <<EOF
{"schema_version":"1","body":"> [!IMPORTANT]\n> Three candidates the validator judges.","approved":false,"comments":[
{"path":"notes.txt","line":2,"side":"RIGHT","body":"$HEDGED","rigor":"said","severity":"critical"},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"$POINTED","rigor":"pointed","severity":"critical"},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"$WALKED","rigor":"walked","severity":"important"}]}
EOF
}

# sig_of <pr> <body> — the stored sig for one body, read from the store the
# dry run merged (never re-derived from the review by hand).
sig_of() { # $1 = pr, $2 = body
  jq -r --arg b "$2" '.[] | select(.body == $b) | .sig' \
    "$EVAL_SANDBOX/.verdict/state/$1/findings.dryrun.json"
}

# write_validation <pr> <results-json> — the pass's artifact at the bundle root.
write_validation() { # $1 = pr, $2 = results array
  printf '{"schema_version":"1","head_sha":"%s","results":%s}\n' \
    "$EVAL_FIXTURE_HEAD" "$2" >"$EVAL_SANDBOX/.verdict/state/$1/validation.json"
}

# canned_gh answers every authoring call a real post makes, so no leg here
# reaches the network-dead stub.
install_canned_stub() {
  cat >"$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
case "$*" in
  'api repos/'*'/reviews --paginate'|'api repos/'*'/comments --paginate') echo '[]'; exit 0;;
  *reviews*) cat > "${EVAL_SAVE_PAYLOAD:-/dev/null}"; printf '%s' '{"id":101,"state":"commented","html_url":"https://example.invalid/r/101","submitted_at":"2026-01-01T00:00:00Z"}'; exit 0;;
  *pulls*) printf 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'; exit 0;;
  *user*) printf 'someone-else'; exit 0;;
esac
echo "evals-stub-gh: network disabled ($*)" >&2
exit 1
EOF
  chmod +x "$EVAL_STUBBIN/gh"
}
STUB_SAVED="$EVAL_SANDBOX/gh-stub-orig"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
restore_stub() { cp "$STUB_SAVED" "$EVAL_STUBBIN/gh"; }

# --- 1. advisory default: a rejection warns, counts, and still posts ------
PR=6101
mkvalid "$PR" none
run_review post acme/app "$PR" --dry-run --json
assert_exit "the dry run merges without a validation pass" 0
SIG_HEDGED="$(sig_of "$PR" "$HEDGED")"
SIG_SHORT="${SIG_HEDGED:0:7}"
write_validation "$PR" '[{"sig":"'"$SIG_HEDGED"'","verdict":"REJECTED",
  "reason":"The body hedges with might and names no evidence in the cited hunk."}]'
HASH="$(shasum -a 256 <"$EVAL_SANDBOX/.verdict/state/$PR/validation.json" | cut -d' ' -f1)"
run_review post acme/app "$PR" --dry-run --json
assert_exit "advisory mode posts the rejected finding (exit 0)" 0
assert_json_field "the envelope reports the applied pass" warn '.data.validation.mode'
assert_json_field "the envelope counts the rejection" 1 '.data.validation.counts.rejected'
assert_json_field "the envelope counts the other two candidates" 2 '.data.validation.counts.unvalidated'
assert_json_field "the envelope carries the exact-file hash" "$HASH" '.data.validation.hash'
assert_json_field "the envelope carries the built-in critical floor" walked '.data.validation.floors.critical'
cp "$EVAL_ERR" "$EVAL_SANDBOX/err-6101.txt"
assert_file_grep "advisory mode warns with the validator reason" "$EVAL_ERR" \
  "validation rejected $SIG_SHORT: The body hedges" -F
jq -r '.[] | select(.sig == "'"$SIG_HEDGED"'") | .withheld // "none"' \
  "$EVAL_SANDBOX/.verdict/state/$PR/findings.dryrun.json" >"$EVAL_SANDBOX/adhoc-withheld.txt"
assert_eq "advisory mode stamps no withheld receipt" "none" "$(cat "$EVAL_SANDBOX/adhoc-withheld.txt")"

# --- 2. no artifact: a null hash and every candidate unvalidated ----------
PR=6102
mkvalid "$PR" none
run_review post acme/app "$PR" --dry-run --json
assert_exit "a run with no validation.json posts (exit 0)" 0
assert_json_field "an absent pass records a null hash" null '.data.validation.hash'
assert_json_field "an absent pass counts all three candidates" 3 '.data.validation.counts.unvalidated'
assert_json_field "an absent pass still reports the floors it would apply" pointed '.data.validation.floors.important'

# --- 3. gate mode withholds through the D2 receipt -----------------------
PR=6103
mkvalid "$PR" gate
run_review post acme/app "$PR" --dry-run --require-rigor critical=said --json
assert_exit "the gate-mode dry run merges" 0
SIG_HEDGED="$(sig_of "$PR" "$HEDGED")"
write_validation "$PR" '[{"sig":"'"$SIG_HEDGED"'","verdict":"REJECTED",
  "reason":"The body hedges with might and names no evidence in the cited hunk."}]'
install_canned_stub
PAYLOAD="$EVAL_SANDBOX/posted-6103.json"
rm -f "$PAYLOAD"
export EVAL_SAVE_PAYLOAD="$PAYLOAD"
run_review post acme/app "$PR" --require-rigor critical=said --json
unset EVAL_SAVE_PAYLOAD || true
assert_exit "gate mode withholds instead of refusing the post (exit 0)" 0
assert_json_field "the envelope reports gate mode" gate '.data.validation.mode'
jq -r '.[] | select(.sig == "'"$SIG_HEDGED"'") | [.status, .withheld.source, .withheld.reason] | @tsv' \
  "$EVAL_SANDBOX/.verdict/state/$PR/findings.json" >"$EVAL_SANDBOX/gate-withheld.txt"
assert_file_grep "the withheld finding stays open under the validation source" \
  "$EVAL_SANDBOX/gate-withheld.txt" '^open	validation	The body hedges'
assert_file_nogrep "the rejected claim never reaches the posted payload" \
  "$PAYLOAD" "might be a race"
assert_file_grep "the confirmed finding still posts inline" \
  "$PAYLOAD" "resets the counter"
restore_stub

# --- 4. a head edit cannot move the gate ---------------------------------
jq -n '{schema_version:"1",rigor_policy:{mode:"warn"}}' >"$EVAL_SANDBOX/.verdict/config.json"
run_review post acme/app "$PR" --dry-run --require-rigor critical=said --json
assert_exit "the post after the head edit still merges" 0
assert_json_field "the gate still reads the trusted base" gate '.data.validation.mode'
jq -r '.[] | select(.sig == "'"$SIG_HEDGED"'") | .withheld.source // "none"' \
  "$EVAL_SANDBOX/.verdict/state/$PR/findings.dryrun.json" >"$EVAL_SANDBOX/head-edit.txt"
assert_eq "the head edit did not release the withheld finding" validation "$(cat "$EVAL_SANDBOX/head-edit.txt")"

# --- 5. a DOWNGRADED verdict moves one rank, never two -------------------
PR=6104
mkvalid "$PR" none
run_review post acme/app "$PR" --dry-run --json
SIG_POINTED="$(sig_of "$PR" "$POINTED")"
write_validation "$PR" '[{"sig":"'"$SIG_POINTED"'","verdict":"DOWNGRADED",
  "reason":"Real bug, local blast radius; important, not critical.",
  "from_severity":"critical","to_severity":"important"}]'
run_review post acme/app "$PR" --dry-run --json
assert_exit "a legal downgrade posts (exit 0)" 0
assert_json_field "the envelope counts the downgrade" 1 '.data.validation.counts.downgraded'
jq -r '.[] | select(.sig == "'"$SIG_POINTED"'") | .severity' \
  "$EVAL_SANDBOX/.verdict/state/$PR/findings.dryrun.json" >"$EVAL_SANDBOX/downgraded.txt"
assert_eq "the stored severity moved exactly one tier" important "$(cat "$EVAL_SANDBOX/downgraded.txt")"
write_validation "$PR" '[{"sig":"'"$SIG_POINTED"'","verdict":"DOWNGRADED",
  "reason":"Real bug, local blast radius; informational, not critical.",
  "from_severity":"critical","to_severity":"informational"}]'
run_review post acme/app "$PR" --dry-run --json
assert_exit "a two-rank downgrade refuses" 1
assert_refusal "the illegal pair keeps its typed code" "bad-validation"

# --- 6. rigor floors: refuse in gate mode, force records the bypass ------
PR=6105
mkvalid "$PR" none
run_review post acme/app "$PR" --dry-run --json
run_review post acme/app "$PR" --dry-run --require-rigor critical=walked --json
assert_exit "--require-rigor refuses a said critical" 1
assert_refusal "the floor refusal is typed" "rigor-below-floor"
assert_file_grep "the refusal names the pair to fix" "$EVAL_OUT" 'critical said < walked'
run_review post acme/app "$PR" --dry-run --require-rigor critical=walked --force --json
assert_exit "--force publishes past the floor" 0
run_review post acme/app "$PR" --dry-run --require-rigor nonsense=walked --json
assert_exit "an unknown tier refuses" 1
assert_refusal "the policy refusal is typed" "bad-rigor-policy"

# --- 7. the real post stores the hash, the tally, and the forced bypass ---
PR=6106
mkvalid "$PR" none
run_review post acme/app "$PR" --dry-run --json
SIG_HEDGED="$(sig_of "$PR" "$HEDGED")"
SIG_SHORT="${SIG_HEDGED:0:7}"
write_validation "$PR" '[{"sig":"'"$SIG_HEDGED"'","verdict":"REJECTED",
  "reason":"The body hedges with might and names no evidence in the cited hunk."}]'
HASH="$(shasum -a 256 <"$EVAL_SANDBOX/.verdict/state/$PR/validation.json" | cut -d' ' -f1)"
install_canned_stub
run_review post acme/app "$PR" --json
assert_exit "the real post publishes in advisory mode" 0
assert_json_field "post.json records the applied hash" "$HASH" '.validation_hash' \
  "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
assert_json_field "post.json records the rejection" 1 '.validation_counts.rejected' \
  "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
assert_json_field "post.json records the mode" warn '.validation_mode' \
  "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
assert_json_field "post.json records the floors" walked '.rigor_floors.critical' \
  "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
# The gate would withhold the rejected finding, so drop the artifact: this leg
# is about a finding that is merely below its floor, not rejected.
rm -f "$EVAL_SANDBOX/.verdict/state/$PR/validation.json"
run_review post acme/app "$PR" --require-rigor critical=walked --force --json
assert_exit "the forced post still publishes" 0
assert_json_field "the forced bypass names its reason" "rigor-below-floor" \
  '.forced[0].reason' "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
assert_json_field "the forced receipt names the finding it published anyway" 1 \
  '.forced[0].sigs | length' "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
restore_stub

# --- 8. record stores the same receipt, with a null hash when skipped ----
eval_mk_bundle local-valrec
eval_write_review local-valrec <<EOF
{"schema_version":"1","body":"> [!IMPORTANT]\n> One candidate.","approved":false,"comments":[
{"path":"notes.txt","line":2,"side":"RIGHT","body":"$WALKED","rigor":"walked","severity":"important"}]}
EOF
rm -f "$EVAL_SANDBOX/.verdict/config.json"
run_review record acme/app local-valrec --json
assert_exit "record stores a reviewed bundle" 0
assert_json_field "record.json records a null hash when no pass ran" null '.validation_hash' \
  "$EVAL_SANDBOX/.verdict/state/local-valrec/record.json"
assert_json_field "record.json counts the skipped candidate" 1 '.validation_counts.unvalidated' \
  "$EVAL_SANDBOX/.verdict/state/local-valrec/record.json"
assert_json_field "record.json records the advisory mode" warn '.validation_mode' \
  "$EVAL_SANDBOX/.verdict/state/local-valrec/record.json"

# --- 9. a stale artifact and an unresolvable sig refuse -----------------
PR=6107
mkvalid "$PR" none
run_review post acme/app "$PR" --dry-run --json
write_validation "$PR" '[{"sig":"0123456789abcdef0123456789abcdef01234567",
  "verdict":"CONFIRMED","reason":"The cited hunk shows the removed guard."}]'
run_review post acme/app "$PR" --dry-run --json
assert_exit "an unresolvable sig refuses" 1
assert_refusal "the bad artifact is typed" "bad-validation"
printf '{"schema_version":"1","head_sha":"%s","results":[]}\n' \
  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" >"$EVAL_SANDBOX/.verdict/state/$PR/validation.json"
run_review post acme/app "$PR" --dry-run --json
assert_exit "a stale head refuses" 1
assert_refusal "the stale artifact is typed" "validation-stale"

# --- 10. a rejected finding the merge moved into the body stays out ------
# The moved section is curated from the entries, so the pass has to stamp a
# withheld entry before that section is built, not after.
PR=6108
MOVED="Out-of-hunk claim the validator rejects."
printf '{"schema_version":"1","rigor_policy":{"mode":"gate"}}\n' >"$EVAL_SANDBOX/.verdict/config.json"
eval_mk_bundle "$PR"
eval_write_review "$PR" <<EOF
{"schema_version":"1","body":"> [!IMPORTANT]\n> One finding outside the diff.","approved":false,"comments":[
{"path":"notes.txt","line":99,"side":"RIGHT","body":"$MOVED","rigor":"walked","severity":"important"}]}
EOF
run_review post acme/app "$PR" --dry-run --json
assert_exit "the moved finding merges outside the diff" 0
SIG_MOVED="$(sig_of "$PR" "$MOVED")"
write_validation "$PR" '[{"sig":"'"$SIG_MOVED"'","verdict":"REJECTED",
  "reason":"The cited line sits outside the diff, so no hunk carries it."}]'
install_canned_stub
PAYLOAD="$EVAL_SANDBOX/posted-6108.json"
rm -f "$PAYLOAD"
export EVAL_SAVE_PAYLOAD="$PAYLOAD"
run_review post acme/app "$PR" --json
unset EVAL_SAVE_PAYLOAD || true
assert_exit "gate mode withholds the moved finding (exit 0)" 0
jq -r '.[] | select(.sig == "'"$SIG_MOVED"'") | [.status, .withheld.source] | @tsv' \
  "$EVAL_SANDBOX/.verdict/state/$PR/findings.json" >"$EVAL_SANDBOX/moved-withheld.txt"
assert_file_grep "the moved finding stays open under the validation source" \
  "$EVAL_SANDBOX/moved-withheld.txt" '^open	validation'
assert_file_nogrep "the moved finding never reaches the posted body" "$PAYLOAD" "Out-of-hunk claim"
restore_stub

# --- 11. a downgrade names the severity the finding carries --------------
PR=6109
mkvalid "$PR" none
eval_write_review "$PR" <<EOF
{"schema_version":"1","body":"> [!IMPORTANT]\n> One low finding.","approved":false,"comments":[
{"path":"notes.txt","line":2,"side":"RIGHT","body":"$WALKED","rigor":"walked","severity":"informational"}]}
EOF
run_review post acme/app "$PR" --dry-run --json
SIG_LOW="$(sig_of "$PR" "$WALKED")"
write_validation "$PR" '[{"sig":"'"$SIG_LOW"'","verdict":"DOWNGRADED",
  "reason":"Real bug, local blast radius; important, not critical.",
  "from_severity":"critical","to_severity":"important"}]'
run_review post acme/app "$PR" --dry-run --json
assert_exit "a downgrade from a severity the finding lacks refuses" 1
assert_refusal "the mismatched pair is typed" "bad-validation"
assert_file_grep "the refusal names the pair the finding does not carry" "$EVAL_OUT" 'from_severity critical'

# --- 12. a finding the floor already withheld is not unvalidated ---------
PR=6110
mkvalid "$PR" none
eval_write_review "$PR" <<EOF
{"schema_version":"1","body":"> [!IMPORTANT]\n> One posted finding.","approved":false,"comments":[
{"path":"notes.txt","line":2,"side":"RIGHT","body":"$WALKED","rigor":"walked","severity":"important"},
{"path":"notes.txt","line":2,"side":"RIGHT","body":"$POINTED","rigor":"pointed","severity":"informational"}]}
EOF
run_review post acme/app "$PR" --dry-run --json
SIG_WALKED="$(sig_of "$PR" "$WALKED")"
write_validation "$PR" '[{"sig":"'"$SIG_WALKED"'","verdict":"CONFIRMED",
  "reason":"The cited hunk resets the counter on every pass."}]'
run_review post acme/app "$PR" --dry-run --min-severity important --json
assert_exit "the floor-withheld dry run merges" 0
assert_json_field "the floor-withheld finding is outside the tally" 0 '.data.validation.counts.unvalidated'
assert_json_field "the pass still counts its own confirmation" 1 '.data.validation.counts.confirmed'

eval_end
