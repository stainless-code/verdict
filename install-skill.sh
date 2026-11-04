#!/usr/bin/env bash
# verdict skill installer: doctor-gated symlink into agent skill dirs.
# (The binary installer is ./install.sh; this script stays checkout-only.)
# Runs from a git checkout only (registry copies already carry the CLI).
# Mini `npx skills` for this repo: link, list, check, uninstall. One skill, no
# registry. Bare run links the shared standard dir only; add --detect for every
# agent with an existing skills setup, --all for everything, or --agent/--dir
# for exact scope. Explicit removals stick: nothing re-links unasked. No
# auto-updater by design. `git pull` keeps symlinked installs current.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
# The skill is one directory of the checkout, the same one the registry
# installs; agents get a link to it, never to the whole checkout.
SRC="$ROOT/skills/verdict"
TAB="$(printf '\t')"

usage() {
  cat <<EOF
usage: install-skill.sh [--agent NAME]... [--dir PATH]... [--project [DIR]] [--detect] [--all] [--force] [--uninstall] [--list] [--check] [--help]

  (no flags)     link ~/.agents/skills, the shared standard dir
  --agent NAME   only this agent (repeatable): agents claude cursor opencode
                 copilot gemini goose qwen windsurf
  --dir PATH     extra skills dir (repeatable, absolute path). Any agent,
                 present or future, that keeps skills in a directory
  --project [D]  link into DIR/.agents/skills (default: current directory)
  --detect       ~/.agents/skills plus every agent with an existing setup
  --all          every known agent
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

[ "$MODE" = "list" ] || [ -f "$SRC/SKILL.md" ] || die "not a verdict checkout (no skills/verdict/SKILL.md under $ROOT)"
if [ "$MODE" != "list" ]; then
  git -C "$ROOT" rev-parse --is-inside-work-tree >/dev/null 2>&1 \
    || die "not a verdict git checkout ($ROOT) — install-skill.sh links checkouts only; registry copies already carry the verdict CLI, nothing to link"
fi

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
      echo "occupied: $label ($d/verdict. Not our link)"
    else
      echo "absent:   $label ($d)"
    fi
  done <<<"$(scope_list)"
  exit 0
fi

if [ "$MODE" = "install" ] || [ "$MODE" = "check" ]; then
  # The doctor gate runs the checkout's binary: prefer a built bin/verdict,
  # else build in place (bin/ is gitignored, so the gate never depends on
  # a stale copy).
  if [ -x "$ROOT/bin/verdict" ]; then
    "$ROOT/bin/verdict" doctor || die "fix the FAIL lines above, then re-run"
  elif command -v go >/dev/null 2>&1; then
    (cd "$ROOT" && go run ./cmd/verdict doctor) || die "fix the FAIL lines above, then re-run"
  else
    die "doctor gate needs $ROOT/bin/verdict (make build) or Go to build it"
  fi
fi

if [ "$MODE" = "uninstall" ]; then
  n=0
  while IFS= read -r line; do
    label="${line%%$TAB*}"
    d="${line#*$TAB}"
    if [ -L "$d/verdict" ] && { [ "$(readlink "$d/verdict")" = "$SRC" ] || [ "$(readlink "$d/verdict")" = "$ROOT" ]; }; then
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
  if [ -L "$dest" ] && [ "$(readlink "$dest")" = "$ROOT" ]; then
    # Older versions linked the whole checkout; the skill is skills/verdict now.
    rm "$dest" && ln -s "$SRC" "$dest" && echo "relinked: $1 ($dest -> $SRC, was the checkout root)"
    linked=$((linked + 1))
    return 0
  fi
  if [ -e "$dest" ] || [ -L "$dest" ]; then
    if [ "$FORCE" = 1 ] && [ ! -d "$dest" ]; then
      rm "$dest" && ln -s "$SRC" "$dest" && echo "replaced: $1 ($dest)"
      linked=$((linked + 1))
      return 0
    fi
    echo "SKIP: $1 occupied ($dest). Move it aside or use --force"
    skipped=$((skipped + 1))
    return 0
  fi
  ln -s "$SRC" "$dest" || die "symlink failed ($dest). Try --dir to a writable location"
  echo "linked: $1 ($dest -> $SRC)"
  linked=$((linked + 1))
}

wire_hooks() {
  git -C "$ROOT" rev-parse --is-inside-work-tree >/dev/null 2>&1 || return 0
  cur="$(git -C "$ROOT" config --local core.hooksPath 2>/dev/null || true)"
  if [ -z "$cur" ]; then
    git -C "$ROOT" config core.hooksPath "$ROOT/hooks" && echo "hooks: pre-commit + pre-push wired ($ROOT/hooks)"
  elif [ "$cur" = "$ROOT/hooks" ]; then
    echo "hooks: already wired"
  elif [ "$FORCE" = 1 ]; then
    git -C "$ROOT" config core.hooksPath "$ROOT/hooks" && echo "hooks: overridden (was $cur)"
  else
    echo "hooks: SKIP foreign hooksPath ($cur). Use --force to override"
  fi
}

while IFS= read -r line; do
  label="${line%%$TAB*}"
  d="${line#*$TAB}"
  link_into "$label" "$d"
done <<<"$(scope_list)"
wire_hooks
echo "done: $linked linked, $skipped skipped. update anytime: git -C \"$ROOT\" pull (symlinks stay current)."
