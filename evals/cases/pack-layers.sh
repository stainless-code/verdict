#!/usr/bin/env bash
#
# pack list: the three layers resolve in precedence order, an authorized
# downward override succeeds, an undeclared one refuses pack-conflict, and a
# missing org path refuses bad-pack. Reads local files only; no host calls.
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
  mkdir -p org-packs/lenses org-packs/rules .verdict/packs/repo/lenses .verdict/packs/repo/rules
  printf '%s' '{"schema_version":"1","name":"acme/org","version":"2.0.0","severity":{"security":"critical"},"lenses":[{"id":"security"}],"rules":[{"id":"no-secrets","severity":"security","globs":["**/*.go"]}]}' >org-packs/pack.json
  printf 'security body\n' >org-packs/lenses/security.md
  printf 'never log a secret\n' >org-packs/rules/no-secrets.md
  printf '%s' '{"schema_version":"1","name":"acme/repo","version":"0.1.0","replace":["acme/org"],"severity":{"style":"important"},"lenses":[{"id":"security"},{"id":"style"}],"rules":[{"id":"repo-style","severity":"style","globs":["**/*.go"],"tools":["gofmt"]}]}' >.verdict/packs/repo/pack.json
  printf 'repo security body\n' >.verdict/packs/repo/lenses/security.md
  printf 'repo style body\n' >.verdict/packs/repo/lenses/style.md
  printf 'run gofmt\n' >.verdict/packs/repo/rules/repo-style.md
  git add -A
  git commit -qm 'pack fixture'
)
printf '{"packs":["org-packs"]}\n' >"$HOME/.config/verdict/settings.json"

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

run_review pack list --json
assert_exit "pack list exits 0" 0
assert_envelope "pack list envelope"
assert_json_field "verb" "pack" '.verb'
assert_json_field "three layers" 3 '.data.packs | length'
assert_json_field "builtin row" "verdict/builtin" '.data.packs[0].name'
assert_json_field "builtin layer" "builtin" '.data.packs[0].layer'
assert_json_field "builtin hash" "23dac5dead2eb78613332103b244cafe21c6031bb8469c73bb1c5b1f1ed42cd5" '.data.packs[0].hash'
assert_json_field "builtin lenses" "api-contract,perf,test" '.data.packs[0].lenses | join(",")'
assert_json_field "builtin drift is null" "null" '.data.packs[0].drift'
assert_json_field "org row" "acme/org" '.data.packs[1].name'
assert_json_field "org source" "dir:org-packs" '.data.packs[1].source'
assert_json_field "org hash" "ec7b01d4ea7b319a383c633bb8c30b464ec5c9734c9b06c6db0ef6dec8218d7b" '.data.packs[1].hash'
assert_json_field "org rule" "no-secrets" '.data.packs[1].rules | join(",")'
assert_json_field "repo row" "acme/repo" '.data.packs[2].name'
assert_json_field "repo layer" "repo" '.data.packs[2].layer'
assert_json_field "repo source" ".verdict/packs/repo" '.data.packs[2].source'
assert_json_field "repo hash" "6017f42729f688b65d17401a27200f0406c1cf8b75d588ce65788850d7817edf" '.data.packs[2].hash'
assert_json_field "repo declares both lenses" "security,style" '.data.packs[2].lenses | join(",")'
assert_json_field "repo drift is missing until locked" "pack-lock-missing" '.data.packs[2].drift'

run_review pack list
assert_exit "human pack list exits 0" 0
assert_file_grep "human line names the builtin layer" "$EVAL_OUT" '^builtin  verdict/builtin@1.0.0  source=builtin  sha256:23dac5dead2e  lenses=api-contract,perf,test  rules=-$'
assert_file_grep "human line names the org layer" "$EVAL_OUT" '^org      acme/org@2.0.0  source=dir:org-packs  sha256:ec7b01d4ea7b  lenses=security  rules=no-secrets$'
assert_file_grep "human line names the repo layer" "$EVAL_OUT" '^repo     acme/repo@0.1.0  source=.verdict/packs/repo  sha256:6017f42729f6  lenses=security,style  rules=repo-style  drift=pack-lock-missing$'

# An undeclared override of a built-in lens refuses pack-conflict and names
# the exact declaration to add.
(
  cd "$EVAL_SANDBOX" || exit 99
  printf '%s' '{"schema_version":"1","name":"acme/repo","version":"0.1.0","lenses":[{"id":"perf"}]}' >.verdict/packs/repo/pack.json
  printf 'repo perf\n' >.verdict/packs/repo/lenses/perf.md
)
run_review pack list --json
assert_exit "undeclared override refuses" 1
assert_refusal "undeclared override keeps the envelope contract" "pack-conflict"
assert_json_field "refusal names the replace entry" "1" \
  '[.error.message | select(contains("replace") and contains("verdict/builtin"))] | length'

# Declaring the lower pack authorizes the same override.
(
  cd "$EVAL_SANDBOX" || exit 99
  printf '%s' '{"schema_version":"1","name":"acme/repo","version":"0.1.0","replace":["verdict/builtin"],"lenses":[{"id":"perf"}]}' >.verdict/packs/repo/pack.json
)
run_review pack list --json
assert_exit "authorized override exits 0" 0
assert_envelope "authorized override envelope"
assert_json_field "repo row still declares perf" "perf" '.data.packs[2].lenses | join(",")'

# A listed org path that carries no pack refuses bad-pack instead of reading
# as an empty layer.
printf '{"packs":["absent"]}\n' >"$HOME/.config/verdict/settings.json"
run_review pack list --json
assert_exit "missing org path refuses" 1
assert_refusal "missing org path keeps the envelope contract" "bad-pack"
assert_json_field "refusal names the path" "1" \
  '[.error.message | select(contains("org pack path") and contains("absent"))] | length'

stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
assert_eq "no host calls" "$stub_before" "$stub_after"

eval_end
