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
# GO_HASH is the first 12 hex digits of the shipped verdict/go pack tree hash;
# a manifest edit moves it, so it pins the curated pack the layer ships.
GO_HASH="0df05aa03881"
export HOME="$EVAL_SANDBOX/home"
mkdir -p "$HOME/.config/verdict"

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q -b main
  mkdir -p org-packs/lenses org-packs/rules .verdict/packs/repo/lenses .verdict/packs/repo/rules
  printf '%s' '{"schema_version":"1","name":"acme/org","version":"2.0.0","severity":{"security":"critical"},"lenses":[{"id":"scan"}],"rules":[{"id":"no-secrets","severity":"security","globs":["**/*.go"]}]}' >org-packs/pack.json
  printf 'scan body\n' >org-packs/lenses/scan.md
  printf 'never log a secret\n' >org-packs/rules/no-secrets.md
  printf '%s' '{"schema_version":"1","name":"acme/repo","version":"0.1.0","replace":["acme/org"],"severity":{"style":"important"},"lenses":[{"id":"scan"},{"id":"style"}],"rules":[{"id":"repo-style","severity":"style","globs":["**/*.go"],"tools":["gofmt"]}]}' >.verdict/packs/repo/pack.json
  printf 'repo scan body\n' >.verdict/packs/repo/lenses/scan.md
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
assert_json_field "every layer's packs load" 15 '.data.packs | length'
assert_json_field "the builtin layer ships the core and twelve stacks" 13 \
  '[.data.packs[] | select(.layer == "builtin")] | length'
assert_json_field "the core pack leads" "verdict/builtin" '.data.packs[0].name'
assert_json_field "builtin layer" "builtin" '.data.packs[0].layer'
assert_json_field "core hash" "120173839a932dde740ff69014e9659669818dbedfc8f36124a26d90c624bc48" \
  '.data.packs[] | select(.name == "verdict/builtin") | .hash'
assert_json_field "core lenses" "api-contract,perf,test" '.data.packs[0].lenses | join(",")'
assert_json_field "core drift is null" "null" '.data.packs[0].drift'
assert_json_field "the go pack ships its lens and rules" "go:go-context-lifecycle,go-defer-loop,go-error-loss,go-mutex-misuse,go-resource-release,go-shared-state,go-slice-aliasing" \
  '.data.packs[] | select(.name == "verdict/go") | "\(.lenses | join(",")):\(.rules | join(","))"'
assert_json_field "the go pack source names its directory" "builtin:go" \
  '.data.packs[] | select(.name == "verdict/go") | .source'
assert_json_field "org row" "acme/org" '.data.packs[13].name'
assert_json_field "org source" "dir:org-packs" '.data.packs[13].source'
assert_json_field "org hash" "f9bde8f8e6cddc596cdb6c18e2ed2774cf086fe1c9780e66fd46b97777dcc130" '.data.packs[13].hash'
assert_json_field "org rule" "no-secrets" '.data.packs[13].rules | join(",")'
assert_json_field "repo row" "acme/repo" '.data.packs[14].name'
assert_json_field "repo layer" "repo" '.data.packs[14].layer'
assert_json_field "repo source" ".verdict/packs/repo" '.data.packs[14].source'
assert_json_field "repo hash" "3807d265381ccee6dcbe1e918f58f6a541cb39d96df0baf08a9076f9aa37b512" '.data.packs[14].hash'
assert_json_field "repo declares both lenses" "scan,style" '.data.packs[14].lenses | join(",")'
assert_json_field "repo drift is missing until locked" "pack-lock-missing" '.data.packs[14].drift'

run_review pack list
assert_exit "human pack list exits 0" 0
assert_file_grep "human line names the builtin layer" "$EVAL_OUT" '^builtin  verdict/builtin@1.0.0  source=builtin  sha256:120173839a93  lenses=api-contract,perf,test  rules=-$'
assert_file_grep "human line names a curated pack" "$EVAL_OUT" '^builtin  verdict/go@1.1.0  source=builtin:go  sha256:'"$GO_HASH"'  lenses=go  rules=go-context-lifecycle,go-defer-loop,go-error-loss,go-mutex-misuse,go-resource-release,go-shared-state,go-slice-aliasing$'
assert_file_grep "human line names the org layer" "$EVAL_OUT" '^org      acme/org@2.0.0  source=dir:org-packs  sha256:f9bde8f8e6cd  lenses=scan  rules=no-secrets$'
assert_file_grep "human line names the repo layer" "$EVAL_OUT" '^repo     acme/repo@0.1.0  source=.verdict/packs/repo  sha256:3807d265381c  lenses=scan,style  rules=repo-style  drift=pack-lock-missing$'

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
assert_json_field "repo row still declares perf" "perf" \
  '.data.packs[] | select(.name == "acme/repo") | .lenses | join(",")'

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
