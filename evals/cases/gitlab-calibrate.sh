#!/usr/bin/env bash
#
# Calibrate joins live GitLab discussion state: a resolved thread on a file
# changed since the review head reads fixed, and an open thread reads open.
# Every glab call is a registered stub argv, so the case fails the moment
# the product shells something the stub does not model.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
# shellcheck source=../lib/hoststub.sh
. "$HERE/../lib/hoststub.sh"

FIX="$EVAL_ROOT/evals/fixtures/gitlab"
HEAD_A="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
HEAD_C="cccccccccccccccccccccccccccccccccccccccc"

hoststub_init glab
hoststub_prove_dead glab
hoststub_route glab "mr view 7 -R acme/app -F json" "$FIX/MR.json"
hoststub_route glab "api projects/acme%2Fapp --jq .id" "$FIX/project-id.txt"
hoststub_route glab "api projects/4242/merge_requests/7/diffs --paginate" "$FIX/diffs.json"
hoststub_route glab "mr diff 7 -R acme/app --raw" "$FIX/full.diff"
hoststub_route glab "api projects/4242/merge_requests/7/notes?per_page=100&page=1" "$FIX/notes.json"
hoststub_route glab "api projects/4242/merge_requests/7/discussions?per_page=100&page=1" "$FIX/discussions.json"
hoststub_route glab "api projects/acme%2Fapp/merge_requests/7" "$FIX/MR-calibrate.json"
hoststub_route glab "api projects/acme%2Fapp/merge_requests/7/discussions?per_page=100 --paginate" "$FIX/discussions-calibrate.json"
hoststub_route glab "api projects/acme%2Fapp/repository/compare?from=$HEAD_A&to=$HEAD_C" "$FIX/compare.json"

run_review fetch acme/app 7 --provider gitlab --json
assert_exit "gitlab fetch" 0
assert_json_field "the fetched bundle carries the gitlab marker" gitlab '.provider' "$EVAL_SANDBOX/.verdict/state/7/PR.json"

eval_write_findings 7 <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Fixed body here with enough substance.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"critical","sig":"c111111111111111111111111111111111111111","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Open body here with enough substance.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","sig":"b222222222222222222222222222222222222222","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
jq -n --arg h "$HEAD_A" \
  '{schema_version:"1",review:{id:99},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null}' \
  > "$EVAL_SANDBOX/.verdict/state/7/post.json"

run_review calibrate acme/app 7 --json
assert_exit "gitlab calibrate" 0
assert_envelope "gitlab calibrate envelope"
assert_json_field "the resolved thread on the changed file reads fixed" 1 '.data.precision.fixed'
assert_json_field "the open thread reads open" 1 '.data.precision.open'
assert_json_field "both findings are judged" 2 '.data.precision.judged'
assert_file_grep "the human line pins the join" "$EVAL_ERR" "precision: fixed=1 resolved-unfixed=0 open=1 unknown=0 (judged 2)"

assert_eq "the case makes zero gh calls" 0 "$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
hoststub_assert_all_routed

eval_end
