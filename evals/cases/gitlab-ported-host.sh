#!/usr/bin/env bash
#
# A self-hosted GitLab on a non-default port cannot be pinned (glab's
# --hostname takes no port), so verdict runs its glab calls unpinned and lets
# glab's default host answer — but only when the environment already makes that
# default exactly the target. This grades the read side (fetch of a URL) and the
# write side (a real post) on a network-dead stub: with GITLAB_HOST naming the
# host and port, every call is the unpinned argv; with GITLAB_HOST on another
# port, or GITLAB_API_HOST or GITLAB_REPO redirecting glab, both sides refuse
# `host-mismatch` before a single glab call.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
# shellcheck source=../lib/hoststub.sh
. "$HERE/../lib/hoststub.sh"

FIX="$EVAL_ROOT/evals/fixtures/gitlab"
PR=7
URL="https://gitlab.example:8443/acme/app/-/merge_requests/$PR"
DIR="$EVAL_SANDBOX/.verdict/state/$PR"
PORTED="$EVAL_SANDBOX/ported"
mkdir -p "$PORTED"
# The same merge request, served from the ported host.
sed 's#https://gitlab.example/#https://gitlab.example:8443/#' "$FIX/MR.json" >"$PORTED/MR.json"

hoststub_init glab
hoststub_prove_dead glab
hoststub_route glab "mr view $PR -R acme/app -F json" "$PORTED/MR.json"
hoststub_route glab "api projects/acme%2Fapp --jq .id" "$FIX/project-id.txt"
hoststub_route glab "api projects/4242/merge_requests/$PR/diffs --paginate" "$FIX/diffs.json"
hoststub_route glab "mr diff $PR -R acme/app --raw" "$FIX/full.diff"
hoststub_route glab "api projects/4242/merge_requests/$PR/notes?per_page=100&page=1" "$FIX/notes.json"
hoststub_route glab "api projects/4242/merge_requests/$PR/discussions?per_page=100&page=1" "$FIX/discussions.json"

# --- read side, mismatching: the environment does not make glab's default the target.
expect_read_refused() { # <label> <env assignment...>: fetch refuses host-mismatch with no glab call
  local label="$1"
  shift
  : >"$HOSTSTUB_LOG"
  run_review_env "$@" -- fetch "$URL" --json
  assert_exit "$label" 1
  assert_json_field "$label is typed" host-mismatch '.error.code'
  assert_eq "$label makes no glab call" 0 "$(hoststub_count glab)"
}
run_review_env() { # run_review with extra environment assignments before `--`
  local envs=()
  while [ "$1" != "--" ]; do
    envs+=("$1")
    shift
  done
  shift
  set +e
  (
    cd "$EVAL_SANDBOX" || exit 99
    env "${envs[@]}" PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" "$@" >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  # shellcheck disable=SC2034 # read by assert_exit
  EVAL_STATUS=$?
  set -e
}
expect_read_refused "GITLAB_API_HOST on another port" GITLAB_HOST=https://gitlab.example:8443 GITLAB_API_HOST=gitlab.example:9443
expect_read_refused "GITLAB_API_HOST as a URL" GITLAB_HOST=https://gitlab.example:8443 GITLAB_API_HOST=https://gitlab.example:8443
expect_read_refused "GITLAB_REPO set" GITLAB_HOST=https://gitlab.example:8443 GITLAB_REPO=acme/app

# --- read side, matching.
export GITLAB_HOST="https://gitlab.example:8443"
: >"$HOSTSTUB_LOG"
run_review fetch "$URL" --json
assert_exit "ported fetch" 0
assert_envelope "ported fetch envelope"
assert_json_field "ported fetch names the provider" gitlab '.data.capabilities.provider'
assert_json_field "PR.json records the ported host" "$URL" '.url' "$DIR/PR.json"
hoststub_argv_log glab "$EVAL_SANDBOX/glab-fetch.txt"
assert_file_nogrep "no fetch call names a host" "$EVAL_SANDBOX/glab-fetch.txt" '[-][-]hostname'
assert_file_nogrep "no fetch call names the ported host" "$EVAL_SANDBOX/glab-fetch.txt" 'gitlab.example'
assert_eq "fetch spends six unpinned glab reads" 6 "$(hoststub_count glab)"

# --- write side. A real post accepts every write the stub is shown.
mv "$EVAL_STUBBIN/glab" "$EVAL_STUBBIN/glab-read"
cat >"$EVAL_STUBBIN/glab" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
case "$*" in
  'api projects/acme%2Fapp/merge_requests/7/discussions --method POST --header Content-Type: application/json --input -' | \
    'api projects/acme%2Fapp/merge_requests/7/notes --method POST --header Content-Type: application/json --input -')
    printf 'stub-glab %s\n' "$*" >>"$HOSTSTUB_LOG"
    cat >/dev/null
    echo '{"id":99,"created_at":"2026-01-01T00:00:00Z"}'
    ;;
  'api projects/acme%2Fapp/merge_requests/7/discussions?per_page=100 --paginate')
    printf 'stub-glab %s\n' "$*" >>"$HOSTSTUB_LOG"
    echo '[]'
    ;;
  'api projects/acme%2Fapp/merge_requests/7')
    printf 'stub-glab %s\n' "$*" >>"$HOSTSTUB_LOG"
    cat "$PORTED_MR"
    ;;
  *) exec "$EVAL_STUBBIN/glab-read" "$@" ;;
esac
STUB
chmod +x "$EVAL_STUBBIN/glab"
export PORTED_MR="$PORTED/MR.json"
eval_write_review "$PR" <<'JSON'
{"schema_version":"1","body":"> ⚠️ One issue to address.","approved":false,"comments":[{"path":"notes.txt","line":2,"body":"Retry count starts at one; start at zero so the third attempt runs."}]}
JSON
printf '[]\n' >"$DIR/inline.json"
printf '[]\n' >"$DIR/reviews.json"
printf '[]\n' >"$DIR/findings.json"

# A GITLAB_HOST on another port refuses the write before any glab call.
: >"$HOSTSTUB_LOG"
run_review_env GITLAB_HOST=https://gitlab.example:9443 -- post acme/app "$PR" --json
assert_exit "post with GITLAB_HOST on another port" 1
assert_json_field "post refusal is typed" host-mismatch '.error.code'
assert_eq "post with a mismatched GITLAB_HOST makes no glab call" 0 "$(hoststub_count glab)"
run_review_env GITLAB_HOST= -- post acme/app "$PR" --json
assert_exit "post with GITLAB_HOST unset" 1
assert_json_field "post unset refusal is typed" host-mismatch '.error.code'
assert_eq "post with GITLAB_HOST unset makes no glab call" 0 "$(hoststub_count glab)"

# With GITLAB_HOST naming the host and port, the same post lands, unpinned.
: >"$HOSTSTUB_LOG"
run_review post acme/app "$PR" --json
assert_exit "ported post" 0
assert_json_field "ported post is not a dry run" false '.data.dry_run'
hoststub_argv_log glab "$EVAL_SANDBOX/glab-post.txt"
assert_file_grep "post wrote a discussion" "$EVAL_SANDBOX/glab-post.txt" 'merge_requests/7/discussions --method POST'
assert_file_nogrep "no post call names a host" "$EVAL_SANDBOX/glab-post.txt" '[-][-]hostname'
hoststub_assert_all_routed
eval_end
