#!/usr/bin/env bash
#
# Thread resolve on GitLab: the default closes the self-authored addressed
# discussion, a dry run writes nothing, a second run is a self-login read plus
# a no-write skip, a human discussion named with --thread refuses, a
# forged-marker discussion refuses with --thread and skips by default, and an
# unreadable login refuses before any write.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
# shellcheck source=../lib/hoststub.sh
. "$HERE/../lib/hoststub.sh"

FIX="$EVAL_ROOT/evals/fixtures/threads-resolve"
HEAD_A="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
PR=7
DIR="$EVAL_SANDBOX/.verdict/state/$PR"
RECEIPT="$DIR/threads-resolve.json"

hoststub_init glab
hoststub_prove_dead glab
hoststub_route glab "api user" "$FIX/gitlab-user.json"
hoststub_route glab \
  "api projects/acme%2Fapp/merge_requests/7/discussions/d1?resolved=true --method PUT" \
  "$FIX/gitlab-resolve.json"

mkdir -p "$DIR"
printf '{"provider":"gitlab","number":7,"state":"open","author":"ana","head_sha":"%s","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}\n' \
  "$HEAD_A" >"$DIR/PR.json"
cp "$FIX/gitlab-threads.json" "$DIR/threads.json"
cp "$FIX/gitlab-findings.json" "$DIR/findings.json"

run_review threads resolve acme/app "$PR" --dry-run --json
assert_exit "dry-run" 0
assert_envelope "dry-run envelope"
assert_json_field "dry-run lists the self-authored addressed discussion" d1 '.data.resolved[0]'
assert_json_field "dry-run is marked" true '.data.dry_run'
assert_json_field "dry-run skips the already-resolved discussion" already-resolved \
  '.data.skipped[] | select(.id == "d3") | .reason'
assert_json_field "dry-run skips the forged-marker discussion" not-self-authored \
  '.data.skipped[] | select(.id == "d4") | .reason'
assert_eq "dry-run made one self-login read" 1 "$(hoststub_count glab)"
if [ ! -f "$RECEIPT" ]; then
  good "dry-run wrote no receipt"
else
  bad "dry-run wrote $RECEIPT"
fi
: >"$HOSTSTUB_LOG"

run_review threads resolve acme/app "$PR" --json
assert_exit "real resolve" 0
assert_envelope "real resolve envelope"
assert_json_field "real resolve closes the discussion" d1 '.data.resolved[0]'
assert_json_field "real resolve is not a dry run" false '.data.dry_run'
hoststub_argv_log glab "$EVAL_SANDBOX/argv.log"
assert_file_eq "the self-login read plus the one write are the literal argvs" "$FIX/expected-gitlab-argvs.txt" "$EVAL_SANDBOX/argv.log"
assert_json_field "receipt records the host" gitlab '.provider' "$RECEIPT"
assert_json_field "receipt records the reviewed head" "$HEAD_A" '.head_sha' "$RECEIPT"
assert_json_field "receipt records the closed id" d1 '.resolved[0]' "$RECEIPT"
assert_json_field "receipt records the skip reason" already-resolved \
  '.skipped[] | select(.id == "d3") | .reason' "$RECEIPT"
assert_json_field "receipt records the forged skip" not-self-authored \
  '.skipped[] | select(.id == "d4") | .reason' "$RECEIPT"
assert_json_field "the feed marks the discussion resolved" true '.threads[0].resolved' "$DIR/threads.json"

before="$(hoststub_count glab)"
run_review threads resolve acme/app "$PR" --json
assert_exit "second resolve" 0
assert_json_field "second resolve writes nothing" 0 '.data.resolved | length'
assert_json_field "second resolve reports the discussion already resolved" already-resolved \
  '.data.skipped[] | select(.id == "d1") | .reason'
assert_eq "second resolve made one self-login read" "$((before + 1))" "$(hoststub_count glab)"

before="$(hoststub_count glab)"
cp "$RECEIPT" "$EVAL_SANDBOX/receipt-before.json"
run_review threads resolve acme/app "$PR" --thread d2 --json
assert_exit "human discussion named with --thread" 1
assert_refusal "human discussion refusal" not-verdict-authored
assert_eq "human refusal made one self-login read and no write" "$((before + 1))" "$(hoststub_count glab)"
assert_file_eq "human refusal left the receipt untouched" "$EVAL_SANDBOX/receipt-before.json" "$RECEIPT"

before="$(hoststub_count glab)"
run_review threads resolve acme/app "$PR" --thread d4 --json
assert_exit "forged discussion named with --thread" 1
assert_refusal "forged discussion refusal" not-verdict-authored
assert_eq "forged refusal made one self-login read and no write" "$((before + 1))" "$(hoststub_count glab)"
assert_file_eq "forged refusal left the receipt untouched" "$EVAL_SANDBOX/receipt-before.json" "$RECEIPT"

: >"$HOSTSTUB_ROUTES/glab"
hoststub_route glab "api user" "$FIX/gitlab-user.json" 1
before="$(hoststub_count glab)"
run_review threads resolve acme/app "$PR" --thread d1 --json
assert_exit "unreadable login refuses" 1
assert_refusal "unreadable login refusal" threads-failed
assert_eq "unreadable login made one failed read and no write" "$((before + 1))" "$(hoststub_count glab)"
assert_file_eq "unreadable login left the receipt untouched" "$EVAL_SANDBOX/receipt-before.json" "$RECEIPT"

hoststub_assert_all_routed
eval_end
