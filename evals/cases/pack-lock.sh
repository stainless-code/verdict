#!/usr/bin/env bash
#
# pack add + verdict.lock: a repo pack with no lock refuses pack-lock-missing
# at fetch, add vendors and pins it, a re-add replaces the tree and reports
# old -> new, adding from inside .verdict/packs refuses, pack list reports
# drift, and a drifted lock refuses at fetch. Local files only; no host calls.
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

MANIFEST='{"schema_version":"1","name":"acme/lenses","version":"1.0.0","replace":["verdict/builtin"],"lenses":[{"id":"perf"}]}'
HASH_V1=9c3212f38c42adef87365072cf069492544eeb43c9825880acd1aac69a826e23
SIG_V1=c5445ca251d13315eed47a494f331f62169604873c333f55f975eabcaa06e11e
HASH_V2=6c56942b22e4bc3e000b9c68b317029690ce7433476afa5cf7f5ffe0ed2677ec
SIG_V2=32784acbd7bb21010a0802d61a608a18defc599ecfa2a091706c985ea3d254c3

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q -b main
  mkdir -p .verdict/packs/lenses/lenses src-packs/lenses/lenses
  printf '%s' "$MANIFEST" >.verdict/packs/lenses/pack.json
  printf 'perf body\n' >.verdict/packs/lenses/lenses/perf.md
  printf '%s' "$MANIFEST" >src-packs/lenses/pack.json
  printf 'perf body\n' >src-packs/lenses/lenses/perf.md
  printf 'top\nold line\nbottom\n' >notes.txt
  git add -A
  git commit -qm 'pack without a lock'
  printf 'top\nnew line\nbottom\n' >notes.txt
)

# A repo pack with no lock entry refuses before a review runs.
run_review fetch-local acme/app lock-missing --json
assert_exit "fetch-local refuses a repo pack with no lock" 1
assert_refusal "missing lock keeps the envelope contract" "pack-lock-missing"
assert_json_field "missing lock names the pack" "1" \
  '[.error.message | select(contains("acme/lenses"))] | length'

# pack add validates the source, vendors it, and writes the strict lock.
run_review pack add src-packs/lenses
assert_exit "pack add vendors and locks" 0
assert_file_grep "pack add prints the added row" "$EVAL_OUT" \
  "^added acme/lenses@1.0.0 source=dir:src-packs/lenses sha256:$HASH_V1\$"
assert_file_eq "the vendored manifest is the source bytes" \
  "$EVAL_SANDBOX/src-packs/lenses/pack.json" "$EVAL_SANDBOX/.verdict/packs/lenses/pack.json"
assert_json_field "lock schema version" "1" '.schema_version' "$EVAL_SANDBOX/.verdict/verdict.lock"
assert_json_field "lock records the source" "dir:src-packs/lenses" '.packs[0].source' "$EVAL_SANDBOX/.verdict/verdict.lock"
assert_json_field "lock records the tree hash" "$HASH_V1" '.packs[0].hash' "$EVAL_SANDBOX/.verdict/verdict.lock"
assert_json_field "lock records the integrity sig" "$SIG_V1" '.packs[0].sig' "$EVAL_SANDBOX/.verdict/verdict.lock"

# Re-adding replaces the tree and reports old -> new.
printf 'perf body v2\n' >"$EVAL_SANDBOX/src-packs/lenses/lenses/perf.md"
run_review pack add src-packs/lenses
assert_exit "re-add replaces the pack" 0
assert_file_grep "re-add reports old -> new" "$EVAL_OUT" \
  "^updated acme/lenses@1.0.0 source=dir:src-packs/lenses sha256:$HASH_V1 -> sha256:$HASH_V2\$"
assert_json_field "lock moves to the new hash" "$HASH_V2" '.packs[0].hash' "$EVAL_SANDBOX/.verdict/verdict.lock"
assert_json_field "lock moves to the new sig" "$SIG_V2" '.packs[0].sig' "$EVAL_SANDBOX/.verdict/verdict.lock"
assert_file_grep "the vendored body is the replacement" "$EVAL_SANDBOX/.verdict/packs/lenses/lenses/perf.md" '^perf body v2$'

# A source inside the vendored tree refuses instead of copying onto itself.
run_review pack add .verdict/packs/lenses --json
assert_exit "add from inside .verdict/packs refuses" 1
assert_refusal "already-vendored source keeps the envelope contract" "bad-pack"
assert_json_field "already-vendored refusal says so" "1" \
  '[.error.message | select(contains("already vendored"))] | length'

# A URL credential refuses before the clone and never reaches the output.
TOKEN=ghp_SUPERSECRET_C5A2
run_review pack add "https://oauth2:$TOKEN@host/acme/packs.git" --json
assert_exit "credentialed source refuses" 1
assert_refusal "credential refusal keeps the envelope contract" "bad-pack"
assert_json_field "credential refusal masks the secret" "1" \
  '[.error.message | select(contains("***"))] | length'
assert_stream_nogrep "the token is not on stdout" "$EVAL_OUT" "$TOKEN"
assert_stream_nogrep "the token is not on stderr" "$EVAL_ERR" "$TOKEN"

# Commit the locked pack: fetch resolves it at base and uses it.
(
  cd "$EVAL_SANDBOX" || exit 99
  git add -A .verdict/packs .verdict/verdict.lock
  git commit -qm 'lock the repo pack'
  printf 'top\nnewer line\nbottom\n' >notes.txt
)
run_review fetch-local acme/app lock-ok --json
assert_exit "fetch-local accepts a matching lock" 0
assert_file_grep "the locked pack reaches the bundle" \
  "$EVAL_SANDBOX/.verdict/state/local-lock-ok/lenses.txt" '^# lens pack acme/lenses@1.0.0 .* sha256:'"$HASH_V2"

# A hand-edited lock hash shows as drift and refuses at fetch.
(
  cd "$EVAL_SANDBOX" || exit 99
  sed -i.bak "s/$HASH_V2/0000000000000000000000000000000000000000000000000000000000000000/" .verdict/verdict.lock
  rm -f .verdict/verdict.lock.bak
)
run_review pack list --json
assert_exit "pack list reports drift" 0
assert_json_field "repo drift is pack-lock-drift" "pack-lock-drift" \
  '.data.packs[] | select(.layer == "repo") | .drift'
run_review pack list
assert_exit "human pack list reports drift" 0
assert_file_grep "human row shows the drift code" "$EVAL_OUT" \
  'drift=pack-lock-drift$'

(
  cd "$EVAL_SANDBOX" || exit 99
  git add .verdict/verdict.lock
  git commit -qm 'drift the lock'
)
run_review fetch-local acme/app lock-drift --json
assert_exit "fetch-local refuses a drifted lock" 1
assert_refusal "drift keeps the envelope contract" "pack-lock-drift"
assert_json_field "drift names the recorded hash" "1" \
  '[.error.message | select(contains("0000000000000000000000000000000000000000000000000000000000000000"))] | length'

stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
assert_eq "no host calls" "$stub_before" "$stub_after"

eval_end
