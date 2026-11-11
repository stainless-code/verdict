#!/usr/bin/env bash
#
# index callers: the Go index builds from a tree, answers exact and lexical
# call sites, warns when the queried tree is stale, and refuses a truncated
# shard. Stdlib parsing only; no network.
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
  mkdir -p a b c lib
  printf 'package a\n\nfunc Target() {}\n\nfunc Caller() {\n\tTarget()\n}\n' >a/a.go
  printf 'package b\n\nimport "example.com/m/a"\n\nfunc Uses() {\n\ta.Target()\n}\n' >b/b.go
  printf 'package c\n\nfunc Target() {}\n\nfunc Local() {\n\tTarget()\n}\n' >c/c.go
  printf 'package lib\n\nfunc Local() {}\n' >lib/lib.go
  git add -A
  GIT_AUTHOR_DATE=2024-04-01T00:00:00Z GIT_COMMITTER_DATE=2024-04-01T00:00:00Z \
    git commit -qm 'index fixture'
)
OLD_TREE="$(cd "$EVAL_SANDBOX" && git rev-parse 'HEAD^{tree}')"

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

run_review index build --json
assert_exit "index build exits 0" 0
assert_envelope "index build envelope"
assert_json_field "build verb" "index" '.verb'
assert_json_field "build kind" "build" '.data.kind'
assert_json_field "four Go files indexed" 4 '.data.files_indexed'
assert_json_field "nothing skipped" 0 '.data.skipped'
assert_json_field "everything parsed on the first build" 4 '.data.parsed'

run_review index status --json
assert_exit "index status exits 0" 0
assert_envelope "index status envelope"
assert_json_field "status kind" "status" '.data.kind'
assert_json_field "HEAD tree is indexed" true '.data.built'
assert_json_field "status is not stale" false '.data.stale'
assert_json_field "one stored tree" 1 '.data.trees | length'

run_review index callers a.Target --json
assert_exit "index callers exits 0" 0
assert_envelope "callers envelope"
assert_json_field "callers kind" "callers" '.data.kind'
assert_json_field "two call sites" 2 '.data.counts.callers'
assert_json_field "first row is exact in a" "exact" '.data.rows[0].resolution'
assert_json_field "first row path" "a/a.go" '.data.rows[0].path'
assert_json_field "first row line" 6 '.data.rows[0].line'
assert_json_field "second row is lexical through the import" "lexical" '.data.rows[1].resolution'
assert_json_field "second row path" "b/b.go" '.data.rows[1].path'
assert_json_field "second row line" 6 '.data.rows[1].line'
assert_json_field "no unresolved candidates" 0 '.data.counts.unresolved'
assert_json_field "rows are not truncated" 0 '.data.truncated'

run_review index callers a.Target
assert_exit "human callers exits 0" 0
assert_file_grep "human output names both call sites" "$EVAL_OUT" '^a/a.go:6 call (exact)$'
assert_file_grep "human output names the lexical site" "$EVAL_OUT" '^b/b.go:6 call (lexical)$'
assert_file_nogrep "the non-importing same-named local is absent" "$EVAL_OUT" '^c/c.go:'

# One new commit makes HEAD's tree unindexed: the old tree still answers, marks
# itself stale, and warns with both tree ids.
(
  cd "$EVAL_SANDBOX" || exit 99
  printf 'package a\n\nfunc Target() {}\n\nfunc Caller() {\n\tTarget()\n}\n\nfunc Second() {\n\tTarget()\n}\n' >a/a.go
  git add -A
  GIT_AUTHOR_DATE=2024-04-02T00:00:00Z GIT_COMMITTER_DATE=2024-04-02T00:00:00Z \
    git commit -qm 'second caller'
)
run_review index callers a.Target --at "$OLD_TREE" --json
assert_exit "a stale tree still answers" 0
assert_envelope "stale callers envelope"
assert_json_field "the answer is marked stale" true '.data.index.stale'
assert_json_field "the stale warning is in warnings" "1" \
  '[.warnings[] | select(test("built for .*HEAD is"))] | length'

# A truncated shard refuses instead of reading as an empty index.
shard="$(find "$EVAL_SANDBOX/.verdict/index/shards" -name '*.json' | head -n 1)"
head -c 5 /dev/zero >"$shard"
run_review index build --json
assert_exit "truncated shard refuses" 1
assert_refusal "truncated shard keeps the envelope contract" "index-corrupt"
assert_json_field "the refusal names the shard" "1" \
  '[.error.message | select(test("is truncated or invalid"))] | length'

stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
assert_eq "no host calls" "$stub_before" "$stub_after"

eval_end
