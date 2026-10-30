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

# run_review <review.sh args...> — run the CLI with cwd=sandbox and the stub
# gh first on PATH. Captures stdout/stderr; sets EVAL_STATUS (never aborts,
# so callers under `set -e` can grade failures as well as passes).
run_review() {
  set +e
  (
    cd "$EVAL_SANDBOX" || exit 99
    PATH="$EVAL_STUBBIN:$PATH" bash "$EVAL_ROOT/scripts/review.sh" "$@" \
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
