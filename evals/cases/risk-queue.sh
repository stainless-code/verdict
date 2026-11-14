#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

BODY='> ✅ Clean review summary with enough length to satisfy record validation.'
STATE="$EVAL_SANDBOX/.verdict/state"

# tree_sum renders every state file's path and digest, so a write shows up as a
# difference between the readings around a pure read.
tree_sum() {
  (
    cd "$EVAL_SANDBOX" || exit 99
    find .verdict/state -type f -print0 | LC_ALL=C sort -z | xargs -0 shasum
  )
}

# mk_claimed <bundle> <level> records a clean store carrying one risk claim.
mk_claimed() {
  eval_mk_bundle "$1"
  printf '%s\n' "{
  \"schema_version\": \"1\",
  \"body\": \"$BODY\",
  \"approved\": true,
  \"coverage\": [{\"file\": \"notes.txt\", \"ranges\": \"all\"}],
  \"risk\": {
    \"level\": \"$2\",
    \"rationale\": \"One note line, fully covered.\",
    \"assessed_head\": \"$EVAL_FIXTURE_HEAD\"
  },
  \"comments\": []
}" | eval_write_review "$1"
  run_review record acme/app "$1" --json
  assert_exit "record the claimed-$2 bundle" 0
}

# mk_merged <bundle> <state> pins a produced bundle's host state, so the
# default filter has a merged and a closed bundle to skip.
mk_merged() {
  jq --arg s "$2" '.state = $s' "$STATE/$1/PR.json" >"$STATE/$1/PR.json.tmp"
  mv "$STATE/$1/PR.json.tmp" "$STATE/$1/PR.json"
}

# The queue's nine shapes. Every produced bundle carries full.diff from the
# fixture and no impact.json until leg 5 backfills one.
eval_mk_bundle local-critical
printf '%s\n' "{
  \"schema_version\": \"1\",
  \"body\": \"> [!IMPORTANT]\n> The changed line drops the saved value.\",
  \"approved\": false,
  \"coverage\": [{\"file\": \"notes.txt\", \"ranges\": \"all\"}],
  \"comments\": [
    {
      \"path\": \"notes.txt\",
      \"line\": 2,
      \"side\": \"RIGHT\",
      \"body\": \"Critical logic error in changed line requiring immediate fix.\",
      \"rigor\": \"walked\",
      \"severity\": \"critical\",
      \"category\": \"security\"
    }
  ]
}" | eval_write_review local-critical
run_review record acme/app local-critical --json
assert_exit "record the critical bundle" 0

eval_mk_bundle local-important
printf '%s\n' "{
  \"schema_version\": \"1\",
  \"body\": \"$BODY\",
  \"approved\": false,
  \"coverage\": [{\"file\": \"notes.txt\", \"ranges\": \"all\"}],
  \"comments\": [
    {
      \"path\": \"notes.txt\",
      \"line\": 2,
      \"side\": \"RIGHT\",
      \"body\": \"The lookup ignores the second key and returns the first match.\",
      \"rigor\": \"walked\",
      \"severity\": \"important\",
      \"category\": \"logic\"
    }
  ]
}" | eval_write_review local-important
run_review record acme/app local-important --json
assert_exit "record the important bundle" 0

mk_claimed local-low low
mk_claimed local-high high

eval_mk_bundle local-unclaimed
printf '%s\n' "{
  \"schema_version\": \"1\",
  \"body\": \"$BODY\",
  \"approved\": true,
  \"coverage\": [{\"file\": \"notes.txt\", \"ranges\": \"all\"}],
  \"comments\": []
}" | eval_write_review local-unclaimed
run_review record acme/app local-unclaimed --json
assert_exit "record the unclaimed but covered bundle" 0

eval_mk_bundle 13
eval_mk_bundle 18
eval_mk_bundle 15
mk_merged 15 MERGED
eval_mk_bundle 16
mk_merged 16 CLOSED

# 1) The default run: seven live bundles ordered worst band first, the merged
# and closed bundles skipped with a count, and unmeasured reach warned about.
before="$(tree_sum)"
run_review risk acme/app --json
assert_exit "risk --json succeeds" 0
assert_envelope "risk success envelope"
assert_json_field "verb is risk" "risk" '.verb'
assert_json_field "the queue carries no PR" "null" '.pr'
assert_json_field "the queue counts the live bundles" "7" '.data.bundles'
assert_json_field "row order is band, then score, then bundle" \
  "local-critical local-important local-high 13 18 local-unclaimed local-low" \
  '[.data.rows[].bundle] | join(" ")'
assert_json_field "rank counts from one in row order" "1 2 3 4 5 6 7" \
  '[.data.rows[].rank] | join(" ")'
assert_json_field "the reported bands read worst first" \
  "critical high high medium medium unknown low" \
  '[.data.rows[].reported] | join(" ")'
assert_json_field "by_band tallies every band" \
  "critical=1 high=2 medium=2 unknown=1 low=1" \
  '.data.by_band | "critical=\(.critical) high=\(.high) medium=\(.medium) unknown=\(.unknown) low=\(.low)"'
assert_json_field "needs_human counts the non-low rows" "6" '.data.needs_human'
assert_json_field "by_band sums to bundles" "7" \
  '.data.by_band | .critical + .high + .medium + .unknown + .low'
assert_json_field "a local bundle reads state local and no PR" "local null" \
  '.data.rows[0] | "\(.state) \(.pr)"'
assert_json_field "an open PR row keeps its number and state" "13 open" \
  '.data.rows[3] | "\(.pr) \(.state)"'
assert_json_field "the claim is never lost" "unknown" '.data.rows[0].claimed'
assert_json_field "the critical finding sets the floor" "critical" '.data.rows[0].floor'
assert_json_field "the critical bundle scores its measured components" "28" '.data.rows[0].score.value'
assert_json_field "three of four components measured without impact.json" "3" '.data.rows[0].score.measured'
assert_json_field "reach reads unmeasured" "false" '.data.rows[0].score.reach.measured'
assert_json_field "unmeasured reach never reads as a zero count" "null" \
  '.data.rows[0].score.reach.symbols'
assert_json_field "churn counts the fixture's replaced line" "1 1" \
  '.data.rows[0].score.churn | "\(.additions) \(.deletions)"'
assert_json_field "the important bundle ties on band but loses on score" "22" '.data.rows[1].score.value'
assert_json_field "a claimed-high clean bundle scores zero" "0" '.data.rows[2].score.value'
assert_json_field "an unreviewed bundle reads the missing-coverage floor" "medium" \
  '.data.rows[3].reported'
assert_json_field "receipt reasons stay in the published vocabulary" "true" \
  'all(.data.rows[].receipt_reasons[]; IN("missing-claim","stale-head","missing-coverage","unreadable","unanchored","deferred"))'
assert_json_field "the unmeasured reach is warned with its count" "1" \
  '[.warnings[] | select(test("^risk: 7 bundle\\(s\\) carry no impact.json"))] | length'
assert_json_field "the follow-up backfills a local bundle through fetch-local" \
  "verdict fetch-local acme/app local-critical --index origin/main" '.next'
after="$(tree_sum)"
assert_eq "the queue leaves every bundle byte-identical" "$before" "$after"

# 2) Human mode renders the header, one row per bundle, the skipped count, and
# the follow-up.
run_review risk acme/app
assert_exit "human risk succeeds" 0
assert_file_grep "human header tallies the queue" "$EVAL_OUT" \
  '^risk: acme/app — 7 bundle[(]s[)], 6 need a human [(]critical=1 high=2 medium=2 unknown=1 low=1[)]$'
assert_file_grep "human row 1 names the critical bundle" "$EVAL_OUT" \
  '^1  critical  score=28  local-critical   open=1  reach=-  churn=[+]1/-1  cat=security  rung=walked$'
assert_file_grep "human rows count down the queue" "$EVAL_OUT" '^7  low       score=0   local-low        open=0  reach=-  churn=[+]1/-1'
assert_file_grep "human mode names the skipped bundles" "$EVAL_OUT" \
  '^skipped: 2 bundle[(]s[)] not open [(]--status all includes them[)]$'
assert_file_grep "human mode prints the follow-up" "$EVAL_OUT" \
  '^next: verdict fetch-local acme/app local-critical --index origin/main$'

# 3) --status all keeps merged and closed bundles and drops the skipped line.
run_review risk acme/app --status all --json
assert_exit "risk --status all succeeds" 0
assert_json_field "--status all counts every bundle" "9" '.data.bundles'
assert_json_field "--status all keeps the merged and closed states" "merged closed" \
  '[.data.rows[] | select(.state == "merged" or .state == "closed") | .state] | join(" ")'
assert_json_field "--status all warns nothing about skips" "0" \
  '[.warnings[] | select(contains("not open"))] | length'

# 4) --limit bounds the rows and carries the response-cap receipt while the
# tallies keep describing the whole queue.
run_review risk acme/app --limit 2 --json
assert_exit "risk --limit 2 succeeds" 0
assert_json_field "two rows published" "2" '.data.rows | length'
assert_json_field "the tallies still describe the queue" "7" '.data.bundles'
assert_json_field "the receipt names the cap" "true response_cap 2 7" \
  '.data | "\(.truncated) \(.truncation_reason) \(.returned) \(.total)"'
run_review risk acme/app --limit 2
assert_exit "human risk --limit succeeds" 0
assert_file_grep "human mode counts the hidden rows" "$EVAL_OUT" '^limit: 2 shown, 5 hidden$'

# 5) A backfilled impact.json measures reach: the warning goes away and the
# top bundle wears the published 37 the score formula gives its components.
for bundle in local-critical local-important local-high local-low local-unclaimed 13 18 15 16; do
  printf '%s\n' '{"sources":["index:tree"],"stale":false,"symbols":{"count":1},"callers":{"count":1}}' \
    >"$STATE/$bundle/impact.json"
done
run_review risk acme/app --json
assert_exit "risk with impact.json succeeds" 0
assert_json_field "no unmeasured reach means no warning" "[]" '.warnings'
assert_json_field "reach counts the symbol and its caller" "2" \
  '(.data.rows[0].score.reach.symbols // 0) + (.data.rows[0].score.reach.callers // 0)'
assert_json_field "reach sits in the 1-3 bucket" "25" '.data.rows[0].score.reach.points'
assert_json_field "all four components measured" "4" '.data.rows[0].score.measured'
assert_json_field "the score takes the published formula" "37" '.data.rows[0].score.value'
assert_json_field "backfilled reach does not reorder the queue" \
  "local-critical local-important local-high 13 18 local-unclaimed local-low" \
  '[.data.rows[].bundle] | join(" ")'

# 6) The MCP tool serves the same data as the CLI envelope.
FRAMES="$EVAL_SANDBOX/risk-frames.jsonl"
MCP_OUT="$EVAL_SANDBOX/risk-mcp-out.jsonl"
cat >"$FRAMES" <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"eval","version":"1"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"risk","arguments":{"repo":"acme/app"}}}
EOF
set +e
(
  cd "$EVAL_SANDBOX" || exit 99
  PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" mcp >"$MCP_OUT" 2>"$EVAL_ERR"
) <"$FRAMES"
# shellcheck disable=SC2034 # EVAL_STATUS is read by assert_exit below
EVAL_STATUS=$?
set -e
assert_exit "mcp risk tool call succeeds" 0
jq -S 'select(.id == 2).result.structuredContent | del(.generated_at)' <"$MCP_OUT" \
  >"$EVAL_SANDBOX/risk-mcp.json"
jq -S '.data | del(.generated_at)' <"$EVAL_OUT" >"$EVAL_SANDBOX/risk-cli.json"
assert_file_eq "mcp risk serves the CLI's data" \
  "$EVAL_SANDBOX/risk-cli.json" "$EVAL_SANDBOX/risk-mcp.json"

# 7) Usage errors stay argv-shaped: the queue takes one repo and two flags.
run_review risk acme/app --status weird
assert_exit "unknown --status returns exit 2" 2
assert_file_grep "the --status refusal names the choices" "$EVAL_ERR" \
  '^--status wants open or all, not "weird"$'
run_review risk acme/app --limit 201
assert_exit "out-of-range --limit returns exit 2" 2
run_review risk acme/app 13
assert_exit "a second positional returns exit 2" 2
assert_file_grep "the second positional points at report --max-risk" "$EVAL_ERR" \
  'gating one bundle is verdict report acme/app 13 --max-risk <band>'
run_review risk
assert_exit "a bare risk returns exit 2" 2
assert_file_grep "the usage lists the queue's flags" "$EVAL_ERR" \
  '^  risk <owner/repo> \[--status open\|all\] \[--limit <n, default 50, max 200>\] \[--json\]$'

eval_end
