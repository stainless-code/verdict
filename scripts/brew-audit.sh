#!/bin/sh
# brew-audit: lint and audit the rendered Homebrew formula the way the real tap
# would see it.
#
#   sh scripts/brew-audit.sh dist/verdict.rb
#
# Homebrew 7 refuses `brew style` and `brew audit` on a bare file ("Homebrew
# requires formulae to be in a tap"), so the formula is copied into a throwaway
# local tap (no git remote, no push, untapped on exit) and graded there.
# Homebrew 7 also refuses to load formulae from an untrusted non-official tap
# ("Run `brew trust ...`"), so the tap is trusted first. Nothing is installed:
# `brew install` can prompt or hang and is never run.
#
# `brew style` runs the layout/formula cops and exits non-zero on any offense,
# so the release dry run fails on a style regression as well as an audit one.
# CI's ubuntu-latest runner ships Homebrew; elsewhere a missing brew degrades to
# a notice (same idiom as hooks/pre-commit).
set -eu

FORMULA="${1:?usage: brew-audit.sh <formula.rb>}"
[ -f "$FORMULA" ] || { echo "brew-audit: no such formula: $FORMULA" >&2; exit 1; }
FORMULA="$(cd "$(dirname "$FORMULA")" && pwd)/$(basename "$FORMULA")"

if ! command -v brew >/dev/null 2>&1; then
  echo "brew-audit: Homebrew not found; skipping the formula style/audit check (CI runs it)" >&2
  exit 0
fi

# Never reach the network from a check, and never phone home.
HOMEBREW_NO_AUTO_UPDATE=1
HOMEBREW_NO_ANALYTICS=1
export HOMEBREW_NO_AUTO_UPDATE HOMEBREW_NO_ANALYTICS

TAP='verdict-lint/tmp'
NAME='verdict'

# A fixed throwaway tap keeps re-runs idempotent; --no-git avoids a repository.
untap() { brew untap "$TAP" >/dev/null 2>&1 || true; }
trap untap EXIT HUP INT TERM
untap
brew tap-new --no-git "$TAP" >/dev/null
tap_repo="$(brew --repository "$TAP")"
[ -n "$tap_repo" ] || { echo "brew-audit: cannot resolve the throwaway tap $TAP" >&2; exit 1; }
mkdir -p "$tap_repo/Formula"
cp "$FORMULA" "$tap_repo/Formula/$NAME.rb"

# The tap is local and throwaway; trusting it lets audit load the formula.
brew trust --tap "$TAP" >/dev/null 2>&1 ||
  echo "brew-audit: could not trust $TAP; audit will report the refusal" >&2

brew style "$TAP/$NAME"
brew audit --strict --formula "$TAP/$NAME"
echo "brew-audit: $NAME.rb passes brew style and brew audit --strict"
