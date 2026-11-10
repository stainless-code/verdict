#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

HOME_DIR="$EVAL_SANDBOX/agents-home"
mkdir -p "$HOME_DIR"

run_agents() {
  set +e
  (
    cd "$EVAL_SANDBOX" || exit 99
    HOME="$HOME_DIR" XDG_CONFIG_HOME="$HOME_DIR/.config" PATH="$EVAL_STUBBIN:$PATH" \
      "$EVAL_CLI" "$@" >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  # shellcheck disable=SC2034 # EVAL_STATUS is read by the assert helpers
  EVAL_STATUS=$?
  set -e
  return 0
}

run_agents skill install --agent claude --agent cursor --json
assert_exit "repeatable --agent installs both agents" 0
assert_json_field "each agent appears once in argv order" "claude,cursor" '.data.agents | join(",")'
assert_json_field "two targets" "2" '.data.targets | length'
assert_file_eq "claude payload" "$EVAL_SKILL/SKILL.md" "$HOME_DIR/.claude/skills/verdict/SKILL.md"
assert_file_eq "cursor payload" "$EVAL_SKILL/SKILL.md" "$HOME_DIR/.cursor/skills/verdict/SKILL.md"

run_agents skill status --agent cursor --agent claude --json
assert_exit "repeatable status is in sync" 0
assert_json_field "status follows argv order" "cursor,claude" '.data.agents | join(",")'
assert_json_field "both targets are in sync" "true" '.data.in_sync'

run_agents skill uninstall --agent cursor --agent claude --json
assert_exit "repeatable uninstall exits 0" 0
assert_json_field "both agents removed" "removed,removed" '[.data.targets[].status] | join(",")'
if [ ! -e "$HOME_DIR/.claude/skills/verdict" ] && [ ! -e "$HOME_DIR/.cursor/skills/verdict" ]; then
  good "both empty target dirs were pruned"
else
  bad "an uninstall left its target behind"
fi

if [ ! -s "$EVAL_STUBLOG" ]; then
  good "the repeatable-agent legs make zero host calls (offline)"
else
  bad "the repeatable-agent legs called gh"
fi

eval_end
