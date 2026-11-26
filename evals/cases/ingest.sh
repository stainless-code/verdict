#!/usr/bin/env bash
#
# Static-analysis ingestion: competitor-neutral SARIF 2.1.0 over --from sarif,
# from a file and from stdin, with typed skips, snippet verification, dedupe,
# refusals, the report --sarif round trip, and the gate reading the result.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

PR=4411
eval_mk_bundle "$PR"
bundle_dir="$EVAL_SANDBOX/.verdict/state/$PR"

# The trusted exclusion control names an in-repo path the diff filter removed:
# a scanner still reports it, and ingest records it withheld, never posted.
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
          "version": "1.90.0",
          "rules": [
            {"id": "go.sec.sql", "properties": {"security-severity": "9.2"}},
            {"id": "go.sec.key", "defaultConfiguration": {"level": "error"}}
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
                "region": {"startLine": 2, "snippet": {"text": "new line"}}
              }
            }
          ]
        },
        {
          "ruleId": "go.sec.sql",
          "level": "warning",
          "message": {"text": "SQL query built from untrusted input."},
          "partialFingerprints": {"primaryLocationLineHash": "fp-in-diff-repeat"},
          "locations": [
            {
              "physicalLocation": {
                "artifactLocation": {"uri": "notes.txt"},
                "region": {"startLine": 2, "snippet": {"text": "new line"}}
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
                "artifactLocation": {"uri": "notes.txt"},
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
        },
        {
          "ruleId": "go.repo.policy",
          "level": "note",
          "message": {"text": "Missing SECURITY.md"}
        },
        {
          "ruleId": "go.sec.traverse",
          "level": "error",
          "message": {"text": "Traversal URI."},
          "locations": [
            {
              "physicalLocation": {
                "artifactLocation": {"uri": "../etc/passwd"},
                "region": {"startLine": 1}
              }
            }
          ]
        },
        {
          "ruleId": "go.uri.windows",
          "level": "warning",
          "message": {"text": "Windows separator URI anchors."},
          "partialFingerprints": {"primaryLocationLineHash": "fp-windows"},
          "locations": [
            {
              "physicalLocation": {
                "artifactLocation": {"uri": ".\\notes.txt"},
                "region": {"startLine": 1}
              }
            }
          ]
        },
        {
          "ruleId": "go.uri.file",
          "level": "note",
          "message": {"text": "Path-only file URI anchors."},
          "partialFingerprints": {"primaryLocationLineHash": "fp-file-uri"},
          "locations": [
            {
              "physicalLocation": {
                "artifactLocation": {"uri": "file:notes.txt"},
                "region": {"startLine": 3}
              }
            }
          ]
        },
        {
          "ruleId": "go.uri.authority",
          "level": "error",
          "message": {"text": "Authority-bearing file URI escapes the checkout."},
          "locations": [
            {
              "physicalLocation": {
                "artifactLocation": {"uri": "file://localhost/etc/passwd"},
                "region": {"startLine": 1}
              }
            }
          ]
        }
      ]
    }
  ]
}
EOF

run_review ingest acme/app "$PR" --from sarif "$sarif1" --json
assert_exit "--from sarif file exits 0" 0
assert_docs "--from sarif file" 1
assert_envelope "--from sarif file envelope"
if [ "$(jq -r '.verb' <"$EVAL_OUT")" = "ingest" ] \
  && [ "$(jq -r '.data.ingested' <"$EVAL_OUT")" = "4" ] \
  && [ "$(jq -r '.data.added' <"$EVAL_OUT")" = "4" ] \
  && [ "$(jq -r '.data.updated' <"$EVAL_OUT")" = "0" ] \
  && [ "$(jq -r '.data.unchanged' <"$EVAL_OUT")" = "0" ] \
  && [ "$(jq -r '.data.anchored' <"$EVAL_OUT")" = "3" ] \
  && [ "$(jq -r '.data.withheld' <"$EVAL_OUT")" = "1" ] \
  && [ "$(jq -r '.data.open' <"$EVAL_OUT")" = "4" ]; then
  good "ingest data counts match (ingested=4 added=4 anchored=3 withheld=1 open=4)"
else
  bad "ingest data mismatch: $(jq -c '.data' <"$EVAL_OUT" 2>/dev/null)"
fi

# Every unanchorable result is published with a typed reason, deduplicated by
# (reason, tool, rule, path, line), and never stored at line 0.
if [ "$(jq -r '[.data.skipped[] | "\(.reason) \(.path) \(.line)"] | sort | join("; ")' <"$EVAL_OUT")" \
  = "no-location  0; out-of-diff notes.txt 40; path-escapes-repo ../etc/passwd 1; path-escapes-repo file://localhost/etc/passwd 1" ]; then
  good "skipped[] carries the typed reasons, paths, and lines (authority-bearing file:// refused)"
else
  bad "skipped[] mismatch: $(jq -c '.data.skipped' <"$EVAL_OUT")"
fi

if [ "$(jq -r '[.[] | select(.fingerprint == "fp-in-diff")][0].anchored' "$bundle_dir/findings.json")" = "true" ] \
  && [ "$(jq -r '[.[] | select(.fingerprint == "fp-in-diff")][0].severity' "$bundle_dir/findings.json")" = "critical" ] \
  && [ "$(jq -r '[.[] | select(.fingerprint == "fp-in-diff")][0].rigor' "$bundle_dir/findings.json")" = "pointed" ] \
  && [ "$(jq -r '[.[] | select(.fingerprint == "fp-in-diff")][0].rigor_source' "$bundle_dir/findings.json")" = "default" ] \
  && [ "$(jq -r '[.[] | select(.fingerprint == "fp-in-diff")][0].quote' "$bundle_dir/findings.json")" = "new line" ] \
  && [ "$(jq -r '[.[] | select(.fingerprint == "fp-in-diff")][0].line' "$bundle_dir/findings.json")" = "2" ] \
  && [ "$(jq -r '[.[] | select(.fingerprint == "fp-in-diff")][0].source' "$bundle_dir/findings.json")" = "tool" ]; then
  good "in-diff finding is anchored, pointed, quoted from the diff line, source=tool"
else
  bad "in-diff entry mismatch: $(jq -c '[.[] | select(.fingerprint == "fp-in-diff")][0]' "$bundle_dir/findings.json")"
fi

if [ "$(jq -r '[.[] | select(.fingerprint == "fp-excluded")][0].withheld.source' "$bundle_dir/findings.json")" = "exclusion" ] \
  && [ "$(jq -r '[.[] | select(.fingerprint == "fp-excluded")][0].withheld.exclusion_id' "$bundle_dir/findings.json")" = "priv-1" ] \
  && [ "$(jq -r '[.[] | select(.fingerprint == "fp-excluded")][0].anchored' "$bundle_dir/findings.json")" = "false" ]; then
  good "excluded path is withheld under exclusion id and never anchored"
else
  bad "excluded entry mismatch: $(jq -c '[.[] | select(.fingerprint == "fp-excluded")][0]' "$bundle_dir/findings.json")"
fi

if [ "$(jq -r '[.[] | select(.fingerprint == "fp-windows")][0].anchored' "$bundle_dir/findings.json")" = "true" ] \
  && [ "$(jq -r '[.[] | select(.fingerprint == "fp-file-uri")][0].anchored' "$bundle_dir/findings.json")" = "true" ]; then
  good "windows-separator and path-only file: URIs anchor on their diff lines"
else
  bad "URI-form entries did not anchor: $(jq -c '[.[] | select(.fingerprint == "fp-windows" or .fingerprint == "fp-file-uri")]' "$bundle_dir/findings.json")"
fi

# The repeated result folded by signature: 8 results, one signature duplicate,
# one withheld, four skipped, three merged.
if [ "$(jq 'length' "$bundle_dir/findings.json")" = "4" ]; then
  good "duplicate result deduped by the stable finding signature"
else
  bad "expected 4 stored findings, got $(jq 'length' "$bundle_dir/findings.json")"
fi

# Idempotent re-ingest from stdin.
EVAL_STATUS=0
(cd "$EVAL_SANDBOX" && PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" ingest acme/app "$PR" --from sarif - --json < "$sarif1" > "$EVAL_OUT" 2> "$EVAL_ERR") || EVAL_STATUS=$?
assert_exit "--from sarif - stdin re-ingest exits 0" 0
if [ "$(jq -r '.data.added' <"$EVAL_OUT")" = "0" ] \
  && [ "$(jq -r '.data.updated' <"$EVAL_OUT")" = "0" ] \
  && [ "$(jq -r '.data.unchanged' <"$EVAL_OUT")" = "4" ] \
  && [ "$(jq -r '.data.skipped | length' <"$EVAL_OUT")" = "4" ] \
  && [ "$(jq 'length' "$bundle_dir/findings.json")" = "4" ]; then
  good "re-ingesting identical SARIF over stdin is idempotent"
else
  bad "re-ingest not idempotent: $(jq -c '.data' <"$EVAL_OUT")"
fi

# Modified result with the same fingerprint updates in place, and a stale
# snippet is refused instead of re-pointed.
sarif2="$EVAL_SANDBOX/scan2.sarif"
jq '(.runs[0].results[] | select(.ruleId == "go.sec.sql")) |= (.level = "error" | .message.text = "SQL query built from untrusted input, now gated.")' \
  "$sarif1" > "$sarif2"
EVAL_STATUS=0
(cd "$EVAL_SANDBOX" && PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" ingest acme/app "$PR" --from sarif - --json < "$sarif2" > "$EVAL_OUT" 2> "$EVAL_ERR") || EVAL_STATUS=$?
assert_exit "updated stdin result exits 0" 0
if [ "$(jq -r '.data.added' <"$EVAL_OUT")" = "0" ] \
  && [ "$(jq -r '.data.updated' <"$EVAL_OUT")" = "1" ] \
  && [ "$(jq -r '.data.unchanged' <"$EVAL_OUT")" = "3" ] \
  && [ "$(jq 'length' "$bundle_dir/findings.json")" = "4" ]; then
  good "changed tool result via stdin updated the existing entry in place"
else
  bad "stdin update mismatch: $(jq -c '.data' <"$EVAL_OUT")"
fi

sarif_stale="$EVAL_SANDBOX/stale-snippet.sarif"
jq '(.runs[0].results[] | select(.ruleId == "go.sec.sql") | .locations[0].physicalLocation.region.snippet.text) = "a line the scanner never saw"' \
  "$sarif2" > "$sarif_stale"
run_review ingest acme/app "$PR" --from sarif "$sarif_stale" --json
assert_exit "stale snippet exits 0 (a skip, not a refusal)" 0
if [ "$(jq -r '.data.added' <"$EVAL_OUT")" = "0" ] \
  && [ "$(jq -r '.data.updated' <"$EVAL_OUT")" = "0" ] \
  && [ "$(jq -r '[.data.skipped[] | select(.reason == "snippet-mismatch")][0].line' <"$EVAL_OUT")" = "2" ] \
  && [ "$(jq -r '[.[] | select(.fingerprint == "fp-in-diff")][0].quote' "$bundle_dir/findings.json")" != "a line the scanner never saw" ]; then
  good "snippet mismatch is skipped with its reason, never re-pointed"
else
  bad "snippet mismatch handling: $(jq -c '.data.skipped' <"$EVAL_OUT")"
fi

# report and trend surface the ingested findings beside model ones.
run_review report acme/app "$PR" --json
assert_exit "report exits 0" 0
if [ "$(jq -r '.data.counts.by_source.tool' <"$EVAL_OUT")" = "3" ] \
  && [ "$(jq -r '.data.counts.by_source.model' <"$EVAL_OUT")" = "0" ] \
  && [ "$(jq -r '.data.counts.withheld' <"$EVAL_OUT")" = "1" ]; then
  good "report counts.by_source surfaces 3 open tool findings and 1 withheld"
else
  bad "report by_source mismatch: $(jq -c '.data.counts' <"$EVAL_OUT")"
fi

run_review trend acme/app --json
assert_exit "trend exits 0" 0
if [ "$(jq -r '.data.by_source.tool' <"$EVAL_OUT")" = "4" ]; then
  good "trend by_source counts all stored tool findings"
else
  bad "trend by_source mismatch: $(jq -c '.data.by_source' <"$EVAL_OUT")"
fi

# Round trip: report --sarif fed back through --from sarif adds nothing.
run_review report acme/app "$PR" --sarif "$EVAL_SANDBOX/roundtrip.sarif" --json
assert_exit "report --sarif exits 0" 0
assert_file_grep "report SARIF carries verdict sigs" "$EVAL_SANDBOX/roundtrip.sarif" '"verdict/sig"'
assert_file_grep "report SARIF carries tool provenance" "$EVAL_SANDBOX/roundtrip.sarif" '"tool": "semgrep"'
EVAL_STATUS=0
(cd "$EVAL_SANDBOX" && PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" ingest acme/app "$PR" --from sarif - --json < "$EVAL_SANDBOX/roundtrip.sarif" > "$EVAL_OUT" 2> "$EVAL_ERR") || EVAL_STATUS=$?
assert_exit "round-trip ingest exits 0" 0
if [ "$(jq -r '.data.added' <"$EVAL_OUT")" = "0" ] \
  && [ "$(jq -r '.data.updated' <"$EVAL_OUT")" = "0" ] \
  && [ "$(jq -r '.data.skipped | length' <"$EVAL_OUT")" = "0" ] \
  && [ "$(jq 'length' "$bundle_dir/findings.json")" = "4" ]; then
  good "report --sarif round-trips through --from sarif with no added finding"
else
  bad "round-trip mismatch: $(jq -c '.data' <"$EVAL_OUT")"
fi

# Gate reads the ingested store: the open critical tool finding fails it, the
# withheld one does not count.
eval_write_record_evidence "$PR"
run_review gate acme/app "$PR" --max-severity critical --live skip --coverage warn
assert_exit "gate fails on the ingested critical finding" 1
assert_file_grep "gate names the ingested finding" "$EVAL_OUT" "1 open finding(s) at-or-above critical"

# Refusals: unsupported source, the removed --sarif spelling, missing source, malformed, wrong version,
# oversize stdin, symlinked path. Nothing above wrote through any of them.
run_review ingest acme/app "$PR" --from yaml "$sarif1" --json
if [ "$EVAL_STATUS" -ne 0 ] && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT")" = "bad-from" ]; then
  good "unsupported --from format refuses with bad-from"
else
  bad "bad --from gave exit $EVAL_STATUS code '$(jq -r '.error.code // empty' <"$EVAL_OUT")'"
fi

run_review ingest acme/app "$PR" --sarif "$sarif1" --json
assert_exit "the removed --sarif spelling is a usage error" 2
assert_file_grep "--sarif is a plain unknown flag" "$EVAL_ERR" "unknown flag '--sarif' for ingest (want --from, --json)"

run_review ingest acme/app "$PR" --json
if [ "$EVAL_STATUS" -ne 0 ] && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT")" = "missing-sarif" ]; then
  good "omitted source refuses with missing-sarif"
else
  bad "missing source gave exit $EVAL_STATUS code '$(jq -r '.error.code // empty' <"$EVAL_OUT")'"
fi

printf '{"version":"2.1.0","runs":[' > "$EVAL_SANDBOX/broken.sarif"
run_review ingest acme/app "$PR" --from sarif "$EVAL_SANDBOX/broken.sarif" --json
if [ "$EVAL_STATUS" -ne 0 ] && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT")" = "bad-sarif" ]; then
  good "truncated SARIF refuses with bad-sarif"
else
  bad "truncated SARIF gave exit $EVAL_STATUS code '$(jq -r '.error.code // empty' <"$EVAL_OUT")'"
fi

printf '{"version":"2.0.0","runs":[]}' > "$EVAL_SANDBOX/old-version.sarif"
run_review ingest acme/app "$PR" --from sarif "$EVAL_SANDBOX/old-version.sarif" --json
if [ "$EVAL_STATUS" -ne 0 ] && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT")" = "bad-sarif" ]; then
  good "unsupported SARIF version refuses with bad-sarif"
else
  bad "wrong version gave exit $EVAL_STATUS code '$(jq -r '.error.code // empty' <"$EVAL_OUT")'"
fi

big_sarif="$EVAL_SANDBOX/too-large.sarif"
{
  printf '{"version":"2.1.0","runs":[]}'
  dd if=/dev/zero bs=1024 count=8192 2>/dev/null | tr '\0' ' '
} > "$big_sarif"
EVAL_STATUS=0
(cd "$EVAL_SANDBOX" && PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" ingest acme/app "$PR" --from sarif - --json \
  < "$big_sarif" > "$EVAL_OUT" 2> "$EVAL_ERR") || EVAL_STATUS=$?
if [ "$EVAL_STATUS" -ne 0 ] && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT")" = "sarif-too-large" ]; then
  good "oversize stdin refuses with sarif-too-large"
else
  bad "oversize stdin gave exit $EVAL_STATUS code '$(jq -r '.error.code // empty' <"$EVAL_OUT")'"
fi

ln -s "$sarif1" "$EVAL_SANDBOX/link.sarif"
run_review ingest acme/app "$PR" --from sarif "$EVAL_SANDBOX/link.sarif" --json
if [ "$EVAL_STATUS" -ne 0 ] && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT")" = "bad-sarif" ]; then
  good "symlinked source refuses with bad-sarif"
else
  bad "symlink gave exit $EVAL_STATUS code '$(jq -r '.error.code // empty' <"$EVAL_OUT")'"
fi

if [ "$(( $(wc -l <"$EVAL_STUBLOG" | tr -d ' ') - stub_before ))" = "0" ]; then
  good "zero stub-gh intercepts across ingest legs (offline)"
else
  bad "stub gh intercepted calls during ingest legs"
fi

eval_end
