#!/usr/bin/env bash
#
# `pr-wait` on a GitLab merge request through a network-dead `glab` stub: the
# wait shells the same MR read `ready` uses (never a bundle, never a local
# write), a merge between two polls ends it, and review-changed fires when the
# approval and the discussion's resolution change between two polls.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
# shellcheck source=../lib/hoststub.sh
. "$HERE/../lib/hoststub.sh"

FIX="$EVAL_ROOT/evals/fixtures/gitlab-pr-wait"
export GL_FIX="$FIX" GL_COUNT="$EVAL_SANDBOX/mr-reads" GL_MODE=merge
printf '0' >"$GL_COUNT"
hoststub_init glab
hoststub_prove_dead glab
# The MR read counts the polls; the later reads answer by that count, so a
# change lands between the first and second observation.
cat >"$EVAL_STUBBIN/glab" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
argv="$*"
printf 'stub-glab %s\n' "$argv" >>"${HOSTSTUB_LOG:?}"
mr='api projects/acme%2Fapp/merge_requests/7'
n="$(cat "$GL_COUNT")"
phase=0
if [ "$n" -ge 2 ]; then phase=1; fi
case "$argv" in
  "$mr")
    n=$((n+1))
    printf '%s' "$n" >"$GL_COUNT"
    if [ "$GL_MODE" = merge ] && [ "$n" -ge 2 ]; then cat "$GL_FIX/mr-merged.json"; else cat "$GL_FIX/mr-open.json"; fi
    exit 0;;
  "$mr/approvals") cat "$GL_FIX/approvals-$phase.json"; exit 0;;
  "$mr/discussions?per_page=100 --paginate") cat "$GL_FIX/discussions-$phase.json"; exit 0;;
esac
printf 'stub-glab UNRECOGNISED %s\n' "$argv" >>"${HOSTSTUB_LOG:?}"
exit 64
STUB
chmod +x "$EVAL_STUBBIN/glab"

run_review pr-wait acme/app 7 --provider gitlab --until merged --timeout 5s --json
assert_exit "gitlab merge wait succeeds after two polls" 0
assert_envelope "gitlab merge wait"
assert_json_field "typed condition result" condition-met '.data.outcome'
assert_json_field "merged snapshot" MERGED '.data.snapshot.state'
assert_json_field "two observations" 2 '.data.observations'
assert_eq "one merge-request read per poll" 2 "$(cat "$GL_COUNT")"
assert_eq "the wait shelled only the MR read" 2 "$(hoststub_count glab)"
assert_file_grep "the real MR argv" "$HOSTSTUB_LOG" '^stub-glab api projects/acme%2Fapp/merge_requests/7$'
if [ -e "$EVAL_SANDBOX/.verdict" ]; then bad "gitlab pr-wait wrote a bundle"; else good "gitlab pr-wait writes no bundle"; fi
assert_eq "no staged probe stderr file" 0 "$(find "$EVAL_SANDBOX" -name '.ready.err' | wc -l | tr -d ' ')"

printf '0' >"$GL_COUNT"
: >"$HOSTSTUB_LOG"
export GL_MODE=review
run_review pr-wait acme/app 7 --provider gitlab --until review-changed --timeout 5s --json
assert_exit "review-changed fires after the approval and resolution" 0
assert_envelope "gitlab review-changed"
assert_json_field "typed condition result" condition-met '.data.outcome'
assert_json_field "the baseline and the change" 2 '.data.observations'
assert_json_field "the approver is in the last snapshot" '["ada"]' '.data.snapshot.reviews'
assert_json_field "the thread reads resolved" '[{"id":"d1","notes":1,"resolved":true}]' '.data.snapshot.threads'
assert_file_grep "approvals argv" "$HOSTSTUB_LOG" '^stub-glab api projects/acme%2Fapp/merge_requests/7/approvals$'
assert_file_grep "discussions argv" "$HOSTSTUB_LOG" \
  '^stub-glab api projects/acme%2Fapp/merge_requests/7/discussions?per_page=100 --paginate$'

hoststub_assert_all_routed
eval_end
