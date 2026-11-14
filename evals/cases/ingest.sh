#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

PR=4411
eval_mk_bundle "$PR"
bundle_dir="$EVAL_SANDBOX/.verdict/state/$PR"

# Add an excluded.json entry so excluded tool findings land withheld.
cat > "$bundle_dir/excluded.json" <<'EOF'
{
  "schema_version": "1",
  "patterns_source": ".gitignore",
  "defaults": [],
  "ignore": ["*.secret"],
  "no_default_excludes": false,
  "excluded": [
    {"path": "secret.go", "rule_id": "priv-1", "pattern": "*.secret", "reason": "ignore", "bytes": 128}
  ],
  "excluded_bytes": 128,
  "kept_bytes": 100,
  "raw_diff_sha256": "0000000000000000000000000000000000000000000000000000000000000000",
  "diff_sha256": "1111111111111111111111111111111111111111111111111111111111111111"
}
EOF

sarif1="$EVAL_SANDBOX/scan1.sarif"
cat > "$sarif1" <<'EOF'
{
  "version": "2.1.0",
  "runs": [
    {
      "tool": {
        "driver": {
          "name": "semgrep",
          "rules": [
            {
              "id": "go.sec.sql",
              "properties": {"security-severity": "9.2"}
            }
          ]
        }
      },
      "results": [
        {
          "ruleId": "go.sec.sql",
          "level": "warning",
          "message": {"text": "SQL query built from untrusted input."},
          "partialFingerprints": {"primaryLocationLineHash": "fp-in-diff"},
          "locations": [
            {
              "physicalLocation": {
                "artifactLocation": {"uri": "notes.txt"},
                "region": {"startLine": 2}
              }
            }
          ]
        },
        {
          "ruleId": "go.lint.dead",
          "level": "note",
          "message": {"text": "Unused helper outside PR diff."},
          "partialFingerprints": {"primaryLocationLineHash": "fp-out-diff"},
          "locations": [
            {
              "physicalLocation": {
                "artifactLocation": {"uri": "other.go"},
                "region": {"startLine": 40}
              }
            }
          ]
        },
        {
          "ruleId": "go.sec.key",
          "level": "error",
          "message": {"text": "Secret candidate in excluded file."},
          "partialFingerprints": {"primaryLocationLineHash": "fp-excluded"},
          "locations": [
            {
              "physicalLocation": {
                "artifactLocation": {"uri": "secret.go"},
                "region": {"startLine": 5}
              }
            }
          ]
        }
      ]
    }
  ]
}
EOF

run_review ingest acme/app "$PR" --sarif "$sarif1" --json
assert_exit "happy ingest exits 0" 0
assert_docs "happy ingest" 1
assert_envelope "happy ingest envelope"
if [ "$(jq -r '.verb' <"$EVAL_OUT")" = "ingest" ] \
  && [ "$(jq -r '.data.ingested' <"$EVAL_OUT")" = "3" ] \
  && [ "$(jq -r '.data.added' <"$EVAL_OUT")" = "3" ] \
  && [ "$(jq -r '.data.updated' <"$EVAL_OUT")" = "0" ] \
  && [ "$(jq -r '.data.unchanged' <"$EVAL_OUT")" = "0" ] \
  && [ "$(jq -r '.data.anchored' <"$EVAL_OUT")" = "1" ] \
  && [ "$(jq -r '.data.withheld' <"$EVAL_OUT")" = "1" ] \
  && [ "$(jq -r '.data.open' <"$EVAL_OUT")" = "3" ]; then
  good "ingest data counts match (ingested=3 added=3 anchored=1 withheld=1 open=3)"
else
  bad "ingest data mismatch: $(jq -c '.data' <"$EVAL_OUT" 2>/dev/null)"
fi

if [ "$(jq -r '[.[] | select(.fingerprint == "fp-in-diff")][0].anchored' "$bundle_dir/findings.json")" = "true" ] \
  && [ "$(jq -r '[.[] | select(.fingerprint == "fp-in-diff")][0].severity' "$bundle_dir/findings.json")" = "critical" ] \
  && [ "$(jq -r '[.[] | select(.fingerprint == "fp-in-diff")][0].rigor' "$bundle_dir/findings.json")" = "said" ] \
  && [ "$(jq -r '[.[] | select(.fingerprint == "fp-in-diff")][0].rigor_source' "$bundle_dir/findings.json")" = "default" ] \
  && [ "$(jq -r '[.[] | select(.fingerprint == "fp-in-diff")][0].source' "$bundle_dir/findings.json")" = "tool" ]; then
  good "in-diff tool finding has anchored=true, critical tier, said/default rigor, source=tool"
else
  bad "in-diff entry mismatch: $(jq -c . "$bundle_dir/findings.json")"
fi

if [ "$(jq -r '[.[] | select(.fingerprint == "fp-out-diff")][0].anchored' "$bundle_dir/findings.json")" = "false" ] \
  && [ "$(jq -r '[.[] | select(.fingerprint == "fp-excluded")][0].withheld.source' "$bundle_dir/findings.json")" = "exclusion" ] \
  && [ "$(jq -r '[.[] | select(.fingerprint == "fp-excluded")][0].withheld.exclusion_id' "$bundle_dir/findings.json")" = "priv-1" ]; then
  good "out-of-diff unanchored and excluded path withheld with exclusion id"
else
  bad "out-of-diff/excluded mismatch: $(jq -c . "$bundle_dir/findings.json")"
fi

# Idempotent re-ingest
run_review ingest acme/app "$PR" --sarif "$sarif1" --json
assert_exit "idempotent re-ingest exits 0" 0
if [ "$(jq -r '.data.added' <"$EVAL_OUT")" = "0" ] \
  && [ "$(jq -r '.data.updated' <"$EVAL_OUT")" = "0" ] \
  && [ "$(jq -r '.data.unchanged' <"$EVAL_OUT")" = "3" ] \
  && [ "$(jq 'length' "$bundle_dir/findings.json")" = "3" ]; then
  good "re-ingesting identical SARIF is idempotent"
else
  bad "re-ingest not idempotent: $(jq -c '.data' <"$EVAL_OUT")"
fi

# Modified result with same fingerprint via stdin updates in place
sarif2="$EVAL_SANDBOX/scan2.sarif"
jq '.runs[0].results[1].level = "warning" | .runs[0].results[1].message.text = "Unused helper now flagged important."' "$sarif1" > "$sarif2"
EVAL_STATUS=0
(cd "$EVAL_SANDBOX" && PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" ingest acme/app "$PR" --sarif - --json < "$sarif2" > "$EVAL_OUT" 2> "$EVAL_ERR") || EVAL_STATUS=$?
assert_exit "stdin ingest updated result exits 0" 0
if [ "$(jq -r '.data.added' <"$EVAL_OUT")" = "0" ] \
  && [ "$(jq -r '.data.updated' <"$EVAL_OUT")" = "1" ] \
  && [ "$(jq -r '.data.unchanged' <"$EVAL_OUT")" = "2" ] \
  && [ "$(jq 'length' "$bundle_dir/findings.json")" = "3" ] \
  && [ "$(jq -r '[.[] | select(.fingerprint == "fp-out-diff")][0].severity' "$bundle_dir/findings.json")" = "important" ]; then
  good "changed tool result via stdin updated existing entry in place"
else
  bad "stdin update mismatch: $(jq -c '.data' <"$EVAL_OUT") findings=$(jq -c '[.[] | {fingerprint, severity, body}]' "$bundle_dir/findings.json")"
fi

# Report and trend surface by_source (open only excludes withheld excluded entry)
run_review report acme/app "$PR" --json
assert_exit "report exits 0" 0
if [ "$(jq -r '.data.counts.by_source.tool' <"$EVAL_OUT")" = "2" ] \
  && [ "$(jq -r '.data.counts.by_source.model' <"$EVAL_OUT")" = "0" ] \
  && [ "$(jq -r '.data.counts.withheld' <"$EVAL_OUT")" = "1" ]; then
  good "report counts.by_source surfaces 2 open tool findings and 1 withheld"
else
  bad "report by_source mismatch: $(jq -c '.data.counts' <"$EVAL_OUT")"
fi

run_review trend acme/app --json
assert_exit "trend exits 0" 0
if [ "$(jq -r '.data.by_source.tool' <"$EVAL_OUT")" = "3" ]; then
  good "trend by_source counts all stored tool findings"
else
  bad "trend by_source mismatch: $(jq -c '.data.by_source' <"$EVAL_OUT")"
fi

# Security/validation refusals
bad_uri="$EVAL_SANDBOX/bad-uri.sarif"
jq '.runs[0].results[0].locations[0].physicalLocation.artifactLocation.uri = "../etc/passwd"' "$sarif1" > "$bad_uri"
run_review ingest acme/app "$PR" --sarif "$bad_uri" --json
if [ "$EVAL_STATUS" -ne 0 ] && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT")" = "bad-sarif" ]; then
  good "path-traversal URI refuses with bad-sarif"
else
  bad "bad URI gave exit $EVAL_STATUS code '$(jq -r '.error.code // empty' <"$EVAL_OUT")'"
fi

bad_ws_uri="$EVAL_SANDBOX/bad-ws-uri.sarif"
jq '.runs[0].results[0].locations[0].physicalLocation.artifactLocation.uri = " ../etc/passwd"' "$sarif1" > "$bad_ws_uri"
run_review ingest acme/app "$PR" --sarif "$bad_ws_uri" --json
if [ "$EVAL_STATUS" -ne 0 ] && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT")" = "bad-sarif" ]; then
  good "whitespace-padded traversal URI refuses with bad-sarif"
else
  bad "whitespace-padded URI gave exit $EVAL_STATUS code '$(jq -r '.error.code // empty' <"$EVAL_OUT")'"
fi

ln -s "$sarif1" "$EVAL_SANDBOX/link.sarif"
run_review ingest acme/app "$PR" --sarif "$EVAL_SANDBOX/link.sarif" --json
if [ "$EVAL_STATUS" -ne 0 ] && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT")" = "bad-sarif" ]; then
  good "symlink --sarif refuses with bad-sarif"
else
  bad "symlink gave exit $EVAL_STATUS code '$(jq -r '.error.code // empty' <"$EVAL_OUT")'"
fi

run_review ingest acme/app "$PR" --json
if [ "$EVAL_STATUS" -ne 0 ] && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT")" = "missing-sarif" ]; then
  good "omitted --sarif refuses with missing-sarif"
else
  bad "missing --sarif gave exit $EVAL_STATUS code '$(jq -r '.error.code // empty' <"$EVAL_OUT")'"
fi

if [ "$(( $(wc -l <"$EVAL_STUBLOG" | tr -d ' ') - stub_before ))" = "0" ]; then
  good "zero stub-gh intercepts across ingest legs (offline)"
else
  bad "stub gh intercepted calls during ingest legs"
fi

eval_end
