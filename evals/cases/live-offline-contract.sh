#!/usr/bin/env bash
# Case live-offline-contract: the explicit offline posture for the
# report gate's live-thread dimension.
#
# Grades (all hermetic — crafted bundles, smart gh stub, no network):
#   S1  strict-unreachable-clean: unreachable lookup on a clean bundle
#       fails the gate (exit 1, never 3) with a typed check: line naming
#       the strict posture plus the recovery (restore network or --live
#       skip); the warn lands in the envelope warnings[] too; the store
#       is unmutated (report never posts).
#   S2  strict-unreachable-dirty: tier failure plus the live fail-closed
#       line coexist, still exit 1.
#   S3  strict-reachable: a reachable clear lookup passes exactly like
#       auto (strict changes only the unreachable outcome).
#   S4  strict-structural-skip: local bundles stay neutral in strict with
#       zero network (no live GitHub state exists to verify).
#   K1  skip-loud: --live skip on a bundle auto would fail (more live than
#       local opens) exits 0 on the local dimensions, pays zero graphql
#       calls, warns naming --live skip, prints a neutral skipped check:
#       line, and carries the skip in the envelope warnings[].
#   K2  skip --json: the skip rides warnings[] with the success-shaped
#       envelope intact.
#   K3  auto-explicit: an explicit --live auto matches the default exactly
#       (unreachable stays neutral, exit 0) — stating the posture never
#       inherits it silently.
#   K4  bad-posture: an unknown --live value refuses with exit 2.
#   K5  advisory-inert: --live with no gate flag makes zero network calls
#       and reads exactly as today (the flag is only consulted on gates).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

# Overlay a smart gh stub: the threads (graphql) read follows
# $EVAL_SANDBOX/recheck-mode (clear/open, else unreachable). Everything
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
    open) printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":false},{"isResolved":true}]}}}}}'; exit 0;;
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
HEAD_D="dddddddddddddddddddddddddddddddddddddddd"

# live_mk_post <pr> <head> — craft a completed-post record.
live_mk_post() {
  local pr="$1" head="$2"
  jq -n --arg h "$head" \
    '{schema_version:"1",review:{id:99},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null}' \
    > "$EVAL_SANDBOX/.review/$pr/post.json"
}

CRIT_FINDING='[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Critical boundary bug here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"critical","sig":"cccccccccccccccccccccccccccccccccccccccc","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]'

# --- S1: strict fails closed on unreachable, clean bundle ---
PR=4371
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
live_mk_post "$PR" "$HEAD_A"
set_mode unreachable
before_hash="$(shasum "$EVAL_SANDBOX/.review/$PR/findings.json" "$EVAL_SANDBOX/.review/$PR/post.json" | shasum | cut -d' ' -f1)"
m="$(mark)"
run_review report acme/app "$PR" --check critical --completeness --live strict
if [ "$EVAL_STATUS" -eq 1 ]; then
  good "strict unreachable on a clean bundle exits 1 (never 3)"
else
  bad "strict unreachable clean exits $EVAL_STATUS, want 1"
fi
if grep -q "^check: live thread recheck unreachable" "$EVAL_ERR" \
  && grep -q "offline posture is strict" "$EVAL_ERR" \
  && grep -q "\-\-live skip" "$EVAL_ERR" \
  && ! grep -q "live open" "$EVAL_ERR"; then
  good "fail-closed line is typed and names the posture plus the recovery"
else
  bad "strict clean stderr: $(head -c 300 "$EVAL_ERR")"
fi
if grep -q "live thread recheck unreachable" "$EVAL_ERR"; then
  good "strict unreachable still warns on stderr"
else
  bad "strict unreachable warn missing: $(head -c 200 "$EVAL_ERR")"
fi
if [ "$(graphql_since "$m")" -ge 1 ]; then
  good "strict still attempted the lookup before failing closed"
else
  bad "strict paid no lookup call before failing"
fi
after_hash="$(shasum "$EVAL_SANDBOX/.review/$PR/findings.json" "$EVAL_SANDBOX/.review/$PR/post.json" | shasum | cut -d' ' -f1)"
if [ "$before_hash" = "$after_hash" ]; then
  good "fail-closed gate mutates no store"
else
  bad "fail-closed gate moved store bytes"
fi
run_review report acme/app "$PR" --check critical --completeness --live strict --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && jq -e '.error == null' <"$EVAL_OUT" >/dev/null 2>&1 \
  && jq -e '.warnings | join(" ") | contains("offline posture is strict")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "strict --json keeps the success-shaped envelope with the posture in warnings[]"
else
  bad "strict --json exits $EVAL_STATUS: $(jq -c '{error:.error,warnings:.warnings}' <"$EVAL_OUT" 2>/dev/null)"
fi

# --- S2: strict unreachable on a tier-dirty bundle ---
PR=4372
eval_mk_bundle "$PR"
printf '%s' "$CRIT_FINDING" | eval_write_findings "$PR"
set_mode unreachable
run_review report acme/app "$PR" --check critical --live strict
if [ "$EVAL_STATUS" -eq 1 ]; then
  good "strict unreachable on a tier-dirty bundle exits 1"
else
  bad "strict unreachable dirty exits $EVAL_STATUS, want 1"
fi
if grep -q "at-or-above critical — gate fails" "$EVAL_ERR" \
  && grep -q "^check: live thread recheck unreachable" "$EVAL_ERR"; then
  good "tier fail line and live fail-closed line coexist"
else
  bad "strict dirty stderr: $(head -c 300 "$EVAL_ERR")"
fi

# --- S3: strict with a reachable clear lookup passes like auto ---
PR=4373
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
live_mk_post "$PR" "$HEAD_A"
set_mode clear
run_review report acme/app "$PR" --check critical --completeness --live strict
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "strict with a reachable clear lookup exits 0"
else
  bad "strict reachable exits $EVAL_STATUS, want 0"
fi
if grep -q "0 live open" "$EVAL_ERR" && grep -q "live recheck passes" "$EVAL_ERR"; then
  good "strict prints the same live pass line as auto"
else
  bad "strict reachable stderr: $(head -c 300 "$EVAL_ERR")"
fi

# --- S4: strict stays neutral on structural skips (local bundles) ---
PR=4374
mkdir -p "$EVAL_SANDBOX/.review/local-offlinec"
cat > "$EVAL_SANDBOX/.review/local-offlinec/source.json" <<EOF
{"schema_version":"1","kind":"range","repo":"acme/app","slug":"offlinec","base_ref":null,"head_ref":"v2","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_sha":"$HEAD_D","range":"v1..v2","dirty":false,"diff_sha256":"0","files":1,"additions":1,"deletions":1,"tool_version":"0.0.0-test","model":null,"created_at":"2026-01-01T00:00:00Z"}
EOF
printf '[]' > "$EVAL_SANDBOX/.review/local-offlinec/findings.json"
jq -n --arg h "$HEAD_D" \
  '{schema_version:"1",review:{id:7},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null}' \
  > "$EVAL_SANDBOX/.review/local-offlinec/post.json"
set_mode unreachable
m="$(mark)"
run_review report acme/app offlinec --check critical --completeness --live strict
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "strict on a local bundle stays neutral (exit 0)"
else
  bad "strict local exits $EVAL_STATUS, want 0"
fi
if grep -q "live recheck skipped" "$EVAL_ERR" \
  && ! grep -q "offline posture is strict" "$EVAL_ERR" \
  && [ "$(graphql_since "$m")" -eq 0 ]; then
  good "local skip warns structurally with zero network, no fail-closed"
else
  bad "strict local stderr paid $(graphql_since "$m") call(s): $(head -c 200 "$EVAL_ERR")"
fi

# --- K1: --live skip is loud and network-free on a would-fail bundle ---
PR=4375
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
live_mk_post "$PR" "$HEAD_A"
set_mode open
m="$(mark)"
run_review report acme/app "$PR" --check critical --completeness --live skip
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "--live skip exits 0 where auto would fail on the extra live open"
else
  bad "--live skip exits $EVAL_STATUS, want 0"
fi
if grep -q "live recheck skipped (--live skip" "$EVAL_ERR" \
  && grep -q "^check: live recheck skipped (--live skip)" "$EVAL_ERR" \
  && ! grep -q "live open" "$EVAL_ERR" \
  && [ "$(graphql_since "$m")" -eq 0 ]; then
  good "skip warns loudly, prints a neutral skipped check: line, pays zero graphql"
else
  bad "skip stderr paid $(graphql_since "$m") call(s): $(head -c 300 "$EVAL_ERR")"
fi
run_review report acme/app "$PR" --check critical --completeness
if [ "$EVAL_STATUS" -eq 1 ]; then
  good "same bundle without --live skip still fails (the skip is opt-in only)"
else
  bad "auto on the open bundle exits $EVAL_STATUS, want 1"
fi

# --- K2: skip rides the --json envelope ---
set_mode open
run_review report acme/app "$PR" --check critical --completeness --live skip --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.error == null and .warnings == ["live recheck skipped (--live skip requested — gating on local dimensions only)"]' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "skip --json keeps the success shape with the skip in warnings[]"
else
  bad "skip --json exits $EVAL_STATUS: $(jq -c '{error:.error,warnings:.warnings}' <"$EVAL_OUT" 2>/dev/null)"
fi

# --- K3: explicit --live auto matches the default ---
PR=4376
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
live_mk_post "$PR" "$HEAD_A"
set_mode unreachable
run_review report acme/app "$PR" --check critical --completeness --live auto
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "explicit --live auto exits 0 on unreachable (fail-open, like default)"
else
  bad "explicit auto exits $EVAL_STATUS, want 0"
fi
if grep -q "live thread recheck unreachable" "$EVAL_ERR" \
  && ! grep -q "live open" "$EVAL_ERR"; then
  good "explicit auto warns neutrally with no live verdict"
else
  bad "explicit auto stderr: $(head -c 300 "$EVAL_ERR")"
fi

# --- K4: unknown posture refuses with exit 2 ---
run_review report acme/app "$PR" --check critical --live bogus
if [ "$EVAL_STATUS" -eq 2 ] && grep -q "unknown --live posture 'bogus'" "$EVAL_ERR"; then
  good "unknown --live value refuses typed with exit 2"
else
  bad "bogus --live exits $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi

# --- K5: --live without a gate flag stays advisory and network-free ---
PR=4377
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
set_mode clear
m="$(mark)"
run_review report acme/app "$PR" --live strict
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "advisory run with --live exits 0"
else
  bad "advisory --live exits $EVAL_STATUS, want 0"
fi
if ! grep -q "check:" "$EVAL_ERR" && [ "$(graphql_since "$m")" -eq 0 ]; then
  good "advisory --live prints no check: line and makes zero calls"
else
  bad "advisory --live paid $(graphql_since "$m") call(s): $(head -c 200 "$EVAL_ERR")"
fi

eval_end
