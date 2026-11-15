#!/usr/bin/env bash
#
# fetch author fingerprint: the advisory author.json. A PR whose body and commits
# carry a harness fingerprint lands {schema_version, agent, model, confidence,
# evidence[]} beside the bundle and names it under data.artifacts; the reviewed
# diff is untouched, nothing warns or blocks, and a fetch that matches no signal
# writes no file (dropping an earlier claim). No network.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_BUDGET_USD REVIEW_MODEL REVIEW_PANEL || true

EVAL_FIXTURE_HEAD="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
BUNDLE="$EVAL_SANDBOX/.verdict/state/4701"

# The reviewed change and the github origin the provider is inferred from.
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q -b main
  mkdir -p a
  printf 'package a\n\nfunc Save() {}\n' >a/save.go
  grep -qxF '.verdict/state/' .gitignore 2>/dev/null || echo ".verdict/state/" >>.gitignore
  git add -A
)
git -C "$EVAL_SANDBOX" remote add origin https://github.com/acme/app.git

cat >"$EVAL_SANDBOX/canned.diff" <<'EOF'
diff --git a/a/save.go b/a/save.go
index 1111111..2222222 100644
--- a/a/save.go
+++ b/a/save.go
@@ -3 +3 @@
-func Save() {}
+func Save() { _ = 1 }
EOF

# A PR a coding harness opened: the body carries the generated-with line and the
# commit carries the trailer naming the model.
cat >"$EVAL_SANDBOX/canned-PR.json" <<EOF
{"state":"OPEN","headRefOid":"$EVAL_FIXTURE_HEAD","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","title":"Harden the retry budget","body":"## Summary\n\nRetries stop at three per host.\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)","headRefName":"sebastian/harden-retry-budget","author":{"login":"teammate"},"files":[{"path":"a/save.go","additions":1,"deletions":1}],"additions":1,"deletions":1,"commits":[{"oid":"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678","messageHeadline":"fetch: cap retries at three","messageBody":"The budget path retried without limit.\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}]}
EOF

mkdir -p "$EVAL_SANDBOX/fakebin"
cat >"$EVAL_SANDBOX/fakebin/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
case "$*" in
  *--jq*) echo "a/save.go (+1/-1)" ;;
  "pr view "*) cat "$EVAL_SANDBOX/canned-PR.json" ;;
  "pr diff "*) cat "$EVAL_SANDBOX/canned.diff" ;;
  "api graphql"*) echo '{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}}' ;;
  "api "*) echo "[]" ;;
  *) echo "canned-gh: unhandled call ($*)" >&2; exit 1 ;;
esac
EOF
chmod +x "$EVAL_SANDBOX/fakebin/gh"

EVAL_STUBBIN="$EVAL_SANDBOX/fakebin:$EVAL_STUBBIN" run_review fetch acme/app 4701 --json
assert_exit "fetch exits 0 over a harness-authored PR" 0
assert_json_field "the envelope names the fingerprint artifact" \
  ".verdict/state/4701/author.json" '.data.artifacts["author.json"]'
assert_json_field "the strongest signal names the agent" claude '.agent' "$BUNDLE/author.json"
assert_json_field "the commit trailer names the model" "Opus 4.5" '.model' "$BUNDLE/author.json"
assert_json_field "the fingerprint is high confidence" high '.confidence' "$BUNDLE/author.json"
assert_json_field "the body and the commit are both kept as evidence" "body,commits" \
  '.evidence | map(.field) | join(",")' "$BUNDLE/author.json"
assert_json_field "the evidence quotes the trailer verbatim" \
  "Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>" '.evidence[1].signal' "$BUNDLE/author.json"
assert_file_grep "the human surface names the fingerprint" "$EVAL_ERR" \
  '^» author: claude \(high confidence\) from 2 signal\(s\) -> .verdict/state/4701/author.json$' "-E"
assert_json_field "an advisory fingerprint warns about nothing" 0 '.warnings | length'

# Advisory means the review is what it was: the same diff, the same file count,
# and a next step that never routes on the fingerprint.
assert_file_eq "the reviewed diff is unchanged" "$EVAL_SANDBOX/canned.diff" "$BUNDLE/full.diff"
assert_json_field "the reviewed file count is unchanged" 1 '.data.files'
assert_json_field "the next step feeds the diff, not the fingerprint" "false" \
  '.next | contains("author.json")'

# A PR carrying no published signal writes no fingerprint, and the earlier claim
# does not survive the rerun: absent means unknown, never human.
cat >"$EVAL_SANDBOX/canned-PR.json" <<EOF
{"state":"OPEN","headRefOid":"$EVAL_FIXTURE_HEAD","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","title":"Harden the retry budget","body":"Retries stop at three per host.\n\nCo-authored-by: Alice <alice@example.com>","headRefName":"sebastian/harden-retry-budget","author":{"login":"teammate"},"files":[{"path":"a/save.go","additions":1,"deletions":1}],"additions":1,"deletions":1,"commits":[{"oid":"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678","messageHeadline":"fetch: cap retries at three","messageBody":"The budget path retried without limit."}]}
EOF
EVAL_STUBBIN="$EVAL_SANDBOX/fakebin:$EVAL_STUBBIN" run_review fetch acme/app 4701 --json
assert_exit "a human-authored PR still exits 0" 0
assert_json_field "the envelope names no fingerprint" "null" '.data.artifacts["author.json"]'
if [ -e "$BUNDLE/author.json" ]; then
  bad "a human co-author trailer left the earlier fingerprint in place"
else
  good "a fetch matching no signal removes the earlier fingerprint"
fi
assert_stream_nogrep "no fingerprint line prints without a signal" "$EVAL_ERR" '^» author: ' "-E"

eval_end
