#!/usr/bin/env bash
#
# STA-477: `run --budget auto` resolves review depth deterministically before
# any agent launches. This case grades the shipped chain end to end: a captured
# `defaults.budget` decides with source config, `--finders` outranks the budget
# and records the override, a configured sensitive path escalates a
# below-threshold docs diff to deep and the coordinator pays for it, a deep
# resolution trips the existing render refusal, and an unknown depth is a usage
# error before anything spawns.
#
# The base is a real git commit and every bundle comes from the real producer
# (`fetch-local --base`), so `run` reads the artifact a fetch writes. The agent
# is the committed cooperating stub on PATH as `claude`: it writes exactly the
# review path the directive names and calls no host.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

STUB_SRC="$EVAL_ROOT/evals/fixtures/run-agent/agent-stub.sh"
SANDBOX="$(cd "$EVAL_SANDBOX" && pwd)"
DOCS="$SANDBOX/.verdict/state/local-docs"
SENSITIVE="$SANDBOX/.verdict/state/local-sensitive"
GIT_ID=(-c user.email=budget@example.com -c user.name=budget -c commit.gpgsign=false)
sandbox_git() { (cd "$EVAL_SANDBOX" && git "${GIT_ID[@]}" "$@"); }

# Two controls documents: the first only asks for the classifying depth, the
# second also extends the sensitive globs with the fixture's own path, which is
# how a repository escalates a tree the built-ins do not name.
CONFIG_AUTO='{"schema_version":"1","defaults":{"budget":"auto"}}'
CONFIG_SENSITIVE='{"schema_version":"1","defaults":{"budget":"auto","sensitive_paths":["notes.txt"]}}'

if [ ! -f "$STUB_SRC" ]; then
  bail "the committed stub agent is missing: $STUB_SRC"
fi
AGENTBIN="$SANDBOX/agentbin"
mkdir -p "$AGENTBIN"
cp "$STUB_SRC" "$AGENTBIN/claude"
chmod +x "$AGENTBIN/claude"
export PATH="$AGENTBIN:$PATH"
# Every review launch runs in the OS review sandbox, which lets the agent
# write only its snapshot, a private TMPDIR, and its own state paths: the
# stub's argv log lives in claude's state directory, relocated into the
# sandbox by CLAUDE_CONFIG_DIR so the case never touches the real ~/.claude.
export CLAUDE_CONFIG_DIR="$SANDBOX/claude-state"
mkdir -p "$CLAUDE_CONFIG_DIR"
export STUB_AGENT_LOG="$CLAUDE_CONFIG_DIR/agent-argv.log"
unset REVIEW_BUDGET_USD REVIEW_PANEL REVIEW_MAX_BYTES REVIEW_MODEL || true

# spawns counts the agent processes the stub itself recorded, so "no process
# started" is the child's own evidence rather than an inference from output.
spawns() {
  if [ -f "$STUB_AGENT_LOG" ]; then
    grep -c '^cwd=' "$STUB_AGENT_LOG" 2>/dev/null || true
  else
    printf '0'
  fi
}

# --- the two bases ----------------------------------------------------------
# The producer diffs the base commit against the checked-out head, so each base
# is a commit and each reviewed change is the commit after it. The changed row
# is a `.txt` file, which the shared role heuristic reads as documentation: a
# docs-only diff under every threshold.
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  echo ".verdict/state/*" >.gitignore
  mkdir -p .verdict
  printf 'top\nold line\nbottom\n' >notes.txt
  printf '%s\n' "$CONFIG_AUTO" >.verdict/config.json
  git add .gitignore notes.txt
  git add -f .verdict/config.json
  git "${GIT_ID[@]}" commit -qm base
)
BASE_AUTO="$(sandbox_git rev-parse HEAD)"
(
  cd "$EVAL_SANDBOX" || exit 99
  printf 'top\nnew line\nbottom\n' >notes.txt
  git add notes.txt
  git "${GIT_ID[@]}" commit -qm docs-head
)
run_review fetch-local acme/app docs --base "$BASE_AUTO" --json -- notes.txt
assert_exit "the docs-only bundle is produced from the base" 0
assert_file_grep "the snapshot carries the base controls document" "$DOCS/controls/config.json" \
  '"budget":"auto"' "-F"

# BASE_SENSITIVE re-establishes the same reviewed change over the second
# controls document, which extends the sensitive globs with notes.txt.
(
  cd "$EVAL_SANDBOX" || exit 99
  printf '%s\n' "$CONFIG_SENSITIVE" >.verdict/config.json
  printf 'top\nold line\nbottom\n' >notes.txt
  git add .verdict/config.json notes.txt
  git "${GIT_ID[@]}" commit -qm sensitive-base
)
BASE_SENSITIVE="$(sandbox_git rev-parse HEAD)"
(
  cd "$EVAL_SANDBOX" || exit 99
  printf 'top\nnew line\nbottom\n' >notes.txt
  git add notes.txt
  git "${GIT_ID[@]}" commit -qm sensitive-head
)
run_review fetch-local acme/app sensitive --base "$BASE_SENSITIVE" --json -- notes.txt
assert_exit "the sensitive-glob bundle is produced from the base" 0

# --- (i) a captured defaults.budget decides, and records source config ------
before="$(spawns)"
export STUB_AGENT_MODE=write
run_review run acme/app local-docs --agent claude --json
assert_exit "a config default budget runs" 0
assert_envelope "config budget"
assert_eq "a docs-only diff pays one finder" "1" "$(( $(spawns) - before ))"
assert_json_field "the tier is light" "light" '.data.budget.tier'
assert_json_field "the config layer asked for it" "config" '.data.budget.source'
assert_json_field "the requested value is auto" "auto" '.data.budget.requested'
assert_json_field "the rule that decided is docs-or-test-only" "docs-or-test-only" '.data.budget.reasons[0]'
assert_json_field "one finder launched" "1" '.data.budget.finders'
assert_json_field "no cross-check launched" "off" '.data.budget.cross_check'
assert_json_field "the file count is the gate's own" "1" '.data.budget.files'
assert_json_field "launcher.json carries the same tier" "light" '.budget.tier' "$DOCS/launcher.json"
assert_json_field "launcher.json carries the same reason" "docs-or-test-only" '.budget.reasons[0]' "$DOCS/launcher.json"
assert_file_nogrep "no override is recorded when only the config asked" "$DOCS/launcher.json" "overridden_by" "-F"

# --- (ii) --finders always wins, and says so --------------------------------
before="$(spawns)"
run_review run acme/app local-docs --finders 2 --rounds 1 --agent claude --json
assert_exit "an explicit --finders 2 launches the two-finder pass" 0
assert_eq "two finders and both cross-checks spawn" "4" "$(( $(spawns) - before ))"
assert_json_field "an explicit --finders records no budget at all" "null" '.data.budget'
assert_file_grep "the capture names the flag as the finder source" "$DOCS/run.json" '"finder_source": "flag"' "-F"

before="$(spawns)"
run_review run acme/app local-docs --finders 1 --budget deep --agent claude --json
assert_exit "--finders 1 with a deep budget still runs" 0
assert_eq "--finders 1 wins the dispatch" "1" "$(( $(spawns) - before ))"
assert_json_field "the requested depth is still recorded" "deep" '.data.budget.requested'
assert_json_field "the recorded finder count is the one that launched" "1" '.data.budget.finders'
assert_json_field "a single finder records no cross-check" "off" '.data.budget.cross_check'
assert_json_field "the override names the winning lever" "finders" '.data.budget.overridden_by'

# --- (iii) a configured sensitive path escalates a docs diff ----------------
before="$(spawns)"
run_review run acme/app local-sensitive --budget auto --rounds 1 --agent claude --json
assert_exit "a sensitive path runs the deep pass" 0
assert_eq "deep pays two finders and both cross-checks" "4" "$(( $(spawns) - before ))"
assert_json_field "a glob outranks the docs-only rule" "deep" '.data.budget.tier'
assert_json_field "the flag layer asked for it" "flag" '.data.budget.source'
assert_json_field "the recorded reason names the glob" "sensitive-path: notes.txt" '.data.budget.reasons[0]'
assert_json_field "deep launches two finders" "2" '.data.budget.finders'
assert_json_field "deep records the cross-check it pays for" "opposite-finder" '.data.budget.cross_check'
assert_json_field "the coordinator's manifest carries the decision" "deep" '.budget.tier' "$SENSITIVE/launcher.json"
assert_json_field "the manifest names the budget as the finder source" "budget" '.finder_source' "$SENSITIVE/launcher.json"

# --- (iv) a two-finder resolution trips the existing render refusal ---------
before="$(spawns)"
run_review run acme/app local-sensitive --budget auto --print --json
assert_exit "rendering a deep tier refuses" 1
assert_refusal "deep render" "bad-flags"
assert_json_field "the refusal points at the override that outranks the budget" "true" \
  '.error.message | contains("use --finders 1")'
assert_eq "a refused render launched nothing" "$before" "$(spawns)"

# --- (v) an unknown depth is a usage error before anything spawns -----------
before="$(spawns)"
run_review run acme/app local-sensitive --budget fast --agent claude --json
assert_exit "an unknown --budget refuses" 2
assert_file_grep "the usage error names the whole vocabulary" "$EVAL_ERR" \
  "--budget must be auto|light|standard|deep" "-F"
assert_eq "an unknown --budget launched nothing" "$before" "$(spawns)"

# --- (vi) nothing in this case called a host -------------------------------
assert_stream_nogrep "the whole case made no host call" "$EVAL_STUBLOG" "stub-gh"

eval_end
