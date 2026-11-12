#!/usr/bin/env bash
#
#
set -euo pipefail

TESTENV_SCRUBBED=0

testenv_is_secret() {
  local norm="$1"
  while :; do
    case "$norm" in
      *[0-9_]) norm=${norm%?} ;;
      *) break ;;
    esac
  done
  case "$norm" in
    *_token | *_secret | *_key | *_password | *_passphrase | *_pat) return 0 ;;
    *_auth | *_credential | *_credentials | *_pass | *_pwd) return 0 ;;
    *token | *secret | *key | *password) return 0 ;;
    aws_access_key_id | ssh_auth_sock | ssh_agent_pid) return 0 ;;
    *) return 1 ;;
  esac
}

testenv_scrub() {
  local line name lower
  while IFS= read -r line; do
    name=${line%%=*}
    [ -n "$name" ] || continue
    lower=$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')
    case "$name" in
      GH_* | GITHUB_* | AWS_* | REVIEW_* | GIT_CONFIG_* | XDG_CONFIG_HOME | NO_COLOR) ;;
      GIT_DIR | GIT_WORK_TREE | GIT_INDEX_FILE | GIT_PREFIX | GIT_OBJECT_DIRECTORY) ;;
      GIT_ALTERNATE_OBJECT_DIRECTORIES | GIT_COMMON_DIR) ;;
      GIT_AUTHOR_DATE | GIT_COMMITTER_DATE) ;;
      GIT_AUTHOR_NAME | GIT_AUTHOR_EMAIL | GIT_COMMITTER_NAME | GIT_COMMITTER_EMAIL) ;;
      *)
        if ! testenv_is_secret "$lower"; then continue; fi
        ;;
    esac
    unset "$name"
    TESTENV_SCRUBBED=$((TESTENV_SCRUBBED + 1))
  done < <(env)
}

testenv_git_config() {
  local sandbox="$1" n=0
  : >"$sandbox/gitconfig-empty"
  export GIT_CONFIG_GLOBAL="$sandbox/gitconfig-empty"
  export GIT_CONFIG_SYSTEM="$sandbox/gitconfig-empty"
  export GIT_CONFIG_NOSYSTEM=1
  set -- core.autocrlf false commit.gpgsign false tag.gpgsign false \
    init.defaultBranch main core.longpaths true maintenance.auto false gc.auto 0
  while [ "$#" -gt 0 ]; do
    export "GIT_CONFIG_KEY_$n=$1" "GIT_CONFIG_VALUE_$n=$2"
    n=$((n + 1))
    shift 2
  done
  export GIT_CONFIG_COUNT="$n"
  export GIT_AUTHOR_NAME=verdict-test GIT_AUTHOR_EMAIL=test@verdict.invalid \
    GIT_COMMITTER_NAME=verdict-test GIT_COMMITTER_EMAIL=test@verdict.invalid
}

testenv_init() {
  local sandbox="$1"
  [ -d "$sandbox" ] || {
    echo "testenv: sandbox missing: $sandbox" >&2
    return 2
  }
  testenv_scrub
  testenv_git_config "$sandbox"
}
