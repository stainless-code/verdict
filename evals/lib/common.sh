#!/usr/bin/env bash
#
# shellcheck disable=SC2034 # EVAL_OUT/EVAL_ERR/EVAL_STATUS are read by cases/*.sh
set -euo pipefail

: "${EVAL_ROOT:?run via evals/run.sh}"
: "${EVAL_SANDBOX:?run via evals/run.sh}"
: "${EVAL_STUBBIN:?run via evals/run.sh}"
: "${EVAL_STUBLOG:?run via evals/run.sh}"

EVAL_SKILL="$EVAL_ROOT/skills/verdict"

EVAL_OUT="$EVAL_SANDBOX/eval-out.bin"
EVAL_ERR="$EVAL_SANDBOX/eval-err.txt"
EVAL_STATUS=0

EVAL_ENVELOPE_KEYS="at data error next pr repo schema_version verb warnings"

# shellcheck disable=SC2034 # the counter is read by cases and by eval_end
fails=0
# shellcheck disable=SC2034 # the counter is read by cases and by eval_end
skips=0
# matches [grep flags] PATTERN: `grep -q` for the end of a pipeline. grep -q
# exits at the first match, so a producer still writing then takes SIGPIPE and,
# under `set -o pipefail`, fails the whole test even though the text matched
# (timing-dependent: it bit Linux CI, never macOS). This reads all of stdin.
matches() { grep "$@" >/dev/null; }

good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

skip() { echo "  SKIP: $1"; skips=$((skips + 1)); }

skip_case() {
  skip "$1"
  echo "case $(basename "${0%.sh}"): SKIP ($1)"
  exit 77
}

bail() {
  bad "$1"
  echo "case $(basename "${0%.sh}"): FAIL ($fails)"
  exit 1
}

eval_end() {
  local name
  name="$(basename "${0%.sh}")"
  if [ "$fails" -ne 0 ]; then
    echo "case $name: FAIL ($fails)"
    exit 1
  fi
  if [ "$skips" -ne 0 ]; then
    echo "case $name: SKIP ($skips skipped leg(s))"
    exit 77
  fi
  echo "case $name: PASS"
}

assert_eq() {
  local label="$1" want="$2" got="$3"
  if [ "$want" = "$got" ]; then
    good "$label"
  else
    bad "$label: got '$got', want '$want'"
  fi
}

assert_exit() {
  local label="$1" want="$2"
  if [ "$EVAL_STATUS" -eq "$want" ]; then
    good "$label (exit $want)"
  else
    bad "$label: exit $EVAL_STATUS, want $want"
  fi
}

assert_docs() {
  local label="$1" want="$2" have
  have="$(stdout_docs)"
  assert_eq "$label (stdout docs)" "$want" "$have"
}

assert_keys() {
  local label="${1:-envelope}" have
  have="$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null || true)"
  if [ "$have" = "$EVAL_ENVELOPE_KEYS" ]; then
    good "$label carries exactly the documented keys"
  else
    bad "$label keys are '$have', want '$EVAL_ENVELOPE_KEYS'"
  fi
}

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

assert_file_eq() {
  local label="$1" want="$2" got="$3"
  if [ -f "$want" ] && [ -f "$got" ] && cmp -s "$want" "$got"; then
    good "$label"
  else
    bad "$label differs ($want vs $got)"
  fi
}

assert_json_field() {
  local label="$1" want="$2" filter="$3" file="${4:-$EVAL_OUT}" got
  got="$(jq -r "$filter" "$file" 2>/dev/null || true)"
  assert_eq "$label" "$want" "$got"
}

assert_file_grep() {
  local label="$1" file="$2" pat="$3" flags="${4:-}"
  # shellcheck disable=SC2086 # flags is a deliberate list of grep options
  if [ ! -e "$file" ]; then
    bad "$label: $file is missing"
  elif grep -q $flags -- "$pat" "$file"; then
    good "$label"
  else
    bad "$label: $file does not match '$pat'"
  fi
}

# assert_file_nogrep <label> <file> <pattern> [grep-flags] — the file must
# exist AND NOT hold the pattern. Existence plus non-emptiness are part of the
# assertion on purpose: `! grep -q` on a missing file returns 2, which `!`
# turns into a pass, so an unwritten output used to satisfy every "omits X"
# check (test-audit F5). For a stream the harness owns, use
# assert_stream_nogrep: those may be legitimately empty.
assert_file_nogrep() {
  local label="$1" file="$2" pat="$3" flags="${4:-}"
  # shellcheck disable=SC2086 # flags is a deliberate list of grep options
  if [ ! -s "$file" ]; then
    bad "$label: $file is missing or empty (cannot prove the omission)"
  elif grep -q $flags -- "$pat" "$file"; then
    bad "$label: $file carries '$pat'"
  else
    good "$label"
  fi
}

assert_stream_nogrep() {
  local label="$1" file="$2" pat="$3" flags="${4:-}"
  # shellcheck disable=SC2086 # flags is a deliberate list of grep options
  if [ ! -e "$file" ]; then
    bad "$label: $file was never written (cannot prove the omission)"
  elif grep -q $flags -- "$pat" "$file"; then
    bad "$label: $file carries '$pat'"
  else
    good "$label"
  fi
}

# eval_fixture_repo <dir> readies the throwaway checkout the fixture
# producer builds from: a committed base carrying the old lines and a
# worktree carrying the reviewed change. It lives apart from the case
# sandbox so the producer captures the fixture and not the harness's own
# scratch files.
eval_fixture_repo() {
  local repo="$1"
  mkdir -p "$repo"
  (
    cd "$repo" || exit 99
    if [ ! -d .git ]; then
      git init -q
      git config user.email "eval@example.com"
      git config user.name "eval"
      printf '.verdict/state/\n' >.gitignore
      printf 'top\nold line\nbottom\n' >notes.txt
      git add -A
      git -c user.email=eval@example.com -c user.name=eval -c commit.gpgsign=false \
        commit -qm eval-base
    fi
    printf 'top\nnew line\nbottom\n' >notes.txt
  )
}

# eval_fixture_scratch returns the throwaway producer workspace. It lives
# outside the case sandbox so the producer's own files never show up as
# case dirt, and it is removed when the case exits.
eval_fixture_scratch() {
  if [ -z "${EVAL_FIXTURE_SCRATCH:-}" ]; then
    EVAL_FIXTURE_SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/verdict-fixture.XXXXXX")"
    # shellcheck disable=SC2064 # expand the scratch path now, not at exit
    trap "rm -rf '$EVAL_FIXTURE_SCRATCH'" EXIT
  fi
  printf '%s' "$EVAL_FIXTURE_SCRATCH"
}

# EVAL_FIXTURE_HEAD is the canonical fixture head every stub and assertion
# shares; EVAL_FIXTURE_BASE is the base the producer captured.
EVAL_FIXTURE_HEAD="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
EVAL_FIXTURE_BASE=""

# eval_mk_bundle <name> produces a typed review bundle through the real
# producer: fetch-local captures the controls snapshot and the run
# evidence, so record/post/verify see the artifact a fetch writes rather
# than a hand-made shell. A local- name stays that bundle; any other name
# is shaped as a provider PR bundle (PR.json instead of source.json).
# The captured base lands in EVAL_FIXTURE_BASE (the producer picks it, so
# expectations read the variable instead of a literal).
eval_mk_bundle() {
  local name="$1" slug repo src dst base scratch
  scratch="$(eval_fixture_scratch)"
  repo="$scratch/repo"
  eval_fixture_repo "$repo"
  # Control files the case wrote at the repo root are the fixture's base
  # controls: commit them so the producer pins them into the snapshot.
  mkdir -p "$repo/.verdict"
  for rel in config.json kb.jsonl kb-joins.jsonl learnings.md overrides; do
    rm -rf "$repo/.verdict/$rel"
    [ -e "$EVAL_SANDBOX/.verdict/$rel" ] || continue
    cp -R "$EVAL_SANDBOX/.verdict/$rel" "$repo/.verdict/$rel"
  done
  # Stage only the controls: the fixture's own change must stay uncommitted
  # so every capture still sees the reviewed diff.
  ( cd "$repo" && git add -A -- .verdict .gitignore && \
    git -c user.email=eval@example.com -c user.name=eval -c commit.gpgsign=false \
      commit -qm eval-controls --allow-empty )
  slug="fx$(printf '%s' "$name" | cksum | cut -d' ' -f1)"
  (
    cd "$repo" || exit 99
    PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" fetch-local acme/app "$slug" --json \
      >"$scratch/fetch.json" 2>"$scratch/fetch.err"
  ) || {
    bad "fixture producer failed for $name: $(head -c 300 "$scratch/fetch.err" 2>/dev/null)"
    return 1
  }
  src="$repo/.verdict/state/local-$slug"
  dst="$EVAL_SANDBOX/.verdict/state/$name"
  base="$(jq -r '.base_sha // empty' "$src/source.json" 2>/dev/null || true)"
  EVAL_FIXTURE_BASE="$base"
  mkdir -p "$EVAL_SANDBOX/.verdict/state"
  rm -rf "$dst"
  mv "$src" "$dst"
  case "$name" in
    local-* | corpus-*)
      jq --arg h "$EVAL_FIXTURE_HEAD" '.head_sha = $h' "$dst/source.json" >"$dst/source.json.tmp"
      mv "$dst/source.json.tmp" "$dst/source.json"
      ;;
    *)
      rm -f "$dst/source.json"
      printf '{"state":"OPEN","headRefOid":"%s","baseRefOid":"%s","author":{"login":"teammate"}}\n' \
        "$EVAL_FIXTURE_HEAD" "$base" >"$dst/PR.json"
      ;;
  esac
}

# eval_fixture_rebase <name> pins a hand-written PR record to the base the
# producer captured: the fixture keeps its own head, files, and author, and
# takes the base from the snapshot it has to match.
eval_fixture_rebase() {
  local dir="$EVAL_SANDBOX/.verdict/state/$1"
  [ -f "$dir/PR.json" ] || return 0
  jq --arg b "$EVAL_FIXTURE_BASE" --arg h "$EVAL_FIXTURE_HEAD" \
    '.baseRefOid = $b | .headRefOid = $h' "$dir/PR.json" >"$dir/PR.json.tmp"
  mv "$dir/PR.json.tmp" "$dir/PR.json"
}

eval_write_review() {
  local pr="$1"
  cat > "$EVAL_SANDBOX/.verdict/state/$pr/review.json"
}

eval_write_findings() {
  local pr="$1"
  cat > "$EVAL_SANDBOX/.verdict/state/$pr/findings.json"
}

eval_seed_base() {
  (
    cd "$EVAL_SANDBOX" || exit 99
    [ -d .git ] || git init -q
    git rev-parse -q --verify HEAD >/dev/null ||
      git -c user.email=eval@example.com -c user.name=eval -c commit.gpgsign=false commit -q --allow-empty -m eval-base
    git update-ref "refs/replace/$(printf 'b%.0s' $(seq 40))" HEAD
  )
}

eval_commit_controls() {
  (
    cd "$EVAL_SANDBOX" || exit 99
    for f in config.json kb.jsonl learnings.md overrides/preamble.md overrides/footer-order.md \
      overrides/walkthrough-threshold.md overrides/attribution.md; do
      if [ -f ".verdict/$f" ]; then
        git add -f ".verdict/$f"
      elif git ls-files --error-unmatch ".verdict/$f" >/dev/null 2>&1; then
        git rm -q --cached ".verdict/$f"
      fi
    done
    git -c user.email=eval@example.com -c user.name=eval -c commit.gpgsign=false commit -q --allow-empty -m controls
  )
}

# eval_unmintable refuses a fixture that hand-writes the controls snapshot:
# the typed pair (controls.json plus its run evidence) is only mintable by
# the producer, so a case that needs one calls eval_mk_bundle instead of
# forging it. Nothing synthesizes provenance.
eval_assert_produced() {
  local dir="$1"
  if [ -f "$dir/controls.json" ] && [ ! -f "$dir/run.json" ]; then
    bad "fixture $dir carries controls.json without its run evidence; build it with eval_mk_bundle"
    return 1
  fi
}

eval_mk_patch() {
  local pr="$1" cmd="$2" dir="$EVAL_SANDBOX/.verdict/state/$1"
  mkdir -p "$dir"
  jq -n --arg c "$cmd" '{verifyCmd:$c}' > "$dir/patch.json"
}

run_review() {
  case "${1:-}" in
    fetch) eval_seed_base ;;
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

stdout_docs() {
  local n
  if n=$(jq -s 'length' <"$EVAL_OUT" 2>/dev/null); then
    printf '%s' "$n"
  else
    printf '0'
  fi
}
