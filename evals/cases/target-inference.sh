#!/usr/bin/env bash
#
# Targets: a pasted pull request URL stands for `<owner/repo> <pr>` and implies
# the provider; with no positionals the pair is inferred offline from the origin
# remote and the bundle recorded for the current branch. Every derived target
# is echoed, and a conflicting --provider refuses before any host call. Stub gh
# only; no network.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_BUDGET_USD REVIEW_MODEL REVIEW_PANEL || true

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q -b main
  mkdir -p a
  printf 'package a\n\nfunc Save() {}\n' >a/save.go
  grep -qxF '.verdict/state/' .gitignore 2>/dev/null || echo ".verdict/state/" >>.gitignore
  git add -A
)
git -C "$EVAL_SANDBOX" remote add origin git@github.com:acme/app.git

cat >"$EVAL_SANDBOX/canned.diff" <<'DIFF'
diff --git a/a/save.go b/a/save.go
index 1111111..2222222 100644
--- a/a/save.go
+++ b/a/save.go
@@ -3 +3 @@
-func Save() {}
+func Save() { _ = 1 }
DIFF
cat >"$EVAL_SANDBOX/canned-PR.json" <<'JSON'
{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","title":"Harden the retry budget","body":"Retries stop at three.","headRefName":"harden-retry","author":{"login":"teammate"},"url":"https://github.com/acme/app/pull/4701","files":[{"path":"a/save.go","additions":1,"deletions":1}],"additions":1,"deletions":1,"commits":[]}
JSON

mkdir -p "$EVAL_SANDBOX/fakebin"
cat >"$EVAL_SANDBOX/fakebin/gh" <<'STUB'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
case "$*" in
  *--jq*) echo "a/save.go (+1/-1)" ;;
  "pr view 4701 "*) cat "$EVAL_SANDBOX/canned-PR.json" ;;
  "pr diff 4701 "*) cat "$EVAL_SANDBOX/canned.diff" ;;
  "api graphql"*) echo '{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}}' ;;
  "api "*) echo "[]" ;;
  *) echo "canned-gh: unhandled call ($*)" >&2; exit 1 ;;
esac
STUB
chmod +x "$EVAL_SANDBOX/fakebin/gh"
export EVAL_STUBBIN="$EVAL_SANDBOX/fakebin:$EVAL_STUBBIN"
: >"$EVAL_STUBLOG"

# T1 a pasted URL (with a view tail and a fragment) is the whole target.
run_review fetch "https://github.com/acme/app/pull/4701/files#discussion_r9" --json
assert_exit "T1 fetch accepts a pull request URL" 0
assert_json_field "T1 envelope repo comes from the URL" acme/app '.repo'
assert_json_field "T1 envelope pr comes from the URL" 4701 '.pr'
assert_file_grep "T1 the bundle landed under the URL's number" "$EVAL_SANDBOX/.verdict/state/4701/PR.json" 'harden-retry'
run_review fetch "https://github.com/acme/app/pull/4701"
assert_exit "T1 human fetch by URL" 0
assert_file_grep "T1 the derived target is echoed" "$EVAL_ERR" '^target: acme/app#4701 \(github, from the URL\)$' "-E"

# T2 no positionals: origin gives the repo, the recorded bundle gives the PR.
git -C "$EVAL_SANDBOX" checkout -q -b harden-retry
run_review fetch --json
assert_exit "T2 fetch with no positionals infers the target" 0
assert_json_field "T2 inferred repo" acme/app '.repo'
assert_json_field "T2 inferred pr" 4701 '.pr'
run_review fetch
assert_file_grep "T2 the inference is echoed, never silent" "$EVAL_ERR" \
  '^target: acme/app#4701 \(github, inferred from branch "harden-retry" recorded in .verdict/state/4701\)$' "-E"

# T3 a branch no bundle records refuses with the exact args to pass.
git -C "$EVAL_SANDBOX" checkout -q -b unrelated
calls_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
run_review fetch --json
assert_exit "T3 an unrecorded branch refuses" 1
assert_json_field "T3 typed refusal" target-unresolved '.error.code'
assert_file_grep "T3 the refusal names the args to pass" "$EVAL_OUT" 'verdict fetch acme/app <pr>'
assert_eq "T3 inference made no host call" "$calls_before" "$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

# T4 a URL's provider conflicting with --provider refuses before any host call.
run_review fetch "https://github.com/acme/app/pull/4701" --provider gitlab --json
assert_exit "T4 conflicting provider refuses" 1
assert_json_field "T4 typed refusal" bad-target-url '.error.code'
assert_eq "T4 the refusal made no host call" "$calls_before" "$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

# T5 hostile and unmapped URLs refuse typed.
for url in "https://github.com/acme/../x/pull/1" "https://ghe.corp.example/acme/app/pull/1" \
  "https://github.com/acme/app/pull/12/files/a/b" "https://u:p@github.com/acme/app/pull/1"; do
  run_review fetch "$url" --json
  assert_exit "T5 $url refuses" 1
  assert_json_field "T5 $url is a typed URL refusal" bad-target-url '.error.code'
done
assert_eq "T5 hostile URLs made no host call" "$calls_before" "$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

# T6 fetch-local keeps its own syntax: a bare invocation is still usage.
run_review fetch-local
assert_exit "T6 fetch-local with no name stays a usage error" 2

eval_end
