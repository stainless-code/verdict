#!/usr/bin/env bash
# verdict installer: doctor-gated symlink into harness skill dirs.
# Mini `npx skills` for this repo: link, list, check, uninstall — one skill, no
# registry. Bare run links the shared standard dir only; add --detect for every
# harness with an existing skills setup, --all for everything, or --agent/--dir
# for exact scope. Explicit removals stick: nothing re-links unasked. No
# auto-updater by design — `git pull` keeps symlinked installs current.
set -euo pipefail

SRC="$(cd "$(dirname "$0")" && pwd)"
TAB="$(printf '\t')"

usage() {
  cat <<EOF
usage: install.sh [--agent NAME]... [--dir PATH]... [--project [DIR]] [--detect] [--all] [--force] [--uninstall] [--list] [--check] [--help]

  (no flags)     link ~/.agents/skills, the shared standard dir
  --agent NAME   only this harness (repeatable): agents claude cursor opencode
                 copilot gemini goose qwen windsurf
  --dir PATH     extra skills dir (repeatable, absolute path) — any agent,
                 present or future, that keeps skills in a directory
  --project [D]  link into DIR/.agents/skills (default: current directory)
  --detect       ~/.agents/skills plus every harness with an existing setup
  --all          every known harness
  --force        replace occupied non-directory slots; override foreign hooksPath
  --uninstall    remove our links in scope (never touches foreign files)
  --list         show link status in scope; no changes, no doctor
  --check        doctor + verify links in scope (exit 1 on problems)
  --help         this text
EOF
}

die() {
  echo "install: $*" >&2
  exit 1
}

config_home() {
  echo "${XDG_CONFIG_HOME:-$HOME/.config}"
}

agent_dir() { # $1 = name; prints its skills dir (env-aware like npx skills)
  case "$1" in
    agents) echo "$HOME/.agents/skills" ;;
    claude) echo "${CLAUDE_CONFIG_DIR:-$HOME/.claude}/skills" ;;
    cursor) echo "$HOME/.cursor/skills" ;;
    opencode) echo "$(config_home)/opencode/skills" ;;
    copilot) echo "$HOME/.copilot/skills" ;;
    gemini) echo "$HOME/.gemini/skills" ;;
    goose) echo "$(config_home)/goose/skills" ;;
    qwen) echo "$HOME/.qwen/skills" ;;
    windsurf) echo "$HOME/.codeium/windsurf/skills" ;;
    *) return 1 ;;
  esac
}

ALL="agents claude cursor opencode copilot gemini goose qwen windsurf"
SCOPE=""
DIRS=""
DETECT=0
FORCE=0
MODE="install"
while [ $# -gt 0 ]; do
  case "$1" in
    --agent)
      [ $# -ge 2 ] || die "--agent needs a name"
      agent_dir "$2" >/dev/null || die "unknown agent: $2 (try --dir PATH)"
      SCOPE="$SCOPE $2"
      shift 2
      ;;
    --dir)
      [ $# -ge 2 ] || die "--dir needs a path"
      case "$2" in
        /*) ;;
        *) die "--dir needs an absolute path: $2" ;;
      esac
      if [ -n "$DIRS" ]; then
        DIRS="$DIRS
$2"
      else
        DIRS="$2"
      fi
      shift 2
      ;;
    --project)
      if [ $# -ge 2 ] && [[ "$2" != --* ]]; then
        p="$2"
        shift 2
      else
        p="$PWD"
        shift
      fi
      p="$(cd "$p" && pwd)" || die "bad project dir"
      if [ -n "$DIRS" ]; then
        DIRS="$DIRS
$p/.agents/skills"
      else
        DIRS="$p/.agents/skills"
      fi
      ;;
    --detect)
      DETECT=1
      shift
      ;;
    --all)
      SCOPE="$ALL"
      shift
      ;;
    --force) FORCE=1; shift ;;
    --uninstall) MODE="uninstall"; shift ;;
    --list) MODE="list"; shift ;;
    --check) MODE="check"; shift ;;
    -h | --help) usage; exit 0 ;;
    *) die "unknown flag: $1 (see --help)" ;;
  esac
done
[ -n "$SCOPE$DIRS" ] || SCOPE="agents"

is_live() { # $1 = dir; true if a skills setup already exists there
  [ -d "$1" ] && [ -n "$(ls -A "$1" 2>/dev/null | head -n 1)" ]
}

if [ "$DETECT" = 1 ]; then
  [ -n "$SCOPE" ] || SCOPE="agents"
  for _a in claude cursor opencode copilot gemini goose qwen windsurf; do
    case " $SCOPE " in
      *" $_a "*) ;;
      *) if is_live "$(agent_dir "$_a")"; then SCOPE="$SCOPE $_a"; fi ;;
    esac
  done
fi

[ "$MODE" = "list" ] || [ -f "$SRC/SKILL.md" ] || die "not a verdict checkout (no SKILL.md in $SRC)"

scope_list() { # prints "label<TAB>dir" lines for names + raw dirs
  local a d
  for a in $SCOPE; do
    printf '%s\t%s\n' "$a" "$(agent_dir "$a")"
  done
  if [ -n "$DIRS" ]; then
    while IFS= read -r d; do
      [ -n "$d" ] || continue
      printf '%s\t%s\n' "$d" "$d"
    done <<<"$DIRS"
  fi
}

if [ "$MODE" = "list" ]; then
  while IFS= read -r line; do
    label="${line%%$TAB*}"
    d="${line#*$TAB}"
    if [ -L "$d/verdict" ] && [ "$(readlink "$d/verdict")" = "$SRC" ]; then
      echo "linked:   $label ($d)"
    elif [ -e "$d/verdict" ] || [ -L "$d/verdict" ]; then
      echo "occupied: $label ($d/verdict — not our link)"
    else
      echo "absent:   $label ($d)"
    fi
  done <<<"$(scope_list)"
  exit 0
fi

if [ "$MODE" = "install" ] || [ "$MODE" = "check" ]; then
  "$SRC/scripts/review.sh" doctor || die "fix the FAIL lines above, then re-run"
fi

if [ "$MODE" = "uninstall" ]; then
  n=0
  while IFS= read -r line; do
    label="${line%%$TAB*}"
    d="${line#*$TAB}"
    if [ -L "$d/verdict" ] && [ "$(readlink "$d/verdict")" = "$SRC" ]; then
      rm "$d/verdict" && echo "removed: $label" && n=$((n + 1))
      rmdir "$d" 2>/dev/null || true
    else
      echo "skip: $label (not our link)"
    fi
  done <<<"$(scope_list)"
  echo "done: removed $n link(s)."
  exit 0
fi

if [ "$MODE" = "check" ]; then
  bad=0
  while IFS= read -r line; do
    label="${line%%$TAB*}"
    d="${line#*$TAB}"
    if [ -L "$d/verdict" ] && [ "$(readlink "$d/verdict")" = "$SRC" ]; then
      echo "ok: $label"
    else
      echo "FAIL: $label ($d/verdict missing or foreign)"
      bad=$((bad + 1))
    fi
  done <<<"$(scope_list)"
  [ "$bad" = 0 ] || exit 1
  echo "check: scope ok."
  exit 0
fi

linked=0
skipped=0
link_into() { # $1 = label, $2 = dir
  mkdir -p "$2"
  dest="$2/verdict"
  if [ -L "$dest" ] && [ "$(readlink "$dest")" = "$SRC" ]; then
    echo "ok: $1 already linked"
    linked=$((linked + 1))
    return 0
  fi
  if [ -e "$dest" ] || [ -L "$dest" ]; then
    if [ "$FORCE" = 1 ] && [ ! -d "$dest" ]; then
      rm "$dest" && ln -s "$SRC" "$dest" && echo "replaced: $1 ($dest)"
      linked=$((linked + 1))
      return 0
    fi
    echo "SKIP: $1 occupied ($dest) — move it aside or use --force"
    skipped=$((skipped + 1))
    return 0
  fi
  ln -s "$SRC" "$dest" || die "symlink failed ($dest) — try --dir to a writable location"
  echo "linked: $1 ($dest -> $SRC)"
  linked=$((linked + 1))
}

wire_hooks() {
  git -C "$SRC" rev-parse --is-inside-work-tree >/dev/null 2>&1 || return 0
  cur="$(git -C "$SRC" config --local core.hooksPath 2>/dev/null || true)"
  if [ -z "$cur" ]; then
    git -C "$SRC" config core.hooksPath "$SRC/hooks" && echo "hooks: pre-commit wired ($SRC/hooks)"
  elif [ "$cur" = "$SRC/hooks" ]; then
    echo "hooks: already wired"
  elif [ "$FORCE" = 1 ]; then
    git -C "$SRC" config core.hooksPath "$SRC/hooks" && echo "hooks: overridden (was $cur)"
  else
    echo "hooks: SKIP foreign hooksPath ($cur) — use --force to override"
  fi
}

while IFS= read -r line; do
  label="${line%%$TAB*}"
  d="${line#*$TAB}"
  link_into "$label" "$d"
done <<<"$(scope_list)"
wire_hooks
echo "done: $linked linked, $skipped skipped. update anytime: git -C \"$SRC\" pull (symlinks stay current)."
