#!/usr/bin/env bash
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL=/dev/null
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
mkdir -p "$XDG_CONFIG_HOME" "$EVAL_SANDBOX/src" "$EVAL_SANDBOX/.verdict/state/42"
(
  cd "$EVAL_SANDBOX"
  git init -q -b main
  git config user.email test@verdict.invalid
  git config user.name verdict-test
  printf 'package main\n' > src/main.go
  git add src/main.go
  git -c commit.gpgsign=false commit -qm base
  printf 'package main\n// changed\n' > src/main.go
)
cp "$HERE/fixtures/learnings-propose/threads.json" "$EVAL_SANDBOX/.verdict/state/42/threads.json"
run_review learn --propose --from-bundle acme/widget 42 --dry-run --json
assert_exit "proposal preview succeeds" 0
assert_json_field "preview finds two candidates" 2 '.data.rules | length'
if [ -d "$EVAL_SANDBOX/.verdict/learnings" ]; then bad "preview wrote proposals"; else good "preview writes nothing"; fi
run_review learn --propose --from-bundle acme/widget 42 --json
assert_exit "propose succeeds offline" 0
ACCEPT_ID="$(jq -r '.data.rules[0].id' "$EVAL_OUT")"
REJECT_ID="$(jq -r '.data.rules[1].id' "$EVAL_OUT")"
run_review fetch-local acme/widget before --json -- src/main.go
assert_exit "fetch before acceptance" 0
if [ -f "$EVAL_SANDBOX/.verdict/state/local-before/kb.txt" ]; then bad "inactive proposal recalled"; else good "inactive proposal not recalled"; fi
run_review learn --accept "$ACCEPT_ID" --dry-run --json
assert_exit "accept preview succeeds" 0
if [ -f "$EVAL_SANDBOX/.verdict/kb.jsonl" ]; then bad "accept preview wrote KB"; else good "accept preview writes nothing"; fi
run_review learn --accept "$ACCEPT_ID" --json
assert_exit "accept succeeds" 0
assert_json_field "accepted source" "learning:$ACCEPT_ID" '.data.rule.source'
run_review learn --reject "$REJECT_ID" --reason 'This concern does not apply to the current implementation.' --json
assert_exit "reject succeeds" 0
run_review learn --propose --from-bundle acme/widget 42 --json
assert_exit "rerun succeeds" 0
assert_json_field "accepted id skipped" 1 '[.data.rules[] | select(.action == "skipped-kb")] | length'
assert_json_field "rejected id skipped" 1 '[.data.rules[] | select(.action == "skipped-rejected")] | length'
(
 cd "$EVAL_SANDBOX"
 git add .verdict/kb.jsonl
 git -c commit.gpgsign=false commit -qm "accept human learning"
)
run_review fetch-local acme/widget after --json -- src/main.go
assert_exit "fetch after acceptance" 0
assert_file_grep "accepted learning recalled" "$EVAL_SANDBOX/.verdict/state/local-after/kb.txt" "$ACCEPT_ID" '-F'
assert_stream_nogrep "rejected learning never recalled" "$EVAL_SANDBOX/.verdict/state/local-after/kb.txt" "$REJECT_ID" '-F'
if [ -d "$EVAL_SANDBOX/.verdict/learnings/$REJECT_ID" ]; then bad "rejected proposal remains"; else good "rejected proposal removed"; fi
assert_file_grep "rejection tombstone retained" "$EVAL_SANDBOX/.verdict/learning-rejections/$REJECT_ID.json" '"status":"rejected"' '-F'
assert_eq "no host calls" 0 "$(wc -l < "$EVAL_STUBLOG" | tr -d ' ')"
eval_end
