#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q
  git config user.email "trend-incidents-eval@example.com"
  git config user.name "trend incidents eval"
  echo ".verdict/state/" >.gitignore
  git add -A
  git commit -qm init
)

SHA_A=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
SHA_B=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
SHA_C=cccccccccccccccccccccccccccccccccccccccc

eval_mk_bundle local-a
eval_write_findings local-a <<'EOF'
[{"kind":"inline","path":"src/a.ts","side":"RIGHT","line":2,"anchored":true,"body":"Unhandled await error path here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","sig":"a111111111111111111111111111111111111111","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"addressed","rationale":"","history":[]}]
EOF
eval_mk_bundle local-b
printf '[]' | eval_write_findings local-b
mkdir -p "$EVAL_SANDBOX/.verdict/state/local-c"
printf '[]' | eval_write_findings local-c

run_review learn --incident local-a --sha "$SHA_A" --path src/a.ts --json
assert_exit "attach to local-a exits 0" 0
run_review learn --incident local-b --sha "$SHA_B" --path src/c.ts --json
assert_exit "attach to local-b exits 0" 0
run_review learn --incident local-c --sha "$SHA_C" --path src/b.ts --json
assert_exit "attach to local-c exits 0" 0
rm "$EVAL_SANDBOX/.verdict/state/local-c/findings.json"

run_review trend acme/app --incidents --json
assert_exit "trend --incidents exits 0" 0
assert_json_field "every attached incident counts" "3" '.data.incidents.incidents'
assert_json_field "two bundles can measure" "2" '.data.incidents.measured'
assert_json_field "the bundle without a findings store is unmeasured" "1" '.data.incidents.unmeasured'
assert_json_field "the caught incident hits" "1" '.data.incidents.hit'
assert_json_field "the unreported incident misses" "1" '.data.incidents.missed'
assert_json_field "miss rate is the measured share" "0.5" '.data.incidents.miss_rate'
assert_json_field "per_bundle splits by bundle" "local-a:1/1/1/0 local-b:1/1/0/1 local-c:1/0/0/0" \
  '.data.incidents.per_bundle | map("\(.bundle):\(.incidents)/\(.measured)/\(.hit)/\(.missed)") | join(" ")'
assert_json_field "an unmeasured bundle reads a null miss rate" "null" \
  '.data.incidents.per_bundle[] | select(.bundle == "local-c") | .miss_rate'
assert_json_field "miss_rate is 4 decimal places" "true" \
  '(.data.incidents.miss_rate | type) == "number"'

run_review trend acme/app --incidents
assert_exit "human trend --incidents exits 0" 0
assert_file_grep "human line tallies hit and miss" "$EVAL_OUT" \
  "incidents: 3 attached, 2 measured, 1 unmeasured — missed 1 of 2 (50.0%)"

run_review trend acme/app --json
assert_exit "un-flagged trend exits 0" 0
assert_file_nogrep "un-flagged trend never mentions incidents" "$EVAL_OUT" '"incidents"'
assert_json_field "un-flagged trend omits the incidents join" "false" 'has("incidents")'

eval_write_findings local-a <<'EOF'
[{"kind":"inline","path":"src/a.ts","side":"RIGHT","line":2,"anchored":true,"body":"Unhandled await error path here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","sig":"a111111111111111111111111111111111111111","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"false-positive","rationale":"","history":[]}]
EOF
run_review trend acme/app --incidents --json
assert_exit "trend --incidents exits 0 after the flip" 0
assert_json_field "a false positive never counts as a catch" "2" '.data.incidents.missed'
assert_json_field "miss rate follows the flip" "1" '.data.incidents.miss_rate'

cp "$EVAL_SANDBOX/.verdict/incidents.jsonl" "$EVAL_SANDBOX/incidents.hold.jsonl"
rm "$EVAL_SANDBOX/.verdict/incidents.jsonl"
run_review trend acme/app --incidents --json
assert_exit "trend --incidents exits 0 without a ledger" 0
assert_json_field "an absent ledger reads a zero tally" "0 0 0 0 0" \
  '"\(.data.incidents.incidents) \(.data.incidents.measured) \(.data.incidents.unmeasured) \(.data.incidents.hit) \(.data.incidents.missed)"'
assert_json_field "an absent ledger reads a null miss rate" "null" '.data.incidents.miss_rate'
assert_json_field "an absent ledger reads no bundle rows" "0" '.data.incidents.per_bundle | length'

: >"$EVAL_SANDBOX/.verdict/incidents.jsonl"
run_review trend acme/app --incidents --json
assert_exit "trend --incidents exits 0 on an empty ledger" 0
assert_json_field "an empty ledger reads zero incidents" "0" '.data.incidents.incidents'
assert_json_field "an empty ledger reads a null miss rate" "null" '.data.incidents.miss_rate'
cp "$EVAL_SANDBOX/incidents.hold.jsonl" "$EVAL_SANDBOX/.verdict/incidents.jsonl"

run_review trend acme/app --incidents --json
assert_exit "trend --incidents exits 0 over the restored ledger" 0
jq -S '.data.incidents' "$EVAL_OUT" >"$EVAL_SANDBOX/cli-incidents.json"

MCP_OUT="$EVAL_SANDBOX/mcp-out.jsonl"
MCP_ERR="$EVAL_SANDBOX/mcp-err.txt"
FRAMES="$EVAL_SANDBOX/mcp-frames.jsonl"
cat >"$FRAMES" <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"eval","version":"1"}}}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"trend","arguments":{"repo":"acme/app","incidents":true}}}
EOF
set +e
(
  cd "$EVAL_SANDBOX" || exit 99
  PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" mcp >"$MCP_OUT" 2>"$MCP_ERR"
) <"$FRAMES"
EVAL_STATUS=$?
set -e
if [ "$EVAL_STATUS" -eq 0 ]; then good "mcp trend exits 0"; else bad "mcp exit $EVAL_STATUS (want 0)"; fi
jq -S 'select(.id == 2) | .result.structuredContent.incidents' "$MCP_OUT" >"$EVAL_SANDBOX/mcp-incidents.json"
assert_file_eq "mcp trend returns the CLI incidents object" \
  "$EVAL_SANDBOX/cli-incidents.json" "$EVAL_SANDBOX/mcp-incidents.json"

assert_eq "trend --incidents makes no host call" "0" "$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

eval_end
