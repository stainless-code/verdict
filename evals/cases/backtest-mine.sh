#!/usr/bin/env bash
#
# backtest mine: SZZ candidates from a pinned local history, no model calls.
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

# A hermetic git: no system or user config, no signing, a fixed identity, and
# the same calendar dates the Go golden fixture pins. The commit ids below are
# the fixture's, not the machine's.
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

dated() {
  local date="$1"
  shift
  GIT_AUTHOR_DATE="$date" GIT_COMMITTER_DATE="$date" git "$@"
}

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q -b main
  printf 'package main\n\nimport "os"\n\nfunc main() {\n\tos.Remove("out.txt")\n}\n' >main.go
  dated 2024-01-01T00:00:00Z add main.go
  dated 2024-01-01T00:00:00Z commit -qm 'feat: base'
  git switch -qc feature
  printf 'package main\n\nimport "os"\n\nfunc main() {\n\tos.Remove("missing")\n}\n' >main.go
  dated 2024-01-02T00:00:00Z add main.go
  dated 2024-01-02T00:00:00Z commit -qm 'feat: remove the output'
  git switch -q main
  dated 2024-01-03T00:00:00Z merge --no-ff feature -m 'Merge pull request #2 from acme/feature' >/dev/null
  printf 'package main\n\nimport "os"\n\nfunc main() {\n\tif err := os.Remove("missing"); err != nil {\n\t\tpanic(err)\n\t}\n}\n' >main.go
  dated 2024-01-04T00:00:00Z add main.go
  dated 2024-01-04T00:00:00Z commit -qm 'fix: guard the removal'
)

BASE=84b31730499aa237889660981350fda2b3edc91d
FIX=29320a6e9fac8488fb678921021d68af35843beb
FEATURE=1d1518cf4adca036e1325e9666d54094dccacfa5
MERGE=e9c8f1d1d1363d94c4c766eacf3d1d6fc6e09eb8
RUN=29320a6-84b3173

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
run_review backtest mine --since "$BASE" --json
assert_exit "mine exits 0" 0
assert_envelope "mine envelope"
assert_json_field "verb" "backtest" '.verb'
assert_json_field "run id" "$RUN" '.data.run'
assert_json_field "mode" "mine" '.data.mode'
assert_json_field "method" "szz-blame-v1" '.data.method'
assert_json_field "since sha" "$BASE" '.data.since.sha'
assert_json_field "until sha" "$FIX" '.data.until.sha'
assert_json_field "candidate count" "1" '.data.mine.candidates | length'
assert_json_field "candidate head" "$FEATURE" '.data.mine.candidates[0].head'
assert_json_field "candidate base" "$BASE" '.data.mine.candidates[0].base'
assert_json_field "candidate landed_by" "$MERGE" '.data.mine.candidates[0].landed_by'
assert_json_field "candidate pr" "2" '.data.mine.candidates[0].pr'
assert_json_field "candidate reconstruction" "first-parent-merge" '.data.mine.candidates[0].reconstruction'
assert_json_field "candidate head_source" "merge-time" '.data.mine.candidates[0].head_source'
assert_json_field "candidate merged_at" "2024-01-03T00:00:00Z" '.data.mine.candidates[0].merged_at'
assert_json_field "candidate head_at" "2024-01-02T00:00:00Z" '.data.mine.candidates[0].head_at'
assert_json_field "candidate fix" "$FIX" '.data.mine.candidates[0].fixes[0]'
assert_json_field "bug path" "main.go" '.data.mine.candidates[0].bugs[0].path'
assert_json_field "bug line" "6" '.data.mine.candidates[0].bugs[0].line'
assert_json_field "bug kind" "missing-error-check" '.data.mine.candidates[0].bugs[0].kind'
assert_json_field "skipped count" "0" '.data.mine.skipped | length'
assert_json_field "artifact mine" ".verdict/backtest/$RUN/mine.json" '.data.artifacts.mine'

MINE="$EVAL_SANDBOX/.verdict/backtest/$RUN/mine.json"
cp "$MINE" "$EVAL_SANDBOX/mine.first.json"
run_review backtest mine --since "$BASE" --json
assert_exit "rerun exits 0" 0
assert_file_eq "rerun mine.json is byte-identical" "$EVAL_SANDBOX/mine.first.json" "$MINE"

cat >"$EVAL_SANDBOX/expected-human.txt" <<EOF
backtest mine $RUN  (84b3173..HEAD 29320a6)
  1d1518c  2024-01-03  #2  first-parent-merge  main.go:6 missing-error-check
skipped: none
not citable — SZZ-derived: candidates are blame-derived guesses, not verified defects
EOF
run_review backtest mine --since "$BASE"
assert_exit "human mode exits 0" 0
assert_file_eq "human output matches the fixed fixture" "$EVAL_SANDBOX/expected-human.txt" "$EVAL_OUT"

run_review backtest mine --since 18m --json
assert_exit "bare m refuses" 1
assert_refusal "bare m" "bad-since"

stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
assert_eq "zero host CLI calls" "$stub_before" "$stub_after"

eval_end
