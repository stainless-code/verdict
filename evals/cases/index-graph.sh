#!/usr/bin/env bash
#
# index graph: tests-for, co-changed, and owners answer from the built index
# and the git objects, not the worktree. Candidate tests cover all three via
# kinds, co-change rows carry literal counts and jaccard with an echoed
# window, the CODEOWNERS last match wins, an uncommitted CODEOWNERS edit
# changes nothing, and a co-changed query on a checkout with no index store
# writes nothing at all. No network.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0=commit.gpgsign
export GIT_CONFIG_VALUE_0=false
export GIT_AUTHOR_NAME=verdict-test
export GIT_AUTHOR_EMAIL=test@verdict.invalid
export GIT_COMMITTER_NAME=verdict-test
export GIT_COMMITTER_EMAIL=test@verdict.invalid
export HOME="$EVAL_SANDBOX/home"
mkdir -p "$HOME"

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q -b main
  printf 'module example.com/m\n\ngo 1.27\n' >go.mod
  mkdir -p a b c .github
  printf 'package a\n\nfunc Target() {}\n\nfunc Caller() {\n\tTarget()\n}\n' >a/a.go
  printf 'package a\n\nfunc Other() {}\n' >a/other.go
  printf 'package a\n\nimport "testing"\n\nfunc TestTarget(t *testing.T) {\n\tTarget()\n}\n' >a/a_test.go
  printf 'package b\n\nimport (\n\t"testing"\n\n\t"example.com/m/a"\n)\n\nfunc TestUses(t *testing.T) {\n\ta.Target()\n}\n' >b/b_test.go
  printf 'package c\n\nfunc Local() {}\n' >c/c.go
  printf 'package c\n\nimport (\n\t"testing"\n\n\t"example.com/m/a"\n)\n\nfunc TestLocal(t *testing.T) {\n\ta.Other()\n}\n' >c/c_test.go
  printf '# global owner\n*       @everyone\n/a/*    @a-owner\na/*.go  @go-owner\n' >.github/CODEOWNERS
  git add -A
  GIT_AUTHOR_DATE=2024-04-01T00:00:00Z GIT_COMMITTER_DATE=2024-04-01T00:00:00Z \
    git commit -qm 'graph fixture'

  printf 'package a\n\nfunc Target() {}\n\nfunc Caller() {\n\tTarget()\n}\n\nfunc Second() {}\n' >a/a.go
  printf 'package b\n\nimport (\n\t"testing"\n\n\t"example.com/m/a"\n)\n\nfunc TestUses(t *testing.T) {\n\ta.Target()\n}\n\nfunc TestMore(t *testing.T) {\n\ta.Target()\n}\n' >b/b_test.go
  git add -A
  GIT_AUTHOR_DATE=2024-04-02T00:00:00Z GIT_COMMITTER_DATE=2024-04-02T00:00:00Z \
    git commit -qm 'step two'

  printf 'package a\n\nfunc Target() {}\n\nfunc Caller() {\n\tTarget()\n}\n\nfunc Second() {}\n\nfunc Third() {}\n' >a/a.go
  printf 'package c\n\nimport (\n\t"testing"\n\n\t"example.com/m/a"\n)\n\nfunc TestLocal(t *testing.T) {\n\ta.Other()\n}\n\nfunc TestMore(t *testing.T) {\n\ta.Other()\n}\n' >c/c_test.go
  git add -A
  GIT_AUTHOR_DATE=2024-04-03T00:00:00Z GIT_COMMITTER_DATE=2024-04-03T00:00:00Z \
    git commit -qm 'step three'

  printf 'package a\n\nfunc Target() {}\n\nfunc Caller() {\n\tTarget()\n}\n\nfunc Second() {}\n\nfunc Third() {}\n\nfunc Fourth() {}\n' >a/a.go
  printf 'package b\n\nimport (\n\t"testing"\n\n\t"example.com/m/a"\n)\n\nfunc TestUses(t *testing.T) {\n\ta.Target()\n}\n\nfunc TestMore(t *testing.T) {\n\ta.Target()\n}\n\nfunc TestThird(t *testing.T) {\n\ta.Target()\n}\n' >b/b_test.go
  git add -A
  GIT_AUTHOR_DATE=2024-04-04T00:00:00Z GIT_COMMITTER_DATE=2024-04-04T00:00:00Z \
    git commit -qm 'step four'
)

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

run_review index build --json
assert_exit "index build exits 0" 0
assert_json_field "four commits build one tree" 6 '.data.files_indexed'

run_review index tests-for a/a.go --json
assert_exit "index tests-for exits 0" 0
assert_envelope "tests-for envelope"
assert_json_field "tests-for kind" "tests-for" '.data.kind'
assert_json_field "three candidate tests" 3 '.data.counts.tests'
assert_json_field "candidates in path order" "a/a_test.go,b/b_test.go,c/c_test.go" \
  '[.data.rows[].path] | join(",")'
assert_json_field "one via kind per candidate" "same-package,references,imports-module" \
  '[.data.rows[].via] | join(",")'
assert_json_field "a non-test file is never a candidate" 0 \
  '[.data.rows[] | select(.path | endswith("_test.go") | not)] | length'
assert_json_field "target owners are the last matching rule" "@go-owner" \
  '[.data.target.owners[]] | join(",")'
assert_json_field "target names the CODEOWNERS source" ".github/CODEOWNERS" '.data.target.owners_source'

run_review index tests-for a/a.go
assert_exit "human tests-for exits 0" 0
assert_file_grep "human names the same-package test" "$EVAL_OUT" '^a/a_test.go (same-package)$'
assert_file_grep "human names the referencing test" "$EVAL_OUT" '^b/b_test.go (references)$'
assert_file_grep "human names the importing test" "$EVAL_OUT" '^c/c_test.go (imports-module)$'
assert_file_nogrep "the non-test source file is absent" "$EVAL_OUT" '^c/c.go '

run_review index tests-for --symbol a.Target --json
assert_exit "symbol tests-for exits 0" 0
assert_json_field "symbol query finds the same three candidates" "a/a_test.go,b/b_test.go,c/c_test.go" \
  '[.data.rows[].path] | join(",")'
assert_json_field "symbol query publishes the symbol" "a.Target" '.data.query.symbol'

run_review index owners a/a.go --json
assert_exit "index owners exits 0" 0
assert_json_field "owners kind" "owners" '.data.kind'
assert_json_field "last matching rule wins" "@go-owner" '[.data.target.owners[]] | join(",")'
assert_json_field "owners source" ".github/CODEOWNERS" '.data.target.owners_source'

# An uncommitted edit to the worktree copy changes nothing: owners read the
# tree's blob.
printf '* @worktree\n' >"$EVAL_SANDBOX/.github/CODEOWNERS"
run_review index owners a/a.go --json
assert_exit "owners after a worktree edit exits 0" 0
assert_json_field "the worktree copy is never read" "@go-owner" '[.data.target.owners[]] | join(",")'

run_review index co-changed a/a.go --json
assert_exit "index co-changed exits 0" 0
assert_envelope "co-changed envelope"
assert_json_field "co-changed kind" "co-changed" '.data.kind'
assert_json_field "the window echoes four commits" 4 '.data.window.commits'
assert_json_field "the window counts the target's commits" 4 '.data.counts.target_commits'
assert_json_field "two partners" 2 '.data.counts.partners'
assert_json_field "partner rows carry literal counts and jaccard" \
  "b/b_test.go 3 0.75,c/c_test.go 2 0.5" \
  '[.data.rows[] | "\(.path) \(.commits) \(.jaccard)"] | join(",")'

run_review index co-changed a/a.go
assert_exit "human co-changed exits 0" 0
assert_file_grep "human names the first partner" "$EVAL_OUT" '^b/b_test.go 3 commit(s) jaccard 0.75$'
assert_file_grep "human names the second partner" "$EVAL_OUT" '^c/c_test.go 2 commit(s) jaccard 0.5$'

# The window cache is deterministic bytes: deleting it and re-running writes
# the same file.
# awk drains find's output; `head -n 1` exits early and pipefail would abort on
# find's SIGPIPE (STA-378).
cache="$(find "$EVAL_SANDBOX/.verdict/index/cochange" -name '*.json' | awk 'NR == 1')"
cp "$cache" "$EVAL_SANDBOX/cache.before"
rm -rf "$EVAL_SANDBOX/.verdict/index/cochange"
run_review index co-changed a/a.go --json
assert_exit "co-changed after a cache delete exits 0" 0
cache_after="$(find "$EVAL_SANDBOX/.verdict/index/cochange" -name '*.json' | awk 'NR == 1')"
if cmp -s "$EVAL_SANDBOX/cache.before" "$cache_after"; then
  good "the rebuilt window cache is byte-identical"
else
  bad "the rebuilt window cache differs"
fi
assert_json_field "the rebuilt window answers the same rows" \
  "b/b_test.go 3 0.75,c/c_test.go 2 0.5" \
  '[.data.rows[] | "\(.path) \(.commits) \(.jaccard)"] | join(",")'

run_review index tests-for nope.go --json
assert_exit "an unindexed path refuses" 1
assert_refusal "tests-for names the refusal code" "bad-flags"
assert_json_field "the refusal names the path and tree" 1 \
  '[.error.message | select(test("nope.go is not an indexed Go file at "))] | length'

run_review index co-changed nope.go --json
assert_exit "an unknown co-change path refuses" 1
assert_refusal "co-changed names the refusal code" "bad-flags"

run_review index owners nope.go --json
assert_exit "an unknown owner path refuses" 1
assert_refusal "owners names the refusal code" "bad-flags"

# A query creates nothing: with no .verdict at all, co-changed answers from git
# and leaves the checkout as it found it.
rm -rf "$EVAL_SANDBOX/.verdict"
run_review index co-changed a/a.go --json
assert_exit "co-changed without an index store exits 0" 0
assert_envelope "co-changed without a store envelope"
assert_json_field "co-changed answers from git alone" 2 '.data.counts.partners'
assert_json_field "the missing cache is named once" \
  "» index: co-change cache not written: the index store is absent (run verdict index build)" \
  '.warnings | join(",")'
if [ -e "$EVAL_SANDBOX/.verdict" ]; then
  bad "a read-only co-changed created .verdict"
else
  good "a read-only co-changed wrote nothing into the checkout"
fi

stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
assert_eq "no host calls" "$stub_before" "$stub_after"

eval_end
