#!/usr/bin/env bash
# verdict installer: doctor-gated symlink into harness skill dirs.
# Mini `npx skills` for this repo: link, list, check, uninstall — one skill, no
# registry. Bare run links every harness (skipping occupied slots); narrow with
# --agent. No auto-updater by design — `git pull` keeps symlinked installs current.
set -euo pipefail

SRC="$(cd "$(dirname "$0")" && pwd)"

usage() {
  cat <<EOF
usage: install.sh [--agent NAME]... [--all] [--force] [--uninstall] [--list] [--check] [--help]

  (no flags)     link into every known harness dir (skips occupied slots)
  --agent NAME   only this harness (repeatable): agents claude cursor opencode
                 copilot gemini goose qwen windsurf
  --all          every harness (the default)
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

agent_dir() { # $1 = name; prints its skills dir
  case "$1" in
    agents) echo "$HOME/.agents/skills" ;;
    claude) echo "$HOME/.claude/skills" ;;
    cursor) echo "$HOME/.cursor/skills" ;;
    opencode) echo "$HOME/.config/opencode/skills" ;;
    copilot) echo "$HOME/.copilot/skills" ;;
    gemini) echo "$HOME/.gemini/skills" ;;
    goose) echo "$HOME/.config/goose/skills" ;;
    qwen) echo "$HOME/.qwen/skills" ;;
    windsurf) echo "$HOME/.codeium/windsurf/skills" ;;
    *) return 1 ;;
  esac
}

SCOPE=""
FORCE=0
MODE="install"
while [ $# -gt 0 ]; do
  case "$1" in
    --agent)
      [ $# -ge 2 ] || die "--agent needs a name"
      agent_dir "$2" >/dev/null || die "unknown agent: $2"
      SCOPE="$SCOPE $2"
      shift 2
      ;;
    --all)
      SCOPE="agents claude cursor opencode copilot gemini goose qwen windsurf"
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
[ -n "$SCOPE" ] || SCOPE="agents claude cursor opencode copilot gemini goose qwen windsurf"

if [ "$MODE" = "list" ]; then
  for a in $SCOPE; do
    d="$(agent_dir "$a")/verdict"
    if [ -L "$d" ] && [ "$(readlink "$d")" = "$SRC" ]; then
      echo "linked:   $a ($d)"
    elif [ -e "$d" ] || [ -L "$d" ]; then
      echo "occupied: $a ($d — not our link)"
    else
      echo "absent:   $a"
    fi
  done
  exit 0
fi

if [ "$MODE" = "install" ] || [ "$MODE" = "check" ]; then
  "$SRC/scripts/review.sh" doctor || die "fix the FAIL lines above, then re-run"
fi

if [ "$MODE" = "uninstall" ]; then
  n=0
  for a in $SCOPE; do
    d="$(agent_dir "$a")/verdict"
    if [ -L "$d" ] && [ "$(readlink "$d")" = "$SRC" ]; then
      rm "$d" && echo "removed: $a" && n=$((n + 1))
    else
      echo "skip: $a (not our link)"
    fi
  done
  echo "done: removed $n link(s)."
  exit 0
fi

if [ "$MODE" = "check" ]; then
  bad=0
  for a in $SCOPE; do
    d="$(agent_dir "$a")/verdict"
    if [ -L "$d" ] && [ "$(readlink "$d")" = "$SRC" ]; then
      echo "ok: $a"
    else
      echo "FAIL: $a ($d missing or foreign)"
      bad=$((bad + 1))
    fi
  done
  [ "$bad" = 0 ] || exit 1
  echo "check: scope ok."
  exit 0
fi

linked=0
skipped=0
link_into() { # $1 = agent name
  dir="$(agent_dir "$1")"
  dest="$dir/verdict"
  mkdir -p "$dir"
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
  ln -s "$SRC" "$dest" && echo "linked: $1 ($dest -> $SRC)"
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

for a in $SCOPE; do link_into "$a"; done
wire_hooks
echo "done: $linked linked, $skipped skipped. update anytime: git -C \"$SRC\" pull (symlinks stay current)."
