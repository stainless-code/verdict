#!/usr/bin/env bash
# Case mcp-stdio: `verdict mcp` is a JSON-RPC 2.0 MCP server over stdio.
#
# Grades (all hermetic; the network-dead gh stub stays first on PATH):
#   M1  initialize negotiates the revision, advertises tools only, and
#       carries the experimental serverInfo marker.
#   M2  tools/list exposes one tool per core verb (install is never a tool),
#       with reflected input schemas and required keys that exist.
#   M3  ping answers `{}`; an unknown method is -32601; malformed JSON is a
#       -32700 frame with a null id.
#   M4  tools/call report succeeds against a temp .review bundle: the text
#       block is the same data as structuredContent and matches the CLI
#       envelope's data shape.
#   M5  a typed refusal is isError with the code/message/hint intact; an
#       unknown tool and missing required args are -32602, not refusals.
#   M6  --read-only filters the list and rejects a writing tool.
#   M7  stdout is protocol frames only and stdin close exits 0.
#   M8  the post tool dry-runs behind a smart gh stub: the envelope data comes
#       back with no reviews POST and a bad approve is a typed refusal.
#   M9  --operator adds triage (18 tools) and reaches the service; combining it
#       with --read-only is a usage error before the server starts.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

MCP_OUT="$EVAL_SANDBOX/mcp-out.jsonl"
MCP_ERR="$EVAL_SANDBOX/mcp-err.txt"
FRAMES="$EVAL_SANDBOX/mcp-frames.jsonl"

# run_mcp_input <frames> [mcp flags...] — feed canned frames and capture.
run_mcp_input() { # $1 = frames file, rest = mcp flags
  local frames="$1"; shift
  set +e
  (
    cd "$EVAL_SANDBOX" || exit 99
    PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" mcp "$@" >"$MCP_OUT" 2>"$MCP_ERR"
  ) <"$frames"
  EVAL_STATUS=$?
  set -e
}

# frame_count_proves_protocol — every stdout line is one JSON-RPC frame.
check_stdout_frames() {
  local lines docs
  lines="$(wc -l <"$MCP_OUT" | tr -d ' ')"
  docs="$(jq -s 'length' <"$MCP_OUT")"
  if [ "$lines" = "$docs" ] && [ "$lines" -gt 0 ]; then
    good "stdout is $lines protocol frames (nothing else)"
  else
    bad "stdout lines=$lines JSON docs=$docs (stray output?)"
  fi
  if jq -s 'all(.[]; .jsonrpc == "2.0")' <"$MCP_OUT" >/dev/null; then
    good "every frame declares jsonrpc 2.0"
  else
    bad "a frame is not JSON-RPC 2.0"
  fi
}

eval_mk_bundle 5101
printf '[]' | eval_write_findings 5101

# --- M1-M3: lifecycle frames ---
cat >"$FRAMES" <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"eval","version":"1"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"ping"}
{"jsonrpc":"2.0","id":4,"method":"no/such/method"}
not json at all
EOF
run_mcp_input "$FRAMES"
if [ "$EVAL_STATUS" -eq 0 ]; then good "stdin close exits 0"; else bad "exit $EVAL_STATUS (want 0)"; fi
check_stdout_frames
if jq -e 'select(.id == 1).result | (.protocolVersion == "2025-06-18") and (.serverInfo.experimental == true) and (.serverInfo.name == "verdict") and (.capabilities | has("tools"))' <"$MCP_OUT" >/dev/null; then
  good "initialize negotiates and marks experimental"
else
  bad "initialize result is missing the revision/experimental marker"
fi
if jq -e 'select(.id == 2).result.tools | length == 17' <"$MCP_OUT" >/dev/null; then
  good "tools/list carries 17 core tools (triage is operator-only)"
else
  bad "tools/list count is not 17"
fi
if jq -e 'select(.id == 2).result.tools | map(.name) | (index("report") != null) and (index("post") != null) and (index("install") == null) and (index("triage") == null)' <"$MCP_OUT" >/dev/null; then
  good "tool names follow the core surface (install and triage are never tools)"
else
  bad "tool names drifted"
fi
if jq -e 'select(.id == 2).result.tools | all(.[]; .inputSchema.type == "object" and (.inputSchema.properties | type == "object") and (.inputSchema.required | type == "array"))' <"$MCP_OUT" >/dev/null; then
  good "every inputSchema is a closed object schema"
else
  bad "an inputSchema is malformed"
fi
if jq -e 'select(.id == 2).result.tools | map(.inputSchema as $s | $s.required[]? | select($s.properties[.] == null)) | length == 0' <"$MCP_OUT" >/dev/null; then
  good "required keys all name real properties"
else
  bad "a required key has no property"
fi
if jq -e 'select(.id == 3).result == {}' <"$MCP_OUT" >/dev/null; then
  good "ping answers {}"
else
  bad "ping did not answer {}"
fi
if jq -e 'select(.id == 4).error.code == -32601' <"$MCP_OUT" >/dev/null; then
  good "unknown method is -32601"
else
  bad "unknown method did not return -32601"
fi
if jq -e 'select(.id == null).error.code == -32700' <"$MCP_OUT" >/dev/null; then
  good "malformed JSON is a -32700 frame with null id"
else
  bad "malformed JSON did not return -32700"
fi

# --- M4-M5: success, refusal, usage errors ---
cat >"$FRAMES" <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"report","arguments":{"repo":"acme/app","pr":"5101"}}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"report","arguments":{"repo":"acme/app","pr":"9999"}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nope","arguments":{}}}
{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"report","arguments":{"repo":"acme/app"}}}
EOF
run_mcp_input "$FRAMES"
if [ "$EVAL_STATUS" -eq 0 ]; then good "success/refusal session exits 0"; else bad "exit $EVAL_STATUS (want 0)"; fi
check_stdout_frames
if jq -e 'select(.id == 2).result | (.isError == false) and (.content[0].type == "text") and ((.content[0].text | fromjson) == .structuredContent) and (.structuredContent.generated_at | type == "string") and (.structuredContent.dir == ".review/5101")' <"$MCP_OUT" >/dev/null; then
  good "report success returns the envelope data as text and structuredContent"
else
  bad "report success shape drifted"
fi
if jq -e 'select(.id == 3).result | .structuredContent.hint as $hint | (.isError == true) and (.structuredContent.code == "missing-findings") and ($hint | startswith("try:")) and (.content[0].text | contains($hint))' <"$MCP_OUT" >/dev/null; then
  good "typed refusal keeps code/message/hint with isError"
else
  bad "typed refusal shape drifted"
fi
if jq -e 'select(.id == 4).error.code == -32602' <"$MCP_OUT" >/dev/null; then
  good "unknown tool is -32602"
else
  bad "unknown tool did not return -32602"
fi
if jq -e 'select(.id == 5).error.code == -32602' <"$MCP_OUT" >/dev/null; then
  good "missing required argument is -32602"
else
  bad "missing required argument did not return -32602"
fi

# --- M6: read-only policy ---
cat >"$FRAMES" <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"report","arguments":{"repo":"acme/app","pr":"5101"}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"record","arguments":{"repo":"acme/app","pr":"5101"}}}
EOF
run_mcp_input "$FRAMES" --read-only
if [ "$EVAL_STATUS" -eq 0 ]; then good "--read-only session exits 0"; else bad "--read-only exit $EVAL_STATUS"; fi
if jq -e 'select(.id == 2).result.tools | map(.name) | (index("report") != null) and (index("record") == null) and (index("triage") == null)' <"$MCP_OUT" >/dev/null; then
  good "--read-only exposes only non-writing tools"
else
  bad "--read-only tool list drifted"
fi
if jq -e 'select(.id == 3).result.isError == false' <"$MCP_OUT" >/dev/null; then
  good "--read-only still runs report"
else
  bad "--read-only blocked report"
fi
if jq -e 'select(.id == 4).error.code == -32602' <"$MCP_OUT" >/dev/null; then
  good "--read-only rejects record as unknown tool"
else
  bad "--read-only did not reject record"
fi

# --- M9: operator surface ---
cat >"$FRAMES" <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"triage","arguments":{"repo":"acme/app","pr":"5101","sig":"deadbeef","status":"wont-fix","rationale":"eval operator surface rationale","dryRun":true}}}
EOF
run_mcp_input "$FRAMES" --operator --read-only
if [ "$EVAL_STATUS" -eq 2 ] && [ ! -s "$MCP_OUT" ]; then
  good "--operator with --read-only is a usage error before serving"
else
  bad "--operator --read-only exit $EVAL_STATUS (want 2, empty stdout)"
fi
run_mcp_input "$FRAMES" --operator
if [ "$EVAL_STATUS" -eq 0 ]; then good "--operator session exits 0"; else bad "--operator exit $EVAL_STATUS"; fi
if jq -e 'select(.id == 2).result.tools | (length == 18) and (map(.name) | index("triage") != null)' <"$MCP_OUT" >/dev/null; then
  good "--operator exposes 18 tools including triage"
else
  bad "--operator tool list drifted"
fi
if jq -e 'select(.id == 3) | (has("result") and (.error == null))' <"$MCP_OUT" >/dev/null; then
  good "--operator triage reaches the service"
else
  bad "--operator triage did not reach the service"
fi

# --- M8: post dry run (smart gh stub; the reviews POST must never fire) ---
STUB_SAVED="$EVAL_SANDBOX/gh-stub-orig"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
restore_stub() { cp "$STUB_SAVED" "$EVAL_STUBBIN/gh"; }
trap restore_stub EXIT
cat >"$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
if [ "${1:-}" = "api" ] && [ "${2:-}" = "graphql" ]; then
  printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}'; exit 0
fi
case "$*" in
  *reviews*) echo "unexpected reviews POST" >&2; exit 1;;
  *pulls*) printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; exit 0;;
  *user*) printf 'someone-else'; exit 0;;
esac
echo "evals-stub-gh: network disabled ($*)" >&2
exit 1
EOF
chmod +x "$EVAL_STUBBIN/gh"

PR=6201
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,"comments":[]}
EOF
cat >"$FRAMES" <<EOF
{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"post","arguments":{"repo":"acme/app","pr":"$PR","flags":{"approve":"false","reviewFile":".review/$PR/review.json","dryRun":true}}}}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"post","arguments":{"repo":"acme/app","pr":"$PR","flags":{"approve":"yes","dryRun":true}}}}
EOF
mark="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
eval_seed_controls
run_mcp_input "$FRAMES"
if [ "$EVAL_STATUS" -eq 0 ]; then good "post dry-run session exits 0"; else bad "post dry-run exit $EVAL_STATUS"; fi
check_stdout_frames
if jq -e 'select(.id == 1).result | (.isError == false) and ((.content[0].text | fromjson) == .structuredContent) and (.structuredContent.dry_run == true) and (.structuredContent.event == "COMMENT") and (.structuredContent.payload_hash | type == "string")' <"$MCP_OUT" >/dev/null; then
  good "post dry-run returns the envelope data without posting"
else
  bad "post dry-run result drifted: $(cat "$MCP_OUT")"
fi
if [ -f "$EVAL_SANDBOX/.review/$PR/findings.dryrun.json" ] \
  && [ ! -e "$EVAL_SANDBOX/.review/$PR/post.json" ]; then
  good "post dry-run stages findings.dryrun.json and writes no post.json"
else
  bad "post dry-run wrote the wrong artifacts"
fi
if [ "$(tail -n +"$(( mark + 1 ))" "$EVAL_STUBLOG" | grep -c "reviews" || true)" -eq 0 ]; then
  good "post dry-run never called the reviews POST"
else
  bad "post dry-run reached the reviews POST"
fi
if jq -e 'select(.id == 2).result | (.isError == true) and (.structuredContent.code == "bad-approve")' <"$MCP_OUT" >/dev/null; then
  good "post bad approve is a typed refusal"
else
  bad "post bad approve did not refuse typed"
fi

if [ "$fails" -ne 0 ]; then
  echo "mcp-stdio: $fails failed"
  exit 1
fi
echo "mcp-stdio: ok"
