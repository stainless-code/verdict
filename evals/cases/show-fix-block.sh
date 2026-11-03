#!/usr/bin/env bash
# Case show-fix-block: `show --json` carries the additive read-only
# `data.fix` repair directive beside the unchanged `data.item`.
#
# Grades (all hermetic — crafted findings + history, stub gh, no network):
#   F1  full finding: fix carries sig/status/severity/files/locator/
#       suggestion/verify_hint/caution, and data.item is byte-identical to
#       the stored record. The bare (no --json) text path is unchanged too.
#   F2  suggestion-less section finding: suggestion null, files read from the
#       record's related list, verify_hint null when no history exists.
#   F3  verification.json history for a short sig wins over a non-matching
#       patch.json: verify_hint names the exact rerun command + resultHead.
#   F4  unknown and ambiguous sigs keep their typed refusals (data null, no
#       fix block), before any block is built.
#   F5  read-path proof: every bundle byte is identical after the runs and no
#       gh call is attempted.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

# Normalize the sandbox path (mktemp leaves a double slash when TMPDIR ends
# in one, which some tooling reports differently).
EVAL_SANDBOX="$(cd "$EVAL_SANDBOX" && pwd -P)"

GOLDENS="$EVAL_ROOT/evals/fixtures/show-fix-block"

# Deterministic surface: no settings file, no ambient REVIEW_* knobs.
mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_PANEL REVIEW_MODEL REVIEW_BUDGET_USD || true

# bundle_checksum <pr> — every file byte under the bundle, sorted.
bundle_checksum() {
  ( cd "$EVAL_SANDBOX/.review/$1" && find . -type f -print | LC_ALL=C sort | xargs shasum )
}

check_fix() { # $1 = label, $2 = golden file
  jq -c '.data.fix' <"$EVAL_OUT" >"$EVAL_SANDBOX/fix-actual.json" 2>/dev/null || true
  if diff -u "$2" "$EVAL_SANDBOX/fix-actual.json" >"$EVAL_SANDBOX/fix-diff.txt" 2>&1; then
    good "$1 fix block matches the golden exactly"
  else
    bad "$1 fix block differs: $(head -6 "$EVAL_SANDBOX/fix-diff.txt" | tr '\n' ' ')"
  fi
}

# --- F1: full record + patch history ---
mkdir -p "$EVAL_SANDBOX/.review/5201"
cat > "$EVAL_SANDBOX/.review/5201/findings.json" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Consider clarifying this line.","suggestion":"Use a clearer name.","inBounds":true,"rigor":"pointed","severity":"important","sig":"c111111111111111111111111111111111111111","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[],"related_files":["notes.txt","src/other.go"]}]
EOF
cat > "$EVAL_SANDBOX/.review/5201/patch.json" <<'EOF'
{"sig":"c1111111","verifyCmd":"go test ./...","resultHead":"aaaaaaa","baseHead":"bbbbbbb"}
EOF
f1_before="$(bundle_checksum 5201)"
run_review show acme/app 5201 --sig c1111111 --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(stdout_docs)" = "1" ]; then
  good "show --json exits 0 with exactly one envelope"
else
  bad "show --json exits $EVAL_STATUS with $(stdout_docs) docs: $(head -2 "$EVAL_ERR" | tr '\n' ' ')"
fi
if [ "$(jq -r '.data | keys_unsorted | join(",")' <"$EVAL_OUT" 2>/dev/null)" = "item,fix" ]; then
  good "data carries item then the additive fix key"
else
  bad "data keys = $(jq -c '.data | keys_unsorted' <"$EVAL_OUT" 2>/dev/null)"
fi
check_fix "F1" "$GOLDENS/full.fix.golden"
if [ "$(jq -c '.data.item' <"$EVAL_OUT" 2>/dev/null)" = "$(jq -c '.[0]' "$EVAL_SANDBOX/.review/5201/findings.json")" ]; then
  good "data.item is the stored record byte-for-byte"
else
  bad "data.item drifted from the stored record"
fi
run_review show acme/app 5201 --sig c1111111
jq '.' "$EVAL_SANDBOX/.review/5201/findings.json" | jq '.[0]' >"$EVAL_SANDBOX/text-expected.json"
if [ "$EVAL_STATUS" -eq 0 ] && diff -q "$EVAL_SANDBOX/text-expected.json" "$EVAL_OUT" >/dev/null 2>&1; then
  good "bare show keeps printing the record via the unchanged text path"
else
  bad "text path changed: exit $EVAL_STATUS"
fi
if [ "$(bundle_checksum 5201)" = "$f1_before" ]; then
  good "F1 bundle is byte-identical after the read"
else
  bad "F1 bundle changed during a read"
fi

# --- F2: suggestion-less section finding, read from a related-files list ---
mkdir -p "$EVAL_SANDBOX/.review/5202"
cat > "$EVAL_SANDBOX/.review/5202/findings.json" <<'EOF'
[{"kind":"section","heading":"Scope note","anchored":"body","body":"Explains the scope.","suggestion":null,"inBounds":true,"rigor":"walked","severity":null,"sig":"d222222222222222222222222222222222222222","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"deferred","rationale":"","history":[],"files":["docs/scope.md"]}]
EOF
run_review show acme/app 5202 --sig d2222222 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.fix.suggestion' <"$EVAL_OUT" 2>/dev/null)" = "null" ] \
  && [ "$(jq -r '.data.fix.verify_hint' <"$EVAL_OUT" 2>/dev/null)" = "null" ] \
  && [ "$(jq -r '.data.fix.locator' <"$EVAL_OUT" 2>/dev/null)" = "Scope note" ]; then
  good "suggestion-less finding keeps a well-formed block with honest nulls"
else
  bad "null-suggestion block: $(jq -c '.data.fix // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi
check_fix "F2" "$GOLDENS/null-suggestion.fix.golden"

# --- F3: verification.json for the sig wins over a non-matching patch ---
mkdir -p "$EVAL_SANDBOX/.review/5203"
cat > "$EVAL_SANDBOX/.review/5203/findings.json" <<'EOF'
[{"kind":"inline","path":"src/a.go","side":"RIGHT","line":10,"anchored":true,"body":"Check the error path.","suggestion":null,"inBounds":true,"rigor":"ran","severity":"informational","sig":"e333333333333333333333333333333333333333","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
cat > "$EVAL_SANDBOX/.review/5203/verification.json" <<'EOF'
[{"sig":"e3333333","verdict":"VERIFIED","verifyCmd":"go test ./internal/a","resultHead":"eeeeeee"}]
EOF
cat > "$EVAL_SANDBOX/.review/5203/patch.json" <<'EOF'
{"sig":"ffffffff","verifyCmd":"go test ./different","resultHead":"fffffff"}
EOF
run_review show acme/app 5203 --sig e3333333 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.fix.verify_hint.verifyCmd' <"$EVAL_OUT" 2>/dev/null)" = "go test ./internal/a" ] \
  && [ "$(jq -r '.data.fix.verify_hint.resultHead' <"$EVAL_OUT" 2>/dev/null)" = "eeeeeee" ]; then
  good "verification.json for the sig supplies the exact rerun hint, not a stale patch"
else
  bad "verify hint: $(jq -c '.data.fix.verify_hint // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi
check_fix "F3" "$GOLDENS/verify-hint.fix.golden"

# --- F4: typed refusals fire before any block is built ---
mkdir -p "$EVAL_SANDBOX/.review/5204"
cat > "$EVAL_SANDBOX/.review/5204/findings.json" <<'EOF'
[{"kind":"inline","path":"a.go","side":"RIGHT","line":1,"anchored":true,"body":"One.","suggestion":null,"inBounds":true,"rigor":"said","severity":"informational","sig":"a111111111111111111111111111111111111111","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},{"kind":"inline","path":"b.go","side":"RIGHT","line":1,"anchored":true,"body":"Two.","suggestion":null,"inBounds":true,"rigor":"said","severity":"informational","sig":"a222222222222222222222222222222222222222","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
run_review show acme/app 5204 --sig deadbeef --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)" = "unknown-sig" ] \
  && [ "$(jq -r '.data' <"$EVAL_OUT" 2>/dev/null)" = "null" ] \
  && [ "$(stdout_docs)" = "1" ]; then
  good "unknown sig keeps the typed refusal with no fix block"
else
  bad "unknown sig: exit $EVAL_STATUS $(jq -c '{error,data}' <"$EVAL_OUT" 2>/dev/null)"
fi
run_review show acme/app 5204 --sig a --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)" = "ambiguous-sig" ] \
  && [ "$(jq -r '.data' <"$EVAL_OUT" 2>/dev/null)" = "null" ]; then
  good "ambiguous sig keeps the typed refusal with no fix block"
else
  bad "ambiguous sig: exit $EVAL_STATUS $(jq -c '{error,data}' <"$EVAL_OUT" 2>/dev/null)"
fi

# --- F5: the whole read path is side-effect free ---
f5_before="$(for pr in 5201 5202 5203 5204; do bundle_checksum "$pr"; done)"
gh_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
run_review show acme/app 5201 --sig c1111111 --json
f5_after="$(for pr in 5201 5202 5203 5204; do bundle_checksum "$pr"; done)"
gh_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$f5_before" = "$f5_after" ]; then
  good "F5 every bundle is byte-identical after the read path"
else
  bad "F5 a show run mutated the bundle"
fi
if [ "$gh_before" = "$gh_after" ]; then
  good "F5 no gh call is attempted by show"
else
  bad "F5 show attempted $((gh_after - gh_before)) gh call(s)"
fi

eval_end
