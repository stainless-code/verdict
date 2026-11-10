#!/usr/bin/env bash
#
# GitLab carries the same incremental re-review contract as GitHub:
# `fetch --provider gitlab --base-sha <sha>` calls the compare API through
# `glab`, writes the literal `incremental.diff`, keeps its stderr sidecar, and
# turns a failed compare into the note instead of a refusal. The delta then
# feeds the ordinary record/advance path a second review round uses.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
# shellcheck source=../lib/hoststub.sh
. "$HERE/../lib/hoststub.sh"

FIX="$EVAL_ROOT/evals/fixtures/gitlab"
PR=7
FAIL_PR=8
HEAD="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
FAIL_HEAD="dddddddddddddddddddddddddddddddddddddddd"
BASE_SHORT="bbbbbbb"
DIR="$EVAL_SANDBOX/.verdict/state/$PR"
FAIL_DIR="$EVAL_SANDBOX/.verdict/state/$FAIL_PR"
COMPARE="api projects/4242/repository/compare?from=$BASE_SHORT"

hoststub_init glab
hoststub_prove_dead glab
hoststub_route glab "mr view $PR -R acme/app -F json" "$FIX/MR.json"
hoststub_route glab "mr view $FAIL_PR -R acme/app -F json" "$FIX/MR-8.json"
hoststub_route glab "api projects/acme%2Fapp --jq .id" "$FIX/project-id.txt"
hoststub_route glab "api projects/4242/merge_requests/$PR/diffs --paginate" "$FIX/diffs.json"
hoststub_route glab "api projects/4242/merge_requests/$FAIL_PR/diffs --paginate" "$FIX/diffs.json"
hoststub_route glab "mr diff $PR -R acme/app --raw" "$FIX/full.diff"
hoststub_route glab "mr diff $FAIL_PR -R acme/app --raw" "$FIX/full.diff"
hoststub_route glab "api projects/4242/merge_requests/$PR/notes?per_page=100&page=1" "$FIX/notes.json"
hoststub_route glab "api projects/4242/merge_requests/$FAIL_PR/notes?per_page=100&page=1" "$FIX/notes.json"
hoststub_route glab "api projects/4242/merge_requests/$PR/discussions?per_page=100&page=1" "$FIX/discussions.json"
hoststub_route glab "api projects/4242/merge_requests/$FAIL_PR/discussions?per_page=100&page=1" "$FIX/discussions.json"
hoststub_route glab "$COMPARE&to=$HEAD" "$FIX/compare.json"

# The failure leg needs glab's stderr, which the route table cannot carry: wrap
# the generated stub so one exact argv answers non-zero with a message while
# every other call delegates to the network-dead route table.
mv "$EVAL_STUBBIN/glab" "$EVAL_STUBBIN/glab-real"
cat >"$EVAL_STUBBIN/glab" <<STUB
#!/usr/bin/env bash
set -uo pipefail
if [ "\$*" = "$COMPARE&to=$FAIL_HEAD" ]; then
  printf 'stub-glab %s\n' "\$*" >>"\${HOSTSTUB_LOG:?}"
  printf 'glab: compare unavailable\n' >&2
  exit 1
fi
exec "\$(dirname "\$0")/glab-real" "\$@"
STUB
chmod +x "$EVAL_STUBBIN/glab"

run_review fetch acme/app "$PR" --provider gitlab --base-sha "$BASE_SHORT" --json
assert_exit "gitlab incremental fetch" 0
assert_envelope "gitlab incremental fetch envelope"
assert_json_field "fetch names the provider" gitlab '.data.capabilities.provider'
assert_json_field "fetch pins the fixture head" "$HEAD" '.data.head_sha'
assert_json_field "fetch carries the incremental base" "$BASE_SHORT" '.data.incremental_base'
assert_json_field "fetch names the delta artifact" ".verdict/state/$PR/incremental.diff" '.data.artifacts["incremental.diff"]'
assert_json_field "fetch warns about nothing" 0 '.warnings | length'
assert_file_eq "fetch writes the literal compare diff" "$FIX/expected-incremental.diff" "$DIR/incremental.diff"
assert_eq "the compare stderr sidecar is empty on success" 0 "$(wc -c <"$DIR/.compare.err" | tr -d ' ')"
if [ -e "$DIR/incremental.diff.note" ]; then
  bad "a successful compare wrote incremental.diff.note"
else
  good "a successful compare writes no note"
fi

hoststub_argv_log glab "$EVAL_SANDBOX/glab-calls-success.txt"
assert_file_eq "every compare-path glab call is a registered argv in order" \
  "$FIX/expected-argvs-incremental.txt" "$EVAL_SANDBOX/glab-calls-success.txt"

run_review fetch acme/app "$FAIL_PR" --provider gitlab --base-sha "$BASE_SHORT" --json
assert_exit "gitlab failed-compare fetch still succeeds" 0
assert_envelope "gitlab failed-compare fetch envelope"
assert_json_field "a failed compare pins no incremental base" null '.data.incremental_base'
assert_json_field "a failed compare names no delta artifact" null '.data.artifacts["incremental.diff"]'
assert_file_eq "a failed compare writes the note" "$FIX/expected-note-fail.txt" "$FAIL_DIR/incremental.diff.note"
if [ -e "$FAIL_DIR/incremental.diff" ]; then
  bad "a failed compare wrote incremental.diff"
else
  good "a failed compare writes no incremental.diff"
fi
assert_eq "the failed compare still spent its one compare read" 14 "$(hoststub_count glab)"
assert_file_grep "the failed compare argv was the compare route" "$HOSTSTUB_LOG" \
  "repository/compare?from=$BASE_SHORT&to=$FAIL_HEAD"

eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,
  "body":"Prior addressed note.","suggestion":null,"kb":[],"inBounds":true,
  "rigor":"pointed","severity":"informational",
  "sig":"1111111111111111111111111111111111111111","sig_version":"1",
  "firstSeen":"1111111","lastSeen":"1111111","status":"addressed","rationale":"fixed earlier",
  "history":[{"at":"2026-11-01T00:00:00Z","event":"reported"}]}]
EOF
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ⚠️ One delta finding.\n> Confidence: 4/5 · Safety: not assessed\n> Basis: 1 walked · 1 pointed · 0 said · 0 rules cited · 0 verified fixes","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Delta review: the retry loop still starts at one.","rigor":"pointed","severity":"important"}]}
EOF
run_review record acme/app "$PR" --json
assert_exit "the delta review records over the GitLab bundle" 0
assert_envelope "gitlab record envelope"
assert_json_field "the delta finding merges open" 2 '.data.findings.open'
assert_json_field "the record names the fetched head" "$HEAD" '.head' "$DIR/record.json"
assert_json_field "the addressed finding reopens on the delta path" open '.[] | select(.sig == "1111111111111111111111111111111111111111") | .status' "$DIR/findings.json"
assert_json_field "the reopen is history-logged" reopened \
  '.[] | select(.sig == "1111111111111111111111111111111111111111") | .history[-1].event' "$DIR/findings.json"
assert_json_field "the addressed finding advances to the fetched head" aaaaaaa \
  '.[] | select(.sig == "1111111111111111111111111111111111111111") | .history[-1].sha' "$DIR/findings.json"
assert_file_eq "the delta stays in the bundle after record" "$FIX/expected-incremental.diff" "$DIR/incremental.diff"

assert_eq "the case makes zero gh calls" 0 "$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
hoststub_assert_all_routed

eval_end
