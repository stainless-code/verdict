#!/usr/bin/env bash
# evals/lib/common.sh — shared fixture + runner helpers for the offline
# contract evals. Sourced by evals/run.sh and evals/cases/*.sh; never executed.
#
# Required environment (exported by evals/run.sh):
#   EVAL_ROOT     absolute path of the repo checkout under test
#   EVAL_SANDBOX  absolute path of an empty temp dir used as the CLI cwd
#   EVAL_STUBBIN  absolute path of a dir holding the network-dead `gh` stub
#   EVAL_STUBLOG  file the stub appends each intercepted call to
# shellcheck disable=SC2034 # EVAL_OUT/EVAL_ERR/EVAL_STATUS are read by cases/*.sh
set -euo pipefail

: "${EVAL_ROOT:?run via evals/run.sh}"
: "${EVAL_SANDBOX:?run via evals/run.sh}"
: "${EVAL_STUBBIN:?run via evals/run.sh}"
: "${EVAL_STUBLOG:?run via evals/run.sh}"

EVAL_OUT="$EVAL_SANDBOX/eval-out.bin"
EVAL_ERR="$EVAL_SANDBOX/eval-err.txt"
EVAL_STATUS=0

# eval_mk_bundle <pr> — craft a minimal hermetic PR bundle (no network):
# GitHub-shaped PR.json (state OPEN) plus a one-hunk diff, inside the sandbox.
eval_mk_bundle() {
  local pr="$1"
  local dir="$EVAL_SANDBOX/.review/$pr"
  mkdir -p "$dir"
  cat > "$dir/PR.json" <<'EOF'
{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"teammate"}}
EOF
  cat > "$dir/full.diff" <<'EOF'
diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 top
-old line
+new line
 bottom
EOF
}

# eval_write_review <pr> — stdin (one review.json doc) becomes the bundle input.
eval_write_review() {
  local pr="$1"
  cat > "$EVAL_SANDBOX/.review/$pr/review.json"
}

# eval_write_findings <pr> — stdin (a findings array) becomes findings.json.
eval_write_findings() {
  local pr="$1"
  cat > "$EVAL_SANDBOX/.review/$pr/findings.json"
}

# eval_seed_base — give the sandbox a git base for canned-gh fetches: the fake
# PR base (bbbb...) resolves to HEAD through a replace ref (an empty commit is
# made when the sandbox has none), so the default controls read finds an object.
eval_seed_base() {
  (
    cd "$EVAL_SANDBOX" || exit 99
    [ -d .git ] || git init -q
    git rev-parse -q --verify HEAD >/dev/null ||
      git -c user.email=eval@example.com -c user.name=eval -c commit.gpgsign=false commit -q --allow-empty -m eval-base
    git update-ref "refs/replace/$(printf 'b%.0s' $(seq 40))" HEAD
  )
}

# eval_commit_controls — track (or untrack) the sandbox's .review controls at
# HEAD, force-added past .gitignore the way a repo commits them, so the base
# read of fetch-local sees the current knob and store files.
eval_commit_controls() {
  (
    cd "$EVAL_SANDBOX" || exit 99
    for f in panel budget max_bytes chunk kb.jsonl kb-policy learnings.md; do
      if [ -f ".review/$f" ]; then
        git add -f ".review/$f"
      elif git ls-files --error-unmatch ".review/$f" >/dev/null 2>&1; then
        git rm -q --cached ".review/$f"
      fi
    done
    git -c user.email=eval@example.com -c user.name=eval -c commit.gpgsign=false commit -q --allow-empty -m controls
  )
}

# eval_seed_controls — stand in for fetch's base resolution on hand-crafted
# bundles: snapshot the sandbox's .review controls into every bundle that has
# no controls.json (or only a seeded head one), so post/record find the
# snapshot they gate on. Skipped when EVAL_NO_SEED is set; a bundle whose
# controls.json came from a real fetch (policy base) is never touched.
eval_seed_controls() {
  local dir rel src base sha
  [ -z "${EVAL_NO_SEED:-}" ] || return 0
  for dir in "$EVAL_SANDBOX"/.review/*/; do
    dir="${dir%/}"
    [ -f "$dir/PR.json" ] || [ -f "$dir/source.json" ] || continue
    if [ -f "$dir/controls.json" ] && [ "$(jq -r .policy "$dir/controls.json")" != head ]; then continue; fi
    base="$(jq -r '.base_sha // .baseRefOid // empty' "$dir/source.json" "$dir/PR.json" 2>/dev/null | head -1 || true)"
    [ -n "$base" ] || continue
    rm -rf "$dir/controls"
    for rel in panel budget max_bytes chunk kb.jsonl kb-policy learnings.md \
      overrides/preamble.md overrides/footer-order.md overrides/walkthrough-threshold.md overrides/attribution.md; do
      src="$EVAL_SANDBOX/.review/$rel"
      if [ -f "$src" ]; then
        mkdir -p "$dir/controls/$(dirname "$rel")"
        cp "$src" "$dir/controls/$rel"
        sha="$( (sha256sum "$src" 2>/dev/null || shasum -a 256 "$src") | cut -d' ' -f1)"
        printf '.review/%s\tused\t%s\t%s\n' "$rel" "$(wc -c <"$src" | tr -d ' ')" "$sha"
      else
        printf '.review/%s\tmissing\t0\t\n' "$rel"
      fi
    done | jq -Rn --arg base "$base" '{schema_version: "1", policy: "head", base_sha: $base, sha: "",
      files: [inputs | split("\t") | {path: .[0], status: .[1], bytes: (.[2] | tonumber), sha256: .[3]}]}' \
      >"$dir/controls.json"
  done
}

# run_review <verdict args...> — run the CLI (the built binary at
# $EVAL_CLI, exported by evals/run.sh and evals/one.sh) with cwd=sandbox
# and the stub gh first on PATH. Captures stdout/stderr; sets EVAL_STATUS
# (never aborts, so callers under `set -e` can grade failures as well as
# passes).
run_review() {
  case "${1:-}" in
    fetch) eval_seed_base ;;
    post | record) eval_seed_controls ;;
  esac
  set +e
  (
    cd "$EVAL_SANDBOX" || exit 99
    PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" "$@" \
      >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  EVAL_STATUS=$?
  set -e
  return 0
}

# stdout_docs — print how many JSON docs the captured stdout holds (0 if none).
stdout_docs() {
  local n
  if n=$(jq -s 'length' <"$EVAL_OUT" 2>/dev/null); then
    printf '%s' "$n"
  else
    printf '0'
  fi
}
