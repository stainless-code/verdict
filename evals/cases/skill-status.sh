#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

SKILLS="$EVAL_SANDBOX/skills"
TARGET="$SKILLS/verdict"

# tree_hash folds every relative path and its bytes into one digest, so a
# read-only run must leave it unchanged.
tree_hash() {
  (cd "$1" && find . -type f | LC_ALL=C sort | while IFS= read -r f; do shasum "$f"; done | shasum)
}

run_review skill install --dir "$SKILLS" --json
assert_exit "seed install" 0

before="$(tree_hash "$TARGET")"
run_review skill status --dir "$SKILLS" --json
assert_exit "status of a fresh install exits 0" 0
assert_envelope "status in sync"
assert_json_field "fresh status" "keep" '.data.status'
assert_json_field "fresh install is in sync" "true" '.data.in_sync'
assert_eq "empty diff list" "0" "$(jq -r '.data.diff // [] | length' <"$EVAL_OUT")"
after="$(tree_hash "$TARGET")"
assert_eq "status wrote nothing (tree hash unchanged)" "$before" "$after"

printf 'hand edited\n' >"$TARGET/SKILL.md"
before="$(tree_hash "$TARGET")"
run_review skill status --dir "$SKILLS" --diff --json
assert_exit "a hand edit drifts (non-zero exit)" 1
assert_json_field "edited status" "update" '.data.status'
assert_json_field "edited install is not in sync" "false" '.data.in_sync'
assert_json_field "edited file is listed" "SKILL.md" '.data.diff[0].path'
assert_json_field "small diff is not truncated" "false" '.data.diff[0].truncated'
jq -r '.data.diff[0].diff' <"$EVAL_OUT" >"$EVAL_SANDBOX/diff.txt"
assert_file_grep "the diff shows the removed hand edit" "$EVAL_SANDBOX/diff.txt" "-hand edited" "-F"
after="$(tree_hash "$TARGET")"
assert_eq "status --diff wrote nothing (tree hash unchanged)" "$before" "$after"

run_review skill status --dir "$SKILLS" --diff
assert_exit "human status drift exits 1" 1
assert_file_grep "human status names the drift" "$EVAL_OUT" "status: drift" "-F"
assert_file_grep "human diff shows the edit" "$EVAL_OUT" "-hand edited" "-F"

FOREIGN="$EVAL_SANDBOX/foreign"
mkdir -p "$FOREIGN/verdict"
printf 'foreign bytes\n' >"$FOREIGN/verdict/keep.txt"
run_review skill status --dir "$FOREIGN" --json
assert_exit "a foreign target reports drift" 1
assert_envelope "blocked status"
assert_json_field "foreign target is blocked" "blocked" '.data.status'
assert_json_field "foreign target is not in sync" "false" '.data.in_sync'

if [ ! -s "$EVAL_STUBLOG" ]; then
  good "status makes zero host calls (offline)"
else
  bad "status called gh"
fi

eval_end
