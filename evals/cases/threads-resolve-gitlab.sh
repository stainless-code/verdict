#!/usr/bin/env bash
#
# Thread resolve on GitLab: the default closes the verdict-authored addressed
# discussion, a dry run writes nothing, a second run is a no-write skip, and a
# human discussion named with --thread refuses before the host is called.
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
assert_json_field "dry-run lists the verdict-authored addressed discussion" d1 '.data.resolved[0]'
assert_json_field "dry-run is marked" true '.data.dry_run'
assert_json_field "dry-run skips the already-resolved discussion" already-resolved \
  '.data.skipped[] | select(.id == "d3") | .reason'
assert_eq "dry-run made zero host calls" 0 "$(hoststub_count glab)"
if [ ! -f "$RECEIPT" ]; then
  good "dry-run wrote no receipt"
else
  bad "dry-run wrote $RECEIPT"
fi

run_review threads resolve acme/app "$PR" --json
assert_exit "real resolve" 0
assert_envelope "real resolve envelope"
assert_json_field "real resolve closes the discussion" d1 '.data.resolved[0]'
assert_json_field "real resolve is not a dry run" false '.data.dry_run'
hoststub_argv_log glab "$EVAL_SANDBOX/argv.log"
assert_file_eq "the one write is the literal resolve argv" "$FIX/expected-gitlab-argvs.txt" "$EVAL_SANDBOX/argv.log"
assert_json_field "receipt records the host" gitlab '.provider' "$RECEIPT"
assert_json_field "receipt records the reviewed head" "$HEAD_A" '.head_sha' "$RECEIPT"
assert_json_field "receipt records the closed id" d1 '.resolved[0]' "$RECEIPT"
assert_json_field "receipt records the skip reason" already-resolved \
  '.skipped[] | select(.id == "d3") | .reason' "$RECEIPT"
assert_json_field "the feed marks the discussion resolved" true '.threads[0].resolved' "$DIR/threads.json"

before="$(hoststub_count glab)"
run_review threads resolve acme/app "$PR" --json
assert_exit "second resolve" 0
assert_json_field "second resolve writes nothing" 0 '.data.resolved | length'
assert_json_field "second resolve reports the discussion already resolved" already-resolved \
  '.data.skipped[] | select(.id == "d1") | .reason'
assert_eq "second resolve made zero host calls" "$before" "$(hoststub_count glab)"

run_review threads resolve acme/app "$PR" --thread d2 --json
assert_exit "human discussion named with --thread" 1
assert_refusal "human discussion refusal" not-verdict-authored
assert_eq "human refusal made zero host calls" "$before" "$(hoststub_count glab)"

hoststub_assert_all_routed
eval_end
