#!/usr/bin/env bash
#
# STA-300: the first-party CI recipe ships a working agent step, not a
# placeholder. The case grades the workflow as the artifact it is: the Agent
# review step must invoke `verdict run --agent` (never echo a placeholder),
# the step's own `run:` body is extracted and executed offline against a
# produced PR bundle with a stub agent and the stub gh, and a stub that writes
# nothing must still fail the job through the fresh-artifact guard. The doc
# page must match what the workflow ships.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

RECIPE="$EVAL_ROOT/docs/ci-recipe.md"
WORKFLOW="$EVAL_ROOT/examples/verdict-review.yml"
# The stub agent the recipe case launches: a cooperating script with no model,
# no credential, and no host, copied onto PATH under every shipped binary name.
STUB_SRC="$EVAL_ROOT/evals/fixtures/run-agent/agent-stub.sh"
SANDBOX="$(cd "$EVAL_SANDBOX" && pwd)"
PR_BUNDLE=4242
PR_DIR="$SANDBOX/.verdict/state/$PR_BUNDLE"

if [ -f "$RECIPE" ]; then
  good "docs/ci-recipe.md exists"
else
  bad "docs/ci-recipe.md missing"
fi
for anchor in \
  "setup-verdict" "verdict fetch" "review.json" "verdict post" \
  "gate --max-severity" "ready --check" "branch protection" "permissions" "40-hex" \
  "verdict run <repo> <pr> --agent" "VERDICT_AGENT" \
  "no-review-artifact" "stale-review-artifact" \
  "no network call of its own" "never proxies your key"; do
  if grep -q -i -F -- "$anchor" "$RECIPE" 2>/dev/null; then
    good "recipe page names $anchor"
  else
    bad "recipe page misses $anchor"
  fi
done

if [ -f "$WORKFLOW" ]; then
  good "examples/verdict-review.yml exists"
else
  bad "examples/verdict-review.yml missing"
fi
uses_total="$(grep -c -E '^[[:space:]-]*uses:' "$WORKFLOW" 2>/dev/null || true)"
uses_pinned="$(grep -c -E 'uses:[^#]*@[0-9a-f]{40}([[:space:]]|$)' "$WORKFLOW" 2>/dev/null || true)"
case "$uses_total" in ''|0) uses_total=0;; esac
case "$uses_pinned" in ''|0) uses_pinned=0;; esac
if [ "$uses_total" -ge 2 ] && [ "$uses_pinned" = "$uses_total" ]; then
  good "workflow pins all $uses_total uses: entries by full SHA"
else
  bad "workflow has $uses_total uses: entries but only $uses_pinned pinned by full SHA"
fi
assert_stream_nogrep "workflow floats on no moving tag" "$WORKFLOW" 'uses:[^#]*@(v[0-9]|latest|main|master)' "-E"
for anchor in "setup-verdict" "verdict fetch" "verdict post" "verdict report" "verdict ready" \
  "verdict gate" "--max-severity" "--check"; do
  if grep -q -F -- "$anchor" "$WORKFLOW" 2>/dev/null; then
    good "workflow runs $anchor"
  else
    bad "workflow misses $anchor"
  fi
done

# The regression guard the slice exists for: the step is a real launcher, and
# the prose that told the operator to wire it themselves is gone for good.
assert_file_grep "the workflow still has its Agent review step" "$WORKFLOW" \
  "- name: Agent review" "-F"
assert_file_nogrep "the workflow never ships the placeholder agent step" "$WORKFLOW" \
  "wire your agent CLI here" "-F"
for anchor in "no network call of its own" "never proxies your key" \
  "no-review-artifact" "stale-review-artifact"; do
  assert_file_grep "the workflow states $anchor" "$WORKFLOW" "$anchor" "-F"
done

# agent_step_body prints the Agent review step's `run:` script, dedented, so
# the case executes the shipped body instead of a copy of it.
agent_step_body() {
  awk '
    /^      - name: / { want = (index($0, "Agent review") > 0); next }
    want && /^        run: \|$/ { body = 1; next }
    body && /^      - name: / { exit }
    body && /^        [a-z]/ { exit }
    body { sub(/^          /, ""); print }
  ' "$WORKFLOW"
}

# agent_step_env prints the step's `env:` mapping as KEY=VALUE lines. A GitHub
# expression becomes a placeholder: the case never holds a real credential.
agent_step_env() {
  awk '
    /^      - name: / { want = (index($0, "Agent review") > 0); next }
    want && /^        env:$/ { env = 1; next }
    env && /^        [a-z]/ { env = 0 }
    env && /^          [A-Za-z_][A-Za-z0-9_]*:/ {
      line = $0
      sub(/^ +/, "", line)
      key = line
      sub(/:.*$/, "", key)
      value = line
      sub(/^[^:]*: */, "", value)
      if (value ~ /^\$\{\{/) value = "eval-placeholder"
      print key "=" value
    }
  ' "$WORKFLOW"
}

STEPBODY="$EVAL_SANDBOX/agent-step.sh"
STEPONELINE="$EVAL_SANDBOX/agent-step.oneline"
STEPSH="$EVAL_SANDBOX/agent-step.run.sh"
STEPENV="$EVAL_SANDBOX/agent-step.env"
agent_step_body >"$STEPBODY"
if [ -s "$STEPBODY" ] && grep -q -F 'verdict' "$STEPBODY"; then
  good "the Agent review step has a runnable run: body"
else
  bail "the workflow lost its Agent review step (or its run: body): no command to grade"
fi
tr '\n' ' ' <"$STEPBODY" | tr -s ' ' >"$STEPONELINE"
assert_file_grep "the agent step invokes verdict run --agent" "$STEPONELINE" \
  'verdict run .* --agent' "-E"
assert_file_grep "the agent step reads the one configured agent variable" "$STEPONELINE" \
  '--agent "$VERDICT_AGENT"' "-F"

# The step body is executed the way GitHub runs a bash `run:` block, with the
# workflow's own env block sourced and the PR expression bound to a produced
# bundle name. A leftover expression is a loud failure, never a shell accident.
sed -e 's/\${{ github\.event\.pull_request\.number }}/'"$PR_BUNDLE"'/g' \
  "$STEPBODY" >"$STEPSH"
if grep -q -F '${{' "$STEPSH"; then
  bail "the agent step carries a GitHub expression this case cannot expand: $(grep -F -m1 '${{' "$STEPSH")"
fi
agent_step_env >"$STEPENV"
# The agent gets the diff and its own model key, never the workflow's write
# token: GH_TOKEN is scoped to the steps that call gh.
assert_file_nogrep "the Agent review step never receives the workflow token" \
  "$STEPENV" "GH_TOKEN=" "-F"
set -a
# shellcheck disable=SC1090 # generated from the workflow's own env: block
. "$STEPENV"
set +a
case "${VERDICT_AGENT:-}" in
  claude|codex|cursor|opencode)
    good "the recipe's VERDICT_AGENT is a shipped run --agent name ($VERDICT_AGENT)" ;;
  *)
    bad "the recipe's VERDICT_AGENT is '${VERDICT_AGENT:-}', not one of claude|codex|cursor|opencode" ;;
esac
if [ -f "$STUB_SRC" ]; then
  good "the committed stub agent exists"
else
  bail "the committed stub agent is missing: $STUB_SRC"
fi
AGENTBIN="$SANDBOX/agentbin"
VERDICTBIN="$SANDBOX/verdictbin"
mkdir -p "$AGENTBIN" "$VERDICTBIN"
for bin in claude codex cursor-agent opencode; do
  cp "$STUB_SRC" "$AGENTBIN/$bin"
  chmod +x "$AGENTBIN/$bin"
done
# The recipe calls the installed `verdict` (setup-verdict puts it on PATH), so
# the case shims that name onto the binary under test. EVAL_CLI may be relative,
# and a symlink target resolves against the link's own directory.
EVAL_CLI_ABS="$(cd "$(dirname "$EVAL_CLI")" && pwd)/$(basename "$EVAL_CLI")"
ln -sf "$EVAL_CLI_ABS" "$VERDICTBIN/verdict"
export STUB_AGENT_LOG="$SANDBOX/agent-argv.log"

# runs the extracted step with the stub agent, the verdict shim, and the stub
# gh ahead of the system PATH, from the sandbox repo root.
run_recipe_step() {
  set +e
  (
    cd "$EVAL_SANDBOX" || exit 99
    PATH="$AGENTBIN:$VERDICTBIN:$EVAL_STUBBIN:$PATH" GITHUB_REPOSITORY=acme/app \
      bash -eo pipefail "$STEPSH" >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  # shellcheck disable=SC2034 # EVAL_STATUS is read by assert_exit in the caller
  EVAL_STATUS=$?
  set -e
  return 0
}

recipe_spawns() {
  if [ -f "$STUB_AGENT_LOG" ]; then
    grep -c '^cwd=' "$STUB_AGENT_LOG" 2>/dev/null || true
  else
    printf '0'
  fi
}

gh_calls() {
  if [ -f "$EVAL_STUBLOG" ]; then
    grep -c 'stub-gh' "$EVAL_STUBLOG" 2>/dev/null || true
  else
    printf '0'
  fi
}

mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_BUDGET_USD REVIEW_MODEL || true
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  git config user.email "recipe@example.com"
  git config user.name "recipe case"
  printf 'top\nold line\nbottom\n' > notes.txt
  git add notes.txt
  git commit -qm base 2>/dev/null || true
  printf 'top\nnew line\nbottom\n' > notes.txt
)

# --- the shipped step, executed offline against a produced PR bundle --------
# One PR bundle named after the number the expression binds to, built by the
# real producer so `run` sees the artifact a fetch writes.
eval_mk_bundle "$PR_BUNDLE"
gh_before="$(gh_calls)"

export STUB_AGENT_MODE=write
run_recipe_step
assert_exit "the shipped agent step exits 0 when the agent writes a review" 0
assert_json_field "the step's agent wrote the one review artifact" "1" \
  '.schema_version' "$PR_DIR/review.json"
assert_json_field "the step's launcher recorded the configured agent" "$VERDICT_AGENT" \
  '.agent' "$PR_DIR/launcher.json"
assert_file_grep "the directive named the artifact the agent wrote" "$STUB_AGENT_LOG" \
  "^arg\\[[0-9]+\\]=.*Write .verdict/state/$PR_BUNDLE/review.json with " "-E"
assert_file_grep "the agent ran in the repository root" "$STUB_AGENT_LOG" "^cwd=$SANDBOX$"

# An earlier run's artifact is not a fresh review: the step must fail.
touch -t 202001010000 "$PR_DIR/review.json"
export STUB_AGENT_MODE=none
run_recipe_step
assert_exit "the step fails on an artifact that predates the launch" 1
assert_file_grep "the stale refusal names the review file" "$EVAL_ERR" \
  ".verdict/state/$PR_BUNDLE/review.json predates this run" "-F"

# An agent that writes nothing must fail the job, not reach `post`.
rm -f "$PR_DIR/review.json"
run_recipe_step
assert_exit "the step fails when the agent writes no review" 1
assert_file_grep "the no-artifact refusal names the file it wanted" "$EVAL_ERR" \
  "wrote no .verdict/state/$PR_BUNDLE/review.json" "-F"
assert_eq "the three recipe legs launched the stub agent once each" "3" "$(recipe_spawns)"
assert_eq "the shipped agent step made zero host calls" "$gh_before" "$(gh_calls)"
unset STUB_AGENT_MODE ANTHROPIC_API_KEY VERDICT_AGENT

# --- the promised mechanics still work in a scratch repo --------------------
gate_body='{"schema_version":"1","body":"> ✅ Recipe gate review, one note.\\n> Basis: 0 reproduced · 0 ran · 1 walked · 0 pointed · 0 said · 0 rules cited · 0 verified fixes","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"RECIPEBODY","rigor":"walked","severity":"RECIPESEV"}]}'
run_review fetch-local acme/app gatecrit
printf '%s' "$gate_body" | sed 's/RECIPEBODY/Critical: verify this line./; s/RECIPESEV/critical/' \
  > "$EVAL_SANDBOX/.verdict/state/local-gatecrit/review.json"
run_review record acme/app local-gatecrit --json
if [ "$EVAL_STATUS" -ne 0 ]; then
  bad "record exits $EVAL_STATUS on the critical review"
else
  good "record merges the critical review"
fi
run_review report acme/app local-gatecrit --check critical
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "gate fails" "$EVAL_ERR"; then
  good "report --check critical fails the gate on the open critical (exit 1)"
else
  bad "red gate exits $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- notes.txt 2>/dev/null || true
  printf 'top\nanother line\nbottom\n' > notes.txt
)
run_review fetch-local acme/app gateok
printf '%s' "$gate_body" | sed 's/RECIPEBODY/Nit: clarify this line./; s/RECIPESEV/informational/' \
  > "$EVAL_SANDBOX/.verdict/state/local-gateok/review.json"
run_review record acme/app local-gateok --json
if [ "$EVAL_STATUS" -ne 0 ]; then
  bad "record exits $EVAL_STATUS on the informational review"
else
  good "record merges the informational review"
fi
run_review report acme/app local-gateok --check critical
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "gate passes" "$EVAL_ERR"; then
  good "report --check critical passes the gate with no critical findings (exit 0)"
else
  bad "green gate exits $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi

(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- notes.txt 2>/dev/null || true
)
rm -rf "$EVAL_SANDBOX/.verdict" "$EVAL_SANDBOX/xdg-empty"
unset XDG_CONFIG_HOME || true

eval_end
