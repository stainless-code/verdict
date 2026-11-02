#!/usr/bin/env bash
# Case tarball-contract: the published tarball ships the whole skill contract.
#
# Grades (all hermetic — `npm pack --dry-run` lists tarball contents locally,
# writes nothing, needs no registry):
#   P1  offline pack: `npm pack --dry-run --json --offline` exits 0 and
#       writes no tarball or pack.log into the repo (pack destination is the
#       sandbox, so even a non-dry-run accident lands outside the tree).
#   P2  required set ships: every scripts/*.sh on disk (the completion
#       script; no shell entry point), every prompts/*.md on disk, every
#       published schema on disk, plus SKILL.md, README.md, LICENSE and
#       package.json are all in the packed file list. The old
#       scripts/review.sh must NOT ship (a stale manifest fails here).
#   P3  no dangling references: every scripts/… or prompts/… path named in
#       a SKILL.md code span exists on disk and ships in the tarball.
#   P4  nothing forbidden ships: no .env, .review/, .scratch, evals/results,
#       *.tgz, or node_modules/ entry in the packed list — and never the
#       deleted shell entry point scripts/review.sh (a stale manifest fails
#       here).
#   P5  drift fails the build: the same comparator run against a packed
#       list with one required file removed reports the gap, so a newly
#       referenced file left out of package.json `files` breaks the suite.
#   P6  offline: zero stub-gh intercepts across every leg.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

command -v npm >/dev/null 2>&1 || { bad "npm not on PATH (pack contract needs npm)"; echo "case tarball-contract: FAIL ($fails)"; exit 1; }

PACK_JSON="$EVAL_SANDBOX/tarball-pack.json"
PACK_ERR="$EVAL_SANDBOX/tarball-pack-err.txt"
PACKED="$EVAL_SANDBOX/tarball-packed.txt"
REQUIRED="$EVAL_SANDBOX/tarball-required.txt"
TAMPERED="$EVAL_SANDBOX/tarball-tampered.txt"

# --- P1: offline pack, nothing written into the repo ---
# --dry-run lists without writing; --offline fails if a registry is needed
# (proving hermeticity by construction); --pack-destination keeps even a
# non-dry-run accident inside the sandbox, never in the repo.
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

# --- P2: required set ships ---
# The entry script plus every prompt on disk, every published schema on disk,
# and the top-level docs the skill and both installers promise
# (SKILL.md, README.md, LICENSE).
{
  echo "SKILL.md"
  echo "README.md"
  echo "LICENSE"
  echo "package.json"
  for f in "$EVAL_ROOT"/prompts/*.md; do echo "prompts/$(basename "$f")"; done
  for f in "$EVAL_ROOT"/scripts/*.sh; do echo "scripts/$(basename "$f")"; done
  for f in "$EVAL_ROOT"/schemas/*.schema.json; do echo "schemas/$(basename "$f")"; done
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

# --- P3: no dangling references ---
# Every scripts/… or prompts/… path SKILL.md names must exist and ship, so a
# renamed prompt without a shipped file fails here, not at an install.
refs_ok="true"
refs_seen=0
while IFS= read -r ref; do
  [ -n "$ref" ] || continue
  refs_seen=$((refs_seen + 1))
  [ -f "$EVAL_ROOT/$ref" ] || { refs_ok="false"; bad "SKILL.md names $ref, missing on disk"; }
  grep -q -x -F "$ref" "$PACKED" || { refs_ok="false"; bad "SKILL.md names $ref, missing from the tarball"; }
done < <(grep -o -E '(scripts|prompts)/[A-Za-z0-9_.-]+' "$EVAL_ROOT/SKILL.md" | LC_ALL=C sort -u)
if [ "$refs_ok" = "true" ] && [ "$refs_seen" -gt 0 ]; then
  good "all $refs_seen SKILL.md script/prompt references resolve on disk and in the tarball"
elif [ "$refs_seen" -eq 0 ]; then
  bad "SKILL.md names no scripts/ or prompts/ paths (reference parse broke?)"
fi

# --- P4: nothing forbidden ships ---
if offenders="$(grep -E -i -e '(^|/)\.env($|/)' -e '^\.review/' -e '(^|/)\.scratch($|/)' -e '^evals/results/' -e '\.tgz$' -e '^node_modules/' -e '^scripts/review\.sh$' "$PACKED" || true)" \
  && [ -z "$offenders" ]; then
  good "tarball holds no .env, .review, .scratch, evals results, tgz, node_modules, or review.sh"
else
  bad "tarball ships forbidden file(s): $(printf '%s' "$offenders" | tr '\n' ' ')"
fi

# --- P5: drift fails the build ---
# Drop one required file from a copy of the packed list: the comparator must
# report exactly that gap, proving a newly referenced file left out of
# package.json `files` breaks the suite instead of an install.
grep -v -x -F "prompts/review.md" "$PACKED" > "$TAMPERED"
if gaps="$(missing_from "$TAMPERED")"; then
  bad "comparator passed a packed list missing prompts/review.md (drift would ship)"
else
  case "$gaps" in
    *prompts/review.md*) good "comparator flags a dropped file (prompts/review.md) as missing" ;;
    *) bad "comparator flagged wrong gap: $gaps" ;;
  esac
fi

# --- P7: source-only files stay out of the tarball ---
# install.sh links git checkouts (refuses elsewhere) and hooks/ wires the
# checkout's pre-commit: neither is meaningful inside a registry copy, and
# the README routes registry users to the bundled CLI instead. If a future
# change ships them, the execution model must be re-settled, not drifted.
if ! grep -q -x -F "install.sh" "$PACKED" \
  && ! grep -q "^hooks/" "$PACKED"; then
  good "install.sh and hooks/ stay out of the tarball (source-only by design)"
else
  bad "tarball ships source-only file(s): $(grep -E -x -e 'install.sh' -e 'hooks/.*' "$PACKED" | tr '\n' ' ')"
fi

# --- P6: offline across every leg ---
if [ "$(( $(wc -l <"$EVAL_STUBLOG" | tr -d ' ') - stub_before ))" = "0" ]; then
  good "zero stub-gh intercepts across pack legs (offline)"
else
  bad "stub gh intercepted calls during pack legs"
fi

if [ "$fails" -ne 0 ]; then echo "case tarball-contract: FAIL ($fails)"; exit 1; fi
echo "case tarball-contract: PASS"
