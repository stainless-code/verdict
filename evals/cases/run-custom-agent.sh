#!/usr/bin/env bash
#
# STA-440: a run agent can also come from the operator's own settings file.
# The stub agent below is generated in the sandbox, defined only under
# XDG_CONFIG_HOME, and derives everything it does from the argv the launcher
# hands it: a subcommand, the prompt flag immediately before the directive,
# and the review path quoted in that directive. No model, no secret, no host.
#
# The second half pins the trust boundary: a repo-local .verdict/config.json
# carrying an `agents` key does not define an agent for run, and a collision
# with a shipped name in the prefs file refuses bad-settings.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

SANDBOX="$(cd "$EVAL_SANDBOX" && pwd)"
LOCAL=local-custom
DIR="$SANDBOX/.verdict/state/$LOCAL"
LAUNCHER="$DIR/launcher.json"
SETTINGS_DIR="$SANDBOX/xdg/verdict"
SETTINGS="$SETTINGS_DIR/settings.json"
AGENTBIN="$SANDBOX/custombin"
STUB_LOG="$SANDBOX/custom-agent.log"
mkdir -p "$SETTINGS_DIR" "$AGENTBIN"
export XDG_CONFIG_HOME="$SANDBOX/xdg"
export STUB_LOG

write_settings() {
  printf '%s\n' "$1" >"$SETTINGS"
}

write_settings '{"agents":{"local":{"binary":"custom-agent","args":["exec"],"prompt_flag":"-p","model_flag":"--model","install":"install custom-agent"}}}'

# The stub is a cooperating agent, not a recording of one expected argv: it
# refuses a wrong subcommand or a prompt flag that is not immediately before
# the directive, then writes only the review file the directive names.
cat >"$AGENTBIN/custom-agent" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
printf 'argc=%s\n' "$#" >>"$STUB_LOG"
i=0
for arg in "$@"; do
  printf 'arg[%s]=%s\n' "$i" "$(printf '%s' "$arg" | tr '\n' ' ')" >>"$STUB_LOG"
  i=$((i + 1))
done
if [ "${1:-}" != "exec" ] || [ "$#" -lt 3 ]; then
  echo "custom-agent: expected 'exec <flags> -p <directive>'" >&2
  exit 2
fi
prompt_at=0
for ((i = 1; i <= $#; i++)); do
  if [ "${!i}" = "-p" ]; then
    prompt_at=$i
  fi
done
if [ "$prompt_at" -ne "$(( $# - 1 ))" ]; then
  echo "custom-agent: -p must sit immediately before the directive" >&2
  exit 2
fi
directive="${!#}"
rest="${directive#*Write }"
artifact="${rest%% FIRST with \{*}"
if [ -z "$artifact" ] || [ "$artifact" = "$rest" ]; then
  echo "custom-agent: the directive named no review path" >&2
  exit 2
fi
cat >"$artifact" <<'JSON'
{"schema_version":"1","body":"Nit: one note.","approved":false,"comments":[]}
JSON
STUB
chmod +x "$AGENTBIN/custom-agent"
export PATH="$AGENTBIN:$PATH"

eval_mk_bundle "$LOCAL"

# --- --print: the custom row renders the exact argv and spawns nothing -------
run_review run acme/app "$LOCAL" --agent local --print --json -- --model test-model
assert_exit "a prefs-defined agent prints" 0
assert_envelope "custom agent --print"
assert_json_field "--print names the custom agent" "local" '.data.agent'
assert_json_field "--print names the custom binary" "custom-agent" '.data.binary'
assert_json_field "--print puts the prompt flag before the directive" \
  "custom-agent exec --model test-model -p [prompt]" \
  '.data.argv | "\(.[0:5] | join(" ")) [prompt]"'
assert_json_field "--print keeps the directive last" "true" \
  '.data.argv[-1] | startswith("Review acme/app local change local-custom")'

# --- the launch: one process, provenance marked as prefs, one review ---------
run_review run acme/app "$LOCAL" --agent local --json -- --model test-model
assert_exit "a prefs-defined agent launches" 0
assert_envelope "custom agent launch"
assert_json_field "the launch accepts one fresh review" "true" '.data.artifact_fresh'
assert_json_field "the recorded argv replaces the directive" "true" \
  '(.data.argv | map(select(. == "<prompt>")) | length) == 1'
assert_json_field "launcher.json names the prefs source" "prefs" '.agent_source' "$LAUNCHER"
assert_json_field "launcher.json names the custom agent" "local" '.agent' "$LAUNCHER"
assert_json_field "launcher.json keeps the operator's model" "test-model" \
  '.argv | .[3]' "$LAUNCHER"
assert_file_grep "the agent got its subcommand" "$STUB_LOG" '^arg\[0\]=exec$'
assert_file_grep "the prompt flag sits before the directive" "$STUB_LOG" '^arg\[3\]=-p$'
assert_file_grep "the directive arrived last" "$STUB_LOG" \
  '^arg\[4\]=Review acme/app local change local-custom'
assert_file_grep "the agent wrote no extra argument" "$STUB_LOG" '^argc=5$'
assert_stream_nogrep "run made no host call" "$EVAL_STUBLOG" "stub-gh"

# --- the missing-binary refusal carries the definition's install hint --------
MINBIN="$SANDBOX/minbin"
mkdir -p "$MINBIN"
for tool in git jq sed grep cat dirname mkdir date env; do
  found="$(command -v "$tool" 2>/dev/null || true)"
  [ -n "$found" ] || continue
  ln -sf "$found" "$MINBIN/$tool"
done
set +e
(
  cd "$EVAL_SANDBOX" || exit 99
  PATH="$MINBIN" "$EVAL_CLI" run acme/app "$LOCAL" --agent local --json >"$EVAL_OUT" 2>"$EVAL_ERR"
)
# shellcheck disable=SC2034 # EVAL_STATUS is read by assert_exit in the caller
EVAL_STATUS=$?
set -e
assert_exit "a missing custom binary refuses" 1
assert_refusal "missing custom binary" "agent-missing"
assert_json_field "the refusal names the prefs install hint" "true" \
  '.error.message | contains("install custom-agent")'

# --- discovery and the prefs-only boundary ----------------------------------
run_review run acme/app "$LOCAL" --agent nope --json
assert_exit "an unknown name refuses" 1
assert_refusal "unknown custom name" "unknown-agent"
assert_json_field "the refusal labels the prefs source" "true" \
  '.error.message | contains("operator-defined in prefs: local")'

rm -f "$DIR/review.json" "$DIR/launcher.json"
printf '%s\n' '{"schema_version":"1","agents":{"local":{"binary":"custom-agent"}}}' >"$EVAL_SANDBOX/.verdict/config.json"
write_settings '{}'
run_review run acme/app "$LOCAL" --agent local --json
assert_exit "a repo config cannot define an agent" 1
assert_refusal "repo config is not an agent source" "unknown-agent"
assert_json_field "the refusal says no operator agent is defined" "true" \
  '.error.message | contains("operator-defined in prefs: none")'
rm -f "$EVAL_SANDBOX/.verdict/config.json"

write_settings '{"agents":{"claude":{"binary":"custom-agent"}}}'
run_review run acme/app "$LOCAL" --agent claude --print --json
assert_exit "a colliding name refuses bad-settings" 1
assert_refusal "collision" "bad-settings"
assert_json_field "the refusal names the agents key" "true" \
  '.error.message | contains("agents.claude")'

eval_end
