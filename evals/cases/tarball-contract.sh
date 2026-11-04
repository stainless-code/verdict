#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

command -v npm >/dev/null 2>&1 || bail "npm not on PATH (pack contract needs npm)"

PACK_JSON="$EVAL_SANDBOX/tarball-pack.json"
PACK_ERR="$EVAL_SANDBOX/tarball-pack-err.txt"
PACKED="$EVAL_SANDBOX/tarball-packed.txt"
REQUIRED="$EVAL_SANDBOX/tarball-required.txt"
TAMPERED="$EVAL_SANDBOX/tarball-tampered.txt"

set +e
(cd "$EVAL_ROOT" && npm pack --dry-run --json --offline \
  --pack-destination "$EVAL_SANDBOX" >"$PACK_JSON" 2>"$PACK_ERR")
pack_status=$?
set -e
if [ "$pack_status" -eq 0 ]; then
  good "npm pack --dry-run --json --offline exits 0 (no registry needed)"
else
  bad "npm pack exits $pack_status: $(head -c 200 "$PACK_ERR")"
fi
if jq -e '.[0].files | length > 0' "$PACK_JSON" >/dev/null 2>&1; then
  good "pack lists tarball contents as JSON"
else
  bad "pack emitted no JSON file list"
fi
jq -r '.[0].files[].path' "$PACK_JSON" 2>/dev/null | LC_ALL=C sort > "$PACKED"
if [ ! -e "$EVAL_ROOT/pack.log" ] && [ -z "$(cd "$EVAL_ROOT" && ls *.tgz 2>/dev/null || true)" ]; then
  good "pack wrote no tarball or pack.log into the repo"
else
  bad "pack left a tarball or pack.log in the repo"
fi

{
  echo "skills/verdict/SKILL.md"
  echo "skills/verdict/scripts/verdict"
  echo "README.md"
  echo "LICENSE"
  echo "package.json"
  for f in "$EVAL_SKILL"/prompts/*.md; do echo "skills/verdict/prompts/$(basename "$f")"; done
  for f in "$EVAL_ROOT"/scripts/*.sh; do echo "scripts/$(basename "$f")"; done
  for f in "$EVAL_SKILL"/schemas/*.schema.json; do echo "skills/verdict/schemas/$(basename "$f")"; done
} | LC_ALL=C sort -u > "$REQUIRED"

missing_from() { # $1 = packed-list file; prints required entries absent there
  local packed="$1" r miss=0
  while IFS= read -r r; do
    [ -n "$r" ] || continue
    grep -q -x -F "$r" "$packed" || { echo "$r"; miss=1; }
  done < "$REQUIRED"
  return $miss
}

if gaps="$(missing_from "$PACKED")"; then
  good "tarball holds the completion script, every prompt, every schema, SKILL.md, README.md, LICENSE ($(wc -l <"$REQUIRED" | tr -d ' ') files)"
else
  bad "tarball missing required file(s): $(printf '%s' "$gaps" | tr '\n' ' ')"
fi

refs_ok="true"
refs_seen=0
while IFS= read -r ref; do
  [ -n "$ref" ] || continue
  refs_seen=$((refs_seen + 1))
  [ -f "$EVAL_SKILL/$ref" ] || { refs_ok="false"; bad "SKILL.md names $ref, missing on disk"; }
  grep -q -x -F "skills/verdict/$ref" "$PACKED" || { refs_ok="false"; bad "SKILL.md names $ref, missing from the tarball"; }
done < <(grep -o -E '(scripts|prompts)/[A-Za-z0-9_.-]+' "$EVAL_SKILL/SKILL.md" | LC_ALL=C sort -u)
if [ "$refs_ok" = "true" ] && [ "$refs_seen" -gt 0 ]; then
  good "all $refs_seen SKILL.md script/prompt references resolve on disk and in the tarball"
elif [ "$refs_seen" -eq 0 ]; then
  bad "SKILL.md names no scripts/ or prompts/ paths (reference parse broke?)"
fi

if offenders="$(grep -E -i -e '(^|/)\.env($|/)' -e '^\.review/' -e '(^|/)\.scratch($|/)' -e '^evals/results/' -e '\.tgz$' -e '^node_modules/' -e '^scripts/review\.sh$' "$PACKED" || true)" \
  && [ -z "$offenders" ]; then
  good "tarball holds no .env, .review, .scratch, evals results, tgz, node_modules, or review.sh"
else
  bad "tarball ships forbidden file(s): $(printf '%s' "$offenders" | tr '\n' ' ')"
fi

grep -v -x -F "skills/verdict/prompts/review.md" "$PACKED" > "$TAMPERED"
if gaps="$(missing_from "$TAMPERED")"; then
  bad "comparator passed a packed list missing skills/verdict/prompts/review.md (drift would ship)"
else
  case "$gaps" in
    *skills/verdict/prompts/review.md*) good "comparator flags a dropped file (skills/verdict/prompts/review.md) as missing" ;;
    *) bad "comparator flagged wrong gap: $gaps" ;;
  esac
fi

assert_file_nogrep "install.sh stays out of the tarball (source-only by design)" "$PACKED" "install.sh" "-xF"
assert_file_nogrep "install.ps1 stays out of the tarball" "$PACKED" "install.ps1" "-xF"
assert_file_nogrep "install-skill.sh stays out of the tarball" "$PACKED" "install-skill.sh" "-xF"
assert_file_nogrep "hooks/ stays out of the tarball" "$PACKED" "^hooks/"

if [ "$(( $(wc -l <"$EVAL_STUBLOG" | tr -d ' ') - stub_before ))" = "0" ]; then
  good "zero stub-gh intercepts across pack legs (offline)"
else
  bad "stub gh intercepted calls during pack legs"
fi

eval_end
