#!/usr/bin/env bash
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

FIXTURE_PACK="$HERE/fixtures/pack-test"
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q -b main
)

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

run_review pack test --dir "$FIXTURE_PACK" --json
assert_exit "fixture directory test reports its failing case" 1
assert_envelope "pack test --dir envelope"
assert_json_field "pack test --dir verb" "pack" '.verb'
assert_json_field "fixture directory resolves name" "acme/pack-test" '.data.pack'
assert_json_field "fixture passing case passes" "PASS" '.data.cases[] | select(.case == "passing") | .status'
assert_json_field "fixture failing case fails" "FAIL" '.data.cases[] | select(.case == "failing") | .status'
assert_json_field "fixture case summary" "1:0:1" '.data.summary | "\(.pass):\(.skip):\(.fail)"'
assert_json_field "fixture failure names unselected rule" "expected rule is not selected: fixture-rule" \
  '.data.cases[] | select(.case == "failing") | .reason'

run_review pack add "$FIXTURE_PACK" --json
assert_exit "fixture pack add succeeds" 0
assert_envelope "pack add envelope"
assert_json_field "fixture pack is installed" "acme/pack-test" '.data.pack.name'

run_review pack test acme/pack-test --json
assert_exit "installed fixture reports its failing case" 1
assert_envelope "installed pack test envelope"
assert_json_field "installed pack test resolves name" "acme/pack-test" '.data.pack'
assert_json_field "installed passing case passes" "PASS" '.data.cases[] | select(.case == "passing") | .status'
assert_json_field "installed failing case fails" "FAIL" '.data.cases[] | select(.case == "failing") | .status'
assert_json_field "installed case summary" "1:0:1" '.data.summary | "\(.pass):\(.skip):\(.fail)"'

assert_file_grep "manual labels the pack case format pre-v1" "$EVAL_ROOT/docs/manual.md" 'pre-v1 design' "-F"

stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
assert_eq "pack test makes no host calls" "$stub_before" "$stub_after"

eval_end
