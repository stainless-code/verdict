#!/usr/bin/env bash
# Case sandbox-recipe-docs: hostile-PR sandbox recipe docs presence (slice 03).
#
# Grades (all hermetic — docs-presence greps, no network, no model):
#   R1  bwrap one-liner present (ro-bind /, rw-bind repo, --unshare-net
#       variant noted) + systemd-nspawn alternative + when-to-use (untrusted
#       diff + networked verifyCmd).
#   R2  tmpfs + secret paths named (~/.aws, ~/.ssh, ~/.config/gh).
#   R3  non-mandate line (recipe, not enforcement, K5) present.
#   R4  system.md names the enforced gate (typed verify-* codes) while the
#       untrusted-data preamble stays untouched.
#   R5  recipe section pins the known-residuals subsection (string-scan
#       ceiling + representative keywords).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

MANUAL="$EVAL_ROOT/docs/manual.md"
SYSTEM="$EVAL_ROOT/prompts/system.md"

# --- R1: bwrap one-liner + systemd-nspawn alternative + when-to-use ---
if grep -q "bwrap" "$MANUAL" \
  && grep -q -- "--ro-bind / /" "$MANUAL" \
  && grep -q -- "--unshare-net" "$MANUAL" \
  && grep -q "systemd-nspawn" "$MANUAL"; then
  good "recipe present (bwrap ro-bind + unshare-net variant + systemd-nspawn)"
else
  bad "recipe missing bwrap/systemd-nspawn one-liners from docs/manual.md"
fi
if awk '/^## Hostile-PR sandbox recipe/,/^## Over-cap/' "$MANUAL" | grep -qi "untrusted" \
  && awk '/^## Hostile-PR sandbox recipe/,/^## Over-cap/' "$MANUAL" | grep -q "verifyCmd"; then
  good "when-to-use named (untrusted diff + networked verifyCmd)"
else
  bad "recipe misses the when-to-use line in docs/manual.md"
fi

# --- R2: tmpfs + secret paths ---
if grep -q "tmpfs" "$MANUAL" \
  && grep -q "[~]/.aws" "$MANUAL" \
  && grep -q "[~]/.ssh" "$MANUAL" \
  && grep -q "[~]/.config/gh" "$MANUAL"; then
  good "tmpfs mounts + secret paths named (~/.aws, ~/.ssh, ~/.config/gh)"
else
  bad "recipe misses tmpfs or secret paths in docs/manual.md"
fi

# --- R3: non-mandate line ---
if grep -qi "recipe, not enforcement" "$MANUAL" \
  && grep -q "K5" "$MANUAL"; then
  good "non-mandate line present (recipe, not enforcement, per K5)"
else
  bad "recipe misses the non-mandate line in docs/manual.md"
fi

# --- R4: system.md names the enforced gate, preamble untouched ---
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

# --- R5: known-residuals subsection pinned in the recipe section ---
if awk '/^## Hostile-PR sandbox recipe/,/^## Over-cap/' "$MANUAL" | grep -q "Known residuals (string-scan ceiling)" \
  && awk '/^## Hostile-PR sandbox recipe/,/^## Over-cap/' "$MANUAL" | grep -q "TOCTOU" \
  && awk '/^## Hostile-PR sandbox recipe/,/^## Over-cap/' "$MANUAL" | grep -q "PYTHONPATH" \
  && awk '/^## Hostile-PR sandbox recipe/,/^## Over-cap/' "$MANUAL" | grep -q "container/VM escape"; then
  good "known-residuals subsection present (ceiling + TOCTOU/PYTHONPATH/container-escape)"
else
  bad "recipe misses the known-residuals subsection in docs/manual.md"
fi

if [ "$fails" -ne 0 ]; then echo "case sandbox-recipe-docs: FAIL ($fails)"; exit 1; fi
echo "case sandbox-recipe-docs: PASS"
