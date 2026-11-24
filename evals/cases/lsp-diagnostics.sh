#!/usr/bin/env bash
#
# `verdict lsp` end to end over stdio: Content-Length framing, the advertised
# capability set, publishDiagnostics on initialized and didOpen with the
# exact severity, code, and range, no frame for an unchanged save or any
# didChange (proved by frame order against a barrier request), a refresh
# that clears a finding triaged out of band, the exit contract, and a store
# the server never writes.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
# shellcheck source=../lib/lsp.sh
. "$HERE/../lib/lsp.sh"

RAW="$EVAL_SANDBOX/lsp-out.bin"
ERR="$EVAL_SANDBOX/lsp-err.txt"
OUT="$EVAL_SANDBOX/lsp-out.jsonl"
SIG_CRIT="c111111111111111111111111111111111111111"
SIG_INFO="d333333333333333333333333333333333333333"
SIG_FP="e444444444444444444444444444444444444444"

finding() { # sig status severity line
  jq -cn --arg s "$1" --arg st "$2" --arg sev "$3" --argjson l "$4" '{
    kind: "inline", path: "notes.txt", side: "RIGHT", line: $l, anchored: true,
    body: ("Finding " + $s[0:8] + " body with enough substance."), suggestion: null,
    inBounds: true, rigor: "walked", severity: $sev, sig: $s, sig_version: "1",
    firstSeen: "aaaaaaa", lastSeen: "aaaaaaa", status: $st, rationale: "", history: []}'
}

# run_lsp runs the server in this shell (never as a pipeline stage, which
# would lose EVAL_STATUS to a subshell); stdin carries the frames.
run_lsp() { # stdin = frames; args = lsp flags
  set +e
  (cd "$EVAL_SANDBOX" && "$EVAL_CLI" lsp "$@" >"$RAW" 2>"$ERR")
  EVAL_STATUS=$?
  set -e
}

# split_frames turns the raw stdout into one JSON body per line.
split_frames() {
  if lsp_bodies "$RAW" >"$OUT"; then :; else bad "stdout is not pure LSP frames"; fi
}

eval_mk_bundle local-lsp
printf '[%s,%s,%s]' "$(finding "$SIG_CRIT" open critical 2)" "$(finding "$SIG_INFO" open informational 1)" \
  "$(finding "$SIG_FP" false-positive important 3)" | eval_write_findings local-lsp
STORE="$EVAL_SANDBOX/.verdict/state/local-lsp/findings.json"
store_sum() { { sha256sum "$STORE" 2>/dev/null || shasum -a 256 "$STORE"; } | cut -d' ' -f1; }
BEFORE="$(store_sum)"
URI="$(lsp_file_uri "$EVAL_SANDBOX/notes.txt")"
TEXT="$(printf 'top\nnew line\nbottom\n')"
open_msg="$(jq -cn --arg u "$URI" --arg t "$TEXT"$'\n' '{jsonrpc:"2.0",method:"textDocument/didOpen",
  params:{textDocument:{uri:$u,languageId:"plaintext",version:1,text:$t}}}')"
save_msg="$(jq -cn --arg u "$URI" --arg t "$TEXT"$'\n' '{jsonrpc:"2.0",method:"textDocument/didSave",
  params:{textDocument:{uri:$u},text:$t}}')"
change_msg() { jq -cn --arg u "$URI" --argjson v "$1" '{jsonrpc:"2.0",method:"textDocument/didChange",
  params:{textDocument:{uri:$u,version:$v},contentChanges:[{text:"edited\n"}]}}'; }

# Leg 1: handshake, initial publish, didOpen, then an unchanged save and two
# keystrokes that must write nothing before the barrier's -32601.
{
  lsp_frame '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"processId":null,"capabilities":{}}}'
  lsp_frame '{"jsonrpc":"2.0","method":"initialized","params":{}}'
  lsp_frame "$open_msg"
  lsp_frame '{"jsonrpc":"2.0","id":2,"method":"verdict/barrier"}'
  lsp_frame "$save_msg"
  lsp_frame "$(change_msg 2)"
  lsp_frame "$(change_msg 3)"
  lsp_frame '{"jsonrpc":"2.0","id":3,"method":"verdict/barrier"}'
  lsp_frame '{"jsonrpc":"2.0","id":4,"method":"shutdown"}'
  lsp_frame '{"jsonrpc":"2.0","method":"exit"}'
} >"$EVAL_SANDBOX/leg1.frames"
run_lsp --root "$EVAL_SANDBOX" --no-watch <"$EVAL_SANDBOX/leg1.frames"
split_frames
if [ "$EVAL_STATUS" -eq 0 ]; then good "shutdown then exit exits 0"; else bad "exit $EVAL_STATUS (want 0): $(head -c 300 "$ERR")"; fi
if jq -se 'length > 0 and all(.[]; .jsonrpc == "2.0")' <"$OUT" >/dev/null; then
  good "stdout is JSON-RPC 2.0 frames only"
else
  bad "a stdout frame is not JSON-RPC 2.0"
fi
if jq -se '.[] | select(.id == 1) | .result |
  (.serverInfo.name == "verdict") and (.capabilities.positionEncoding == "utf-16") and
  (.capabilities.textDocumentSync == {openClose: true, change: 1, save: {includeText: true}}) and
  (.capabilities.executeCommandProvider.commands == ["verdict.refresh"]) and
  (.capabilities | keys == ["executeCommandProvider", "positionEncoding", "textDocumentSync"])' <"$OUT" >/dev/null; then
  good "initialize advertises full sync, save text, and verdict.refresh only (no hover, no code actions)"
else
  bad "initialize capabilities drifted: $(jq -sc '.[] | select(.id == 1)' <"$OUT")"
fi
if jq -se --arg u "$URI" --arg c "$SIG_CRIT" --arg i "$SIG_INFO" '
  [.[] | select(.method == "textDocument/publishDiagnostics")] |
  (length == 2) and (.[0].params.version == null) and (.[1].params.uri == $u) and (.[1].params.version == 1) and
  ([.[].params.diagnostics | map(.code)] == [[$i, $c], [$i, $c]]) and
  (.[1].params.diagnostics[1] | (.severity == 1) and (.source == "verdict") and
    (.range == {start: {line: 1, character: 0}, end: {line: 1, character: 8}}) and
    (.message == "Finding c1111111 body with enough substance. · rigor walked") and
    (.data.anchor == "exact") and (.data.span_matches == true) and (.data.bundle == "local-lsp")) and
  (.[1].params.diagnostics[0] | (.severity == 3) and (.range.start.line == 0))' <"$OUT" >/dev/null; then
  good "initialized and didOpen publish critical→1 and informational→3 at exact ranges; false-positive never publishes"
else
  bad "publishDiagnostics frames drifted: $(jq -sc '[.[] | select(.method)]' <"$OUT")"
fi
if jq -se '[.[] | (.id // .method)] as $seq | ($seq | index(3)) == (($seq | index(2)) + 1)' <"$OUT" >/dev/null; then
  good "an unchanged save and two didChange notifications wrote zero frames (barrier 3 follows barrier 2)"
else
  bad "frames appeared between the barriers: $(jq -sc '[.[] | (.id // .method)]' <"$OUT")"
fi

# Leg 2: a triage in a terminal mid-session, then verdict.refresh clears it.
# The producer runs beside the server, so the rewrite lands mid-stream; the
# assertion reads only the final published set, which holds whichever frame
# the server read first.
leg2() {
  lsp_frame '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{}}}'
  lsp_frame '{"jsonrpc":"2.0","method":"initialized","params":{}}'
  lsp_frame "$open_msg"
  printf '[%s,%s]' "$(finding "$SIG_CRIT" wont-fix critical 2)" "$(finding "$SIG_INFO" open informational 1)" |
    eval_write_findings local-lsp
  lsp_frame '{"jsonrpc":"2.0","id":2,"method":"workspace/executeCommand","params":{"command":"verdict.refresh"}}'
  lsp_frame '{"jsonrpc":"2.0","id":3,"method":"workspace/executeCommand","params":{"command":"verdict.reviewFile"}}'
  lsp_frame '{"jsonrpc":"2.0","id":4,"method":"shutdown"}'
  lsp_frame '{"jsonrpc":"2.0","method":"exit"}'
}
run_lsp --root "$EVAL_SANDBOX" --no-watch < <(leg2)
split_frames
if jq -se --arg u "$URI" --arg i "$SIG_INFO" '
  ([.[] | select(.method == "textDocument/publishDiagnostics" and .params.uri == $u)] | last.params.diagnostics | map(.code)) == [$i] and
  (.[] | select(.id == 2) | .result.roots[0].bundle == "local-lsp")' <"$OUT" >/dev/null; then
  good "verdict.refresh republishes and the wont-fix finding is cleared"
else
  bad "refresh did not clear the triaged finding: $(jq -sc '[.[] | select(.id == 2 or .method)]' <"$OUT")"
fi
if jq -se '.[] | select(.id == 3) | .error | (.code == -32602) and (.data.code == "bad-params") and (.data.hint | startswith("try: "))' <"$OUT" >/dev/null; then
  good "an unadvertised command refuses -32602 bad-params with a hint"
else
  bad "unknown command answer drifted: $(jq -sc '.[] | select(.id == 3)' <"$OUT")"
fi
AFTER="$(store_sum)"
EXPECT="$(printf '[%s,%s]' "$(finding "$SIG_CRIT" wont-fix critical 2)" "$(finding "$SIG_INFO" open informational 1)" |
  { sha256sum 2>/dev/null || shasum -a 256; } | cut -d' ' -f1)"
if [ "$AFTER" = "$EXPECT" ] && [ "$BEFORE" != "$AFTER" ]; then
  good "the findings store holds exactly what the terminal wrote: the server wrote nothing"
else
  bad "findings.json changed under the server"
fi

# Leg 3: the exit contract and the start refusals.
{
  lsp_frame '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{}}}'
  lsp_frame '{"jsonrpc":"2.0","method":"exit"}'
} >"$EVAL_SANDBOX/leg3.frames"
run_lsp --root "$EVAL_SANDBOX" --no-watch <"$EVAL_SANDBOX/leg3.frames"
split_frames
if [ "$EVAL_STATUS" -eq 1 ] && jq -se 'length == 1 and .[0].id == 1' <"$OUT" >/dev/null; then
  good "exit without shutdown exits 1 after answering initialize"
else
  bad "exit without shutdown = $EVAL_STATUS (want 1)"
fi
run_lsp --root "$EVAL_SANDBOX" --bundle local-nope --json </dev/null
if [ "$EVAL_STATUS" -eq 1 ] && jq -e '.error.code == "missing-bundle"' <"$RAW" >/dev/null; then
  good "--bundle naming no bundle refuses missing-bundle before serving"
else
  bad "--bundle local-nope = exit $EVAL_STATUS: $(head -c 300 "$RAW")"
fi
set +e
"$EVAL_CLI" lsp --allow-run </dev/null >/dev/null 2>"$ERR"
EVAL_STATUS=$?
set -e
if [ "$EVAL_STATUS" -eq 2 ] && matches -F "unknown flag '--allow-run' for lsp" <"$ERR"; then
  good "--allow-run is not accepted by this build (exit 2)"
else
  bad "--allow-run = exit $EVAL_STATUS: $(head -c 200 "$ERR")"
fi

eval_end
