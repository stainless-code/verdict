#!/usr/bin/env bash
#
# Trigger gate: ready --filter reads the closed trigger predicates from the
# trusted base snapshot, refuses filtered-out before any host call, and records
# both decisions in run.json. A PR that touches a protected path runs.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

STUB_SAVED="$EVAL_SANDBOX/gh-stub-orig"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
restore_stub() { cp "$STUB_SAVED" "$EVAL_STUBBIN/gh"; }
trap restore_stub EXIT
cat >"$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
case "$*" in
  *"isDraft,mergeable,mergeStateStatus,body,state,headRefOid"*)
    printf '%s' '{"isDraft":false,"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","body":"A long enough description for the readiness check to pass without a blocker.","state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}' ;;
  *"pr checks"*) printf '%s' '[]' ;;
  *"graphql"*) printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}' ;;
  *) echo "trigger-eval: unhandled gh call ($*)" >&2; exit 1 ;;
esac
EOF
chmod +x "$EVAL_STUBBIN/gh"

mkdir -p "$EVAL_SANDBOX/.verdict"
cat >"$EVAL_SANDBOX/.verdict/config.json" <<'EOF'
{"schema_version":"1","trigger":{"paths_include":["protected/**"],"authors":["teammate"]}}
EOF
eval_mk_bundle 42
PR="$EVAL_SANDBOX/.verdict/state/42"
jq '. + {title:"fix: retry", baseRefName:"main", labels:[{"name":"deps"}],
  files:[{path:"protected/key.go"}]}' "$PR/PR.json" >"$PR/PR.json.tmp"
mv "$PR/PR.json.tmp" "$PR/PR.json"
printf '[]' >"$PR/findings.json"

mark="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
run_review ready acme/app 42 --filter --json
assert_exit "a PR touching a protected path runs" 0
assert_json_field "the matching PR is still advisory" true '.data.ready'
if [ "$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')" -gt "$mark" ]; then
  good "a matching PR reaches the live reads"
else
  bad "a matching PR paid no host calls"
fi
assert_file_grep "the receipt records the run" "$PR/run.json" '"reason": "matched"'
assert_file_grep "the receipt records run true" "$PR/run.json" '"run": true'

jq '.files = [{path:"docs/README.md"}]' "$PR/PR.json" >"$PR/PR.json.tmp"
mv "$PR/PR.json.tmp" "$PR/PR.json"
mark="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
run_review ready acme/app 42 --filter --json
if [ "$EVAL_STATUS" -eq 1 ] && [ "$(jq -r '.error.code' "$EVAL_OUT")" = "filtered-out" ] \
  && jq -e '.error.message | contains("paths_include")' "$EVAL_OUT" >/dev/null; then
  good "an untouched PR refuses filtered-out naming paths_include"
else
  bad "filtered PR exits $EVAL_STATUS: $(head -c 300 "$EVAL_OUT")"
fi
assert_eq "the gate pays zero host calls" 0 "$(( $(wc -l <"$EVAL_STUBLOG" | tr -d ' ') - mark ))"
assert_file_grep "the receipt records the failing predicate" "$PR/run.json" '"reason": "paths_include"'
assert_file_grep "the receipt records run false" "$PR/run.json" '"run": false'

mark="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
run_review ready acme/app 42 --json
assert_exit "without --filter the same PR is unchanged" 0
if [ "$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')" -gt "$mark" ]; then
  good "without --filter the readiness read still pays host calls"
else
  bad "without --filter no host call was made"
fi

assert_file_nogrep "no unhandled gh call escaped the stub" "$EVAL_STUBLOG" "unhandled gh call"
eval_end
