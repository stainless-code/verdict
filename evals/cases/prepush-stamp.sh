#!/usr/bin/env bash
#
# hooks/pre-push skips the heavy suite only for commits `verify-stamp.sh write`
# recorded, on a clean tree. The sandbox repo carries the real hook and stamp
# script plus a stub scripts/prepush-checks.sh that logs a line per run, so no
# real build, test, or lint runs here.
set -euo pipefail
HERE="$(dirname "$0")"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

REPO="$EVAL_SANDBOX/prepushtest"
LOG="$EVAL_SANDBOX/prepush-stub.log"
rm -rf "$REPO"
mkdir -p "$REPO/hooks" "$REPO/scripts"
git -C "$REPO" init -q
cp "$EVAL_ROOT/hooks/pre-push" "$REPO/hooks/pre-push"
cp "$EVAL_ROOT/scripts/verify-stamp.sh" "$EVAL_ROOT/scripts/ci-changes.sh" "$REPO/scripts/"
cat >"$REPO/scripts/prepush-checks.sh" <<'EOF'
#!/bin/sh
echo ran >>"$PREPUSH_STUB_LOG"
exit "${PREPUSH_STUB_EXIT:-0}"
EOF
printf 'one\n' >"$REPO/a.txt"

gc() { git -C "$REPO" -c user.email=t@t.invalid -c user.name=t commit -q "$@"; }
git -C "$REPO" add -A
gc -m one
SHA_A="$(git -C "$REPO" rev-parse HEAD)"
printf 'two\n' >"$REPO/a.txt"
gc -qam two
SHA_B="$(git -C "$REPO" rev-parse HEAD)"
ZERO=0000000000000000000000000000000000000000
STAMP="$(git -C "$REPO" rev-parse --absolute-git-dir)/verdict-verified"

# push SHA...: feed one new-branch ref line per sha to the hook. Same bash 3.2
# set -e dance as precommit-hook.
push() {
  local sha refs=""
  for sha in "$@"; do refs="$refs refs/heads/b-$sha $sha refs/heads/b-$sha $ZERO
"; done
  : >"$LOG"
  set +e
  (
    cd "$REPO" || exit 99
    printf '%s' "$refs" | PREPUSH_STUB_LOG="$LOG" bash hooks/pre-push >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  EVAL_STATUS=$?
  set -e
  return 0
}
stamp() { (cd "$REPO" && sh scripts/verify-stamp.sh write "$@" >"$EVAL_OUT" 2>"$EVAL_ERR"); }
ran_full() { [ -s "$LOG" ]; }
skipped() { [ ! -s "$LOG" ] && grep -q 'already verified by `make verify-stamp` at .*; skipping the full suite' "$EVAL_OUT"; }

push "$SHA_B"
if [ "$EVAL_STATUS" -eq 0 ] && ran_full && ! grep -q 'already verified' "$EVAL_OUT"; then
  good "an unstamped sha runs the full suite"
else
  bad "unstamped leg: exit $EVAL_STATUS, log $(cat "$LOG")"
fi

PREPUSH_STUB_EXIT=1 push "$SHA_B"
if [ "$EVAL_STATUS" -ne 0 ] && ran_full; then
  good "a failing suite fails the hook"
else
  bad "failing suite leg: exit $EVAL_STATUS"
fi

stamp
if [ "$(sed 's/ .*//' "$STAMP")" = "$SHA_B" ] && [ -z "$(git -C "$REPO" status --porcelain)" ]; then
  good "write records the sha inside the git dir and leaves the tree clean"
else
  bad "stamp file after write: $(cat "$STAMP" 2>/dev/null)"
fi

push "$SHA_B"
if [ "$EVAL_STATUS" -eq 0 ] && skipped; then
  good "a stamped clean sha skips the full suite and says so"
else
  bad "stamped leg: exit $EVAL_STATUS, log $(cat "$LOG"), out $(cat "$EVAL_OUT")"
fi

push "$SHA_A"
if [ "$EVAL_STATUS" -eq 0 ] && ran_full; then
  good "a sha that is not the stamped one runs the full suite"
else
  bad "mismatched-sha leg: exit $EVAL_STATUS, log $(cat "$LOG")"
fi

push "$SHA_B" "$SHA_A"
if [ "$EVAL_STATUS" -eq 0 ] && ran_full; then
  good "a push is skipped only when every pushed sha is stamped"
else
  bad "multi-ref partial leg: exit $EVAL_STATUS, log $(cat "$LOG")"
fi
git -C "$REPO" checkout -q "$SHA_A"
stamp
git -C "$REPO" checkout -q "$SHA_B"
push "$SHA_B" "$SHA_A"
if [ "$EVAL_STATUS" -eq 0 ] && skipped; then
  good "every pushed sha stamped skips the full suite"
else
  bad "multi-ref full leg: exit $EVAL_STATUS, log $(cat "$LOG")"
fi

VERDICT_PREPUSH_FULL=1 push "$SHA_B"
if [ "$EVAL_STATUS" -eq 0 ] && ran_full && ! grep -q 'already verified' "$EVAL_OUT"; then
  good "VERDICT_PREPUSH_FULL=1 forces the full suite for a stamped sha"
else
  bad "forced leg: exit $EVAL_STATUS, log $(cat "$LOG")"
fi

printf 'dirty\n' >>"$REPO/a.txt"
push "$SHA_B"
if [ "$EVAL_STATUS" -eq 0 ] && ran_full && ! grep -q 'already verified' "$EVAL_OUT"; then
  good "a modified tree runs the full suite for a stamped sha"
else
  bad "dirty leg: exit $EVAL_STATUS, log $(cat "$LOG")"
fi
if stamp "$SHA_B"; then
  bad "write stamped a dirty tree"
else
  good "write refuses a dirty tree"
fi
git -C "$REPO" checkout -q -- a.txt
printf 'x\n' >"$REPO/untracked.txt"
push "$SHA_B"
if [ "$EVAL_STATUS" -eq 0 ] && ran_full; then
  good "an untracked file runs the full suite for a stamped sha"
else
  bad "untracked leg: exit $EVAL_STATUS, log $(cat "$LOG")"
fi
rm -f "$REPO/untracked.txt"

if stamp "$SHA_A"; then
  bad "write stamped a HEAD that is not the sha it was given"
else
  good "write refuses when HEAD is not the expected sha"
fi

# Same tree, different commit: only the sha tells them apart.
gc -q --allow-empty -m same-tree
SHA_C="$(git -C "$REPO" rev-parse HEAD)"
push "$SHA_C"
if [ "$EVAL_STATUS" -eq 0 ] && ran_full; then
  good "a different commit with the stamped tree runs the full suite"
else
  bad "same-tree leg: exit $EVAL_STATUS, log $(cat "$LOG")"
fi

# A stamp that is not the hook's own shape must not skip: right sha with a
# wrong tree, a bare sha, a non-numeric time, an extra field.
TREE_B="$(git -C "$REPO" rev-parse "$SHA_B^{tree}")"
n=0
for line in "$SHA_B $SHA_A soon" "$SHA_B $ZERO 1700000000" "$SHA_B" "$SHA_B $TREE_B 1700000000 extra" "$SHA_B $TREE_B 17e9" "STAMP $SHA_B $TREE_B 1700000000"; do
  n=$((n + 1))
  printf '%s\n' "$line" >"$STAMP"
  push "$SHA_B"
  if [ "$EVAL_STATUS" -eq 0 ] && ran_full; then
    good "foreign stamp line $n runs the full suite"
  else
    bad "foreign stamp line $n skipped: $line"
  fi
done
printf '%s %s 1700000000\n' "$SHA_B" "$TREE_B" >"$STAMP"
push "$SHA_B"
if [ "$EVAL_STATUS" -eq 0 ] && skipped; then
  good "a well-formed hand-written stamp line is accepted (sanity control)"
else
  bad "control stamp did not skip: log $(cat "$LOG")"
fi

rm -rf "$REPO" "$LOG"
eval_end
