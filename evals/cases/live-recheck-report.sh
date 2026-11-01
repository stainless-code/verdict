#!/usr/bin/env bash
# Case live-recheck-report: live-thread dimension on the report gate tail.
#
# Grades (all hermetic — crafted bundles, smart gh stub, no network):
#   G1  clear-pass: zero live opens on a clean reviewed bundle -> gate exits
#       0 with tier, completeness, and live check: pass lines naming both
#       counts; the one-live leg proves the comparison key is the
#       whole-store open count (a lone below-tier finding still passes).
#   G2  unknown-fails: more live opens than local opens -> exit 1, the
#       check: line names both counts plus the fetch-and-triage path, the
#       store is unmutated (report never posts), and --force is refused
#       (no --force on report).
#   G3  unreachable-neutral both directions: a failed lookup warns (stderr +
#       envelope warnings[]) and stays neutral — a clean bundle still exits
#       0, a tier-dirty bundle still exits 1 on the tier; never exit 3, and
#       no live check: line prints either way.
#   G4  no-flag-zero-calls: advisory report without a gate flag makes zero
#       graphql calls, prints no check: line, and reads deterministically.
#   G5  provider/local skip: non-GitHub and local bundles warn-skip with
#       zero graphql calls while the local dimensions still gate.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

# Overlay a smart gh stub: the threads (graphql) read follows
# $EVAL_SANDBOX/recheck-mode (clear/one/open, else unreachable). Everything
# else stays network-dead. Restored on exit so sibling cases keep theirs.
STUB_SAVED="$EVAL_SANDBOX/gh-stub-orig"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
restore_stub() { cp "$STUB_SAVED" "$EVAL_STUBBIN/gh"; }
trap restore_stub EXIT
cat > "$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
if [ "${1:-}" = "api" ] && [ "${2:-}" = "graphql" ]; then
  mode="$(cat "${EVAL_SANDBOX:-/tmp}/recheck-mode" 2>/dev/null || echo unreachable)"
  case "$mode" in
    clear) printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}'; exit 0;;
    one) printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":false}]}}}}}'; exit 0;;
    open) printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":false},{"isResolved":false},{"isResolved":true}]}}}}}'; exit 0;;
    *) echo "evals-stub-gh: threads unreachable" >&2; exit 1;;
  esac
fi
echo "evals-stub-gh: network disabled ($*)" >&2
exit 1
EOF
chmod +x "$EVAL_STUBBIN/gh"

# graphql_since <mark> — recheck (graphql) calls logged after <mark>.
graphql_since() {
  local n
  n=$(tail -n +"$(( $1 + 1 ))" "$EVAL_STUBLOG" 2>/dev/null | grep -c "graphql" || true)
  printf '%s' "$n"
}
mark() { wc -l <"$EVAL_STUBLOG" | tr -d ' '; }
set_mode() { printf '%s' "$1" > "$EVAL_SANDBOX/recheck-mode"; }

HEAD_A="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
HEAD_C="cccccccccccccccccccccccccccccccccccccccc"
HEAD_D="dddddddddddddddddddddddddddddddddddddddd"

# live_mk_post <pr> <head> — craft a completed-post record.
live_mk_post() {
  local pr="$1" head="$2"
  jq -n --arg h "$head" \
    '{schema_version:"1",review:{id:99},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null}' \
    > "$EVAL_SANDBOX/.review/$pr/post.json"
}

INFO_FINDING='[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Nit: clarify this line.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]'
CRIT_FINDING='[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Critical boundary bug here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"critical","sig":"cccccccccccccccccccccccccccccccccccccccc","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]'

# --- G1: clear-pass (tier + completeness + live all pass) ---
PR=4284
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
live_mk_post "$PR" "$HEAD_A"
set_mode clear
m="$(mark)"
run_review report acme/app "$PR" --check critical --completeness
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "clear gate exits 0"
else
  bad "clear gate exits $EVAL_STATUS, want 0"
fi
if grep -q "no open findings at-or-above critical — gate passes" "$EVAL_ERR" \
  && grep -q "completeness passes" "$EVAL_ERR" \
  && grep -q "0 live open" "$EVAL_ERR" \
  && grep -q "0 local open" "$EVAL_ERR"; then
  good "clear gate prints all three pass lines with both live counts"
else
  bad "clear stderr: $(head -c 300 "$EVAL_ERR")"
fi
if [ "$(graphql_since "$m")" -ge 1 ]; then
  good "gate run paid the live recheck call"
else
  bad "gate run made no live recheck call"
fi
run_review report acme/app "$PR" --check critical --completeness --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.error == null and .warnings == []' <"$EVAL_OUT" >/dev/null 2>&1 \
  && [ "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null)" = "at data error next pr repo schema_version verb warnings" ]; then
  good "clear --json keeps the envelope shape with null error and no warnings"
else
  bad "clear --json exits $EVAL_STATUS: $(jq -c '{error:.error,warnings:.warnings}' <"$EVAL_OUT" 2>/dev/null)"
fi

# G1b: whole-store key — one below-tier open finding, one live open thread,
# --check critical still passes (live 1 vs local 1, not vs tier hits 0).
PR=4285
eval_mk_bundle "$PR"
printf '%s' "$INFO_FINDING" | eval_write_findings "$PR"
live_mk_post "$PR" "$HEAD_A"
set_mode one
run_review report acme/app "$PR" --check critical --completeness
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "below-tier finding with matching live count exits 0"
else
  bad "whole-store leg exits $EVAL_STATUS, want 0"
fi
if grep -q "1 live open" "$EVAL_ERR" && grep -q "1 local open" "$EVAL_ERR"; then
  good "live line compares against the whole-store open count"
else
  bad "whole-store stderr: $(head -c 300 "$EVAL_ERR")"
fi

# --- G2: unknown-live fails the gate ---
PR=4286
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
live_mk_post "$PR" "$HEAD_A"
before_hash="$(shasum "$EVAL_SANDBOX/.review/$PR/findings.json" "$EVAL_SANDBOX/.review/$PR/post.json" | shasum | cut -d' ' -f1)"
set_mode open
run_review report acme/app "$PR" --check critical --completeness
if [ "$EVAL_STATUS" -eq 1 ]; then
  good "unknown-live exits 1"
else
  bad "unknown-live exits $EVAL_STATUS, want 1"
fi
if grep -q "^check:" "$EVAL_ERR" \
  && grep -q "2 live open" "$EVAL_ERR" \
  && grep -q "0 local open" "$EVAL_ERR" \
  && grep -q "fetch + triage" "$EVAL_ERR" \
  && grep -q "re-check" "$EVAL_ERR"; then
  good "fail line names both counts plus the fetch-and-triage path"
else
  bad "fail stderr: $(head -c 300 "$EVAL_ERR")"
fi
if grep -q "no open findings at-or-above critical — gate passes" "$EVAL_ERR" \
  && grep -q "completeness passes" "$EVAL_ERR"; then
  good "local dimensions still print their own pass lines"
else
  bad "local pass lines missing: $(head -c 300 "$EVAL_ERR")"
fi
after_hash="$(shasum "$EVAL_SANDBOX/.review/$PR/findings.json" "$EVAL_SANDBOX/.review/$PR/post.json" | shasum | cut -d' ' -f1)"
if [ "$before_hash" = "$after_hash" ]; then
  good "failed gate mutates no store (report never posts)"
else
  bad "failed gate moved store bytes"
fi
run_review report acme/app "$PR" --check critical --completeness --json
if [ "$EVAL_STATUS" -eq 1 ] && jq -e '.error == null' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "failed gate --json keeps the success-shaped envelope (exit carries the failure)"
else
  bad "failed gate --json exits $EVAL_STATUS: $(jq -c '.error' <"$EVAL_OUT" 2>/dev/null)"
fi
run_review report acme/app "$PR" --check critical --force
if [ "$EVAL_STATUS" -eq 2 ] && grep -q "unknown flag '--force'" "$EVAL_ERR"; then
  good "report has no --force escape (usage exit 2)"
else
  bad "--force gave exit $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi

# --- G3: unreachable stays neutral, both directions ---
PR=4287
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
live_mk_post "$PR" "$HEAD_A"
set_mode unreachable
run_review report acme/app "$PR" --check critical --completeness --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "unreachable on a clean bundle stays exit 0 (never exit 3)"
else
  bad "unreachable clean exits $EVAL_STATUS, want 0"
fi
if grep -q "live thread recheck unreachable" "$EVAL_ERR" \
  && ! grep -q "live open" "$EVAL_ERR"; then
  good "unreachable warns with no live check: line (neutral)"
else
  bad "unreachable clean stderr: $(head -c 300 "$EVAL_ERR")"
fi
if jq -e '.warnings | join(" ") | contains("live thread recheck unreachable")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "envelope warnings carry the unreachable reason"
else
  bad "envelope warnings miss the reason: $(jq -c '.warnings' <"$EVAL_OUT" 2>/dev/null)"
fi

PR=4288
eval_mk_bundle "$PR"
printf '%s' "$CRIT_FINDING" | eval_write_findings "$PR"
set_mode unreachable
run_review report acme/app "$PR" --check critical --json
if [ "$EVAL_STATUS" -eq 1 ]; then
  good "unreachable on a tier-dirty bundle still exits 1 on the tier"
else
  bad "unreachable dirty exits $EVAL_STATUS, want 1"
fi
if grep -q "at-or-above critical — gate fails" "$EVAL_ERR" \
  && grep -q "live thread recheck unreachable" "$EVAL_ERR" \
  && ! grep -q "live open" "$EVAL_ERR"; then
  good "tier fail line plus the neutral unreachable warn, no live verdict"
else
  bad "unreachable dirty stderr: $(head -c 300 "$EVAL_ERR")"
fi
if jq -e '.warnings | join(" ") | contains("live thread recheck unreachable")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "dirty envelope warnings carry the unreachable reason"
else
  bad "dirty envelope warnings: $(jq -c '.warnings' <"$EVAL_OUT" 2>/dev/null)"
fi

# --- G4: no gate flag means zero network ---
PR=4290
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
set_mode clear
m="$(mark)"
run_review report acme/app "$PR"
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "advisory report exits 0"
else
  bad "advisory report exits $EVAL_STATUS, want 0"
fi
if ! grep -q "check:" "$EVAL_ERR" && [ "$(graphql_since "$m")" -eq 0 ]; then
  good "advisory report prints no check: line and makes zero recheck calls"
else
  bad "advisory run paid $(graphql_since "$m") call(s): $(head -c 200 "$EVAL_ERR")"
fi
cp "$EVAL_OUT" "$EVAL_SANDBOX/live-g4-first.txt"
run_review report acme/app "$PR"
if cmp -s "$EVAL_OUT" "$EVAL_SANDBOX/live-g4-first.txt"; then
  good "advisory output reads deterministically across runs"
else
  bad "advisory output differs between runs"
fi
rm -f "$EVAL_SANDBOX/live-g4-first.txt"
m="$(mark)"
run_review report acme/app "$PR" --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.warnings == []' <"$EVAL_OUT" >/dev/null 2>&1 \
  && ! grep -q "check:" "$EVAL_ERR" && [ "$(graphql_since "$m")" -eq 0 ]; then
  good "advisory --json stays warn-free with zero recheck calls"
else
  bad "advisory --json paid $(graphql_since "$m") call(s)"
fi

# --- G5: provider + local bundles warn-skip with zero network ---
PR=4289
eval_mk_bundle "$PR"
cat > "$EVAL_SANDBOX/.review/$PR/PR.json" <<EOF
{"schema_version":"1","provider":"gitlab","number":$PR,"title":"t","body":"","base_ref":"main","head_ref":"feat","base_sha":"$HEAD_C","head_sha":"$HEAD_A","author":"mate","url":"https://gitlab.example/acme/app/-/merge_requests/$PR","state":"open","draft":false,"files":[],"additions":0,"deletions":0}
EOF
printf '[]' | eval_write_findings "$PR"
live_mk_post "$PR" "$HEAD_A"
set_mode clear
m="$(mark)"
run_review report acme/app "$PR" --completeness --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "provider bundle with reviewed HEAD exits 0"
else
  bad "provider skip exits $EVAL_STATUS, want 0"
fi
if grep -q "live recheck skipped: gitlab threads unreadable here" "$EVAL_ERR" \
  && ! grep -q "live open" "$EVAL_ERR" && [ "$(graphql_since "$m")" -eq 0 ]; then
  good "provider run warn-skips the GitHub-only read with zero calls"
else
  bad "provider stderr paid $(graphql_since "$m") call(s): $(head -c 300 "$EVAL_ERR")"
fi
if jq -e '.warnings | join(" ") | contains("live recheck skipped")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "provider skip rides the envelope warnings"
else
  bad "provider envelope warnings: $(jq -c '.warnings' <"$EVAL_OUT" 2>/dev/null)"
fi
live_mk_post "$PR" "$HEAD_C"
m="$(mark)"
run_review report acme/app "$PR" --completeness
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "stale-head" "$EVAL_ERR" && [ "$(graphql_since "$m")" -eq 0 ]; then
  good "stale provider bundle still fails on the local dimension with zero calls"
else
  bad "stale provider exits $EVAL_STATUS with $(graphql_since "$m") call(s)"
fi

mkdir -p "$EVAL_SANDBOX/.review/local-liverep"
cat > "$EVAL_SANDBOX/.review/local-liverep/source.json" <<EOF
{"schema_version":"1","kind":"range","repo":"acme/app","slug":"liverep","base_ref":null,"head_ref":"v2","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_sha":"$HEAD_D","range":"v1..v2","dirty":false,"diff_sha256":"0","files":1,"additions":1,"deletions":1,"tool_version":"0.0.0-test","model":null,"created_at":"2026-01-01T00:00:00Z"}
EOF
printf '[]' > "$EVAL_SANDBOX/.review/local-liverep/findings.json"
jq -n --arg h "$HEAD_D" \
  '{schema_version:"1",review:{id:7},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null}' \
  > "$EVAL_SANDBOX/.review/local-liverep/post.json"
set_mode clear
m="$(mark)"
run_review report acme/app liverep --check critical --completeness
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "local bundle with reviewed HEAD exits 0"
else
  bad "local skip exits $EVAL_STATUS, want 0"
fi
if grep -q "live recheck skipped" "$EVAL_ERR" \
  && ! grep -qi "fetch-local" "$EVAL_ERR" \
  && ! grep -q "live open" "$EVAL_ERR" && [ "$(graphql_since "$m")" -eq 0 ]; then
  good "local run warn-skips with zero calls and no re-fetch noise"
else
  bad "local stderr paid $(graphql_since "$m") call(s): $(head -c 300 "$EVAL_ERR")"
fi
printf '%s' "$CRIT_FINDING" > "$EVAL_SANDBOX/.review/local-liverep/findings.json"
m="$(mark)"
run_review report acme/app liverep --check critical
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "at-or-above critical — gate fails" "$EVAL_ERR" && [ "$(graphql_since "$m")" -eq 0 ]; then
  good "tier-dirty local bundle still fails on the local dimension with zero calls"
else
  bad "dirty local exits $EVAL_STATUS with $(graphql_since "$m") call(s)"
fi

if [ "$fails" -ne 0 ]; then echo "case live-recheck-report: FAIL ($fails)"; exit 1; fi
echo "case live-recheck-report: PASS"
