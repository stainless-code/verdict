#!/bin/sh
#
set -eu

FORMULA="${1:?usage: brew-audit.sh <formula.rb>}"
[ -f "$FORMULA" ] || { echo "brew-audit: no such formula: $FORMULA" >&2; exit 1; }
FORMULA="$(cd "$(dirname "$FORMULA")" && pwd)/$(basename "$FORMULA")"

if ! command -v brew >/dev/null 2>&1; then
  echo "brew-audit: Homebrew not found; skipping the formula style/audit check (CI runs it)" >&2
  exit 0
fi

HOMEBREW_NO_AUTO_UPDATE=1
HOMEBREW_NO_ANALYTICS=1
export HOMEBREW_NO_AUTO_UPDATE HOMEBREW_NO_ANALYTICS

TAP='verdict-lint/tmp'
NAME='verdict'

untap() { brew untap "$TAP" >/dev/null 2>&1 || true; }
trap untap EXIT HUP INT TERM
untap
brew tap-new --no-git "$TAP" >/dev/null
tap_repo="$(brew --repository "$TAP")"
[ -n "$tap_repo" ] || { echo "brew-audit: cannot resolve the throwaway tap $TAP" >&2; exit 1; }
mkdir -p "$tap_repo/Formula"
cp "$FORMULA" "$tap_repo/Formula/$NAME.rb"

brew trust --tap "$TAP" >/dev/null 2>&1 ||
  echo "brew-audit: could not trust $TAP; audit will report the refusal" >&2

brew style "$TAP/$NAME"
brew audit --strict --formula "$TAP/$NAME"
echo "brew-audit: $NAME.rb passes brew style and brew audit --strict"
