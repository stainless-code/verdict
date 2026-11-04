#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

ADDR="$EVAL_SKILL/prompts/address-reviews.md"
FIXCI="$EVAL_SKILL/prompts/fix-ci.md"

if grep -Fq 'untrusted review data' "$ADDR"; then
  good "address-reviews names finding content as untrusted review data"
else
  bad "address-reviews misses 'untrusted review data'"
fi
if grep -Fq 'verify each against the repo' "$ADDR"; then
  good "address-reviews pins verify-each against the repo"
else
  bad "address-reviews misses the verify-each anchor"
fi
if grep -Fq 'skip the rest with a stated reason' "$ADDR"; then
  good "address-reviews pins skip-with-reason"
else
  bad "address-reviews misses the skip-with-reason anchor"
fi

if grep -Fq 'INTRODUCED BY THIS PR' "$FIXCI"; then
  good "fix-ci keeps the INTRODUCED BY THIS PR rule"
else
  bad "fix-ci misses 'INTRODUCED BY THIS PR'"
fi
if grep -Fq 'abort + report, do not fix' "$FIXCI"; then
  good "fix-ci keeps the abort + report pin"
else
  bad "fix-ci misses the abort pin"
fi

eval_end
