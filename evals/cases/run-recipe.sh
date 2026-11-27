#!/usr/bin/env bash
#
# STA-330: `run --recipe <slug>` resolves a declared review pipeline out of the
# PR base and records it. The pipeline is a control, so this case grades the
# security property with the CLI: a head edit to .verdict/recipes.json is
# invisible, tampered snapshot bytes refuse `controls-stale`, and both recipe
# refusals (`unknown-recipe`, `bad-recipe`) land before any process starts.
#
# The base is a real git commit and every bundle comes from the real producer
# (`fetch-local --base`), so `run` and `record` read the artifact a fetch
# writes. The agent is the committed cooperating stub on PATH as `claude`: it
# writes exactly the review path the directive names and calls no host.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

STUB_SRC="$EVAL_ROOT/evals/fixtures/run-agent/agent-stub.sh"
SANDBOX="$(cd "$EVAL_SANDBOX" && pwd)"
BUNDLE="$SANDBOX/.verdict/state/local-demo"
LAUNCHER="$BUNDLE/launcher.json"
WORKTREE_RECIPES="$SANDBOX/.verdict/recipes.json"
GIT_ID=(-c user.email=recipes@example.com -c user.name=recipes -c commit.gpgsign=false)
sandbox_git() { (cd "$EVAL_SANDBOX" && git "${GIT_ID[@]}" "$@"); }

# Base document: `quick` declares one lens with a hint and both caps plus a
# budget inside the limit; `spendy` declares no lenses and a budget over it.
BASE_RECIPES='{"schema_version":"1","recipes":{"quick":{"version":"1.2.3","description":"Cheap pass.","budget_usd":0.5,"lenses":[{"id":"logic","model":"sonnet","max_files":8,"max_findings":6}],"synthesize":{"model":"opus"}},"spendy":{"version":"2.0.0","budget_usd":5,"synthesize":{}}}}'
# The head's rewrite: a different version and lens id, plus a `default` entry
# the base does not carry, which the base resolution must never surface.
HEAD_RECIPES='{"schema_version":"1","recipes":{"default":{"version":"9.9.9","synthesize":{}},"quick":{"version":"9.9.9","lenses":[{"id":"evil-lens"}],"synthesize":{}},"spendy":{"version":"9.9.9","synthesize":{}}}}'

# A personal spend limit of 1 makes the declared 5 in `spendy` an over-limit
# projection; the bundle carries it in run.json because the real producer
# resolved it at fetch time.
mkdir -p "$SANDBOX/xdg/verdict"
export XDG_CONFIG_HOME="$SANDBOX/xdg"
printf '{"spend":{"budget_usd":1}}\n' >"$XDG_CONFIG_HOME/verdict/settings.json"
unset REVIEW_BUDGET_USD REVIEW_PANEL REVIEW_MAX_BYTES REVIEW_MODEL || true

if [ ! -f "$STUB_SRC" ]; then
  bail "the committed stub agent is missing: $STUB_SRC"
fi
AGENTBIN="$SANDBOX/agentbin"
mkdir -p "$AGENTBIN"
cp "$STUB_SRC" "$AGENTBIN/claude"
chmod +x "$AGENTBIN/claude"
export PATH="$AGENTBIN:$PATH"
# The review sandbox lets the agent write only its snapshot, TMPDIR, and its
# own state paths: the argv log lives in claude's relocated state directory.
export CLAUDE_CONFIG_DIR="$SANDBOX/claude-state"
mkdir -p "$CLAUDE_CONFIG_DIR"
export STUB_AGENT_LOG="$CLAUDE_CONFIG_DIR/agent-argv.log"

# spawns counts the agent processes the stub itself recorded, so "no process
# started" is the child's own evidence rather than an inference from output.
spawns() {
  if [ -f "$STUB_AGENT_LOG" ]; then
    grep -c '^cwd=' "$STUB_AGENT_LOG" 2>/dev/null || true
  else
    printf '0'
  fi
}

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  echo ".verdict/state/*" >.gitignore
  mkdir -p .verdict
  printf 'top\nold line\nbottom\n' >notes.txt
  printf '%s\n' "$BASE_RECIPES" >.verdict/recipes.json
  git add .gitignore notes.txt
  git add -f .verdict/recipes.json
  git "${GIT_ID[@]}" commit -qm base
)
BASE="$(sandbox_git rev-parse HEAD)"
(
  cd "$EVAL_SANDBOX" || exit 99
  printf 'top\nnew line\nbottom\n' >notes.txt
  printf '%s\n' "$HEAD_RECIPES" >.verdict/recipes.json
  git add notes.txt
  git add -f .verdict/recipes.json
  git "${GIT_ID[@]}" commit -qm head
)

# --- the base document is the producer's snapshot, not the worktree ----------
run_review fetch-local acme/app demo --base "$BASE" --json -- notes.txt
assert_exit "the bundle is produced from the base" 0
assert_file_grep "the snapshot carries the base recipe document" "$BUNDLE/controls/recipes.json" \
  '"version":"1.2.3"' "-F"
assert_file_grep "the worktree carries the head document" "$WORKTREE_RECIPES" '9.9.9' "-F"

# --- (i) --print: data.recipe, the rendered pipeline, and no writes ----------
export STUB_AGENT_MODE=none
before_spawns="$(spawns)"
run_review run acme/app local-demo --finders 1 --agent claude --recipe quick --print --json
assert_exit "--print exits 0 without launching" 0
assert_envelope "--print"
assert_json_field "--print is marked printed" "true" '.data.printed'
assert_json_field "a whole-pipeline print still offers the launch" "true" \
  '.next | contains("re-run without --print to launch it")'
assert_json_field "data.recipe names the base entry" "quick" '.data.recipe.name'
assert_json_field "data.recipe carries the base version, not the head's" "1.2.3" '.data.recipe.version'
assert_json_field "data.recipe names its source" "repo" '.data.recipe.source'
assert_json_field "data.recipe pins the entry with a sha256" "true" \
  '(.data.recipe.sha256 | test("^[0-9a-f]{64}$"))'
assert_json_field "data.recipe is the four stages in pipeline order" "lenses synthesize dedupe rank" \
  '.data.recipe.stages | map(.kind) | join(" ")'
assert_json_field "the lenses stage declares the base lens and its caps" \
  "declared logic sonnet 8 6" \
  '.data.recipe.stages[0] | "\(.lens_source) \(.lenses[0].id) \(.lenses[0].model) \(.lenses[0].max_files) \(.lenses[0].max_findings)"'
assert_json_field "the synthesize stage carries the declared hint" "opus" \
  '.data.recipe.stages[1].model'
assert_json_field "data.recipe carries the declared budget" "0.5" '.data.recipe.budget_usd'
assert_json_field "the rendered argv names the pipeline" "true" \
  '.data.argv[-1] | contains("recipe quick@1.2.3 (repo)")'
assert_file_nogrep "the head's version never reaches the plan" "$EVAL_OUT" "9.9.9" "-F"
assert_file_nogrep "the head's lens never reaches the plan" "$EVAL_OUT" "evil-lens" "-F"
assert_eq "--print launched no agent process" "$before_spawns" "$(spawns)"
if [ ! -e "$LAUNCHER" ] && [ ! -e "$BUNDLE/review.json" ]; then
  good "--print wrote no launcher record and no review"
else
  bad "--print wrote into the bundle (launcher.json or review.json)"
fi

# A declared lens the bundle did not load warns and the run proceeds: lens
# offers are per plan group, and fetch is the authority on them.
assert_json_field "a declared lens the bundle lacks warns once" "1" '.warnings | length'
assert_json_field "the warning names the absent lens" "true" \
  '.warnings[0] | contains("lens \"logic\"")'

# --- (ix) no --recipe resolves the built-in, and says nothing ----------------
# A bundle that loaded no lens is the ordinary default path: the loader reads
# lenses.txt and an absent one means no lens. The producer writes the layer
# when a pack or panel offers lenses, so this leg drops it from a copy to grade
# the run a lens-less bundle actually gets.
LENSLESS="$SANDBOX/.verdict/state/local-lensless"
cp -R "$BUNDLE" "$LENSLESS"
rm -f "$LENSLESS/lenses.txt"
if [ ! -e "$LENSLESS/lenses.txt" ]; then
  good "the lens-less leg really removed the bundle's lens layer"
else
  bad "the lens-less leg left lenses.txt in place (it grades nothing)"
fi
run_review run acme/app local-lensless --finders 1 --agent claude --print --json
assert_exit "the implicit pipeline exits 0 without launching" 0
assert_json_field "the implicit pipeline is the built-in default" "default" '.data.recipe.name'
assert_json_field "the implicit pipeline names the built-in version" "1.0.0" '.data.recipe.version'
assert_json_field "the implicit pipeline names its source" "builtin" '.data.recipe.source'
assert_json_field "the implicit pipeline has four stages" "lenses synthesize dedupe rank" \
  '.data.recipe.stages | map(.kind) | join(" ")'
assert_json_field "the recipe that declares no lenses inherits the bundle's" "bundle" \
  '.data.recipe.stages[0].lens_source'
assert_json_field "a lens-less default run warns about nothing" "0" '.warnings | length'
assert_json_field "the rendered argv names the built-in pipeline" "true" \
  '.data.argv[-1] | contains("recipe default@1.0.0 (builtin)")'

# --- (xii) --stage renders one stage's command, launching nothing ----------
# One stage at a time is the operator's own CI chaining models: the render names
# the stage, its declared hint (as the agent's model argument) and caps, and the
# review.json the stages chain through. Verdict itself still starts no process.
export STUB_AGENT_MODE=none
stage_spawns="$(spawns)"
run_review run acme/app local-demo --finders 1 --agent claude --recipe quick --print --stage 2 --json
assert_exit "--print --stage exits 0 without launching" 0
assert_envelope "--print --stage"
assert_json_field "data.stage names the rendered position" "2" '.data.stage.index'
assert_json_field "data.stage names the stage kind" "synthesize" '.data.stage.kind'
assert_json_field "the rendered command carries the stage's declared model" \
  "claude -p --disallowedTools=Bash,PowerShell --setting-sources=user --allowedTools=Edit(./.verdict/state/local-demo/**) --model opus" \
  '.data.argv[:7] | join(" ")'
assert_json_field "the stage directive names the stage and its declared hint" "true" \
  '.data.argv[-1] | contains("stage 2 of 4 (synthesize) of recipe quick@1.2.3 (repo)") and contains("(model hint opus)")'
assert_json_field "the stage directive names the review it reads" "true" \
  '.data.argv[-1] | contains("Read .verdict/state/local-demo/review.json, the review the previous stage wrote.")'
assert_json_field "the stage hint names the next stage, not a launch" "true" \
  '.next | contains("--print --stage 3") and (contains("re-run without --print") | not)'
assert_json_field "data.recipe still records the whole pipeline" "4" '.data.recipe.stages | length'
assert_eq "--print --stage launched no agent process" "$stage_spawns" "$(spawns)"
if [ ! -e "$LAUNCHER" ] && [ ! -e "$BUNDLE/review.json" ]; then
  good "--print --stage wrote no launcher record and no review"
else
  bad "--print --stage wrote into the bundle (launcher.json or review.json)"
fi

run_review run acme/app local-demo --finders 1 --agent claude --recipe quick --print --stage 1 --json
assert_exit "the lens stage renders its own command" 0
assert_json_field "the lens stage renders the declared lens hint and caps" "true" \
  '.data.argv[-1] | contains("logic (model sonnet, max_files 8, max_findings 6)")'
assert_json_field "the lens stage renders no model argument" "claude -p" '.data.argv[:2] | join(" ")'
assert_json_field "the lens stage's argv is the agent flags, the discovery restriction, the review grant, and one directive" \
  "6" '.data.argv | length'
assert_json_field "the first stage reads no previous review" "true" \
  '.data.argv[-1] | contains("the previous stage wrote") | not'

run_review run acme/app local-demo --finders 1 --agent claude --recipe quick --print --stage 4 --json
assert_exit "the rank stage renders its own command" 0
assert_json_field "the last stage's hint closes the pipeline" "true" \
  '.next | contains("stage 4 closes the pipeline") and contains("verdict record acme/app local-demo")'
assert_json_field "the last stage's hint never points past the pipeline" "true" \
  '.next | contains("--stage 5") | not'

# --- (xiii) every --stage refusal lands before any process -----------------
run_review run acme/app local-demo --finders 1 --agent claude --recipe quick --stage 2 --json
assert_exit "--stage without --print refuses" 1
assert_refusal "--stage without --print" "bad-flags"
assert_json_field "the refusal names what a stage render needs" "true" \
  '.error.message | contains("renders one stage'"'"'s command for --print")'

run_review run acme/app local-demo --finders 1 --agent claude --print --stage 2 --json
assert_exit "--stage without --recipe refuses" 1
assert_refusal "--stage without --recipe" "bad-flags"
assert_json_field "the refusal names the missing pipeline" "true" \
  '.error.message | contains("--recipe is missing")'

run_review run acme/app local-demo --finders 1 --agent claude --recipe quick --print --stage 5 --json
assert_exit "a stage index past the last stage refuses" 1
assert_refusal "a stage index past the last stage" "bad-flags"
assert_json_field "the refusal names the four resolved stages" "true" \
  '.error.message | contains("1 lenses, 2 synthesize, 3 dedupe, 4 rank")'

run_review run acme/app local-demo --finders 1 --agent claude --recipe quick --print --stage 02 --json
assert_exit "a non-canonical stage index refuses" 1
assert_refusal "a non-canonical stage index" "bad-flags"
assert_json_field "the refusal echoes the index it could not read" "true" \
  '.error.message | contains("got '"'"'02'"'"'")'

run_review run acme/app local-demo --finders 1 --agent claude --recipe quick --print --stage ""
assert_exit "an empty stage operand is a usage error, not a whole pipeline" 2
assert_file_grep "the usage error names the operand it wants" "$EVAL_ERR" \
  "--stage needs a value (want <n>)" "-F"

run_review run acme/app local-demo --finders 1 --agent claude --recipe quick --print --stage 2 -- --model sonnet
assert_exit "a pass-through model against a declared hint refuses" 1
assert_file_grep "the refusal names the declared hint" "$EVAL_ERR" \
  "--print --stage 2 renders the declared model hint 'opus'" "-F"
assert_eq "no --stage refusal launched an agent process" "$stage_spawns" "$(spawns)"

# --- (viii) a budget over the personal limit warns once ---------------------
run_review run acme/app local-demo --finders 1 --agent claude --recipe spendy --print --json
assert_exit "an over-limit budget still runs" 0
assert_json_field "the over-limit budget is recorded, not refused" "5" '.data.recipe.budget_usd'
assert_json_field "an over-limit budget warns once" "1" '.warnings | length'
assert_json_field "the budget warning names both amounts" "true" \
  '.warnings[0] | contains("budget_usd 5") and contains("personal spend limit $1")'

# --- (xi) --allow-head-controls resolves the head's own document ------------
# The snapshot normally holds the base bytes; the opt-out holds the head
# worktree's copy instead, which is the one case where a recipe declares from
# the PR it judges. The base document has no `default` entry, so resolving one
# under the flag proves the head's document answered.
run_review fetch-local acme/app demo-head --base "$BASE" --allow-head-controls --json -- notes.txt
assert_exit "a bundle can be fetched from the head worktree controls" 0
HEADSNAP="$SANDBOX/.verdict/state/local-demo-head"
assert_file_grep "the snapshot carries the head document under the flag"   "$HEADSNAP/controls/recipes.json" '"version":"9.9.9"' "-F"
assert_json_field "the opt-out stamps policy head" "head" '.data.controls.policy'
assert_json_field "the opt-out warns once" "1" \
  '[.warnings[] | select(contains("allow-head-controls"))] | length'
run_review run acme/app local-demo-head --finders 1 --agent claude --recipe quick --print --json
assert_exit "the head-controlled bundle exits 0 without launching" 0
assert_json_field "the head's version is what the plan records" "9.9.9" '.data.recipe.version'
assert_json_field "the head entry is still sourced from the repository" "repo" '.data.recipe.source'
assert_json_field "the head's own lens reaches the plan" "evil-lens" \
  '.data.recipe.stages[0].lenses[0].id'
run_review run acme/app local-demo-head --finders 1 --agent claude --recipe default --print --json
assert_exit "the head-only default entry resolves" 0
assert_json_field "a name only the head declares resolves to the repository entry" "9.9.9" \
  '.data.recipe.version'
assert_json_field "...and not to the built-in default" "repo" '.data.recipe.source'
assert_eq "neither head-controlled --print launched an agent process" \
  "$before_spawns" "$(spawns)"

# --- (ii) the launch records the same plan the envelope carries -------------
export STUB_AGENT_MODE=write
run_review run acme/app local-demo --finders 1 --agent claude --recipe quick --json
assert_exit "the launch exits 0 when the agent writes review.json" 0
assert_envelope "launch"
assert_eq "launch ran exactly one agent process" "1" "$(spawns)"
assert_file_grep "the directive the agent received names the pipeline" "$STUB_AGENT_LOG" \
  "recipe quick@1.2.3 (repo)" "-F"
assert_file_grep "the directive carries the declared lens and caps" "$STUB_AGENT_LOG" \
  "logic (model sonnet, max_files 8, max_findings 6)" "-F"
assert_file_nogrep "the directive never carries the recipe description" "$STUB_AGENT_LOG" \
  "Cheap pass." "-F"
assert_json_field "launcher.json records the recipe version" "1.2.3" '.recipe.version' "$LAUNCHER"
jq -c '.data.recipe' "$EVAL_OUT" >"$SANDBOX/recipe-envelope.json"
jq -c '.recipe' "$LAUNCHER" >"$SANDBOX/recipe-launcher.json"
assert_file_eq "launcher.json carries exactly the data.recipe shape" \
  "$SANDBOX/recipe-envelope.json" "$SANDBOX/recipe-launcher.json"

launched_sum="$(shasum "$BUNDLE/review.json")"
run_review record acme/app local-demo --json
assert_exit "record accepts the review the launcher accepted" 0
assert_json_field "record stores exactly one open finding" "1" '.data.findings.open'
assert_json_field "record's item keeps the launcher's anchor" "notes.txt:2" '.data.items[0].locator'
if [ "$(shasum "$BUNDLE/review.json")" = "$launched_sum" ] && [ -f "$BUNDLE/record.json" ]; then
  good "record consumed the launched review byte-identically (record.json written)"
else
  bad "record rewrote review.json or wrote no record.json"
fi

# --- (v) unknown-recipe refuses before any process starts -------------------
before_spawns="$(spawns)"
run_review run acme/app local-demo --finders 1 --agent claude --recipe nope --json
assert_exit "an unknown recipe refuses" 1
assert_refusal "unknown recipe" "unknown-recipe"
assert_json_field "the refusal lists the built-in name" "true" '.error.message | contains("default")'
assert_json_field "the refusal lists the repository names" "true" '.error.message | contains("quick")'
assert_eq "an unknown recipe launched no agent process" "$before_spawns" "$(spawns)"
# The hint reaches a human run (a --json refusal carries the code only), so the
# operator is told where a recipe lives instead of only that this one is gone.
run_review run acme/app local-demo --finders 1 --agent claude --recipe nope
assert_exit "an unknown recipe refuses in human output too" 1
assert_file_grep "the hint names the base document to commit" "$EVAL_ERR" \
  "recipes live in .verdict/recipes.json at the base" "-F"
assert_eq "the second unknown-recipe refusal launched nothing either" "$before_spawns" "$(spawns)"

# --- (iv) tampered snapshot bytes refuse controls-stale ---------------------
TAMPERED="$SANDBOX/.verdict/state/local-tampered"
cp -R "$BUNDLE" "$TAMPERED"
printf '{"schema_version":"1","recipes":{"default":{"version":"6.6.6","synthesize":{}}}}\n' \
  >"$TAMPERED/controls/recipes.json"
run_review run acme/app local-tampered --finders 1 --agent claude --print --json
assert_exit "a recipe edited inside the snapshot refuses" 1
assert_refusal "tampered snapshot" "controls-stale"
assert_eq "a tampered snapshot launched no agent process" "$before_spawns" "$(spawns)"

# --- (vi) a malformed document refuses bad-recipe before any process --------
(
  cd "$EVAL_SANDBOX" || exit 99
  printf '{"schema_version":"1","recipes":{"default":{"version":"1.0.0","synthesize":{},"nope":1}}}\n' \
    >.verdict/recipes.json
  git add -f .verdict/recipes.json
  git "${GIT_ID[@]}" commit -qm malformed
)
BAD_BASE="$(sandbox_git rev-parse HEAD)"
run_review fetch-local acme/app bad --base "$BAD_BASE" --json -- notes.txt
assert_exit "a bundle over a malformed document is still produced" 0
run_review run acme/app local-bad --finders 1 --agent claude --print --json
assert_exit "a malformed recipe refuses" 1
assert_refusal "malformed recipe" "bad-recipe"
assert_json_field "the diagnostic names the JSON path" "true" \
  '.error.message | contains("$.recipes.default")'
assert_json_field "the diagnostic names the offending key" "true" \
  '.error.message | contains("\"nope\"")'
assert_eq "a malformed recipe launched no agent process" "$before_spawns" "$(spawns)"

# --- (x) nothing in this case called a host ---------------------------------
assert_stream_nogrep "the whole case made no host call" "$EVAL_STUBLOG" "stub-gh"

eval_end
