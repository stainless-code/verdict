#!/usr/bin/env bash
#
#

_verdict_verbs() {
  printf '%s\n' fetch post triage report next show verify doctor calibrate fetch-local record adopt fix check-tree ready gate threads trend learn init skill upgrade uninstall config
}

_verdict_flags() { # $1 = verb
  case "$1" in
    fetch) printf '%s\n' --base-sha --since --provider --consent-over-budget --json;;
    post) printf '%s\n' --approve --request-changes --bind --force --review-file --dry-run --json;;
    triage) printf '%s\n' --sig --status --rationale --force --dry-run --json;;
    report) printf '%s\n' --status --kind --output --sarif --cost --check --json;;
    next) printf '%s\n' --status --json;;
    show) printf '%s\n' --sig --json;;
    check-tree) printf '%s\n' --expect;;
    verify) printf '%s\n' --timeout --json;;
    config) printf '%s\n' --explain --preview --file --json;;
    doctor|calibrate|record|trend) printf '%s\n' --json;;
    threads) printf '%s\n' --provider --json;;
    fetch-local) printf '%s\n' --staged --range --base --stash --consent-over-budget --json;;
    learn) printf '%s\n' --rule --scope --id --provenance --rigor --status --retire --restore --reason --json;;
    adopt) printf '%s\n' --from --json;;
    fix) printf '%s\n' --sig --json;;
    ready) printf '%s\n' --json --check;;
    gate) printf '%s\n' --max-severity --require-head --live --json;;
    init) printf '%s\n' --preset --yes --force --json;;
    skill) printf '%s\n' show files install --format --dir --agent --force --dry-run --json;;
    upgrade) printf '%s\n' --version --dry-run --json;;
    uninstall) printf '%s\n' --remove-path --dry-run --json;;
    *) return 0;;
  esac
}

_verdict_values() { # $1 = verb, $2 = flag
  case "$2" in
    --provider) printf '%s\n' github gitlab gitcafe forgejo bitbucket azure;;
    --check)
      case "$1" in
        report) printf '%s\n' critical important informational;;
        *) return 0;;
      esac;;
    --kind) printf '%s\n' inline section all;;
    --rigor)
      case "$1" in
        learn) printf '%s\n' said pointed walked ran reproduced;;
        *) return 0;;
      esac;;
    --approve) printf '%s\n' true false;;
    --preset) printf '%s\n' chill standard strict;;
    --max-severity) printf '%s\n' critical important informational;;
    --live) printf '%s\n' auto strict skip;;
    --status)
      case "$1" in
        triage) printf '%s\n' open addressed wont-fix false-positive deferred;;
        report) printf '%s\n' all open addressed deferred wont-fix false-positive;;
        *) printf '%s\n' open all;;
      esac;;
    *) return 0;;
  esac
}

_verdict_is_verb() {
  local w="$1" v
  while IFS= read -r v; do
    if [[ "$v" == "$w" ]]; then return 0; fi
  done <<<"$(_verdict_verbs)"
  return 1
}

_verdict_bash_complete() {
  local cur prev verb vals v
  COMPREPLY=()
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev=""
  verb=""
  if [ "$COMP_CWORD" -ge 1 ]; then prev="${COMP_WORDS[COMP_CWORD-1]}"; fi
  if [ "$COMP_CWORD" -ge 2 ]; then verb="${COMP_WORDS[1]}"; fi
  if ! _verdict_is_verb "$verb"; then verb=""; fi
  if [[ -z "$verb" ]]; then
    while IFS= read -r v; do
      if [[ "$v" == "$cur"* ]]; then COMPREPLY+=("$v"); fi
    done <<<"$(_verdict_verbs)"
    return 0
  fi
  vals=$(_verdict_values "$verb" "$prev")
  if [[ -n "$vals" ]]; then
    while IFS= read -r v; do
      if [[ "$v" == "$cur"* ]]; then COMPREPLY+=("$v"); fi
    done <<<"$vals"
    return 0
  fi
  if [[ "$cur" == -* ]]; then
    while IFS= read -r v; do
      if [[ "$v" == "$cur"* ]]; then COMPREPLY+=("$v"); fi
    done <<<"$(_verdict_flags "$verb")"
    return 0
  fi
  return 0
}

_verdict_zsh_complete() {
  # words/CURRENT are provided by the zsh completion system at call time.
  # shellcheck disable=SC2154
  local cur="${words[CURRENT]}"
  # shellcheck disable=SC2154
  local prev="${words[CURRENT-1]}"
  # shellcheck disable=SC2154
  local verb="${words[2]}"
  local vals v
  if ! _verdict_is_verb "$verb"; then verb=""; fi
  if [[ -z "$verb" ]]; then
    while IFS= read -r v; do
      case "$v" in "$cur"*) compadd -- "$v";; esac
    done <<<"$(_verdict_verbs)"
    return 0
  fi
  vals=$(_verdict_values "$verb" "$prev")
  if [[ -n "$vals" ]]; then
    while IFS= read -r v; do
      case "$v" in "$cur"*) compadd -- "$v";; esac
    done <<<"$vals"
    return 0
  fi
  if [[ "$cur" == -* ]]; then
    while IFS= read -r v; do
      case "$v" in "$cur"*) compadd -- "$v";; esac
    done <<<"$(_verdict_flags "$verb")"
    return 0
  fi
  return 0
}

if [[ -n "${BASH_VERSION:-}" ]]; then
  complete -F _verdict_bash_complete verdict 2>/dev/null || true
elif [[ -n "${ZSH_VERSION:-}" ]]; then
  compdef _verdict_zsh_complete verdict 2>/dev/null || true
fi
