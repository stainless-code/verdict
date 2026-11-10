#!/usr/bin/env bash
#
# Ready advises on GitLab merge requests through `glab`: a draft MR with a
# conflict and a failed pipeline reports all three blockers, and a clean MR
# reports ready. Every glab call is a registered stub argv, so the case
# fails the moment the product shells something the stub does not model.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
# shellcheck source=../lib/hoststub.sh
. "$HERE/../lib/hoststub.sh"

FIX="$EVAL_ROOT/evals/fixtures/gitlab"

hoststub_init glab
hoststub_prove_dead glab
# PR 7: blocked (draft + conflict + failed pipeline).
hoststub_route glab "mr view 7 -R acme/app -F json" "$FIX/MR.json"
hoststub_route glab "api projects/acme%2Fapp --jq .id" "$FIX/project-id.txt"
hoststub_route glab "api projects/4242/merge_requests/7/diffs --paginate" "$FIX/diffs.json"
hoststub_route glab "mr diff 7 -R acme/app --raw" "$FIX/full.diff"
hoststub_route glab "api projects/4242/merge_requests/7/notes?per_page=100&page=1" "$FIX/notes.json"
hoststub_route glab "api projects/4242/merge_requests/7/discussions?per_page=100&page=1" "$FIX/discussions.json"
hoststub_route glab "api projects/acme%2Fapp/merge_requests/7" "$FIX/MR-ready-blocked.json"
hoststub_route glab "api projects/acme%2Fapp/merge_requests/7/pipelines?per_page=100" "$FIX/pipelines-blocked.json"
hoststub_route glab "api projects/acme%2Fapp/merge_requests/7/discussions?per_page=100 --paginate" "$FIX/discussions-resolved.json"
# PR 8: clean.
hoststub_route glab "mr view 8 -R acme/app -F json" "$FIX/MR8.json"
hoststub_route glab "api projects/4242/merge_requests/8/diffs --paginate" "$FIX/diffs.json"
hoststub_route glab "mr diff 8 -R acme/app --raw" "$FIX/full.diff"
hoststub_route glab "api projects/4242/merge_requests/8/notes?per_page=100&page=1" "$FIX/notes.json"
hoststub_route glab "api projects/4242/merge_requests/8/discussions?per_page=100&page=1" "$FIX/discussions.json"
hoststub_route glab "api projects/acme%2Fapp/merge_requests/8" "$FIX/MR-ready-clean.json"
hoststub_route glab "api projects/acme%2Fapp/merge_requests/8/pipelines?per_page=100" "$FIX/pipelines-clean.json"
hoststub_route glab "api projects/acme%2Fapp/merge_requests/8/discussions?per_page=100 --paginate" "$FIX/discussions-resolved.json"

run_review fetch acme/app 7 --provider gitlab --json
assert_exit "gitlab fetch 7" 0
printf '[]' > "$EVAL_SANDBOX/.verdict/state/7/findings.json"

run_review ready acme/app 7 --json
assert_exit "blocked ready stays advisory" 0
assert_envelope "blocked ready envelope"
assert_json_field "the blocked MR is not ready" false '.data.ready'
assert_json_field "the blocked MR names three blockers" 3 '.data.blockers | length'
assert_json_field "the first blocker is the failing pipeline" ci-failing '.data.blockers[0].kind'
assert_json_field "the pipeline detail names the failed run" "1 failing check(s): #13 failed" '.data.blockers[0].detail'
assert_json_field "the second blocker is the draft" draft '.data.blockers[1].kind'
assert_json_field "the third blocker is the conflict" conflict '.data.blockers[2].kind'
assert_json_field "the conflict detail carries both merge fields" "mergeable=CONFLICTING mergeStateStatus=CONFLICT" '.data.blockers[2].detail'

run_review fetch acme/app 8 --provider gitlab --json
assert_exit "gitlab fetch 8" 0
printf '[]' > "$EVAL_SANDBOX/.verdict/state/8/findings.json"

run_review ready acme/app 8 --json
assert_exit "clean ready stays advisory" 0
assert_envelope "clean ready envelope"
assert_json_field "the clean MR is ready" true '.data.ready'
assert_json_field "the clean MR names no blockers" 0 '.data.blockers | length'

assert_eq "the case makes zero gh calls" 0 "$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
hoststub_assert_all_routed

eval_end
