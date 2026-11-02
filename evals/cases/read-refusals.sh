#!/usr/bin/env bash
# Case read-refusals: the pure-read verbs fail closed typed on corrupt stores.
#
# Grades (all hermetic — crafted local/PR bundles in the sandbox, the
# network-dead gh stub stays first on PATH):
#   R1  report refuses corrupt-store on a numeric-sig entry (was jq exit-5,
#       no envelope — the shape guard now names the store instead).
#   R2  report refuses corrupt-store on a numeric-body entry.
#   R3  report refuses corrupt-store on a non-object entry.
#   R4  show resolves a valid prefix beside a null-sig entry (was a
#       startswith crash — null sigs never match, they no longer kill).
#   R5  show refuses corrupt-store on a non-array store (show had no store
#       check at all before).
#   R6  calibrate --probe refuses corrupt-store on a misshapen entry.
#   R7  trend warns and skips a corrupt bundle, rolling up the readable
#       ones (exit 0 — one bad bundle never fails the whole audit).
#   R8  report refuses corrupt-store on an unreadable patch.json.
#   R9  trend survives a boolean-kb entry (was a length crash).
#   R10 calibrate (live path) refuses missing-post on an unreadable
#       post.json — before any gh call, so the stub stays silent.
#   R11 calibrate --probe grades a string-kb entry as uncited (was a map
#       crash — non-array kb now reads as no cites, exit 0).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

# Cases share one sandbox: stash any bundles other cases left behind so the
# rollup below grades exactly this case's fixtures (restored on exit —
# later cases must see their own stores untouched).
HOLD="$EVAL_SANDBOX/.review-hold-readref"
mkdir -p "$EVAL_SANDBOX/.review" "$HOLD"
for e in "$EVAL_SANDBOX"/.review/*; do
  [ -e "$e" ] || continue
  mv "$e" "$HOLD/"
done
restore_rr() {
  for e in "$HOLD"/*; do
    [ -e "$e" ] || continue
    mv "$e" "$EVAL_SANDBOX/.review/"
  done
  rmdir "$HOLD" 2>/dev/null || true
}
trap restore_rr EXIT

VALID_SIG="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

# mkf <slug> — stdin becomes findings.json inside a minimal local bundle.
mkf() { # $1 = slug
  local dir="$EVAL_SANDBOX/.review/$1"
  rm -rf "$dir"; mkdir -p "$dir"
  cat > "$dir/source.json" <<'EOF'
{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head_ref":"work"}
EOF
  cat > "$dir/full.diff" <<'EOF'
diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 top
-old line
+new line
 bottom
EOF
  cat > "$dir/findings.json"
}

ENTRY_TMPL='{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Usable body here.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"SIG","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}'

# expect_store_refusal <label> <verb...> — the verb refuses corrupt-store
# (exit 1, envelope code) and the seeded findings.json stays byte-identical.
expect_store_refusal() { # $1 = label, rest = review args
  local label="$1"; shift
  local before=""
  before="$(shasum -a 1 "$EVAL_SANDBOX/.review/$SLUG/findings.json" | cut -d' ' -f1)"
  run_review "$@" --json
  local got=""
  got="$(jq -r '.error.code // empty' "$EVAL_OUT" 2>/dev/null || true)"
  if [ "$EVAL_STATUS" -eq 1 ] && [ "$got" = "corrupt-store" ]; then
    good "$label refuses corrupt-store"
  else
    bad "$label exits $EVAL_STATUS with error '$got' (want exit 1, corrupt-store)"
  fi
  local after=""
  after="$(shasum -a 1 "$EVAL_SANDBOX/.review/$SLUG/findings.json" | cut -d' ' -f1)"
  if [ "$before" = "$after" ]; then
    good "$label left findings.json byte-identical"
  else
    bad "$label rewrote findings.json on refusal"
  fi
}

# --- R1: numeric sig ---
SLUG="local-rr1"
printf '[%s]' "$(printf '%s' "$ENTRY_TMPL" | sed 's/"sig":"SIG"/"sig":42/')" | mkf "$SLUG"
expect_store_refusal "R1 numeric sig" report acme/app "$SLUG"

# --- R2: numeric body ---
SLUG="local-rr2"
printf '[%s]' "$(printf '%s' "$ENTRY_TMPL" | sed 's/"sig":"SIG"/"sig":"'"$VALID_SIG"'"/; s/"body":"Usable body here."/"body":42/')" | mkf "$SLUG"
expect_store_refusal "R2 numeric body" report acme/app "$SLUG"

# --- R3: non-object entry ---
SLUG="local-rr3"
printf '[42]' | mkf "$SLUG"
expect_store_refusal "R3 non-object entry" report acme/app "$SLUG"

# --- R4: null sig beside a valid entry — show still resolves ---
SLUG="local-rr4"
{
  printf '['
  printf '%s' "$ENTRY_TMPL" | sed 's/"sig":"SIG"/"sig":null/'
  printf ','
  printf '%s' "$ENTRY_TMPL" | sed 's/"sig":"SIG"/"sig":"'"$VALID_SIG"'"/'
  printf ']'
} | mkf "$SLUG"
run_review show acme/app "$SLUG" --sig "${VALID_SIG:0:8}" --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.item.sig // empty' "$EVAL_OUT" 2>/dev/null)" = "$VALID_SIG" ]; then
  good "R4 null-sig entry no longer kills show (valid prefix resolves)"
else
  bad "R4 show exits $EVAL_STATUS (want 0 with the valid sig)"
fi

# --- R5: show on a non-array store ---
SLUG="local-rr5"
printf '{"not":"a list"}' | mkf "$SLUG"
run_review show acme/app "$SLUG" --sig aaa --json
got="$(jq -r '.error.code // empty' "$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$got" = "corrupt-store" ]; then
  good "R5 non-array store refuses corrupt-store on show"
else
  bad "R5 show exits $EVAL_STATUS with error '$got' (want exit 1, corrupt-store)"
fi

# --- R6: calibrate --probe on a misshapen entry ---
SLUG="local-rr6"
printf '[%s]' "$(printf '%s' "$ENTRY_TMPL" | sed 's/"sig":"SIG"/"sig":"'"$VALID_SIG"'"/; s/"severity":"informational"/"severity":[1]/')" | mkf "$SLUG"
expect_store_refusal "R6 misshapen severity" calibrate acme/app "$SLUG" --probe

# R1-R6 fixtures stay behind as extra skip warnings; clear them so R7
# counts exactly its own two bundles.
rm -rf "$EVAL_SANDBOX/.review/local-rr1" "$EVAL_SANDBOX/.review/local-rr2" \
  "$EVAL_SANDBOX/.review/local-rr3" "$EVAL_SANDBOX/.review/local-rr4" \
  "$EVAL_SANDBOX/.review/local-rr5" "$EVAL_SANDBOX/.review/local-rr6"

# --- R7: trend warns and skips the corrupt bundle ---
SLUG="local-rr7bad"
printf '[%s]' "$(printf '%s' "$ENTRY_TMPL" | sed 's/"sig":"SIG"/"sig":7/')" | mkf "$SLUG"
SLUG="local-rr7good"
printf '[%s]' "$(printf '%s' "$ENTRY_TMPL" | sed 's/"sig":"SIG"/"sig":"'"$VALID_SIG"'"/')" | mkf "$SLUG"
cat > "$EVAL_SANDBOX/.review/$SLUG/kb-recall.json" <<'EOF'
[{"id":"r-ok","score":1,"hits":1,"judged":1}]
EOF
run_review trend acme/app --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "trend skips .review/local-rr7bad" "$EVAL_ERR" \
  && [ "$(jq -r '.data.bundles' "$EVAL_OUT" 2>/dev/null)" = "1" ]; then
  good "R7 trend warns and skips the corrupt bundle (rolls up the readable one)"
else
  bad "R7 trend exits $EVAL_STATUS (want 0 with a skip warning, 1 bundle)"
fi
rm -rf "$EVAL_SANDBOX/.review/local-rr7bad" "$EVAL_SANDBOX/.review/local-rr7good"

# --- R8: unreadable patch.json ---
SLUG="local-rr8"
printf '[%s]' "$(printf '%s' "$ENTRY_TMPL" | sed 's/"sig":"SIG"/"sig":"'"$VALID_SIG"'"/')" | mkf "$SLUG"
printf '{broken' > "$EVAL_SANDBOX/.review/$SLUG/patch.json"
expect_store_refusal "R8 unreadable patch.json" report acme/app "$SLUG"

# --- R9: boolean kb survives trend ---
SLUG="local-rr9"
printf '[%s]' "$(printf '%s' "$ENTRY_TMPL" | sed 's/"sig":"SIG"/"sig":"'"$VALID_SIG"'"/' | sed 's/"history":\[\]/"history":[],"kb":true/')" | mkf "$SLUG"
run_review trend acme/app --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "R9 boolean-kb entry no longer crashes trend"
else
  bad "R9 trend exits $EVAL_STATUS (want 0)"
fi
rm -rf "$EVAL_SANDBOX/.review/$SLUG"

# --- R10: unreadable post.json refuses before any gh call ---
SLUG="7701"
rm -rf "$EVAL_SANDBOX/.review/$SLUG"; mkdir -p "$EVAL_SANDBOX/.review/$SLUG"
printf '[%s]' "$(printf '%s' "$ENTRY_TMPL" | sed 's/"sig":"SIG"/"sig":"'"$VALID_SIG"'"/')" \
  > "$EVAL_SANDBOX/.review/$SLUG/findings.json"
printf '{broken' > "$EVAL_SANDBOX/.review/$SLUG/post.json"
stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
run_review calibrate acme/app "$SLUG" --json
stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
got="$(jq -r '.error.code // empty' "$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$got" = "missing-post" ] && [ "$stub_before" = "$stub_after" ]; then
  good "R10 unreadable post.json refuses missing-post with zero gh calls"
else
  bad "R10 calibrate exits $EVAL_STATUS with error '$got' (want exit 1, missing-post, no gh)"
fi
rm -rf "$EVAL_SANDBOX/.review/$SLUG"

# --- R11: string kb grades as uncited ---
SLUG="local-rr11"
printf '[%s]' "$(printf '%s' "$ENTRY_TMPL" | sed 's/"sig":"SIG"/"sig":"'"$VALID_SIG"'"/; s/"status":"open"/"status":"addressed"/' | sed 's/"history":\[\]/"history":[],"kb":"r-stray"/')" | mkf "$SLUG"
run_review calibrate acme/app "$SLUG" --probe --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.data.grades[0].rule == null' "$EVAL_OUT" >/dev/null 2>&1; then
  good "R11 string-kb entry grades with a null rule (uncited, no crash)"
else
  bad "R11 probe exits $EVAL_STATUS (want 0 with a null rule)"
fi
rm -rf "$EVAL_SANDBOX/.review/$SLUG"

if [ "$fails" -ne 0 ]; then echo "case read-refusals: FAIL ($fails)"; exit 1; fi
echo "case read-refusals: PASS"
