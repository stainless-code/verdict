#!/usr/bin/env bash
#
# Bitbucket has no CLI of its own: fetch shells `curl` with the token (or the
# user/password pair) read from a config document, never from the argv. Every
# curl call is a registered stub argv against a reserved host, and the stub
# refuses a call whose config does not carry the exact credential, so a fetch
# that lost its auth cannot pass on a fixture bundle.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
# shellcheck source=../lib/hoststub.sh
. "$HERE/../lib/hoststub.sh"

FIX="$EVAL_ROOT/evals/fixtures/bitbucket"
PR=7
HEAD="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
TOKEN="eval-token"
USER="eval-user"
PASS="eval-pass"
CONFIG="header = \"Authorization: Bearer $TOKEN\""
USER_CONFIG="user = \"$USER:$PASS\""
DIR="$EVAL_SANDBOX/.verdict/state/$PR"
BASE="https://api.bitbucket.org/2.0/repos/acme/app/pullrequests/$PR"

routes() {
  local config="$1"
  hoststub_init curl
  hoststub_route curl "--config - -sf $BASE" "$FIX/PR.json" 0 "$config"
  hoststub_route curl "--config - -sfL $BASE/diff" "$FIX/full.diff" 0 "$config"
  hoststub_route curl "--config - -sfL $BASE/diffstat?pagelen=100" "$FIX/diffstat.json" 0 "$config"
  hoststub_route curl "--config - -sfL $BASE/comments?pagelen=100" "$FIX/comments.json" 0 "$config"
}

routes "$CONFIG"
hoststub_prove_dead curl
routes "$CONFIG"
unset BITBUCKET_TOKEN BITBUCKET_USERNAME BITBUCKET_PASSWORD
run_review fetch acme/app "$PR" --provider bitbucket --json
assert_exit "bitbucket fetch without a credential refuses" 1
assert_json_field "missing credential refuses typed" auth-missing '.error.code'
assert_eq "the auth gate shells no curl" 0 "$(hoststub_count curl)"

export BITBUCKET_TOKEN="$TOKEN"
run_review fetch acme/app "$PR" --provider bitbucket --json
assert_exit "bitbucket fetch" 0
assert_envelope "bitbucket fetch envelope"
assert_json_field "fetch names the provider" bitbucket '.data.capabilities.provider'
assert_json_field "fetch counts one changed file" 1 '.data.files'
assert_json_field "fetch pins the fixture head" "$HEAD" '.data.head_sha'
assert_json_field "fetch warns about nothing" 0 '.warnings | length'

assert_file_eq "fetch writes the fixture diff" "$FIX/full.diff" "$DIR/full.diff"
assert_file_eq "fetch indexes the fixture hunks" "$FIX/expected-hunks.tsv" "$DIR/hunks.tsv"
assert_json_field "PR.json carries the provider marker" bitbucket '.provider' "$DIR/PR.json"
assert_json_field "PR.json carries the PR index" 7 '.number' "$DIR/PR.json"
assert_json_field "PR.json carries the base ref" main '.base_ref' "$DIR/PR.json"
assert_json_field "PR.json carries the head ref" feat/retry '.head_ref' "$DIR/PR.json"
assert_json_field "PR.json carries the head" "$HEAD" '.head_sha' "$DIR/PR.json"
assert_json_field "PR.json carries the author" mate '.author' "$DIR/PR.json"
assert_json_field "PR.json carries the page URL" "https://bitbucket.org/acme/app/pull-requests/7" '.url' "$DIR/PR.json"
assert_json_field "PR.json carries the changed file" notes.txt '.files[0].path' "$DIR/PR.json"
assert_json_field "PR.json counts the line changes" 1 '.files[0].additions' "$DIR/PR.json"
assert_json_field "threads carry the provider" bitbucket '.provider' "$DIR/threads.json"
assert_json_field "threads read the one review comment" 1 '.threads | length' "$DIR/threads.json"
assert_json_field "the review comment keeps its anchor" notes.txt '.threads[0].path' "$DIR/threads.json"
assert_json_field "the review comment keeps its line" 2 '.threads[0].line' "$DIR/threads.json"
assert_json_field "the review comment reads as a review thread" review-comment '.threads[0].kind' "$DIR/threads.json"
assert_json_field "the review comment is open" false '.threads[0].resolved' "$DIR/threads.json"
assert_json_field "the inline comment is stored verbatim" 1 'length' "$DIR/inline.json"

assert_eq "the token leg makes zero gh calls" 0 "$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
hoststub_argv_log curl "$EVAL_SANDBOX/calls-token.txt"
assert_file_eq "every token curl call is a registered argv in order" "$FIX/expected-argvs.txt" "$EVAL_SANDBOX/calls-token.txt"
assert_file_nogrep "no token-leg curl argv carries the fixture token" "$EVAL_SANDBOX/calls-token.txt" "$TOKEN" -F
assert_eq "fetch spends four curl reads" 4 "$(hoststub_count curl)"
hoststub_assert_all_routed

rm -rf "$DIR"
routes "$USER_CONFIG"
unset BITBUCKET_TOKEN
export BITBUCKET_USERNAME="$USER" BITBUCKET_PASSWORD="$PASS"
run_review fetch acme/app "$PR" --provider bitbucket --json
assert_exit "bitbucket fetch with user/password" 0
assert_json_field "the basic leg pins the fixture head" "$HEAD" '.data.head_sha'
hoststub_argv_log curl "$EVAL_SANDBOX/calls-basic.txt"
assert_file_eq "every basic curl call is a registered argv in order" "$FIX/expected-argvs.txt" "$EVAL_SANDBOX/calls-basic.txt"
assert_file_nogrep "no basic-leg curl argv carries the fixture password" "$EVAL_SANDBOX/calls-basic.txt" "$PASS" -F
assert_eq "the basic leg spends four curl reads" 4 "$(hoststub_count curl)"
hoststub_assert_all_routed

eval_end
