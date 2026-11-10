#!/usr/bin/env bash
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

if [ -z "${EVAL_CLI:-}" ] || [ ! -x "$EVAL_CLI" ]; then
  bail "EVAL_CLI missing (run via evals/run.sh or evals/one.sh after make build)"
fi

export HOME="$EVAL_SANDBOX/fakehome"
mkdir -p "$HOME"
cd "$EVAL_SANDBOX"

set +e
"$EVAL_CLI" mcp config --agent claude --write >"$EVAL_SANDBOX/c-out.txt" 2>"$EVAL_SANDBOX/c-err.txt"
st=$?
set -e
if [ "$st" -eq 0 ] && grep -q "create" "$EVAL_SANDBOX/c-out.txt" && grep -q '"verdict"' "$EVAL_SANDBOX/.mcp.json"; then
  good "claude --write creates .mcp.json"
else
  bad "claude --write create failed (exit $st)"
fi

printf '{"zeta":1,"mcpServers":{"other":{"command":"x"},"verdict":{"command":"verdict","args":["mcp"]}}}' >"$EVAL_SANDBOX/.mcp.json"
cp "$EVAL_SANDBOX/.mcp.json" "$EVAL_SANDBOX/pre-update.json"
set +e
"$EVAL_CLI" mcp config --agent claude --write --read-only >"$EVAL_SANDBOX/u-out.txt" 2>"$EVAL_SANDBOX/u-err.txt"
st=$?
set -e
if [ "$st" -eq 0 ] && grep -q "update" "$EVAL_SANDBOX/u-out.txt"; then
  good "claude --write --read-only updates in place"
else
  bad "claude --write --read-only update failed (exit $st)"
fi
if [ -f "$EVAL_SANDBOX/.mcp.json.verdict.bak" ] && cmp -s "$EVAL_SANDBOX/.mcp.json.verdict.bak" "$EVAL_SANDBOX/pre-update.json"; then
  good "backup is byte-identical to the pre-update file"
else
  bad "backup missing or drifted"
fi
if grep -q '"zeta"' "$EVAL_SANDBOX/.mcp.json" && grep -q -- "--read-only" "$EVAL_SANDBOX/.mcp.json" \
  && [ "$(jq -r '.zeta' "$EVAL_SANDBOX/.mcp.json")" = "1" ] \
  && [ "$(jq -r '.mcpServers.other.command' "$EVAL_SANDBOX/.mcp.json")" = "x" ]; then
  good "unknown keys preserved byte-identical around the update"
else
  bad "unknown keys drifted: $(cat "$EVAL_SANDBOX/.mcp.json")"
fi

printf '{"mcpServers":{"verdict":{"command":"verdict","args":["mcp","--bogus"]}}}' >"$EVAL_SANDBOX/.mcp.json"
cp "$EVAL_SANDBOX/.mcp.json" "$EVAL_SANDBOX/foreign.json"
set +e
"$EVAL_CLI" mcp config --agent claude --write --json >"$EVAL_OUT" 2>"$EVAL_SANDBOX/f-err.txt"
st=$?
set -e
if [ "$st" -eq 1 ] && [ "$(jq -r '.error.code' "$EVAL_OUT")" = "mcp-config-foreign" ]; then
  good "foreign argv still refuses typed mcp-config-foreign"
else
  bad "foreign argv exit $st (want typed mcp-config-foreign)"
fi
if cmp -s "$EVAL_SANDBOX/.mcp.json" "$EVAL_SANDBOX/foreign.json"; then
  good "foreign refusal leaves the file untouched"
else
  bad "foreign refusal rewrote the file"
fi

set +e
"$EVAL_CLI" mcp config --agent opencode --write --json >"$EVAL_OUT" 2>"$EVAL_SANDBOX/o-err.txt"
st=$?
set -e
if [ "$st" -eq 1 ] && [ "$(jq -r '.error.code' "$EVAL_OUT")" = "mcp-config-unmergeable" ] \
  && grep -q "opencode" "$EVAL_OUT"; then
  good "--write for opencode refuses typed naming the host"
else
  bad "opencode --write exit $st (want typed refusal naming opencode)"
fi
if [ ! -e "$EVAL_SANDBOX/opencode.json" ]; then
  good "opencode --write writes nothing"
else
  bad "opencode --write wrote opencode.json"
fi

mkdir -p "$HOME/.gemini"
printf '{"theme":"dark","mcpServers":{"other":{"command":"x"}}}' >"$HOME/.gemini/settings.json"
set +e
"$EVAL_CLI" mcp config --agent gemini --write >"$EVAL_SANDBOX/g-out.txt" 2>"$EVAL_SANDBOX/g-err.txt"
st=$?
set -e
if [ "$st" -eq 0 ] && [ "$(jq -r '.theme' "$HOME/.gemini/settings.json")" = "dark" ] \
  && [ "$(jq -r '.mcpServers.verdict.command' "$HOME/.gemini/settings.json")" = "verdict" ]; then
  good "--write for gemini merges preserving other keys"
else
  bad "gemini --write merge failed (exit $st)"
fi

if "$EVAL_CLI" mcp config --agent claude --read-only 2>/dev/null | matches -- "--read-only" \
  && "$EVAL_CLI" mcp config --agent claude --operator 2>/dev/null | matches -- "--operator"; then
  good "print snippets carry the posture argv"
else
  bad "print snippets miss the posture argv"
fi

set +e
"$EVAL_CLI" mcp config --agent claude --read-only --operator >"$EVAL_SANDBOX/p-out.txt" 2>"$EVAL_SANDBOX/p-err.txt"
st=$?
set -e
if [ "$st" -eq 2 ] && grep -q -- "--read-only and --operator are mutually exclusive" "$EVAL_SANDBOX/p-err.txt" \
  && [ ! -s "$EVAL_SANDBOX/p-out.txt" ]; then
  good "the --read-only --operator pair refuses with the mcp wording"
else
  bad "pair refusal exit $st (want 2 with the mcp wording)"
fi

if [ -e "$EVAL_SANDBOX/.mcp.json" ]; then
  cp "$EVAL_SANDBOX/.mcp.json" "$EVAL_SANDBOX/pre-pair.json"
else
  rm -f "$EVAL_SANDBOX/pre-pair.json"
fi
set +e
"$EVAL_CLI" mcp config --agent claude --read-only --operator --write >"$EVAL_SANDBOX/pw-out.txt" 2>"$EVAL_SANDBOX/pw-err.txt"
st=$?
set -e
if [ "$st" -eq 2 ] && grep -q -- "--read-only and --operator are mutually exclusive" "$EVAL_SANDBOX/pw-err.txt"; then
  if [ -e "$EVAL_SANDBOX/pre-pair.json" ]; then
    if cmp -s "$EVAL_SANDBOX/.mcp.json" "$EVAL_SANDBOX/pre-pair.json"; then
      good "pair refusal under --write leaves the file byte-identical"
    else
      bad "pair refusal under --write rewrote the file"
    fi
  elif [ ! -e "$EVAL_SANDBOX/.mcp.json" ]; then
    good "pair refusal under --write writes nothing"
  else
    bad "pair refusal under --write wrote .mcp.json"
  fi
else
  bad "pair --write exit $st (want 2 with the mcp wording)"
fi

eval_end
