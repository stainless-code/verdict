#!/usr/bin/env bash
#
# fetch linked-issue context: a GitHub PR whose body closes an issue brings
# that issue's title and body into the bundle as a spec source, and an explicit
# --issue adds one by reference. An unreadable issue is a typed, non-fatal
# warning; a malformed ref is the issue-invalid refusal. Reads stay forge-native:
# a third-party tracker ref is refused, and its text comes in through --spec. No
# network: a case-local gh answers the exact argv the case registers (anything
# else exits 64 with an UNRECOGNISED line).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
# shellcheck source=../lib/hoststub.sh
. "$HERE/../lib/hoststub.sh"

FIX="$EVAL_ROOT/evals/fixtures/issues"
BUNDLE="$EVAL_SANDBOX/.verdict/state/4701"

mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_BUDGET_USD REVIEW_MODEL REVIEW_PANEL || true

# The case-local gh replaces the harness stub first on PATH: it logs every
# call, answers the exact argv the case expects, and refuses anything else
# loudly, so no unregistered call can pass and no call reaches a host.
cat >"$EVAL_STUBBIN/gh" <<'STUB'
#!/usr/bin/env bash
echo "stub-gh $*" >>"${EVAL_STUBLOG:-/dev/null}"
case "$*" in
  "api graphql -F owner=acme -F name=app -F pr=4701 -f query="*closingIssuesReferences*) cat "$EVAL_FIX/closing-issues.json" ;;
  "api repos/acme/app/issues/12") cat "$EVAL_FIX/issue-12.json" ;;
  "api repos/acme/app/issues/99") echo "gh: Not Found (HTTP 404)" >&2; exit 1 ;;
  "pr view 4701 --repo acme/app --json files --jq "*)
    echo "notes.txt (+1/-1)" ;;
  "pr view 4701 --repo acme/app --json "*)
    cat "${EVAL_PR_JSON:-$EVAL_FIX/pr.json}" ;;
  "pr diff 4701 --repo acme/app")
    cat "$EVAL_FIX/full.diff" ;;
  "api repos/acme/app/pulls/4701/reviews --paginate")
    echo "[]" ;;
  "api repos/acme/app/pulls/4701/comments --paginate")
    echo "[]" ;;
  "api graphql "*reviewThreads*)
    echo '{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}}' ;;
  "api repos/acme/app/contents/"*)
    echo "gh: Not Found (HTTP 404)" >&2; exit 1 ;;
  *)
    echo "UNRECOGNISED $*" >>"${EVAL_STUBLOG:-/dev/null}"
    echo "evals-stub-gh: unrecognised argv (network disabled): $*" >&2
    exit 64 ;;
esac
STUB
chmod +x "$EVAL_STUBBIN/gh"
export EVAL_FIX="$FIX"

# No curl at all on this path: the issue reads are gh reads, and a forge-native
# core never posts to a third-party tracker's API.
hoststub_init curl
hoststub_prove_dead curl

# Positive leg: the closing keyword brings the linked issue in as a spec source.
run_review fetch acme/app 4701 --json
assert_exit 'fetch with a linked issue' 0
assert_json_field 'the issue URL reaches data.sources' \
  'https://github.com/acme/app/issues/12' '.data.sources[0]'
assert_json_field 'the linked issue is a spec source' spec '.[0].role' "$BUNDLE/context.json"
assert_json_field 'the linked issue is untrusted' untrusted '.[0].trust' "$BUNDLE/context.json"
assert_json_field 'the linked issue records its source' \
  'https://github.com/acme/app/issues/12' '.[0].source' "$BUNDLE/context.json"
assert_eq 'the linked issue document keeps title and body' \
  "$(printf '# Add retry budget\n\nRetries stop at three per host.')" "$(cat "$BUNDLE/context/000.md")"
assert_file_grep 'toc marks the issue row as a spec source' "$BUNDLE/toc.txt" \
  '^- https://github.com/acme/app/issues/12 \[spec\]$' -E
assert_json_field 'a readable linked issue warns about nothing' 0 '.warnings | length'
assert_eq 'the linked-issue read made no curl call' 0 "$(hoststub_count curl)"
assert_file_nogrep 'every gh call was a registered argv' "$EVAL_STUBLOG" 'UNRECOGNISED'

# Positive leg: an explicit --issue reads the same issue through one REST call,
# and the keyword-linked one beside it is not read or warned about twice.
run_review fetch acme/app 4701 --issue 'acme/app#12' --json
assert_exit 'fetch with an explicit issue the PR also links' 0
assert_json_field 'the explicit issue is one spec source' 1 '[.[] | select(.role == "spec")] | length' "$BUNDLE/context.json"
assert_json_field 'the explicit ref does not warn twice' 0 '.warnings | length'

# Negative leg: an unreadable explicit issue is a typed warning, not a refusal.
run_review fetch acme/app 4701 --issue 'acme/app#99' --json
assert_exit 'fetch with an unreadable explicit issue' 0
assert_json_field 'the unreadable issue warns with its reason' \
  'issue-unavailable (not-found): acme/app#99' '.warnings[0]'

# Negative leg: a malformed ref is the typed refusal, and nothing is written.
run_review fetch acme/app 4701 --issue wat --json
assert_exit 'a malformed --issue is refused' 1
assert_refusal 'malformed --issue' issue-invalid
assert_json_field 'the refusal names the operand' \
  '--issue wat: unrecognized issue reference (want owner/repo#12, #12, or a GitHub issue URL)' \
  '.error.message | split("\n")[0]'

# Negative leg: a third-party tracker ref is not an issue shape — the core is
# forge-native, and that text belongs in --spec.
run_review fetch acme/app 4701 --issue STA-466 --json
assert_exit 'a Linear ref is refused' 1
assert_refusal 'a Linear ref' issue-invalid
assert_json_field 'the refusal says what --issue takes' \
  '--issue STA-466: unrecognized issue reference (want owner/repo#12, #12, or a GitHub issue URL)' \
  '.error.message | split("\n")[0]'

# Negative leg: an empty --issue is a missing-value usage error.
run_review fetch acme/app 4701 --issue '' --json
assert_exit 'an empty --issue is a usage error' 2
assert_file_grep 'the usage error names the flag' "$EVAL_ERR" \
  '--issue needs a value (want <ref>)' -x

# Negative leg: an untrusted PR body cannot steer a host read. The body names a
# closing issue through a path, which is not an issue link at all.
sed 's|Fixes #12|Fixes ../..#12|' "$FIX/pr.json" >"$EVAL_SANDBOX/traversal-pr.json"
export EVAL_PR_JSON="$EVAL_SANDBOX/traversal-pr.json"
run_review fetch acme/app 4701 --json
assert_exit 'a traversal slug in the PR body is not a fetch failure' 0
assert_json_field 'the traversal slug lands no source' 0 '(.data.sources // []) | length'
assert_json_field 'the traversal slug warns about nothing' 0 '.warnings | length'
assert_file_nogrep 'the traversal slug never reached a gh argv' "$EVAL_STUBLOG" 'repos/../..'
unset EVAL_PR_JSON

eval_end
