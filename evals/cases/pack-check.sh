#!/usr/bin/env bash
#
# pack check: one path resolves through the shipped builtin packs, the org and
# repo layers, and the trusted-base pack configuration. The base commit is the
# merge base with the default branch, so a branch commit that tries to disable
# the builtin layer changes nothing here. Reads local files only; no host calls.
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
mkdir -p "$HOME/.config/verdict"

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q -b main
  mkdir -p .verdict
  # The base commit turns the shell pack off: the operator's own decision, and
  # the one this checkout must honor.
  printf '%s' '{"schema_version":"1","packs":{"disable_packs":["verdict/shell"]}}' >.verdict/config.json
  git add -A
  git commit -qm 'base: disable the shell pack'
  git checkout -q -b feature
  # A branch commit that tries to silence the whole builtin layer must not
  # reach the resolution.
  printf '%s' '{"schema_version":"1","packs":{"merge_builtin":false,"disable_packs":["verdict/go"],"disable_lenses":["go"]}}' >.verdict/config.json
  git add -A
  git commit -qm 'head: try to disable the builtin packs'
)

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

run_review pack check cmd/app/main.go --json
assert_exit "pack check exits 0" 0
assert_envelope "pack check envelope"
assert_json_field "pack check verb" "pack" '.verb'
assert_json_field "pack check names the path" "cmd/app/main.go" '.data.path'
assert_json_field "pack check reads the trusted base, not the branch" "1" \
  '[.data.config | select(startswith("base@"))] | length'
assert_json_field "the branch cannot merge the builtin layer off" "1" \
  '[.data.packs[] | select(.name == "verdict/go")] | length'
assert_json_field "the base turns one pack off" "pack:verdict/shell" '.data.disabled | join(",")'
assert_json_field "the disabled pack is gone" "0" \
  '[.data.packs[] | select(.name == "verdict/shell")] | length'
assert_json_field "every remaining builtin pack merges" "11" \
  '[.data.packs[] | select(.layer == "builtin")] | length'
assert_json_field "the go lens is offered for a Go file" "1" \
  '[.data.lenses[] | select(.id == "go" and .pack == "verdict/go" and .layer == "builtin")] | length'
assert_json_field "the security lens rides beside it" "1" \
  '[.data.lenses[] | select(.id == "security" and .pack == "verdict/security")] | length'
assert_json_field "the go rules apply by glob" \
  "go-context-lifecycle,go-error-loss,go-resource-release,go-shared-state" \
  '[.data.rules[] | select(.pack == "verdict/go") | .id] | join(",")'
assert_json_field "the go rules resolve their severity" "critical,important,important,critical" \
  '[.data.rules[] | select(.pack == "verdict/go") | .severity] | join(",")'
assert_json_field "a rust rule stays out of a Go path" "0" \
  '[.data.rules[] | select(.pack == "verdict/rust")] | length'

# A TypeScript path resolves the TypeScript pack and none of the Go rules.
run_review pack check app/view.tsx --json
assert_exit "pack check resolves a second stack" 0
assert_json_field "ts-js lens offered" "1" \
  '[.data.lenses[] | select(.id == "ts-js" and .pack == "verdict/ts-js")] | length'
assert_json_field "ts-js rules apply" "1" \
  '[.data.rules[] | select(.id == "ts-js-floating-async")] | length'
assert_json_field "no Go rule reaches a TypeScript path" "0" \
  '[.data.rules[] | select(.pack == "verdict/go")] | length'

# The human rows read the same resolution.
run_review pack check scripts/deploy.sh
assert_exit "human pack check exits 0" 0
assert_file_grep "human row names the path" "$EVAL_OUT" '^path     scripts/deploy.sh$'
assert_file_grep "human row names the base config" "$EVAL_OUT" '^config   base@'
assert_file_grep "human row names the applied toggle" "$EVAL_OUT" '^disabled pack:verdict/shell$'
assert_file_grep "human rows still offer the core lenses" "$EVAL_OUT" '^  api-contract  pack=verdict/builtin  layer=builtin  sha256:'
assert_file_nogrep "the disabled pack's rules are absent" "$EVAL_OUT" 'sh-unquoted-expansion'

# A path that is not a repository member refuses with the typed code.
run_review pack check ../outside.go --json
assert_exit "an escaping path refuses" 1
assert_refusal "escaping path keeps the envelope contract" "bad-path"

# A missing path is a usage error, not a silent list.
run_review pack check
assert_exit "missing path is a usage error" 2
assert_file_grep "usage names the subcommand" "$EVAL_ERR" 'usage: verdict pack'

# A flag-shaped argument is named back instead of read as a path.
run_review pack check --bogus --json
assert_exit "unknown flag is a usage error" 2
assert_file_grep "usage names the flag" "$EVAL_ERR" '--bogus'

stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
assert_eq "no host calls" "$stub_before" "$stub_after"

eval_end
