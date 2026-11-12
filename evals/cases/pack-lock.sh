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
HASH_V1=7f53f7b36f0b2a75aea39e43fcb0c9fce11b9c76b57aeb2bef8991676ddf9d35
SIG_V1=29cecaecc9760d50cfc5c54bf13a5cfbec43c0464165ec534c73c28c2501e87c
HASH_V2=ef9184633096867f4a27af6b719188baa0a1813f74b105d24358bd7fd593ac96
SIG_V2=457d68cda9e45e3017a84acc4313e187ef604f66fe19e589e182e870708a74d8

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
lock_sum="$(shasum -a 256 "$EVAL_SANDBOX/.verdict/verdict.lock" | cut -d' ' -f1)"
TOKEN=ghp_SUPERSECRET_C5A2
run_review pack add "https://oauth2:$TOKEN@host/acme/packs.git" --json
assert_exit "credentialed source refuses" 1
assert_refusal "credential refusal keeps the envelope contract" "bad-pack"
assert_json_field "credential refusal masks the source" "1" \
  '[.error.message | select(contains("<invalid source>"))] | length'
assert_stream_nogrep "the token is not on stdout" "$EVAL_OUT" "$TOKEN"
assert_stream_nogrep "the token is not on stderr" "$EVAL_ERR" "$TOKEN"

# A token-only userinfo and a second @ are the same credential class.
run_review pack add "https://$TOKEN@host/acme/packs.git" --json
assert_exit "token-only source refuses" 1
assert_refusal "token-only refusal keeps the envelope contract" "bad-pack"
assert_json_field "token-only refusal masks the source" "1" \
  '[.error.message | select(contains("<invalid source>"))] | length'
assert_stream_nogrep "the token-only token is not on stdout" "$EVAL_OUT" "$TOKEN"
assert_stream_nogrep "the token-only token is not on stderr" "$EVAL_ERR" "$TOKEN"

run_review pack add "https://us@er:$TOKEN@host/x.git" --json
assert_exit "second-at source refuses" 1
assert_refusal "second-at refusal keeps the envelope contract" "bad-pack"
assert_json_field "second-at refusal masks the source" "1" \
  '[.error.message | select(contains("<invalid source>"))] | length'
assert_stream_nogrep "the second-at token is not on stdout" "$EVAL_OUT" "$TOKEN"
assert_stream_nogrep "the second-at token is not on stderr" "$EVAL_ERR" "$TOKEN"

# A (re-check): a one-slash scheme is a mangled URL, not a directory.
TOKEN_A=S3CRET_ONESLASH_C5A4
run_review pack add "https:/user:$TOKEN_A@host/acme/packs.git" --json
assert_exit "one-slash scheme source refuses" 1
assert_refusal "one-slash refusal keeps the envelope contract" "bad-pack"
assert_json_field "one-slash refusal masks the source" "1" \
  '[.error.message | select(contains("<invalid source>"))] | length'
assert_stream_nogrep "the one-slash token is not on stdout" "$EVAL_OUT" "$TOKEN_A"
assert_stream_nogrep "the one-slash token is not on stderr" "$EVAL_ERR" "$TOKEN_A"

# B (re-check): git percent-decodes the ssh user, so %3A is a password.
TOKEN_B=TOKSECRET_SSH_C5A4
run_review pack add "ssh://user%3A$TOKEN_B@host/acme/packs.git" --json
assert_exit "percent-colon ssh source refuses" 1
assert_refusal "percent-colon ssh refusal keeps the envelope contract" "bad-pack"
assert_json_field "percent-colon ssh refusal masks the source" "1" \
  '[.error.message | select(contains("<invalid source>"))] | length'
assert_stream_nogrep "the percent-colon ssh token is not on stdout" "$EVAL_OUT" "$TOKEN_B"
assert_stream_nogrep "the percent-colon ssh token is not on stderr" "$EVAL_ERR" "$TOKEN_B"

# The scp spelling of the same escape refuses too.
run_review pack add "user%3A$TOKEN_B@host:acme/packs.git" --json
assert_exit "percent-colon scp source refuses" 1
assert_refusal "percent-colon scp refusal keeps the envelope contract" "bad-pack"
assert_json_field "percent-colon scp refusal masks the source" "1" \
  '[.error.message | select(contains("<invalid source>"))] | length'
assert_stream_nogrep "the percent-colon scp token is not on stdout" "$EVAL_OUT" "$TOKEN_B"
assert_stream_nogrep "the percent-colon scp token is not on stderr" "$EVAL_ERR" "$TOKEN_B"

assert_eq "refused sources leave the lock untouched" "$lock_sum" \
  "$(shasum -a 256 "$EVAL_SANDBOX/.verdict/verdict.lock" | cut -d' ' -f1)"

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

# A' (re-check): the one-slash source as a hand-written lock row refuses
# instead of listing, on both pack list surfaces.
cp "$EVAL_SANDBOX/.verdict/verdict.lock" "$EVAL_SANDBOX/lock.bak"
TOKEN_LOCK=S3CRET_LOCKROW_C5A4
BAD_SRC="git:https:/user:$TOKEN_LOCK@host/x.git@cccccccccccccccccccccccccccccccccccccccc"
BAD_SIG="$(printf 'name=acme/lenses\nsource=%s\nversion=1.0.0\nhash=%s\n' "$BAD_SRC" "$HASH_V2" \
  | shasum -a 256 | cut -d' ' -f1)"
jq -n --arg src "$BAD_SRC" --arg hash "$HASH_V2" --arg sig "$BAD_SIG" \
  '{schema_version:"1",packs:[{name:"acme/lenses",source:$src,version:"1.0.0",hash:$hash,sig:$sig}]}' \
  >"$EVAL_SANDBOX/.verdict/verdict.lock"
run_review pack list --json
assert_exit "a one-slash lock row refuses pack list" 1
assert_refusal "the bad lock row keeps the envelope contract" "bad-pack"
assert_json_field "the bad lock row masks the source" "1" \
  '[.error.message | select(contains("<invalid source>"))] | length'
assert_stream_nogrep "the lock token is not on stdout" "$EVAL_OUT" "$TOKEN_LOCK"
assert_stream_nogrep "the lock token is not on stderr" "$EVAL_ERR" "$TOKEN_LOCK"
run_review pack list
assert_exit "the bad lock row refuses human pack list" 1
assert_stream_nogrep "the lock token is not on the human stderr" "$EVAL_ERR" "$TOKEN_LOCK"
mv "$EVAL_SANDBOX/lock.bak" "$EVAL_SANDBOX/.verdict/verdict.lock"

stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
assert_eq "no host calls" "$stub_before" "$stub_after"

eval_end
