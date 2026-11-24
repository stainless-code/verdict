#!/bin/sh
# A local "this exact commit passed the full pre-push bar" record, so
# `hooks/pre-push` does not repeat a suite that just went green.
#
#   verify-stamp.sh clean          exit 0 when the working tree is clean
#   verify-stamp.sh write [sha]    record HEAD (refuses a dirty tree, or a HEAD
#                                  that is not `sha` when one is given)
#   verify-stamp.sh check <sha>... exit 0 only when EVERY sha has a valid stamp
#                                  whose tree matches and the tree is clean;
#                                  prints `<sha> <epoch>` per sha
#
# The stamp is one `<sha> <tree> <epoch>` line per verified commit in
# `<git-dir>/verdict-verified`: inside .git, never committed, per worktree.
# Any line that is not exactly that shape is ignored. Callers: `make
# verify-stamp` (runs scripts/prepush-checks.sh first), the factory ladder after
# a green t2, and hooks/pre-push (`check`).
set -eu

usage() { echo "usage: verify-stamp.sh clean | write [sha] | check <sha>..." >&2; exit 2; }

[ $# -ge 1 ] || usage
cmd="$1"
shift

git_dir="$(git rev-parse --absolute-git-dir)"
stamp="$git_dir/verdict-verified"
keep=32 # newest stamps retained

tree_clean() { [ -z "$(git status --porcelain --untracked-files=normal)" ]; }

is_hex() {
  case "$1" in '' | *[!0-9a-f]*) return 1 ;; esac
  [ "${#1}" -eq 40 ] || [ "${#1}" -eq 64 ]
}

# stamped_at <sha>: print the epoch of a valid stamp for sha, else fail.
stamped_at() {
  want="$1"
  tree="$(git rev-parse --verify --quiet "$want^{tree}")" || return 1
  [ -f "$stamp" ] || return 1
  while read -r s t ts extra; do
    [ -z "$extra" ] || continue
    is_hex "$s" && is_hex "$t" || continue
    case "$ts" in '' | *[!0-9]*) continue ;; esac
    if [ "$s" = "$want" ] && [ "$t" = "$tree" ]; then
      printf '%s\n' "$ts"
      return 0
    fi
  done <"$stamp"
  return 1
}

case "$cmd" in
  clean)
    tree_clean || { echo "verify-stamp: working tree is dirty; commit or stash first" >&2; exit 1; }
    ;;
  write)
    [ $# -le 1 ] || usage
    tree_clean || { echo "verify-stamp: working tree is dirty; nothing stamped" >&2; exit 1; }
    head="$(git rev-parse --verify HEAD)"
    if [ $# -eq 1 ] && [ "$head" != "$1" ]; then
      echo "verify-stamp: HEAD moved from $1 to $head during the run; nothing stamped" >&2
      exit 1
    fi
    tree="$(git rev-parse --verify "HEAD^{tree}")"
    tmp="$stamp.$$"
    {
      if [ -f "$stamp" ]; then grep -v "^$head " "$stamp" || true; fi
      printf '%s %s %s\n' "$head" "$tree" "$(date +%s)"
    } | tail -n "$keep" >"$tmp"
    mv "$tmp" "$stamp"
    echo "verify-stamp: $(printf %.12s "$head") stamped in $stamp"
    ;;
  check)
    [ $# -ge 1 ] || usage
    tree_clean || exit 1
    out=""
    for sha in "$@"; do
      ts="$(stamped_at "$sha")" || exit 1
      out="$out$sha $ts
"
    done
    printf '%s' "$out"
    ;;
  *) usage ;;
esac
