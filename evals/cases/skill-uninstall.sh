#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

SKILLS="$EVAL_SANDBOX/skills"
TARGET="$SKILLS/verdict"

tree_hash() {
  (cd "$1" && find . -type f | LC_ALL=C sort | while IFS= read -r f; do shasum "$f"; done | shasum)
}

run_review skill install --dir "$SKILLS" --json
assert_exit "seed install" 0
printf 'planted foreign file\n' >"$TARGET/keep.txt"

before="$(tree_hash "$TARGET")"
run_review skill uninstall --dir "$SKILLS" --dry-run --json
assert_exit "uninstall --dry-run exits 0" 0
assert_json_field "dry-run plans a removal" "remove" '.data.status'
assert_json_field "dry-run records no removals" "0" '.data.removed | length'
assert_json_field "dry-run names the payload it would remove" "true" '(.data.would_remove | length) > 0'
after="$(tree_hash "$TARGET")"
assert_eq "--dry-run deleted nothing (tree hash unchanged)" "$before" "$after"

run_review skill uninstall --dir "$SKILLS" --json
assert_exit "uninstall exits 0" 0
assert_json_field "uninstall removed the payload" "removed" '.data.status'
assert_json_field "the receipt was removed" "true" '.data.receipt_removed'
assert_json_field "the foreign file was kept" "keep.txt" '.data.kept[0]'
if [ ! -e "$TARGET/SKILL.md" ] && [ ! -e "$TARGET/.verdict-managed" ] \
  && [ -e "$TARGET/keep.txt" ] && [ ! -e "$TARGET/prompts" ]; then
  good "uninstall removed only inventory paths and the receipt"
else
  bad "uninstall left payload behind or removed the foreign file"
fi

FOREIGN="$EVAL_SANDBOX/foreign"
mkdir -p "$FOREIGN/verdict"
printf 'foreign bytes\n' >"$FOREIGN/verdict/keep.txt"
run_review skill uninstall --dir "$FOREIGN" --json
assert_exit "a target without a receipt refuses" 1
assert_refusal "no-receipt refusal" "skill-dir-foreign"
if [ -e "$FOREIGN/verdict/keep.txt" ]; then
  good "the no-receipt refusal removed nothing"
else
  bad "the no-receipt refusal deleted the foreign file"
fi

COPY="$EVAL_SANDBOX/copy"
run_review skill install --dir "$COPY" --json
assert_exit "seed a registry-shaped copy" 0
rm "$COPY/verdict/.verdict-managed"
printf 'copy foreign\n' >"$COPY/verdict/keep.txt"
run_review skill uninstall --dir "$COPY" --force --json
assert_exit "uninstall --force on a no-receipt copy exits 0" 0
if [ ! -e "$COPY/verdict/SKILL.md" ] && [ -e "$COPY/verdict/keep.txt" ]; then
  good "--force removed only inventory paths"
else
  bad "--force kept payload or removed the foreign file"
fi

EMPTY="$EVAL_SANDBOX/empty"
mkdir -p "$EMPTY"
run_review skill uninstall --dir "$EMPTY" --json
assert_exit "an absent install refuses" 1
assert_refusal "absent install refusal" "skill-not-installed"

if [ ! -s "$EVAL_STUBLOG" ]; then
  good "uninstall makes zero host calls (offline)"
else
  bad "uninstall called gh"
fi

eval_end
