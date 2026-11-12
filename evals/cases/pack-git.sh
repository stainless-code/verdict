#!/usr/bin/env bash
#
# pack add <https-url>: a stand-in git on PATH answers the hermetic clone with
# a pack tree, so the real CLI, vendor, lock, and pack list path run without a
# network or a real remote. Local files only; no host calls.
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
HASH=9c3212f38c42adef87365072cf069492544eeb43c9825880acd1aac69a826e23
HEAD_SHA=cccccccccccccccccccccccccccccccccccccccc
URL=https://example.invalid/acme/packs.git
SIG=b8ee6d1cc08e7bfe076481e111b3716e18644b7fc27ceb86c0b41390a15e85ef
GIT_LOG="$EVAL_SANDBOX/git-calls.log"
REAL_GIT="$(command -v git)"
: >"$GIT_LOG"
printf '%s' "$MANIFEST" >"$EVAL_SANDBOX/source-pack.json"

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q -b main
  printf 'top\nold line\nbottom\n' >notes.txt
  git add -A
  git commit -qm 'pack fixture'
)

cat >"$EVAL_STUBBIN/git" <<STUB
#!/usr/bin/env bash
set -uo pipefail
log='$GIT_LOG'
real='$REAL_GIT'
manifest='$MANIFEST'
head='$HEAD_SHA'
sub=''
for arg in "\$@"; do
  case "\$arg" in clone|rev-parse|checkout) sub="\$arg"; break;; esac
done
{
  printf 'CALL\n'
  for arg in "\$@"; do printf 'ARG %s\n' "\$arg"; done
  if [ "\$sub" = clone ]; then
    printf 'ENV HOME=%s\n' "\${HOME-}"
    printf 'ENV GIT_CONFIG_NOSYSTEM=%s\n' "\${GIT_CONFIG_NOSYSTEM-}"
    printf 'ENV GIT_TERMINAL_PROMPT=%s\n' "\${GIT_TERMINAL_PROMPT-}"
  fi
} >>"\$log"
case "\$sub" in
  clone)
    for last; do :; done
    mkdir -p "\$last/lenses" "\$last/.git/hooks"
    printf '%s' "\$manifest" >"\$last/pack.json"
    printf 'perf body\n' >"\$last/lenses/perf.md"
    printf 'planted\n' >"\$last/.git/config"
    printf '#!/bin/sh\n' >"\$last/.git/hooks/post-checkout"
    ;;
  rev-parse)
    case " \$* " in
      *" rev-parse HEAD "*) printf '%s\n' "\$head" ;;
      *) exec "\$real" "\$@" ;;
    esac
    ;;
  checkout) ;;
  *) exec "\$real" "\$@" ;;
esac
STUB
chmod +x "$EVAL_STUBBIN/git"

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

# The real pack add path runs against the stand-in clone.
run_review pack add "$URL"
assert_exit "pack add against the stand-in git" 0
assert_file_grep "the human row names the resolved commit" "$EVAL_OUT" \
  "^added acme/lenses@1.0.0 source=git:$URL@$HEAD_SHA sha256:$HASH\$"
assert_file_eq "the vendored manifest is the clone bytes" \
  "$EVAL_SANDBOX/source-pack.json" \
  "$EVAL_SANDBOX/.verdict/packs/lenses/pack.json"
assert_json_field "the lock records the git source" "git:$URL@$HEAD_SHA" \
  '.packs[0].source' "$EVAL_SANDBOX/.verdict/verdict.lock"
assert_json_field "the lock records the tree hash" "$HASH" \
  '.packs[0].hash' "$EVAL_SANDBOX/.verdict/verdict.lock"
assert_json_field "the lock records the integrity sig" "$SIG" \
  '.packs[0].sig' "$EVAL_SANDBOX/.verdict/verdict.lock"
assert_eq "the clone metadata is not vendored" "absent" \
  "$([ -e "$EVAL_SANDBOX/.verdict/packs/lenses/.git" ] && echo present || echo absent)"

# The stand-in saw the hermetic clone argv and scrubbed environment.
assert_file_grep "the stand-in git receives clone" "$GIT_LOG" '^ARG clone$'
assert_file_grep "the clone carries --depth=1" "$GIT_LOG" '^ARG --depth=1$'
assert_file_grep "the clone carries --no-tags" "$GIT_LOG" '^ARG --no-tags$'
assert_file_grep "the clone disables file transport" "$GIT_LOG" '^ARG protocol.file.allow=never$'
assert_file_grep "the clone names the source URL" "$GIT_LOG" "^ARG $URL$"
assert_file_grep "the clone runs with the hermetic config" "$GIT_LOG" '^ENV GIT_CONFIG_NOSYSTEM=1$'
assert_file_grep "the clone disables terminal prompts" "$GIT_LOG" '^ENV GIT_TERMINAL_PROMPT=0$'
assert_file_grep "the clone HOME is the scratch tree" "$GIT_LOG" '^ENV HOME=.*verdict-pack-.*/home$'
assert_file_nogrep "the caller HOME does not reach git" "$GIT_LOG" "ENV HOME=$HOME" -F

# pack list reads the vendored tree and the pinned lock.
run_review pack list --json
assert_exit "pack list after the stand-in clone" 0
assert_json_field "the repo row names its vendored tree" ".verdict/packs/lenses" \
  '.data.packs[] | select(.layer == "repo") | .source'
assert_json_field "the repo row keeps the tree hash" "$HASH" \
  '.data.packs[] | select(.layer == "repo") | .hash'
assert_json_field "the repo row is not drifted" "null" \
  '.data.packs[] | select(.layer == "repo") | .drift'
run_review pack list
assert_exit "human pack list after the stand-in clone" 0
assert_file_grep "the human row names the vendored tree" "$EVAL_OUT" \
  "^repo     acme/lenses@1.0.0  source=.verdict/packs/lenses  sha256:9c3212f38c42  lenses=perf  rules=-\$"

stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
assert_eq "no host calls" "$stub_before" "$stub_after"

eval_end
