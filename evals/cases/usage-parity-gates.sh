#!/usr/bin/env bash
# Case usage-parity-gates: usage text names the verify refusal codes and the
# doctor excess-scope row (parity with the operator manual, which covers both).
#
# Grades (all hermetic — --help output plus doc greps, no network):
#   U1  `verify` usage names all four refusal codes (verify-destructive,
#       verify-networked, verify-credential-read, verify-repo-escape).
#   U2  `doctor` usage names the token-scopes-excess row.
#   U3  the operator manual still covers both (parity anchor against drift).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

run_review --help
if [ "$EVAL_STATUS" -ne 0 ]; then
  bad "--help exits $EVAL_STATUS"
fi

# --- U1: verify usage names the four refusal codes ---
for code in verify-destructive verify-networked verify-credential-read verify-repo-escape; do
  if grep -q "$code" "$EVAL_OUT"; then
    good "verify usage names $code"
  else
    bad "verify usage misses $code"
  fi
done

# --- U2: doctor usage names the excess-scope row ---
if grep -q "token-scopes-excess" "$EVAL_OUT"; then
  good "doctor usage names the token-scopes-excess row"
else
  bad "doctor usage misses the token-scopes-excess row"
fi

# --- U3: the manual still covers both (parity anchor) ---
if grep -q "verify-destructive" "$EVAL_ROOT/docs/manual.md" \
  && grep -q "verify-networked" "$EVAL_ROOT/docs/manual.md" \
  && grep -q "verify-credential-read" "$EVAL_ROOT/docs/manual.md" \
  && grep -q "verify-repo-escape" "$EVAL_ROOT/docs/manual.md"; then
  good "manual names the four verify refusal codes"
else
  bad "manual misses a verify refusal code"
fi
if grep -q "token-scopes-excess" "$EVAL_ROOT/docs/manual.md"; then
  good "manual names the token-scopes-excess row"
else
  bad "manual misses the token-scopes-excess row"
fi

eval_end
