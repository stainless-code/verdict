#!/usr/bin/env bash
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
# shellcheck source=../lib/hoststub.sh
. "$HERE/../lib/hoststub.sh"

FIX="$EVAL_ROOT/evals/fixtures/pr-wait"
export WAIT_FIX="$FIX" WAIT_COUNT="$EVAL_SANDBOX/poll-count"
printf '0' >"$WAIT_COUNT"
hoststub_init gh
hoststub_prove_dead gh
cat >"$EVAL_STUBBIN/gh" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
argv="$*"
printf 'stub-gh %s\n' "$argv" >>"${HOSTSTUB_LOG:?}"
case "$argv" in
  'api repos/acme/app/pulls/42 --include'|'api repos/acme/app/pulls/42 --include -H If-None-Match: "revision-1"')
    n="$(cat "$WAIT_COUNT")"
    n=$((n+1))
    printf '%s' "$n" >"$WAIT_COUNT"
    if [ "$n" -eq 1 ]; then cat "$WAIT_FIX/open.http"; else cat "$WAIT_FIX/merged.http"; fi
    exit 0;;
  'pr view 42 --repo acme/app --json headRefOid,statusCheckRollup,reviews')
    cat "$WAIT_FIX/detail.json"; exit 0;;
esac
if [ "$#" -eq 10 ] && [ "$1" = api ] && [ "$2" = graphql ] &&
   [ "$3" = -F ] && [ "$4" = owner=acme ] && [ "$5" = -F ] &&
   [ "$6" = name=app ] && [ "$7" = -F ] && [ "$8" = pr=42 ] &&
   [ "$9" = -f ] && [ "${10}" = "query=$(cat "$WAIT_FIX/threads-query.txt")" ]; then
  cat "$WAIT_FIX/threads.json"
  exit 0
fi
printf 'stub-gh UNRECOGNISED %s\n' "$argv" >>"${HOSTSTUB_LOG:?}"
exit 64
STUB
chmod +x "$EVAL_STUBBIN/gh"

run_review pr-wait acme/app 42 --until merged --timeout 5s --json
assert_exit "merge wait succeeds after two polls" 0
assert_envelope "merge wait"
assert_json_field "typed condition result" condition-met '.data.outcome'
assert_json_field "merged snapshot" MERGED '.data.snapshot.state'
assert_json_field "two observations" 2 '.data.observations'
assert_eq "host flipped on second poll" 2 "$(cat "$WAIT_COUNT")"
if [ -e "$EVAL_SANDBOX/.verdict/state/42" ]; then bad "pr-wait wrote a bundle"; else good "pr-wait writes no bundle"; fi

printf '0' >"$WAIT_COUNT"
run_review pr-wait acme/app 42 --until merged --timeout 1s --json
assert_exit "timeout has exit 1" 1
assert_envelope "timeout is data"
assert_json_field "typed timeout outcome" wait-timeout '.data.outcome'
assert_eq "timeout made only its initial poll" 1 "$(cat "$WAIT_COUNT")"

printf '0' >"$WAIT_COUNT"
run_review pr-wait acme/app 42 --until merged --timeout 5s --once
assert_exit "one-shot unmet exit" 1
assert_docs "snapshot plus final one-shot result" 2
assert_eq "one-shot outcome" condition-unmet "$(jq -s -r '.[1].outcome' "$EVAL_OUT")"

printf '0' >"$WAIT_COUNT"
run_review pr-wait acme/app 42 --until merged --once --json
assert_exit "one-shot without timeout exit" 1
assert_envelope "one-shot without timeout"
assert_json_field "one-shot without timeout outcome" condition-unmet '.data.outcome'
assert_json_field "one-shot without timeout observations" 1 '.data.observations'

run_review pr-wait acme/app 42 --until merged --timeout 5s --provider forgejo --json
assert_exit "Forgejo refuses explicitly" 1
assert_refusal "unsupported host" provider-unimplemented
hoststub_assert_all_routed

eval_end
