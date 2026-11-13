#!/usr/bin/env bash
#
# GitLab is a shipped fetch+post host: fetch builds the whole MR bundle through
# `glab` reads, and `post --dry-run` renders the same discussions a real post
# would write. Every glab call is a registered stub argv, so the case fails the
# moment the product shells something the stub does not model.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
# shellcheck source=../lib/hoststub.sh
. "$HERE/../lib/hoststub.sh"

FIX="$EVAL_ROOT/evals/fixtures/gitlab"
PR=7
HEAD="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
SIG="e3b29dcf4f68fde96227eebd9983ef8316624d9d"
DIR="$EVAL_SANDBOX/.verdict/state/$PR"

hoststub_init glab
hoststub_prove_dead glab
hoststub_route glab "mr view $PR -R acme/app -F json" "$FIX/MR.json"
hoststub_route glab "api projects/acme%2Fapp --jq .id" "$FIX/project-id.txt"
hoststub_route glab "api projects/4242/merge_requests/$PR/diffs --paginate" "$FIX/diffs.json"
hoststub_route glab "mr diff $PR -R acme/app --raw" "$FIX/full.diff"
hoststub_route glab "api projects/4242/merge_requests/$PR/notes?per_page=100&page=1" "$FIX/notes.json"
hoststub_route glab "api projects/4242/merge_requests/$PR/discussions?per_page=100&page=1" "$FIX/discussions.json"
hoststub_route glab "api projects/acme%2Fapp/merge_requests/$PR" "$FIX/MR.json"

run_review fetch acme/app "$PR" --provider gitlab --json
assert_exit "gitlab fetch" 0
assert_envelope "gitlab fetch envelope"
assert_json_field "fetch names the provider" gitlab '.data.capabilities.provider'
assert_json_field "fetch counts one changed file" 1 '.data.files'
assert_json_field "fetch pins the fixture head" "$HEAD" '.data.head_sha'
assert_json_field "fetch warns about nothing" 0 '.warnings | length'

assert_file_eq "fetch writes the fixture diff" "$FIX/full.diff" "$DIR/full.diff"
assert_file_eq "fetch indexes the fixture hunks" "$FIX/expected-hunks.tsv" "$DIR/hunks.tsv"
assert_json_field "PR.json carries the provider marker" gitlab '.provider' "$DIR/PR.json"
assert_json_field "PR.json carries the MR iid" 7 '.number' "$DIR/PR.json"
assert_json_field "PR.json carries the folded state" open '.state' "$DIR/PR.json"
assert_json_field "PR.json carries the head" "$HEAD" '.head_sha' "$DIR/PR.json"
assert_json_field "PR.json carries the changed file" notes.txt '.files[0].path' "$DIR/PR.json"
assert_json_field "PR.json counts the line changes" 1 '.files[0].additions' "$DIR/PR.json"
assert_json_field "threads carry the provider" gitlab '.provider' "$DIR/threads.json"
assert_json_field "threads read both discussions" 2 '.threads | length' "$DIR/threads.json"
assert_json_field "the MR comment reads as an issue thread" issue-comment '.threads[0].kind' "$DIR/threads.json"
assert_json_field "the MR comment is unresolved" false '.threads[0].resolved' "$DIR/threads.json"
assert_json_field "the DiffNote keeps its anchor" notes.txt '.threads[1].path' "$DIR/threads.json"
assert_json_field "the DiffNote keeps its line" 2 '.threads[1].line' "$DIR/threads.json"
assert_json_field "the DiffNote reads as resolved" true '.threads[1].resolved' "$DIR/threads.json"
assert_json_field "the notes split keeps the DiffNote inline" 1 'length' "$DIR/inline.json"
assert_json_field "the notes split keeps the MR comment in reviews" 1 'length' "$DIR/reviews.json"
assert_eq "fetch spends six glab reads" 6 "$(hoststub_count glab)"

eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ⚠️ One issue to address.\n> Confidence: 4/5 · Safety: not assessed\n> Basis: 0 0 0 0 1 0 0","approved":false,"comments":[{"path":"notes.txt","line":2,"body":"Retry count starts at one; start at zero so the third attempt runs."}]}
EOF
run_review post acme/app "$PR" --dry-run --json
assert_exit "gitlab post --dry-run" 0
assert_envelope "gitlab post envelope"
assert_json_field "the dry run posts nothing" true '.data.dry_run'
assert_json_field "a non-binding review stays a comment" COMMENT '.data.event'
assert_json_field "the dry run covers the fetched head" "$HEAD" '.data.commit_id'
assert_json_field "the dry run keeps the inline anchor" 1 '.data.anchors.kept'
assert_json_field "the dry run renders one discussion" notes.txt:2 '.data.items[0].locator'
assert_json_field "the discussion keeps its finding sig" "$SIG" '.data.items[0].sig'
assert_json_field "the dry run warns about nothing" 0 '.warnings | length'
assert_json_field "the rendered discussion lands on the changed line" 2 '.[0].line' "$DIR/findings.dryrun.json"
assert_json_field "the rendered discussion takes the right side" RIGHT '.[0].side' "$DIR/findings.dryrun.json"
assert_json_field "the rendered discussion is anchored" true '.[0].anchored' "$DIR/findings.dryrun.json"
assert_json_field "the rendered discussion keeps the review body" \
  "Retry count starts at one; start at zero so the third attempt runs." '.[0].body' "$DIR/findings.dryrun.json"
if [ -e "$DIR/post.json" ]; then
  bad "the dry run wrote post.json"
else
  good "the dry run posts nothing to the host"
fi

assert_eq "the case makes zero gh calls" 0 "$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
hoststub_argv_log glab "$EVAL_SANDBOX/glab-calls.txt"
assert_file_eq "every glab call is a registered argv in order" "$FIX/expected-argvs.txt" "$EVAL_SANDBOX/glab-calls.txt"
assert_eq "the dry run adds one live-SHA read" 7 "$(hoststub_count glab)"
hoststub_assert_all_routed

# Exercise the real CLI against a host that accepts one discussion, then fails.
export RECOVERY_STATE="$EVAL_SANDBOX/recovery"
mkdir -p "$RECOVERY_STATE"
mv "$EVAL_STUBBIN/glab" "$EVAL_STUBBIN/glab-read"
cat >"$EVAL_STUBBIN/glab" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
case "$*" in
  'api projects/4242/merge_requests/7/notes?per_page=100&page=1')
    printf 'stub-glab %s\n' "$*" >>"$HOSTSTUB_LOG"
    jq -s 'map(. + {type:"DiffNote"})' "$RECOVERY_STATE/accepted.jsonl"
    ;;
  'api projects/acme%2Fapp/merge_requests/7/discussions --method POST --header Content-Type: application/json --input -')
    printf 'stub-glab %s\n' "$*" >>"$HOSTSTUB_LOG"
    n=0
    if [ -f "$RECOVERY_STATE/count" ]; then n=$(cat "$RECOVERY_STATE/count"); fi
    n=$((n+1))
    echo "$n" >"$RECOVERY_STATE/count"
    if [ "$n" -eq 2 ]; then
      cat >/dev/null
      echo 'injected partial publish failure' >&2
      exit 1
    fi
    jq -c . >>"$RECOVERY_STATE/accepted.jsonl"
    echo '{"id":99}'
    ;;
  'api projects/acme%2Fapp/merge_requests/7/notes --method POST --header Content-Type: application/json --input -')
    printf 'stub-glab %s\n' "$*" >>"$HOSTSTUB_LOG"
    cat >"$RECOVERY_STATE/summary.json"
    echo '{"id":100,"created_at":"2026-01-01T00:00:00Z"}'
    ;;
  *) exec "$EVAL_STUBBIN/glab-read" "$@" ;;
esac
STUB
chmod +x "$EVAL_STUBBIN/glab"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ⚠️ Two issues.","approved":false,"comments":[{"path":"notes.txt","line":2,"body":"First recovery finding needs a fix."},{"path":"notes.txt","line":2,"body":"Second recovery finding needs a fix."}]}
EOF
# Existing fetched findings are unrelated to this recovery scenario.
printf '[]\n' >"$DIR/inline.json"
printf '[]\n' >"$DIR/reviews.json"
printf '[]\n' >"$DIR/findings.json"
run_review post acme/app "$PR" --json
assert_exit "partial host failure" 1
assert_json_field "partial failure is typed" post-failed '.error.code'
run_review fetch acme/app "$PR" --provider gitlab --json
assert_exit "re-fetch accepted remote discussion" 0
assert_json_field "one comment landed before failure" 1 'length' "$DIR/inline.json"
run_review post acme/app "$PR" --json
assert_exit "partial rerun" 0
jq -s . "$RECOVERY_STATE/accepted.jsonl" >"$RECOVERY_STATE/accepted.json"
assert_json_field "rerun publishes exactly the missing comment" 2 'length' "$RECOVERY_STATE/accepted.json"
assert_json_field "each rendered body lands once" 2 '[.[].body] | unique | length' "$RECOVERY_STATE/accepted.json"
assert_eq "one failure plus two successful discussions" 3 "$(cat "$RECOVERY_STATE/count")"
hoststub_assert_all_routed

eval_end
