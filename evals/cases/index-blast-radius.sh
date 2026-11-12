#!/usr/bin/env bash
#
# index blast-radius: changed declarations from --paths and from a fetched
# bundle diff, callers to depth 2 (a known depth-3 caller and a call cycle prove
# the cap and the visited set), candidate tests, co-change partners, owners,
# per-list truncation counts, and null counts when the index is absent. No
# network.
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
  mkdir -p a b c d .github
  printf 'package a\n\nfunc A() {}\n\nfunc B() {}\n' >a/ab.go
  printf 'package a\n\nfunc Top() {\n\tA()\n\tB()\n}\n' >a/top.go
  printf 'package a\n\ntype holder struct{}\n\nfunc Use() {\n\tvar h holder\n\th.Top()\n}\n' >a/use.go
  printf 'package a\n\nfunc Ping() {\n\tPong()\n}\n\nfunc Pong() {\n\tPing()\n}\n' >a/cycle.go
  printf 'package a\n\nimport "testing"\n\nfunc TestA(t *testing.T) {\n\tA()\n}\n' >a/a_test.go
  printf 'package b\n\nimport "example.com/m/a"\n\nfunc UseA() {\n\ta.A()\n}\n' >b/b.go
  printf 'package b\n\nimport (\n\t"testing"\n\n\t"example.com/m/a"\n)\n\nfunc TestUsesA(t *testing.T) {\n\ta.A()\n}\n' >b/b_test.go
  printf 'package c\n\nimport "example.com/m/b"\n\nfunc Deep() {\n\tb.UseA()\n}\n' >c/c.go
  printf 'package d\n\nimport "example.com/m/c"\n\nfunc Deeper() {\n\tc.Deep()\n}\n' >d/d.go
  printf '*       @everyone\n/a/*    @a-owner\na/*.go  @go-owner\n' >.github/CODEOWNERS
  git add -A
  GIT_AUTHOR_DATE=2024-04-01T00:00:00Z GIT_COMMITTER_DATE=2024-04-01T00:00:00Z \
    git commit -qm 'one'

  # a/ab.go moves with b's test in step two and with c's file in step three,
  # so the co-change counts are fixture data.
  printf 'package a\n\nfunc A() {\n\t_ = 1\n}\n\nfunc B() {}\n' >a/ab.go
  printf 'package b\n\nimport (\n\t"testing"\n\n\t"example.com/m/a"\n)\n\nfunc TestUsesA(t *testing.T) {\n\ta.A()\n}\n\nfunc TestMore(t *testing.T) {\n\ta.A()\n}\n' >b/b_test.go
  git add -A
  GIT_AUTHOR_DATE=2024-04-02T00:00:00Z GIT_COMMITTER_DATE=2024-04-02T00:00:00Z \
    git commit -qm 'two'

  printf 'package a\n\nfunc A() {\n\t_ = 1\n}\n\nfunc B() {}\n\nfunc Third() {}\n' >a/ab.go
  printf 'package c\n\nimport "example.com/m/b"\n\nfunc Deep() {\n\tb.UseA()\n}\n\nfunc More() {}\n' >c/c.go
  git add -A
  GIT_AUTHOR_DATE=2024-04-03T00:00:00Z GIT_COMMITTER_DATE=2024-04-03T00:00:00Z \
    git commit -qm 'three'
)

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

run_review index build --json
assert_exit "index build exits 0" 0
assert_json_field "nine files indexed" 9 '.data.files_indexed'

run_review index blast-radius --paths a/ab.go --json
assert_exit "blast-radius --paths exits 0" 0
assert_envelope "blast-radius envelope"
assert_json_field "blast-radius kind" "blast-radius" '.data.kind'
assert_json_field "the input names the one path" "a/ab.go" '[.data.input.paths[]] | join(",")'
assert_json_field "no PR named" "null" '.data.input.pr'
assert_json_field "three changed declarations" 3 '.data.symbols.count'
assert_json_field "changed declarations in source order" "A,B,Third" \
  '[.data.symbols.rows[].name] | join(",")'
assert_json_field "seven callers within depth 2" 7 '.data.callers.count'
assert_json_field "one candidate the index could not attribute" 1 '.data.callers.unresolved'
assert_json_field "the depth-2 caller is reachable through the import chain" \
  "c/c.go 6 lexical 2" \
  '[.data.callers.rows[] | select(.depth == 2) | "\(.path) \(.line) \(.resolution) \(.depth)"] | join(",")'
assert_json_field "no depth-3 caller appears" 0 \
  '[.data.callers.rows[] | select(.path == "d/d.go")] | length'
assert_json_field "two candidate tests" 2 '.data.tests.count'
assert_json_field "candidates carry the strongest mechanism" "a/a_test.go,b/b_test.go" \
  '[.data.tests.rows[].path] | join(",")'
assert_json_field "two co-change partners" 2 '.data.co_changed.count'
assert_json_field "the partner count names distinct files, not pairs" 2 '.data.co_changed.partners'
assert_json_field "partners carry literal counts and jaccard" \
  "b/b_test.go 2 0.6667,c/c.go 2 0.6667" \
  '[.data.co_changed.rows[] | "\(.path) \(.commits) \(.jaccard)"] | join(",")'
assert_json_field "the changed path's owners" "@go-owner" \
  '[.data.owners.rows[].owners[]] | join(",")'

run_review index blast-radius --paths a/cycle.go --json
assert_exit "a call cycle exits 0" 0
assert_json_field "the cycle answers one row per edge" 2 '.data.callers.count'
assert_json_field "the cycle rows are both depth 1" "1,1" \
  '[.data.callers.rows[].depth] | join(",")'

run_review index blast-radius --paths a/ab.go --limit 2 --json
assert_exit "a truncated blast-radius exits 0" 0
assert_json_field "the count is the whole list" 7 '.data.callers.count'
assert_json_field "the truncation count names the hidden rows" 5 '.data.callers.truncated'
assert_json_field "two rows are shown" 2 '[.data.callers.rows[]] | length'

run_review index blast-radius --paths a/ab.go
assert_exit "the human blast-radius exits 0" 0
assert_file_grep "the human header counts the changed declarations" "$EVAL_OUT" \
  '^index: 1 changed file(s), 3 changed symbol(s) at '
assert_file_grep "the human caller row names the depth" "$EVAL_OUT" \
  '^c/c.go:6 call (lexical, depth 2) example.com/m/a.A$'

run_review index blast-radius --paths a/ab.go --limit 2
assert_exit "the truncated human blast-radius exits 0" 0
assert_file_grep "the human truncation line counts what it hid" "$EVAL_OUT" \
  '^index: showing 2 of 7 callers (5 not shown; raise --limit up to 200)$'

# The bundle leg: the diff names the touched lines, so the changed symbols come
# from the declaration spans rather than the whole file.
(
  cd "$EVAL_SANDBOX" || exit 99
  git diff HEAD~2 HEAD -- a/ab.go >bundle.diff
  head_sha="$(git rev-parse HEAD)"
  base_sha="$(git rev-parse HEAD~2)"
  mkdir -p .verdict/state/7
  mv bundle.diff .verdict/state/7/full.diff
  printf '{"state":"OPEN","headRefOid":"%s","baseRefOid":"%s","author":{"login":"t"}}\n' \
    "$head_sha" "$base_sha" >.verdict/state/7/PR.json
)
run_review index blast-radius --pr 7 --json
assert_exit "blast-radius --pr exits 0" 0
assert_json_field "the input names the PR" 7 '.data.input.pr | tonumber'
assert_json_field "the input names the bundle head" "$(cd "$EVAL_SANDBOX" && git rev-parse HEAD)" \
  '.data.input.head'
assert_json_field "the diff's changed paths are the input paths" "a/ab.go" \
  '[.data.input.paths[]] | join(",")'
assert_json_field "the hunk's declarations are the changed symbols" 3 '.data.symbols.count'
assert_json_field "the changed declarations are A, B, and Third" "A,B,Third" \
  '[.data.symbols.rows[].name] | join(",")'

# With no index the symbol, caller, and test counts read null, never 0, while
# the git-measured lists still answer; the query writes nothing into the
# checkout.
rm -rf "$EVAL_SANDBOX/.verdict/index"
run_review index blast-radius --paths a/ab.go --json
assert_exit "blast-radius without an index exits 0" 0
assert_json_field "no index object" "false" '.data | has("index")'
assert_json_field "symbol count is null" "null" '.data.symbols.count'
assert_json_field "caller count is null" "null" '.data.callers.count'
assert_json_field "test count is null" "null" '.data.tests.count'
assert_json_field "truncation counts are null" "null" '.data.callers.truncated'
assert_json_field "co-change is still measured" 2 '.data.co_changed.count'
assert_json_field "owners are still measured" 1 '.data.owners.count'
assert_json_field "the absent index is named once" "true" \
  '.warnings[0] | startswith("» index: no index for tree ")'

run_review index blast-radius --paths a/ab.go --limit 0 --json
assert_exit "an out-of-range limit exits 2" 2
run_review index blast-radius --json
assert_exit "no input exits 2" 2
run_review index blast-radius --paths nope.go --json
assert_exit "an unknown path refuses" 1
assert_refusal "the unknown path names bad-flags" "bad-flags"
run_review index blast-radius --pr 99 --json
assert_exit "a missing bundle refuses" 1
assert_refusal "the missing bundle names the code" "missing-bundle"

stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
assert_eq "no host calls" "$stub_before" "$stub_after"

eval_end
