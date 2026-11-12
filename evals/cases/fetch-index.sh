#!/usr/bin/env bash
#
# fetch --index / fetch-local --index: the explicit index hand-off. A named
# index answers the bundle's diff into impact.json plus a capped "## impact"
# section on toc.txt; a bundle fetched without the flag carries neither, and an
# earlier run's impact.json is removed; a directory with no index store and an
# unresolvable ref refuse typed. No network.
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

# The reviewed change touches a.Save's line, so the changed declaration, its
# cross-package caller, and its test are the three rows the hand-off names.
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q -b main
  git config user.email "index-eval@example.com"
  git config user.name "index eval"
  printf 'module example.com/m\n\ngo 1.27\n' >go.mod
  mkdir -p a b
  printf 'package a\n\nfunc Save() {}\n' >a/save.go
  printf 'package b\n\nimport "example.com/m/a"\n\nfunc Use() {\n\ta.Save()\n}\n' >b/use.go
  printf 'package a\n\nimport "testing"\n\nfunc TestSave(t *testing.T) {\n\tSave()\n}\n' >a/save_test.go
  grep -qxF '.verdict/state/' .gitignore 2>/dev/null || echo ".verdict/state/" >>.gitignore
  git add -A
  GIT_AUTHOR_DATE=2024-05-01T00:00:00Z GIT_COMMITTER_DATE=2024-05-01T00:00:00Z \
    git commit -qm 'base'
  printf 'package a\n\nfunc Save() { _ = 1 }\n' >a/save.go
)

run_review index build --json
assert_exit "index build exits 0" 0
tree="$(jq -r '.data.tree' "$EVAL_OUT")"

# The local leg: a named index answers the worktree diff.
run_review fetch-local acme/app impact-local --index HEAD --json
assert_exit "fetch-local --index exits 0" 0
assert_json_field "the next step feeds toc.txt, where the impact section lives" \
  "true" '.next | endswith("local-impact-local/toc.txt to any agent; ask it to write .verdict/state/local-impact-local/review.json")'
assert_json_field "the impact artifact is named in the envelope" \
  ".verdict/state/local-impact-local/impact.json" '.data.artifacts["impact.json"]'
assert_file_grep "impact.json names the answering tree as its source" \
  "$EVAL_SANDBOX/.verdict/state/local-impact-local/impact.json" "\"index:$tree\""
assert_file_grep "impact.json records the changed declaration" \
  "$EVAL_SANDBOX/.verdict/state/local-impact-local/impact.json" '"name": "Save"'
assert_file_grep "impact.json records the cross-package caller" \
  "$EVAL_SANDBOX/.verdict/state/local-impact-local/impact.json" '"resolution": "lexical"'
assert_file_grep "toc.txt gains the impact section" \
  "$EVAL_SANDBOX/.verdict/state/local-impact-local/toc.txt" '^## impact$'
assert_file_grep "the section names the index it came from" \
  "$EVAL_SANDBOX/.verdict/state/local-impact-local/toc.txt" "^index: index:$tree (tree $tree, commit "
assert_file_grep "the section names the changed declaration" \
  "$EVAL_SANDBOX/.verdict/state/local-impact-local/toc.txt" '^- a/save.go:3 Save (func)$'
assert_file_grep "the section names the cross-package caller at its depth" \
  "$EVAL_SANDBOX/.verdict/state/local-impact-local/toc.txt" \
  '^- b/use.go:6 example.com/m/a.Save (lexical, depth 1)$'
assert_file_grep "the section names the candidate test" \
  "$EVAL_SANDBOX/.verdict/state/local-impact-local/toc.txt" '^- a/save_test.go (same-package, depth 0)$'
assert_file_nogrep "no hostile fence marker reaches the section" \
  "$EVAL_SANDBOX/.verdict/state/local-impact-local/toc.txt" '<<<'

# The same bundle fetched again without the flag loses the whole hand-off.
run_review fetch-local acme/app impact-local
assert_exit "the twin fetch without --index exits 0" 0
assert_file_nogrep "the next step names no toc.txt without --index" "$EVAL_OUT" 'toc\.txt to any agent'
if [ -e "$EVAL_SANDBOX/.verdict/state/local-impact-local/impact.json" ]; then
  bad "impact.json survived a fetch without --index"
else
  good "impact.json is removed when the flag is absent"
fi
assert_file_nogrep "toc.txt loses the impact section" \
  "$EVAL_SANDBOX/.verdict/state/local-impact-local/toc.txt" '^## impact$'

# The remote leg, over a canned host: the PR head is not in this clone, so the
# hand-off warns and answers for the named tree instead of mislabelling it.
cat > "$EVAL_SANDBOX/canned-PR.json" <<EOF
{"state":"OPEN","headRefOid":"$EVAL_FIXTURE_HEAD","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"teammate"},"files":[{"path":"a/save.go","additions":1,"deletions":1}],"additions":1,"deletions":1}
EOF
cat > "$EVAL_SANDBOX/canned.diff" <<'EOF'
diff --git a/a/save.go b/a/save.go
index 1111111..2222222 100644
--- a/a/save.go
+++ b/a/save.go
@@ -3 +3 @@
-func Save() {}
+func Save() { _ = 1 }
EOF
mkdir -p "$EVAL_SANDBOX/fakebin"
cat > "$EVAL_SANDBOX/fakebin/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
case "$*" in
  *--jq*) echo "a/save.go (+1/-1)" ;;
  "pr view "*) cat "$EVAL_SANDBOX/canned-PR.json" ;;
  "pr diff "*) cat "$EVAL_SANDBOX/canned.diff" ;;
  "api "*) echo "[]" ;;
  *) echo "canned-gh: unhandled call ($*)" >&2; exit 1 ;;
esac
EOF
chmod +x "$EVAL_SANDBOX/fakebin/gh"

EVAL_STUBBIN="$EVAL_SANDBOX/fakebin:$EVAL_STUBBIN" run_review fetch acme/app 4701 --index HEAD --json
assert_exit "fetch --index exits 0" 0
assert_json_field "the remote next step feeds toc.txt" "true" '.next | contains("4701/toc.txt to any agent")'
assert_json_field "the remote bundle names the impact artifact" \
  ".verdict/state/4701/impact.json" '.data.artifacts["impact.json"]'
assert_file_grep "the remote section names the changed declaration" \
  "$EVAL_SANDBOX/.verdict/state/4701/toc.txt" '^- a/save.go:3 Save (func)$'
assert_file_grep "an unknown head warns instead of mislabelling the rows" \
  "$EVAL_OUT" "» impact: the bundle head $EVAL_FIXTURE_HEAD is not in this clone; impact rows describe $tree, not the reviewed head"

# Refusals: a directory with no store, and a ref this clone cannot resolve.
mkdir -p "$EVAL_SANDBOX/no-store"
run_review fetch-local acme/app impact-bad-dir --index "$EVAL_SANDBOX/no-store" --json
assert_exit "a directory with no index store refuses" 1
assert_refusal "the storeless directory names bad-flags" "bad-flags"
assert_file_grep "the refusal names the directory and the missing store" \
  "$EVAL_OUT" "carries no index store (no .verdict/index directory)"

run_review fetch-local acme/app impact-bad-ref --index nosuchref --json
assert_exit "an unresolvable ref refuses" 1
assert_refusal "the unresolvable ref names bad-at" "bad-at"

if [ -f "$EVAL_SANDBOX/.verdict/state/local-impact-bad-ref/impact.json" ]; then
  bad "a refused hand-off still wrote impact.json"
else
  good "a refused hand-off writes no impact.json"
fi

eval_end
