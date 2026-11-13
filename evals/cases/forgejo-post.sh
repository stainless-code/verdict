#!/usr/bin/env bash
#
# Forgejo has no CLI: fetch builds the bundle with `curl` reads and post submits
# one atomic review with a `curl` write. Every call is a registered stub argv
# against a reserved `.invalid` host, so a call that escaped the stub could not
# resolve. The write is read with `-i`, so the fixtures carry raw status lines
# and bodies and the case grades the same split the writer parses.
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
WANT_WRITE="--config FILE -sS -i -H Content-Type: application/json -X POST --data-binary @- $PULL/reviews"

fetch_routes() {
  hoststub_init curl
  hoststub_route curl "--config - -sf $PULL" "$FIX/PR.json" 0 "$CONFIG"
  hoststub_route curl "--config - -sf $PULL.diff" "$FIX/full.diff" 0 "$CONFIG"
  hoststub_route curl "--config - -sf $PULL/files?limit=50&page=1" "$FIX/files.json" 0 "$CONFIG"
  hoststub_route curl "--config - -sf $PULL/reviews?limit=50&page=1" "$FIX/reviews.json" 0 "$CONFIG"
  hoststub_route curl "--config - -sf $PULL/reviews/301/comments" "$FIX/comments.json" 0 "$CONFIG"
}

# post_routes swaps the one write answer, so each post leg grades the status
# and body its own fixture carries while every read stays on the fetch fixtures.
post_routes() {
  local write="$1"
  hoststub_init curl
  hoststub_route curl "--config - -sf $PULL" "$FIX/PR.json" 0 "$CONFIG"
  hoststub_route curl "--config - -sf $PULL/reviews?limit=50&page=1" "$FIX/reviews.json" 0 "$CONFIG"
  hoststub_route curl "--config - -sf $PULL/reviews/301/comments" "$FIX/comments.json" 0 "$CONFIG"
  hoststub_route curl "--config - -sf https://forgejo.invalid/api/v1/user" "$FIX/user.json" 0 "$CONFIG"
  hoststub_route curl "$WANT_WRITE" "$write" 0 "$CONFIG"
}

write_calls() { grep -c " $PULL/reviews\$" "$HOSTSTUB_LOG" || true; }

fetch_routes
hoststub_prove_dead curl
fetch_routes
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
assert_json_field "fetch pins the fixture head" "$HEAD" '.data.head_sha'
assert_json_field "fetch warns about nothing" 0 '.warnings | length'

assert_file_eq "fetch writes the fixture diff" "$FIX/full.diff" "$DIR/full.diff"
assert_file_eq "fetch indexes the fixture hunks" "$FIX/expected-hunks.tsv" "$DIR/hunks.tsv"
assert_json_field "PR.json carries the provider marker" forgejo '.provider' "$DIR/PR.json"
assert_json_field "PR.json carries the fixture author" mate '.author' "$DIR/PR.json"
assert_json_field "threads carry the provider" forgejo '.provider' "$DIR/threads.json"
assert_json_field "threads read the one review comment" 1 '.threads | length' "$DIR/threads.json"
assert_json_field "the review comment keeps its line" 2 '.threads[0].line' "$DIR/threads.json"
assert_json_field "the review comment is open" false '.threads[0].resolved' "$DIR/threads.json"

assert_eq "the fetch case makes zero gh calls" 0 "$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
hoststub_argv_log curl "$EVAL_SANDBOX/curl-calls.txt"
assert_file_eq "every fetch call is a registered argv in order" "$FIX/expected-argvs.txt" "$EVAL_SANDBOX/curl-calls.txt"
assert_file_nogrep "no fetch curl argv carries the fixture token" "$EVAL_SANDBOX/curl-calls.txt" "$TOKEN" -F
assert_eq "fetch spends five curl reads" 5 "$(hoststub_count curl)"

eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — one note below.\n> Confidence: 4/5 · Safety: not assessed\n> Basis: 0 0 0 1 0 0 0","approved":false,"comments":[{"path":"notes.txt","line":2,"body":"Name the retry boundary so the third attempt is visible here.","rigor":"pointed","severity":"informational"}]}
EOF

post_routes "$FIX/review-created.http"
unset FORGEJO_TOKEN
run_review post acme/app "$PR" --dry-run --json
assert_exit "post without a token refuses" 1
assert_json_field "the post auth gate refuses typed" auth-missing '.error.code'
assert_eq "the post auth gate shells no curl" 0 "$(hoststub_count curl)"
if [ ! -e "$DIR/post.json" ] && [ -z "$(ls "$DIR"/.posted_* 2>/dev/null || true)" ]; then
  good "the refused post arms nothing"
else
  bad "the refused post wrote a receipt or marker"
fi
export FORGEJO_TOKEN="$TOKEN"

post_routes "$FIX/review-created.http"
run_review post acme/app "$PR" --dry-run --json
assert_exit "forgejo post --dry-run" 0
assert_envelope "forgejo post dry-run envelope"
assert_json_field "the dry run posts nothing" true '.data.dry_run'
assert_json_field "a non-binding review stays a comment" COMMENT '.data.event'
assert_json_field "the dry run covers the fetched head" "$HEAD" '.data.commit_id'
assert_json_field "the dry run keeps the inline anchor" 1 '.data.anchors.kept'
assert_json_field "the dry run renders the anchor locator" notes.txt:2 '.data.items[0].locator'
assert_json_field "the dry run warns about nothing" 0 '.warnings | length'
if [ ! -e "$DIR/post.json" ]; then
  good "the dry run writes no post.json"
else
  bad "the dry run wrote post.json"
fi
assert_eq "the dry run adds one live-SHA read" 1 "$(hoststub_count curl)"

post_routes "$FIX/review-created.http"
run_review post acme/app "$PR" --json
assert_exit "forgejo post" 0
assert_envelope "forgejo post envelope"
assert_json_field "the receipt pins the reviewed head" "$HEAD" '.head' "$DIR/post.json"
assert_json_field "the receipt records the host's review state" COMMENT '.review.state' "$DIR/post.json"
assert_json_field "the receipt records the host's review id" 901 '.review.id' "$DIR/post.json"
assert_eq "the post arms one payload marker" 1 "$(ls "$DIR"/.posted_* 2>/dev/null | wc -l | tr -d ' ')"
assert_eq "the post makes one write" 1 "$(write_calls)"
hoststub_argv_log curl "$EVAL_SANDBOX/post-calls.txt"
assert_file_eq "every post call is a registered argv in order" "$FIX/expected-argvs-post.txt" "$EVAL_SANDBOX/post-calls.txt"
assert_file_nogrep "no post curl argv carries the fixture token" "$EVAL_SANDBOX/post-calls.txt" "$TOKEN" -F

run_review post acme/app "$PR" --json
assert_exit "empty remote permits publication despite local receipt" 0
assert_json_field "second post still records accepted review" 901 '.data.review.id'
assert_eq "second post makes its reads and write" 8 "$(hoststub_count curl)"
assert_eq "second post adds one write" 2 "$(write_calls)"

eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — approval with one note below.","approved":false,"comments":[{"path":"notes.txt","line":2,"body":"Name the retry boundary so the third attempt is visible here.","rigor":"pointed","severity":"informational"}]}
EOF
post_routes "$FIX/review-approve.http"
run_review post acme/app "$PR" --approve true --bind --force --json
assert_exit "a bound forgejo approval posts" 0
assert_json_field "the approval reaches the host" APPROVED '.review.state' "$DIR/post.json"
assert_json_field "the approval receipt is the created review" 902 '.review.id' "$DIR/post.json"
assert_eq "the approval makes one write" 1 "$(write_calls)"
assert_eq "the approval reads the user, the reviews, the comments, and the user again" 7 "$(hoststub_count curl)"

cp "$DIR/post.json" "$EVAL_SANDBOX/post-before-pending.json"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — this answer must not become a receipt.","approved":false,"comments":[]}
EOF
post_routes "$FIX/review-pending.http"
run_review post acme/app "$PR" --force --json
assert_exit "a PENDING answer refuses" 1
assert_json_field "a PENDING answer refuses as post-failed" post-failed '.error.code'
assert_file_grep "the refusal names the state mismatch" "$EVAL_OUT" "answered state PENDING, not the requested COMMENT"
assert_eq "the PENDING answer writes no second review" 1 "$(write_calls)"
if cmp -s "$EVAL_SANDBOX/post-before-pending.json" "$DIR/post.json"; then
  good "the PENDING answer leaves the prior receipt untouched"
else
  bad "the PENDING answer rewrote the prior receipt"
fi

eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — the host's 422 must be quoted.","approved":false,"comments":[]}
EOF
post_routes "$FIX/review-422.http"
run_review post acme/app "$PR" --force --json
assert_exit "a 422 refuses" 1
assert_json_field "a 422 refuses as post-failed" post-failed '.error.code'
assert_file_grep "the refusal quotes the host's message" "$EVAL_OUT" "approve your own pull is not allowed"
assert_eq "a 422 is never replayed" 1 "$(write_calls)"

assert_eq "the case makes zero gh calls" 0 "$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
hoststub_assert_all_routed

eval_end
