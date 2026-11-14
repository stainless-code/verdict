#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q
  git config user.email "incident-eval@example.com"
  git config user.name "incident eval"
  echo ".verdict/state/" >.gitignore
  mkdir -p src
  echo "console.log(1)" >src/a.ts
  git add -A
  git commit -qm init
)

SHA_A=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
SHA_B=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
eval_mk_bundle local-a
eval_write_findings local-a <<'EOF'
[{"kind":"inline","path":"src/a.ts","side":"RIGHT","line":2,"anchored":true,"body":"Unhandled await error path here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","sig":"a111111111111111111111111111111111111111","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"addressed","rationale":"","history":[]}]
EOF

run_review learn --incident local-a --sha "$SHA_A" --path src/a.ts --path src/a.ts --json
assert_exit "attach exits 0" 0
assert_envelope "attach envelope"
assert_json_field "attach reports appended" "appended" '.data.incident.attached'
assert_json_field "attach names the bundle" "local-a" '.data.incident.bundle'
assert_json_field "attach keeps the whole sha" "$SHA_A" '.data.incident.sha'
assert_json_field "repeated --path dedupes and sorts" "src/a.ts" '.data.incident.paths | join(",")'
assert_json_field "absent note reads null" "null" '.data.incident.note'
assert_json_field "absent url reads null" "null" '.data.incident.url'
assert_json_field "detected_at is RFC 3339" "true" '(.data.incident.detected_at | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))'
assert_json_field "ledger row keeps the published key order" \
  "schema_version,bundle,sha,paths,note,url,detected_at" \
  'keys_unsorted | join(",")' "$EVAL_SANDBOX/.verdict/incidents.jsonl"
assert_json_field "ledger row carries the incident" "local-a $SHA_A" \
  '"\(.bundle) \(.sha)"' "$EVAL_SANDBOX/.verdict/incidents.jsonl"

cp "$EVAL_SANDBOX/.verdict/incidents.jsonl" "$EVAL_SANDBOX/incidents.first.jsonl"
run_review learn --incident local-a --sha "$SHA_A" --path src/a.ts --path src/other.ts --json
assert_exit "second attach exits 0" 0
assert_json_field "second attach reports existing" "existing" '.data.incident.attached'
assert_file_eq "second attach leaves the ledger byte-identical" \
  "$EVAL_SANDBOX/incidents.first.jsonl" "$EVAL_SANDBOX/.verdict/incidents.jsonl"
assert_eq "the ledger holds one row" "1" "$(wc -l <"$EVAL_SANDBOX/.verdict/incidents.jsonl" | tr -d ' ')"

run_review learn --incident local-a --sha "$SHA_A" --path src/a.ts
assert_exit "human attach exits 0" 0
assert_file_grep "human attach names the bundle, short sha, and path count" "$EVAL_OUT" \
  "attached incident local-a aaaaaaa -> .verdict/incidents.jsonl (1 path)"

rm -rf "$EVAL_SANDBOX/.verdict/state/local-empty"
mkdir -p "$EVAL_SANDBOX/.verdict/state/local-empty"
run_review learn --incident nosuch --sha "$SHA_A" --path src/a.ts --json
assert_refusal "unknown bundle refuses" missing-bundle
run_review learn --incident local-empty --sha "$SHA_A" --path src/a.ts --json
assert_refusal "bundle without findings refuses" missing-findings
run_review learn --incident local-a --sha "$SHA_A" --path ../etc/passwd --json
assert_refusal "escaping path refuses" bad-scope
run_review learn --incident local-a --sha zzz --path src/a.ts --json
assert_refusal "malformed sha refuses" bad-flags
run_review learn --incident local-a --sha "$SHA_A" --json
assert_refusal "attach without a path refuses" bad-flags
run_review learn --sha "$SHA_A" --path src/a.ts --json
assert_refusal "attach flags without --incident refuse" bad-flags
run_review learn --status --sha "$SHA_A" --path src/a.ts --json
assert_refusal "attach flags beside another mode refuse" bad-flags
assert_file_grep "the reflex names the mode they belong to" "$EVAL_OUT" \
  "belong to --incident <bundle>"
run_review learn --incident local-a --sha "$SHA_A" --path src/a.ts --url ftp://example.test/x --json
assert_refusal "a non-http url refuses" bad-flags
run_review learn --incident local-a --sha "$SHA_A" --path src/a.ts --at 2026-11-01 --json
assert_refusal "a bare date refuses" bad-at
assert_eq "a refused attach never writes the ledger" "1" \
  "$(wc -l <"$EVAL_SANDBOX/.verdict/incidents.jsonl" | tr -d ' ')"

run_review learn --incident local-a --sha "$SHA_B" --path src/a.ts --dry-run --json
assert_exit "dry-run attach exits 0" 0
assert_json_field "dry-run reports the append it would make" "appended" '.data.incident.attached'
assert_json_field "dry-run reports the new sha" "$SHA_B" '.data.incident.sha'
assert_file_eq "dry-run leaves the ledger byte-identical" \
  "$EVAL_SANDBOX/incidents.first.jsonl" "$EVAL_SANDBOX/.verdict/incidents.jsonl"

for n in 1 2 3 4 5 6 7 8; do
  (
    cd "$EVAL_SANDBOX" || exit 99
    PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" learn --incident local-a --sha "$(printf '%040d' "$n")" \
      --path "src/p$n.ts" --json >"$EVAL_SANDBOX/parallel-$n.json" 2>&1
  ) &
done
wait
assert_eq "eight parallel attaches keep every row" "9" \
  "$(wc -l <"$EVAL_SANDBOX/.verdict/incidents.jsonl" | tr -d ' ')"
assert_eq "every parallel attach reports an append" "8" \
  "$(jq -s '[.[] | select(.data.incident.attached == "appended")] | length' "$EVAL_SANDBOX"/parallel-*.json)"
if [ -e "$EVAL_SANDBOX/.verdict/incidents.jsonl.lock" ]; then
  bad "the parallel attaches left the ledger lock behind"
else
  good "the parallel attaches leave no ledger lock"
fi

assert_eq "attaching an incident makes no host call" "0" "$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

eval_end
