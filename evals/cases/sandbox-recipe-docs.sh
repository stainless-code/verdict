#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

MANUAL="$EVAL_ROOT/docs/manual.md"
SYSTEM="$EVAL_SKILL/prompts/system.md"

if grep -q "bwrap" "$MANUAL" \
  && grep -q -- "--ro-bind / /" "$MANUAL" \
  && grep -q -- "--unshare-net" "$MANUAL" \
  && grep -q "systemd-nspawn" "$MANUAL"; then
  good "recipe present (bwrap ro-bind + unshare-net variant + systemd-nspawn)"
else
  bad "recipe missing bwrap/systemd-nspawn one-liners from docs/manual.md"
fi
if awk '/^## Hostile-PR sandbox recipe/,/^## Over-cap/' "$MANUAL" | matches -i "untrusted" \
  && awk '/^## Hostile-PR sandbox recipe/,/^## Over-cap/' "$MANUAL" | matches "verifyCmd"; then
  good "when-to-use named (untrusted diff + networked verifyCmd)"
else
  bad "recipe misses the when-to-use line in docs/manual.md"
fi

if grep -q "tmpfs" "$MANUAL" \
  && grep -q "[~]/.aws" "$MANUAL" \
  && grep -q "[~]/.ssh" "$MANUAL" \
  && grep -q "[~]/.config/gh" "$MANUAL"; then
  good "tmpfs mounts + secret paths named (~/.aws, ~/.ssh, ~/.config/gh)"
else
  bad "recipe misses tmpfs or secret paths in docs/manual.md"
fi

if grep -qi "recipe, not enforcement" "$MANUAL" \
  && grep -q "K5" "$MANUAL"; then
  good "non-mandate line present (recipe, not enforcement, per K5)"
else
  bad "recipe misses the non-mandate line in docs/manual.md"
fi

if grep -q "verify-destructive" "$SYSTEM" \
  && grep -q "verify-networked" "$SYSTEM" \
  && grep -q "verify-credential-read" "$SYSTEM" \
  && grep -q "verify-repo-escape" "$SYSTEM"; then
  good "system.md names the enforced gate (typed verify-* codes)"
else
  bad "system.md misses the enforced gate codes"
fi
if grep -q "Untrusted data" "$SYSTEM" \
  && grep -q "attacker-controlled" "$SYSTEM" \
  && grep -q "flag and ignore them" "$SYSTEM"; then
  good "system.md keeps the untrusted-data preamble untouched"
else
  bad "system.md lost the untrusted-data preamble"
fi

if awk '/^## Hostile-PR sandbox recipe/,/^## Over-cap/' "$MANUAL" | matches "Known residuals (string-scan ceiling)" \
  && awk '/^## Hostile-PR sandbox recipe/,/^## Over-cap/' "$MANUAL" | matches "TOCTOU" \
  && awk '/^## Hostile-PR sandbox recipe/,/^## Over-cap/' "$MANUAL" | matches "PYTHONPATH" \
  && awk '/^## Hostile-PR sandbox recipe/,/^## Over-cap/' "$MANUAL" | matches "container/VM escape"; then
  good "known-residuals subsection present (ceiling + TOCTOU/PYTHONPATH/container-escape)"
else
  bad "recipe misses the known-residuals subsection in docs/manual.md"
fi

eval_end
