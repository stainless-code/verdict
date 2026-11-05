#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

SLUG="local-refuse"
LB="$EVAL_SANDBOX/.review/$SLUG"

mk_local() { # stdin review.json becomes the bundle input
  rm -rf "$LB"
  mkdir -p "$LB"
  cat > "$LB/source.json" <<'EOF'
{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}
EOF
  cat > "$LB/full.diff" <<'EOF'
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
  cat > "$LB/review.json"
}

kb_snapshot() {
  KB_BEFORE=""
  for f in "$EVAL_SANDBOX/.review/kb.jsonl" "$EVAL_SANDBOX/.review/kb-joins.jsonl"; do
    if [ -e "$f" ]; then
      KB_BEFORE="$KB_BEFORE$f:$(shasum -a 256 "$f" | cut -d' ' -f1)
"
    else
      KB_BEFORE="$KB_BEFORE$f:absent
"
    fi
  done
}

kb_unchanged() { # $1 = label
  local label="$1" f want have ok="true"
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    f="${line%%:*}"; want="${line##*:}"
    if [ -e "$f" ]; then
      have="$(shasum -a 256 "$f" | cut -d' ' -f1)"
    else
      have="absent"
    fi
    [ "$have" = "$want" ] || ok="false"
  done <<<"$KB_BEFORE"
  if [ "$ok" = "true" ]; then
    good "$label left the kb stores alone"
  else
    bad "$label touched the kb stores on refusal"
  fi
}

expect_refusal() { # $1 = label, $2 = code
  local label="$1" code="$2" got=""
  kb_snapshot
  run_review record acme/app "$SLUG" --json
  got="$(jq -r '.error.code // empty' "$EVAL_OUT" 2>/dev/null || true)"
  if [ "$EVAL_STATUS" -eq 1 ] && [ "$got" = "$code" ]; then
    good "$label refuses $code"
  else
    bad "$label exits $EVAL_STATUS with error '$got' (want exit 1, $code): $(head -c 300 "$EVAL_ERR")"
  fi
  expect_no_artifacts "$label"
}

expect_no_artifacts() { # $1 = label
  local label="$1"
  if [ -e "$LB/findings.json" ] || [ -e "$LB/record.json" ]; then
    bad "$label wrote findings/record artifacts on refusal"
  else
    good "$label wrote nothing (no findings.json/record.json)"
  fi
  kb_unchanged "$label"
}

expect_refusal_keep() { # $1 = label, $2 = code, $3 = sha-before
  local label="$1" code="$2" before="$3" got="" after=""
  kb_snapshot
  run_review record acme/app "$SLUG" --json
  got="$(jq -r '.error.code // empty' "$EVAL_OUT" 2>/dev/null || true)"
  if [ "$EVAL_STATUS" -eq 1 ] && [ "$got" = "$code" ]; then
    good "$label refuses $code"
  else
    bad "$label exits $EVAL_STATUS with error '$got' (want exit 1, $code): $(head -c 300 "$EVAL_ERR")"
  fi
  after="$(shasum -a 256 "$LB/findings.json" | cut -d' ' -f1)"
  if [ "$before" = "$after" ] && [ ! -e "$LB/record.json" ]; then
    good "$label left the seeded findings.json byte-identical (no record.json)"
  else
    bad "$label rewrote artifacts on refusal"
  fi
  kb_unchanged "$label"
}

mk_local <<'EOF'
{"broken":
EOF
expect_refusal "R1 corrupt review.json" "bad-review-input"

mk_local <<'EOF'
[1,2]
EOF
expect_refusal "R2 non-object review.json" "bad-review-input"

mk_local <<'EOF'
{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[5]}
EOF
expect_refusal "R3 non-object comments[] entry" "bad-review-input"

mk_local <<'EOF'
{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[{"path":5,"line":2,"side":"RIGHT","body":"x body with enough substance."}]}
EOF
expect_refusal "R4 numeric path" "bad-review-input"
mk_local <<'EOF'
{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":5}]}
EOF
expect_refusal "R4 numeric body" "bad-review-input"
mk_local <<'EOF'
{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"x body with enough substance.","suggestion":5}]}
EOF
expect_refusal "R4 numeric suggestion" "bad-review-input"

mk_local <<'EOF'
{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[],"resolved":[5]}
EOF
expect_refusal "R5 numeric resolved[] entry" "bad-review-input"

mk_local <<'EOF'
{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[],"triage":[5]}
EOF
expect_refusal "R6 non-object triage[] entry" "bad-review-input"

mk_local <<'EOF'
{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[]}
EOF
echo '{"sig":"x"}' > "$LB/verification.json"
expect_refusal "R7 non-array verification.json" "bad-review-input"
mk_local <<'EOF'
{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[]}
EOF
echo '[5]' > "$LB/verification.json"
expect_refusal "R7 non-object verification.json claim" "bad-review-input"
mk_local <<'EOF'
{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[]}
EOF
echo '{oops' > "$LB/verification.json"
run_review record acme/app "$SLUG" --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "R7 unparsable verification.json reads as no claims"
else
  bad "R7 unparsable verification.json exits $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi

mk_local <<'EOF'
{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[]}
EOF
printf '%s' '{broken' > "$LB/findings.json"
before="$(shasum -a 256 "$LB/findings.json" | cut -d' ' -f1)"
expect_refusal_keep "R8 corrupt prior findings.json" "corrupt-store" "$before"
mk_local <<'EOF'
{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[]}
EOF
printf '%s' '{"sig":"x"}' > "$LB/findings.json"
before="$(shasum -a 256 "$LB/findings.json" | cut -d' ' -f1)"
expect_refusal_keep "R8 non-list prior findings.json" "corrupt-store" "$before"

mk_local <<'EOF'
{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[]}
EOF
echo '{oops' > "$LB/source.json"
run_review record acme/app "$SLUG" --json
if [ "$EVAL_STATUS" -eq 1 ] && [ ! -e "$LB/record.json" ] \
  && [ "$(jq -r '.error.code // empty' "$EVAL_OUT" 2>/dev/null)" = "controls-unavailable" ]; then
  good "R9 corrupt source.json refuses controls-unavailable"
else
  bad "R9 corrupt source.json exits $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi

PR=4299
eval_mk_bundle "$PR"
printf '%s' '{broken' | eval_write_review "$PR"
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 1 ] && [ "$(jq -r '.error.code // empty' "$EVAL_OUT" 2>/dev/null)" = "bad-review-input" ]; then
  good "R10 post --dry-run refuses corrupt review.json typed"
else
  bad "R10 post --dry-run exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
rm -rf "$EVAL_SANDBOX/.review/$PR"

mk_local <<'EOF'
{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"x body with enough substance.","rigor":"pointed"}]}
EOF
run_review record acme/app "$SLUG" --json
if [ "$EVAL_STATUS" -eq 0 ] && [ -f "$LB/findings.json" ] && [ -f "$LB/record.json" ]; then
  good "R11 valid review records (exit 0, artifacts written)"
else
  bad "R11 valid review exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi

rm -rf "$LB"

eval_end
