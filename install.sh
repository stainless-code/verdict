#!/usr/bin/env bash
# verdict installer: doctor-gated symlink into harness skill dirs.
# Re-run anytime to update links. No auto-updater by design — `git pull` keeps
# symlinked installs current.
set -euo pipefail

SRC="$(cd "$(dirname "$0")" && pwd)"

"$SRC/scripts/review.sh" doctor || {
  echo "install: fix the FAIL lines above, then re-run install.sh" >&2
  exit 1
}

link_into() { # $1 = skills dir
  mkdir -p "$1"
  dest="$1/verdict"
  if [[ -L "$dest" && "$(readlink "$dest")" == "$SRC" ]]; then
    echo "ok: $dest already linked"
    return
  fi
  if [[ -e "$dest" || -L "$dest" ]]; then
    echo "SKIP: $dest exists and is not our link (move it aside to link)"
    return
  fi
  ln -s "$SRC" "$dest" && echo "linked: $dest -> $SRC"
}

# Git hooks: repo-local, check-only, no frameworks. Only fires on commits in this clone.
if git -C "$SRC" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  git -C "$SRC" config core.hooksPath "$SRC/hooks" && echo "hooks: pre-commit wired ($SRC/hooks)"
fi

link_into "$HOME/.agents/skills"
link_into "$HOME/.claude/skills"
link_into "$HOME/.cursor/skills"
link_into "$HOME/.config/opencode/skills"
link_into "$HOME/.copilot/skills"
link_into "$HOME/.gemini/skills"
link_into "$HOME/.config/goose/skills"
link_into "$HOME/.qwen/skills"
link_into "$HOME/.codeium/windsurf/skills"

echo 'done. update anytime: git -C "'"$SRC"'" pull (symlinks stay current).'
