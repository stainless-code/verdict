#!/usr/bin/env bash
#
# shellcheck disable=SC2034 # EVAL_OUT/EVAL_ERR/EVAL_STATUS are read by cases/*.sh
set -euo pipefail

: "${EVAL_ROOT:?run via evals/run.sh}"
: "${EVAL_SANDBOX:?run via evals/run.sh}"
: "${EVAL_STUBBIN:?run via evals/run.sh}"
: "${EVAL_STUBLOG:?run via evals/run.sh}"

EVAL_SKILL="$EVAL_ROOT/skills/verdict"
EVAL_PROCEDURE="$EVAL_ROOT/procedure"

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
    # The die that ended the command writes to stderr, and the status alone
    # named nothing the one time this bit CI (STA-378: release-body leg 1).
    sed -e 's/^/    | /' "$EVAL_ERR" 2>/dev/null || true
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

# file_url <path> is the file:// URL for a local path, with the bytes a URL
# cannot carry raw percent-encoded. curl refuses a path holding a space (a
# sandbox under a TMPDIR with one) rather than reading the file (STA-379).
file_url() {
  local p="$1"
  p="${p//%/%25}"
  p="${p// /%20}"
  p="${p//\#/%23}"
  p="${p//\?/%3F}"
  # curl reads [ ] { } as its own URL globs, so a path holding one never
  # reaches the file it names.
  p="${p//\[/%5B}"
  p="${p//\]/%5D}"
  p="${p//\{/%7B}"
  p="${p//\}/%7D}"
  printf 'file://%s' "$p"
}

# eval_fixture_commit <message> [git-commit flags] closes the fixture's commit
# at a pinned date: a wall-clock date makes every capture of the same fixture a
# different base SHA, and the provenance run_id hashes that SHA, so two
# equivalent bundles stop rendering an identical footer (hunk-snapshot).
eval_fixture_commit() {
  GIT_AUTHOR_DATE=2024-06-01T00:00:00Z GIT_COMMITTER_DATE=2024-06-01T00:00:00Z \
    git -c user.email=eval@example.com -c user.name=eval -c commit.gpgsign=false \
    commit -qm "$@"
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
      eval_fixture_commit eval-base
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
  for rel in config.json kb.jsonl kb-joins.jsonl learnings.md overrides verify-env; do
    rm -rf "$repo/.verdict/$rel"
    [ -e "$EVAL_SANDBOX/.verdict/$rel" ] || continue
    cp -R "$EVAL_SANDBOX/.verdict/$rel" "$repo/.verdict/$rel"
  done
  # Stage only the controls: the fixture's own change must stay uncommitted
  # so every capture still sees the reviewed diff.
  ( cd "$repo" && git add -A -- .verdict .gitignore && \
    eval_fixture_commit eval-controls --allow-empty )
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
      # The review URL names the host a real fetch records; post pins its host
      # calls to it.
      printf '{"state":"OPEN","headRefOid":"%s","baseRefOid":"%s","author":{"login":"teammate"},"url":"https://github.com/acme/app/pull/%s"}\n' \
        "$EVAL_FIXTURE_HEAD" "$base" "$name" >"$dst/PR.json"
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

# eval_pin_head <name> [sha] binds a PR bundle to the checkout it is run in:
# the fixture head is a placeholder no checkout is on, while a verb that acts on
# the checkout (fix) proves HEAD equals the bundle's reviewed head. Without a
# sha it pins the sandbox's own HEAD; a sha pins that one instead.
eval_pin_head() {
  local dir="$EVAL_SANDBOX/.verdict/state/$1" head="${2:-}"
  [ -f "$dir/PR.json" ] || return 0
  [ -n "$head" ] || head="$(git -C "$EVAL_SANDBOX" rev-parse HEAD)"
  jq --arg h "$head" '.headRefOid = $h' "$dir/PR.json" >"$dir/PR.json.tmp"
  mv "$dir/PR.json.tmp" "$dir/PR.json"
}

# eval_hunk_receipts <bundle dir> prints the read receipts a review of that
# bundle needs: one per changed hunk, quoting the first line the hunk carries on
# its NEW side. Record approves only a review whose every non-exempt hunk is
# receipted, so a fixture that wants an approval asks for these the way an agent
# reading the diff would.
eval_hunk_receipts() {
  local dir="$1"
  if [ ! -f "$dir/hunks.tsv" ] || [ ! -f "$dir/full.diff" ]; then
    printf '[]\n'
    return
  fi
  awk -F'\t' '
    FNR == NR { want[$0] = 1; next }
    /^\+\+\+ b\// { path = substr($0, 7); next }
    /^@@ / {
      flush()
      if (!match($0, /\+[0-9]+(,[0-9]+)?/)) next
      split(substr($0, RSTART + 1, RLENGTH - 1), span, ",")
      start = span[1] + 0
      span_len = (2 in span) ? span[2] + 0 : 1
      key = path "\tNEW\t" start "\t" start + span_len - 1
      next
    }
    key == "" { next }
    {
      mark = substr($0, 1, 1)
      if (mark != "+" && mark != " ") next
      if (quote == "") {
        quote = substr($0, 2)
        gsub(/\t/, " ", quote)
      }
    }
    function flush() {
      if (key != "" && quote != "" && (key in want)) printf "%s\t%s\n", key, quote
      key = ""
      quote = ""
    }
    END { flush() }
  ' "$dir/hunks.tsv" "$dir/full.diff" |
    jq -R -s 'split("\n") | map(select(length > 0)) | map(split("\t"))
      | map({hunk: (.[0:(length - 1)] | join("\t")), quote: .[-1]})'
}

# eval_diff_lines <bundle dir> prints a JSON object mapping "<path>\t<line>" to
# the text full.diff carries at that NEW-side line. A fixture finding that names
# a line without quoting it quotes that line, so the review states the evidence
# the evidence pass checks instead of quoting its own prose.
eval_diff_lines() {
  local dir="$1"
  if [ ! -f "$dir/full.diff" ]; then
    printf '{}'
    return
  fi
  awk '
    /^\+\+\+ b\// { path = substr($0, 7); next }
    /^@@ / {
      if (!match($0, /\+[0-9]+(,[0-9]+)?/)) next
      split(substr($0, RSTART + 1, RLENGTH - 1), span, ",")
      newline = span[1] + 0
      next
    }
    path == "" || newline == 0 { next }
    {
      mark = substr($0, 1, 1)
      if (mark == "-") next
      if (mark != "+" && mark != " ") next
      text = substr($0, 2)
      gsub(/\t/, " ", text)
      printf "%s\t%d\t%s\n", path, newline, text
      newline++
    }
  ' "$dir/full.diff" |
    jq -R -s 'split("\n") | map(select(length > 0)) | map(split("\t"))
      | map({key: (.[0:-1] | join("\t")), value: .[-1]}) | from_entries'
}

eval_write_review_at() {
  local path="$1"
  local raw tmp lines
  raw=$(cat)
  tmp="$path.tmp"
  if printf '%s\n' "$raw" | jq -e . >/dev/null 2>&1; then
    # The diff-line map is one entry per changed line, so it passes as a file:
    # Linux caps a single argv entry at 128 KiB, and a bundle over the review cap
    # carries megabytes of lines.
    lines="$path.lines.json"
    if ! eval_diff_lines "$(dirname "$path")" >"$lines"; then
      rm -f "$lines" "$tmp"
      return 1
    fi
    if ! printf '%s\n' "$raw" | jq --slurpfile lines "$lines" '
      if type != "object" or has("findings") then . else (
      def anchor: (.line // 1 | if type == "number" then . else tonumber? // 1 end);
      {
        schema_version: "1",
        findings: ((.comments // []) | map(if type == "object" then {
          path: (.path // "a.txt"),
          line: anchor,
          start_line: (if .start_line != null and (.start_line|type) == "number" then .start_line else null end),
          side: (.side // "RIGHT"),
          severity: (.severity // "important"),
          claim: (.body // "converted finding"),
          quote: (.quote // $lines[0][((.path // "a.txt") | tostring) + "\t"
            + ((.line // 1) | tostring)] // .body // "converted finding"),
          confidence: "high",
          rigor: (.rigor // "said"),
          category: (.category // null),
          suggestion: (.suggestion // null),
          brief: (.brief // null),
          sources: (.sources // null),
          kb: (.kb // null)
        } | with_entries(select(.value != null)) else . end)),
        summary: ((.body // "") | if type == "string" and length > 0 then .[0:2000] else "No findings in this review." end)
      }
      + (if has("spec_checks") then {spec_checks: .spec_checks} else {} end)
      + (if has("receipts") then {receipts: .receipts} else {} end)
      + (if has("resolved") then {resolved: .resolved} else {} end)
      + (if has("triage") then {triage: .triage} else {} end)
      + (if has("coverage") then {coverage: .coverage} else {} end)
      + (if has("chunks") then {chunks: .chunks} else {} end)
      + (if has("suppressed") then {suppressed: .suppressed} else {} end)
      + (if has("risk") then {risk: .risk} else {} end)
      + (if has("security_summary") then {security_summary: .security_summary} else {} end)
      ) end' > "$tmp"
    then
      rm -f "$lines" "$tmp"
      return 1
    fi
    rm -f "$lines"
  else
    printf '%s\n' "$raw" > "$tmp"
  fi
  mv "$tmp" "$path"
  eval_stamp_receipts "$path"
}

# eval_stamp_receipts <review path> adds the hunk receipts a review needs unless
# the review already states its receipts: an explicit receipts key, even an
# empty one, is the case saying it wants the shortfall.
eval_stamp_receipts() {
  local path="$1" receipts
  jq -e 'has("receipts") | not' "$path" >/dev/null 2>&1 || return 0
  # The receipt list scales with the changed hunks, so it passes as a file for
  # the same reason the diff-line map does.
  receipts="$path.receipts.json"
  if ! eval_hunk_receipts "$(dirname "$path")" >"$receipts"; then
    rm -f "$receipts"
    return 1
  fi
  if [ "$(jq 'length' "$receipts")" = "0" ]; then
    rm -f "$receipts"
    return 0
  fi
  if ! jq --slurpfile r "$receipts" '. + {receipts: $r[0]}' "$path" >"$path.tmp"; then
    rm -f "$receipts" "$path.tmp"
    return 1
  fi
  mv "$path.tmp" "$path"
  rm -f "$receipts"
}

eval_write_review() {
  eval_write_review_at "$EVAL_SANDBOX/.verdict/state/$1/review.json"
}

# eval_write_record_evidence <pr> stores the record.json a completed record
# leaves behind: the reviewed head, the bundle's own diff digest, and the reading
# receipts its hunks support. A gate run grades this receipt, so a fixture that
# expects a passing gate on an unposted bundle states the evidence a recorded
# review carries.
eval_write_record_evidence() {
  local pr="$1" dir head
  dir="$EVAL_SANDBOX/.verdict/state/$pr"
  head="$(jq -r '.headRefOid // .head_sha // ""' "$dir/PR.json" 2>/dev/null || printf '')"
  [ -n "$head" ] || head="$(jq -r '.head_sha // ""' "$dir/source.json" 2>/dev/null || printf '')"
  [ -n "$head" ] || head="$EVAL_FIXTURE_HEAD"
  jq -n --arg h "$head" --argjson e "$(eval_evidence_receipt "$dir" "$head")" \
    '{schema_version: "1", status: "github", head: $h, diff_sha256: $e.diff_sha256,
      recorded_at: "2026-01-01T00:00:00Z", findings: 0, open: 0,
      approval: {reason: "no gating findings", tier: "important", gating_sigs: [], approved: true},
      evidence: $e}' >"$dir/record.json"
}

# eval_evidence_receipt <bundle dir> <head> prints the evidence receipt a
# recording verb stores for that bundle: the head, the bundle's own diff digest,
# and one matched receipt per changed hunk. A gate run grades this receipt, so a
# fixture that expects a passing gate states the evidence a recorded review
# carries.
eval_evidence_receipt() {
  local dir="$1" head="$2" receipts total sha
  receipts="$(eval_hunk_receipts "$dir")"
  total="$(printf '%s' "$receipts" | jq 'length')"
  sha="$( { sha256sum "$dir/full.diff" 2>/dev/null || shasum -a 256 "$dir/full.diff" 2>/dev/null; } | cut -d' ' -f1 || true)"
  jq -n --arg h "$head" --arg d "$sha" --argjson n "$total" '
    {head: $h, diff_sha256: $d,
     receipts: {total: $n, covered: $n, exempt: 0, complete: true},
     spec: {total: 0, covered: 0, min: 0, met: true},
     insufficient: false}'
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
    for f in config.json kb.jsonl learnings.md verify-env overrides/preamble.md overrides/footer-order.md \
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
