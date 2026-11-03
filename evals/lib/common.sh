#!/usr/bin/env bash
# evals/lib/common.sh — shared fixture, assertion and runner helpers for the
# offline contract evals. Sourced by evals/run.sh and evals/cases/*.sh; never
# executed. Sourcing it is what arms a case: it defines the `fails` counter
# and the good/bad/bail vocabulary, the envelope key list, and the assertion
# helpers below, so a case body is assertions only.
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

# The documented --json envelope (`schema_version: "1"`): exactly these keys,
# nothing else. One literal for the whole suite — six cases used to copy it.
EVAL_ENVELOPE_KEYS="at data error next pr repo schema_version verb warnings"

# --- assertion vocabulary (the shared case preamble) ---------------------
# `fails` counts failed assertions; `good`/`bad` print the per-assertion lines
# the runner replays.
# shellcheck disable=SC2034 # the counter is read by cases and by eval_end
fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

# bail <message> — hard-stop a case that cannot grade anything at all (a tool
# missing, an unsupported platform) instead of letting it exit 0 having proved
# nothing: records the failure, prints the case verdict, exits 1.
bail() {
  bad "$1"
  echo "case $(basename "${0%.sh}"): FAIL ($fails)"
  exit 1
}

# eval_end — the shared case epilogue: exit 1 when any assertion failed, else
# print the PASS line and exit 0. The case name comes from $0, so a case calls
# it bare.
eval_end() {
  local name
  name="$(basename "${0%.sh}")"
  if [ "$fails" -ne 0 ]; then
    echo "case $name: FAIL ($fails)"
    exit 1
  fi
  echo "case $name: PASS"
}

# --- assertions ---------------------------------------------------------
# Each helper grades one claim and reports through good/bad, so a case never
# hand-rolls an if/good/else/bad block for the common contract shapes.

# assert_eq <label> <want> <got>
assert_eq() {
  local label="$1" want="$2" got="$3"
  if [ "$want" = "$got" ]; then
    good "$label"
  else
    bad "$label: got '$got', want '$want'"
  fi
}

# assert_exit <label> <want> — grade the status of the last run_review.
assert_exit() {
  local label="$1" want="$2"
  if [ "$EVAL_STATUS" -eq "$want" ]; then
    good "$label (exit $want)"
  else
    bad "$label: exit $EVAL_STATUS, want $want"
  fi
}

# assert_docs <label> <want> — grade the stdout doc count of the last run.
assert_docs() {
  local label="$1" want="$2" have
  have="$(stdout_docs)"
  assert_eq "$label (stdout docs)" "$want" "$have"
}

# assert_keys <label> — the last --json doc carries exactly EVAL_ENVELOPE_KEYS.
assert_keys() {
  local label="${1:-envelope}" have
  have="$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null || true)"
  if [ "$have" = "$EVAL_ENVELOPE_KEYS" ]; then
    good "$label carries exactly the documented keys"
  else
    bad "$label keys are '$have', want '$EVAL_ENVELOPE_KEYS'"
  fi
}

# assert_envelope <label> — a success envelope: exact key set, schema_version
# "1", null error.
assert_envelope() {
  local label="${1:-success envelope}"
  assert_keys "$label"
  assert_eq "$label schema_version" "1" "$(jq -r '.schema_version // empty' <"$EVAL_OUT" 2>/dev/null || true)"
  if jq -e '.error == null' <"$EVAL_OUT" >/dev/null 2>&1; then
    good "$label carries a null error"
  else
    bad "$label error is not null"
  fi
}

# assert_refusal <label> [want-code] — a refusal envelope: exactly one doc, the
# documented key set, data null with a string error.code, and (when given) the
# exact code. Exit status is graded separately by assert_exit: not every
# refusal exits 1.
assert_refusal() {
  local label="$1" want="${2:-}" have
  assert_docs "$label" 1
  assert_keys "$label refusal"
  if jq -e '.data == null and (.error.code | type) == "string"' <"$EVAL_OUT" >/dev/null 2>&1; then
    good "$label keeps data null and a string error.code"
  else
    bad "$label breaks the refusal data/error contract"
  fi
  if [ -n "$want" ]; then
    have="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
    assert_eq "$label refusal code" "$want" "$have"
  fi
}

# assert_file_eq <label> <want-file> <got-file>
assert_file_eq() {
  local label="$1" want="$2" got="$3"
  if [ -f "$want" ] && [ -f "$got" ] && cmp -s "$want" "$got"; then
    good "$label"
  else
    bad "$label differs ($want vs $got)"
  fi
}

# assert_json_field <label> <want> <jq-filter> [file] — default file EVAL_OUT.
assert_json_field() {
  local label="$1" want="$2" filter="$3" file="${4:-$EVAL_OUT}" got
  got="$(jq -r "$filter" "$file" 2>/dev/null || true)"
  assert_eq "$label" "$want" "$got"
}

# assert_file_grep <label> <file> <pattern> — the file must exist and hold the
# pattern. Positive greps fail loudly on a missing file already; the helper
# keeps the diagnostic explicit.
assert_file_grep() {
  local label="$1" file="$2" pat="$3"
  if [ ! -e "$file" ]; then
    bad "$label: $file is missing"
  elif grep -q "$pat" "$file"; then
    good "$label"
  else
    bad "$label: $file does not match '$pat'"
  fi
}

# assert_file_nogrep <label> <file> <pattern> — the file must exist AND NOT hold
# the pattern. Existence is part of the assertion on purpose: `! grep -q` on a
# missing file returns 2, which `!` turns into a pass, so an unwritten output
# used to satisfy every "omits X" check (test-audit F5).
assert_file_nogrep() {
  local label="$1" file="$2" pat="$3"
  if [ ! -s "$file" ]; then
    bad "$label: $file is missing or empty (cannot prove the omission)"
  elif grep -q "$pat" "$file"; then
    bad "$label: $file carries '$pat'"
  else
    good "$label"
  fi
}

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

# eval_mk_patch <pr> <verifyCmd> — a verify bundle claiming that command, with
# no execution: the gate (or the execution leg) is the thing under test.
eval_mk_patch() {
  local pr="$1" cmd="$2" dir="$EVAL_SANDBOX/.review/$1"
  mkdir -p "$dir"
  jq -n --arg c "$cmd" '{verifyCmd:$c}' > "$dir/patch.json"
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
