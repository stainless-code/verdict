#!/usr/bin/env bash
#
# Forgejo has no CLI of its own: fetch shells `curl` with the API token. Every
# curl call is a registered stub argv against a reserved `.invalid` host, so a
# fetch that reached the network could not resolve and could not produce the
# fixture bundle this case asserts.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
# shellcheck source=../lib/hoststub.sh
. "$HERE/../lib/hoststub.sh"

FIX="$EVAL_ROOT/evals/fixtures/forgejo"
PR=7
HEAD="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
TOKEN="eval-token"
CONFIG="header = \"Authorization: token $TOKEN\""
DIR="$EVAL_SANDBOX/.verdict/state/$PR"
PULL="https://forgejo.invalid/api/v1/repos/acme/app/pulls/$PR"

hoststub_init curl
hoststub_prove_dead curl
hoststub_route curl "--config - -sf $PULL" "$FIX/PR.json" 0 "$CONFIG"
hoststub_route curl "--config - -sf $PULL.diff" "$FIX/full.diff" 0 "$CONFIG"
hoststub_route curl "--config - -sf $PULL/files?limit=50&page=1" "$FIX/files.json" 0 "$CONFIG"
hoststub_route curl "--config - -sf $PULL/reviews?limit=50&page=1" "$FIX/reviews.json" 0 "$CONFIG"
hoststub_route curl "--config - -sf $PULL/reviews/301/comments" "$FIX/comments.json" 0 "$CONFIG"

export FORGEJO_HOST="https://forgejo.invalid"
unset FORGEJO_TOKEN
run_review fetch acme/app "$PR" --provider forgejo --json
assert_exit "forgejo fetch without a token refuses" 1
assert_json_field "missing token refuses typed" auth-missing '.error.code'
assert_eq "the auth gate shells no curl" 0 "$(hoststub_count curl)"
assert_eq "the refused fetch writes no bundle file" 0 "$(find "$DIR" -type f 2>/dev/null | wc -l | tr -d ' ')"

export FORGEJO_TOKEN="$TOKEN"
run_review fetch acme/app "$PR" --provider forgejo --json
assert_exit "forgejo fetch" 0
assert_envelope "forgejo fetch envelope"
assert_json_field "fetch names the provider" forgejo '.data.capabilities.provider'
assert_json_field "fetch counts one changed file" 1 '.data.files'
assert_json_field "fetch pins the fixture head" "$HEAD" '.data.head_sha'
assert_json_field "fetch warns about nothing" 0 '.warnings | length'

assert_file_eq "fetch writes the fixture diff" "$FIX/full.diff" "$DIR/full.diff"
assert_file_eq "fetch indexes the fixture hunks" "$FIX/expected-hunks.tsv" "$DIR/hunks.tsv"
assert_json_field "PR.json carries the provider marker" forgejo '.provider' "$DIR/PR.json"
assert_json_field "PR.json carries the PR index" 7 '.number' "$DIR/PR.json"
assert_json_field "PR.json carries the base ref" main '.base_ref' "$DIR/PR.json"
assert_json_field "PR.json carries the head ref" feat/retry '.head_ref' "$DIR/PR.json"
assert_json_field "PR.json carries the head" "$HEAD" '.head_sha' "$DIR/PR.json"
assert_json_field "PR.json carries the author" mate '.author' "$DIR/PR.json"
assert_json_field "PR.json carries the page URL" "https://forgejo.invalid/acme/app/pulls/$PR" '.url' "$DIR/PR.json"
assert_json_field "PR.json carries the changed file" notes.txt '.files[0].path' "$DIR/PR.json"
assert_json_field "PR.json counts the line changes" 1 '.files[0].additions' "$DIR/PR.json"
assert_json_field "threads carry the provider" forgejo '.provider' "$DIR/threads.json"
assert_json_field "threads read the one review comment" 1 '.threads | length' "$DIR/threads.json"
assert_json_field "the review comment keeps its anchor" notes.txt '.threads[0].path' "$DIR/threads.json"
assert_json_field "the review comment keeps its line" 2 '.threads[0].line' "$DIR/threads.json"
assert_json_field "the review comment reads as a review thread" review-comment '.threads[0].kind' "$DIR/threads.json"
assert_json_field "the review comment is open" false '.threads[0].resolved' "$DIR/threads.json"
assert_json_field "the review list is stored verbatim" 1 'length' "$DIR/reviews.json"
assert_json_field "the inline comment is stored verbatim" 1 'length' "$DIR/inline.json"

assert_eq "the case makes zero gh calls" 0 "$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
hoststub_argv_log curl "$EVAL_SANDBOX/curl-calls.txt"
assert_file_eq "every curl call is a registered argv in order" "$FIX/expected-argvs.txt" "$EVAL_SANDBOX/curl-calls.txt"
assert_file_nogrep "no curl argv carries the fixture token" "$EVAL_SANDBOX/curl-calls.txt" "$TOKEN" -F
assert_eq "fetch spends five curl reads" 5 "$(hoststub_count curl)"
hoststub_assert_all_routed

eval_end
