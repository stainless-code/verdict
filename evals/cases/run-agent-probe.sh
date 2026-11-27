#!/usr/bin/env bash
#
# STA-440: a binary-name clash is settled by a help probe before a launch. The
# two `fx` stubs below derive every answer from argv: the agent answers its own
# ask --help and writes the directive's review, while the JSON viewer prints the
# wrong identity. No model, no secret, no host.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

SANDBOX="$(cd "$EVAL_SANDBOX" && pwd)"
LOCAL=local-probe
DIR="$SANDBOX/.verdict/state/$LOCAL"
LAUNCHER="$DIR/launcher.json"
AGENTBIN="$SANDBOX/probebin"
STUB_LOG="$SANDBOX/fx-probe.log"
mkdir -p "$AGENTBIN"
export STUB_LOG

cat >"$AGENTBIN/fx" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
printf 'argc=%s argv=%s\n' "$#" "$*" >>"$STUB_LOG"
if [ "${1:-}" != "ask" ]; then
  echo "fx: expected ask <directive>" >&2
  exit 2
fi
if [ "${2:-}" = "--help" ]; then
  echo "fx ask: runs one noninteractive request and exits"
  echo "  --model <model>       choose the model"
  echo "  --effort <level>      set the reasoning effort"
  echo "  --fast, --ultrafast   request a faster answer"
  exit 0
fi
directive="${!#}"
case "$directive" in
  *" FIRST with {"*)
    rest="${directive#*Write }"
    artifact="${rest%% FIRST with \{*}"
    ;;
  *" in place:"*)
    rest="${directive#*Update }"
    artifact="${rest%% in place:*}"
    ;;
  *)
    rest=""
    artifact=""
    ;;
esac
if [ -z "$artifact" ] || [ "$artifact" = "$rest" ]; then
  echo "fx: the directive named no review path" >&2
  exit 2
fi
cat >"$artifact" <<'JSON'
{"schema_version":"1","summary":"Nit: one note, nothing blocking.","findings":[{"path":"notes.txt","line":2,"side":"RIGHT","claim":"Nit: rename this line.","quote":"new line","confidence":"high","severity":"informational","rigor":"walked"}],"receipts":[{"hunk":"notes.txt\tNEW\t1\t3","quote":"new line"}]}
JSON
STUB
chmod +x "$AGENTBIN/fx"
export PATH="$AGENTBIN:$PATH"

eval_mk_bundle "$LOCAL"

# --- --print renders argv and spawns nothing, so it cannot probe -------------
: >"$STUB_LOG"
run_review run acme/app "$LOCAL" --finders 1 --agent fx --print --json -- --model M
assert_exit "fx --print renders" 0
assert_envelope "fx --print envelope"
assert_json_field "--print uses fx ask" "fx ask --model M" \
  '.data.argv | .[0:4] | join(" ")'
if [ -s "$STUB_LOG" ]; then
  echo "--print spawned a probe: $(cat "$STUB_LOG")" >&2
  exit 1
fi

# --- the real agent passes the probe and records it before the launch --------
# fx declares no state paths, so in the OS review sandbox its launch could not
# write the case's log; this leg grades the probe, so the review opts out.
: >"$STUB_LOG"
run_review run acme/app "$LOCAL" --finders 1 --agent fx --allow-unsandboxed-review --json -- --model M
assert_exit "fx launches after a matching probe" 0
assert_envelope "fx launch envelope"
assert_json_field "the launch accepts one fresh review" "true" '.data.artifact_fresh'
assert_json_field "launcher.json records the probe args" "ask,--help" \
  '.probe.args | join(",")' "$LAUNCHER"
assert_json_field "launcher.json records the passing probe" "true" \
  '.probe.ok' "$LAUNCHER"
assert_file_grep "the probe ran first" "$STUB_LOG" '^argc=2 argv=ask --help$'
assert_file_grep "the launch followed it" "$STUB_LOG" '^argc=4 argv=ask --model M Review acme/app'

# --- a different fx refuses before starting any agent -----------------------
cat >"$AGENTBIN/fx" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
printf 'wrong argc=%s argv=%s\n' "$#" "$*" >>"$STUB_LOG"
if [ "${2:-}" = "--help" ]; then
  echo "fx 1.0: terminal JSON viewer"
  exit 0
fi
echo "fx: JSON viewer has no ask command" >&2
exit 1
STUB
chmod +x "$AGENTBIN/fx"
rm -f "$DIR/review.json" "$DIR/launcher.json"
: >"$STUB_LOG"
run_review run acme/app "$LOCAL" --finders 1 --agent fx --json -- --model M
assert_exit "a clashing binary refuses" 1
assert_refusal "clash" "agent-mismatch"
assert_json_field "the refusal names the documented flag" "true" \
  '.error.message | contains("--ultrafast")'
assert_json_field "the refusal names the resolved path" "true" \
  '.error.message | contains("probe")'
assert_json_field "the refusal hint names settings.json" "true" \
  '(.error.message // "") | contains("settings.json")'
assert_file_grep "only the probe ran" "$STUB_LOG" '^wrong argc=2 argv=ask --help$'
if [ -e "$DIR/review.json" ] || [ -e "$LAUNCHER" ]; then
  echo "the mismatch wrote a launch artifact" >&2
  exit 1
fi
if [ "$(wc -l <"$STUB_LOG" | tr -d ' ')" != "1" ]; then
  echo "the mismatch started an agent: $(cat "$STUB_LOG")" >&2
  exit 1
fi
assert_stream_nogrep "run made no host call" "$EVAL_STUBLOG" "stub-gh"

eval_end
