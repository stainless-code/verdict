#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(dirname "$0")"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

REPO="$EVAL_SANDBOX/hooktest"
rm -rf "$REPO"
mkdir -p "$REPO"
git -C "$REPO" init -q
git -C "$REPO" config user.email "hook-eval@example.invalid"
git -C "$REPO" config user.name "hook eval"
printf '#!/usr/bin/env bash\n# baseline\n' > "$REPO/ok.sh"
git -C "$REPO" add ok.sh
git -C "$REPO" -c user.email=t@t -c user.name=t commit -qm baseline 2>/dev/null

hook() {
  # Mirrors run_review's set +e dance: on bash 3.2 (macOS /bin/bash) a bare
  # `( ... ); EVAL_STATUS=$?` in a function body dies under set -e even when
  # the assignment succeeds — a trailing `return 0` alone does not save it.
  set +e
  (
    cd "$REPO" || exit 99
    PATH="$EVAL_STUBBIN:$PATH" bash "$EVAL_ROOT/hooks/pre-commit" >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  EVAL_STATUS=$?
  set -e
  return 0
}

printf '#!/usr/bin/env bash\nunused_for_hook_test=1\necho "clean"\n' > "$REPO/bad.sh"
git -C "$REPO" add bad.sh
hook
if [ "$EVAL_STATUS" -ne 0 ] && grep -q "shellcheck" "$EVAL_OUT"; then
  good "staged violating .sh fails the hook"
else
  bad "violating .sh gave exit $EVAL_STATUS"
fi
git -C "$REPO" reset -q

printf '#!/usr/bin/env bash\necho "clean"\n' > "$REPO/good.sh"
git -C "$REPO" add good.sh
hook
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "staged clean .sh passes the hook"
else
  bad "clean .sh gave exit $EVAL_STATUS :: $(cat "$EVAL_ERR")"
fi
git -C "$REPO" reset -q

mkdir -p "$EVAL_SANDBOX/stubbin-nosc"
for t in git bash xargs; do
  p=$(command -v "$t" 2>/dev/null || true)
  [ -n "$p" ] && ln -sf "$p" "$EVAL_SANDBOX/stubbin-nosc/$t"
done
git -C "$REPO" add good.sh
(
  cd "$REPO" || exit 99
  env -i PATH="$EVAL_SANDBOX/stubbin-nosc" HOME="$HOME" bash "$EVAL_ROOT/hooks/pre-commit" \
    >"$EVAL_OUT" 2>"$EVAL_ERR"
  EVAL_STATUS=$?
)
if [ "$EVAL_STATUS" -eq 0 ] && grep -q "shellcheck not found" "$EVAL_OUT"; then
  good "missing shellcheck skips with a notice and passes"
else
  bad "missing-binary leg gave exit $EVAL_STATUS"
fi

rm -rf "$REPO" "$EVAL_SANDBOX/stubbin-nosc"
eval_end
