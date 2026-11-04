#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

SANDBOX="$(cd "$EVAL_SANDBOX" && pwd)"  # logical pwd: the sandbox may carry //
DETECT_HOME="$SANDBOX/detect-home"
mkdir -p "$DETECT_HOME/.codex/skills" "$DETECT_HOME/.cursor/skills" "$DETECT_HOME/.claude/skills"
printf 'existing skill\n' >"$DETECT_HOME/.codex/skills/existing.md"
printf 'existing skill\n' >"$DETECT_HOME/.claude/skills/existing.md"

run_detect() {
  local home="$1"
  shift
  set +e
  (
    cd "$SANDBOX" || exit 99
    HOME="$home" XDG_CONFIG_HOME="$home/.config" PATH="$EVAL_STUBBIN:$PATH" \
      "$EVAL_CLI" "$@" >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  # shellcheck disable=SC2034 # EVAL_STATUS is read by the assert helpers
  EVAL_STATUS=$?
  set -e
  return 0
}

run_detect "$DETECT_HOME" skill install --detect --dry-run
assert_exit "detect previews over the live skills dirs" 0
assert_eq "human targets are the live agents in table order" "claude codex " \
  "$(sed -n 's/^--- \([a-z]*\): .*/\1 /p' "$EVAL_OUT" | tr -d '\n')"
if [ ! -e "$DETECT_HOME/.cursor/skills/verdict" ] && [ ! -e "$DETECT_HOME/.gemini" ]; then
  good "an empty skills dir and an absent one are not targeted"
else
  bad "detect targeted a skills dir that is not live"
fi

run_detect "$DETECT_HOME" skill install --detect --dry-run --json
assert_exit "detect dry-run --json exits 0" 0
assert_envelope "detect dry-run"
assert_json_field "detected agents" "claude,codex" '.data.agents | join(",")'
assert_json_field "target count" "2" '.data.targets | length'
assert_json_field "first target agent" "claude" '.data.targets[0].agent'
assert_json_field "second target agent" "codex" '.data.targets[1].agent'
assert_json_field "first target status" "create" '.data.targets[0].status'
assert_json_field "first target path" "$DETECT_HOME/.claude/skills/verdict" '.data.targets[0].target'
assert_json_field "dry_run is recorded" "true" '.data.dry_run'
if [ ! -e "$DETECT_HOME/.claude/skills/verdict" ] && [ ! -e "$DETECT_HOME/.codex/skills/verdict" ]; then
  good "dry-run created no target"
else
  bad "dry-run created a target"
fi

run_detect "$DETECT_HOME" skill install --detect
assert_exit "detect applies" 0
assert_file_eq "claude payload is the embedded SKILL.md" "$EVAL_SKILL/SKILL.md" \
  "$DETECT_HOME/.claude/skills/verdict/SKILL.md"
assert_file_eq "codex payload is the embedded SKILL.md" "$EVAL_SKILL/SKILL.md" \
  "$DETECT_HOME/.codex/skills/verdict/SKILL.md"
assert_eq "claude receipt version" "$(jq -r .version "$EVAL_ROOT/package.json")" \
  "$(jq -r .version "$DETECT_HOME/.claude/skills/verdict/.verdict-managed")"
assert_eq "codex receipt version" "$(jq -r .version "$EVAL_ROOT/package.json")" \
  "$(jq -r .version "$DETECT_HOME/.codex/skills/verdict/.verdict-managed")"
assert_eq "an existing skill file survives" "existing skill" \
  "$(cat "$DETECT_HOME/.claude/skills/existing.md")"
run_detect "$DETECT_HOME" skill install --detect --json
assert_json_field "claude re-run keeps" "keep" '.data.targets[0].status'
assert_json_field "codex re-run keeps" "keep" '.data.targets[1].status'
assert_json_field "a keep writes nothing" "0" '[.data.targets[].written] | add'

NONE_HOME="$SANDBOX/none-home"
mkdir -p "$NONE_HOME"
run_detect "$NONE_HOME" skill install --detect --json
assert_exit "none detected refuses" 1
assert_refusal "none detected" "skill-no-detected-agent"
assert_file_grep "refusal names --agent" "$EVAL_OUT" "--agent <name>" "-F"
if [ -z "$(ls -A "$NONE_HOME")" ]; then
  good "none detected wrote nothing"
else
  bad "none detected wrote into HOME: $(ls -A "$NONE_HOME")"
fi

run_detect "$DETECT_HOME" skill install --detect --dir "$SANDBOX/dir-scope" --json
assert_exit "--detect with --dir is a usage error" 2
assert_stream_nogrep "the --dir usage error writes no envelope" "$EVAL_OUT" '"schema_version"'
run_detect "$DETECT_HOME" skill install --detect --agent claude --json
assert_exit "--detect with --agent is a usage error" 2
assert_stream_nogrep "the --agent usage error writes no envelope" "$EVAL_OUT" '"schema_version"'
if [ ! -e "$SANDBOX/dir-scope/verdict" ]; then
  good "the excluded --dir scope was never installed"
else
  bad "the usage error still installed into --dir"
fi

FOREIGN_HOME="$SANDBOX/foreign-home"
mkdir -p "$FOREIGN_HOME/.claude/skills/verdict" "$FOREIGN_HOME/.codex/skills"
printf 'foreign bytes\n' >"$FOREIGN_HOME/.claude/skills/verdict/keep.txt"
printf 'existing skill\n' >"$FOREIGN_HOME/.codex/skills/existing.md"
run_detect "$FOREIGN_HOME" skill install --detect --json
assert_exit "a foreign target refuses" 1
assert_refusal "foreign target" "skill-dir-foreign"
assert_file_grep "the refusal names the blocked target" "$EVAL_OUT" \
  "$FOREIGN_HOME/.claude/skills/verdict" "-F"
if [ ! -e "$FOREIGN_HOME/.codex/skills/verdict" ]; then
  good "a blocked target wrote no other target"
else
  bad "one blocked target still installed into the other"
fi
assert_file_grep "the foreign file is untouched" "$FOREIGN_HOME/.claude/skills/verdict/keep.txt" \
  "foreign bytes" "-F"
run_detect "$FOREIGN_HOME" skill install --detect --force
assert_exit "force installs the whole detected set" 0
assert_file_eq "force wrote the payload beside the foreign file" "$EVAL_SKILL/SKILL.md" \
  "$FOREIGN_HOME/.claude/skills/verdict/SKILL.md"
assert_file_grep "force spared the foreign file" "$FOREIGN_HOME/.claude/skills/verdict/keep.txt" \
  "foreign bytes" "-F"

if [ ! -s "$EVAL_STUBLOG" ]; then
  good "zero stub-gh intercepts across the detect legs (offline)"
else
  bad "the detect legs called gh"
fi

eval_end
