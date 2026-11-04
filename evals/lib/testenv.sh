#!/usr/bin/env bash
# evals/lib/testenv.sh — deterministic, secret-free environment for the
# offline contract evals. Sourced by evals/run.sh and evals/one.sh.
#
# This is the shell twin of internal/testenv: both sides pin the same git
# configuration and drop the same credential-shaped variables, so a developer
# shell or CI runner cannot change what a fixture commit does or hand a
# "hermetic" case a live token.
#
#   testenv_init <sandbox>  scrub the environment, then export GIT_CONFIG_*
#                           (the empty global config lives in <sandbox>).
#   TESTENV_SCRUBBED        number of variables removed, for the runner log.
set -euo pipefail

TESTENV_SCRUBBED=0

# testenv_is_secret <lowercased-name> — the suffix rule from
# internal/envsecret: strip trailing rotation digits/underscores, then match.
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

# testenv_scrub drops harness knobs and credential-shaped variables from the
# exported environment. GIT_CONFIG_* is dropped here and re-exported by
# testenv_git_config, so an inherited GIT_CONFIG_COUNT cannot shadow ours.
testenv_scrub() {
  local line name lower
  while IFS= read -r line; do
    name=${line%%=*}
    [ -n "$name" ] || continue
    lower=$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')
    case "$name" in
      GH_* | GITHUB_* | AWS_* | REVIEW_* | GIT_CONFIG_* | XDG_CONFIG_HOME | NO_COLOR) ;;
      *)
        if ! testenv_is_secret "$lower"; then continue; fi
        ;;
    esac
    unset "$name"
    TESTENV_SCRUBBED=$((TESTENV_SCRUBBED + 1))
  done < <(env)
}

# testenv_git_config exports the fixed repository configuration: no system
# config, an empty global config in the sandbox, and command-scope keys that
# beat any repo-local setting.
testenv_git_config() {
  local sandbox="$1" n=0
  : >"$sandbox/gitconfig-empty"
  export GIT_CONFIG_GLOBAL="$sandbox/gitconfig-empty"
  export GIT_CONFIG_NOSYSTEM=1
  set -- core.autocrlf false commit.gpgsign false tag.gpgsign false \
    init.defaultBranch main core.longpaths true
  while [ "$#" -gt 0 ]; do
    export "GIT_CONFIG_KEY_$n=$1" "GIT_CONFIG_VALUE_$n=$2"
    n=$((n + 1))
    shift 2
  done
  export GIT_CONFIG_COUNT="$n"
}

# testenv_init <sandbox> — both halves, in the order that matters: scrub
# first, then export our own git config.
testenv_init() {
  local sandbox="$1"
  [ -d "$sandbox" ] || {
    echo "testenv: sandbox missing: $sandbox" >&2
    return 2
  }
  testenv_scrub
  testenv_git_config "$sandbox"
}
