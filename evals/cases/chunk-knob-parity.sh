#!/usr/bin/env bash
# Case chunk-knob-parity: explicit --chunk flag + .review/chunk knob file with
# flag-wins -> file -> off precedence, cap-override-wins, and absent-knob
# byte-identical refusal. No slicing behavior yet — this grades the settled
# opt-in predicate plus the parity lock (later slices replace the opted-in
# refusal with real chunking).
#
# Grades (all hermetic — sandbox git repo, stub gh, no network):
#   K1  absent-knob parity vs pre-change HEAD: the over-cap refusal without
#       the knob is byte-identical (exit, stdout, stderr) in text mode, and
#       identical modulo the `at` timestamp in --json mode.
#   K2  precedence: --chunk alone opts in; .review/chunk true alone opts in;
#       flag wins over a false file; a false/empty file alone reads off
#       (stderr identical to the absent baseline). Opted in still refuses as
#       today (same exit, code, candidates, hint) plus one stderr notice
#       naming the winning source.
#   K3  cap-override-wins: an overridden cap (env or file) disables chunking
#       outright — a fitting diff succeeds with no chunk notice, and a
#       still-over diff refuses with no chunk notice.
#   K4  under-cap unaffected: small fetches succeed with identical bytes
#       (stdout + full.diff) whether the knob is absent, flagged, or filed.
#   K5  bad knob refuses typed (bad-chunk-knob) on the over-cap path only;
#       under-cap fetches never consult the knob.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

# Deterministic surface: no settings file, no ambient REVIEW_* knobs.
mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_BUDGET_USD REVIEW_MODEL || true

# Scratch git repo at the sandbox root (run_review runs with cwd=sandbox, so
# fetch-local worktree mode sees this repo). Explicit adds only — earlier
# cases' .review fixtures stay untracked and out of every diff.
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  git config user.email "chunk-eval@example.com"
  git config user.name "chunk eval"
  if [ ! -f .gitignore ]; then
    cat > .gitignore <<'EOF'
.review/
stubbin/
*.log
eval-out.bin
eval-err.txt
case-results.txt
EOF
  fi
  awk 'BEGIN{for(i=0;i<16000;i++) printf "base line %06d padding padding padding padding\n", i}' > chunkbig.txt
  printf 'alpha\nbeta\ngamma\n' > chunksmall.txt
  git add chunkbig.txt chunksmall.txt .gitignore
  git commit -qm base 2>/dev/null || true
)
# Over-cap worktree state: rewrite the big file end-to-end (diff ~1.4 MB,
# well above the 512000 default cap) while the small file stays committed.
(
  cd "$EVAL_SANDBOX" || exit 99
  awk 'BEGIN{for(i=0;i<16000;i++) printf "changed line %06d completely new words words words\n", i}' > chunkbig.txt
)

KNOB="$EVAL_SANDBOX/.review/chunk"

# --- K1: absent-knob baseline + byte-identical parity vs HEAD ---
run_review fetch-local acme/app chunkbig
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "exceeds cap 512000 bytes" "$EVAL_ERR" \
  && grep -q "try: split the range" "$EVAL_ERR" \
  && grep -q "candidates (suggested scopes" "$EVAL_ERR" \
  && ! grep -q "chunking" "$EVAL_ERR"; then
  good "absent knob: over-cap fetch-local refuses diff-too-large with candidates + hint, no chunk mention"
else
  bad "absent baseline exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
cp "$EVAL_OUT" "$EVAL_SANDBOX/k0-out.txt"
cp "$EVAL_ERR" "$EVAL_SANDBOX/k0-err.txt"
K0_STATUS="$EVAL_STATUS"
run_review fetch-local acme/app chunkbig --json
cp "$EVAL_OUT" "$EVAL_SANDBOX/k0-json.txt"
K0_JSON_STATUS="$EVAL_STATUS"

mkdir -p "$EVAL_SANDBOX/oldrun/scripts"
git -C "$EVAL_ROOT" show HEAD:scripts/review.sh > "$EVAL_SANDBOX/oldrun/scripts/review.sh"
chmod +x "$EVAL_SANDBOX/oldrun/scripts/review.sh"
cp "$EVAL_ROOT/package.json" "$EVAL_SANDBOX/oldrun/package.json"
(
  cd "$EVAL_SANDBOX" || exit 99
  set +e
  PATH="$EVAL_STUBBIN:$PATH" bash "$EVAL_SANDBOX/oldrun/scripts/review.sh" \
    fetch-local acme/app chunkbig >"$EVAL_SANDBOX/old-out.txt" 2>"$EVAL_SANDBOX/old-err.txt"
  echo "$?" > "$EVAL_SANDBOX/old-status.txt"
)
if [ "$(cat "$EVAL_SANDBOX/old-status.txt")" = "$K0_STATUS" ] \
  && cmp -s "$EVAL_SANDBOX/old-out.txt" "$EVAL_SANDBOX/k0-out.txt" \
  && cmp -s "$EVAL_SANDBOX/old-err.txt" "$EVAL_SANDBOX/k0-err.txt"; then
  good "absent knob: text refusal byte-identical to pre-change HEAD (exit, stdout, stderr)"
else
  bad "absent-knob text parity broke (exit $(cat "$EVAL_SANDBOX/old-status.txt") vs $K0_STATUS)"
fi
(
  cd "$EVAL_SANDBOX" || exit 99
  set +e
  PATH="$EVAL_STUBBIN:$PATH" bash "$EVAL_SANDBOX/oldrun/scripts/review.sh" \
    fetch-local acme/app chunkbig --json >"$EVAL_SANDBOX/old-json.txt" 2>/dev/null
  true # last-word status stays 0: the refusal exit above must not trip set -e
)
if [ "$K0_JSON_STATUS" -eq 1 ] \
  && cmp -s <(jq 'del(.at)' "$EVAL_SANDBOX/old-json.txt") <(jq 'del(.at)' "$EVAL_SANDBOX/k0-json.txt"); then
  good "absent knob: --json refusal identical to HEAD modulo the at timestamp"
else
  bad "absent-knob --json parity broke (exit $K0_JSON_STATUS)"
fi
if [ "$(jq -r '.error.code' <"$EVAL_SANDBOX/k0-json.txt")" = "diff-too-large" ] \
  && jq -e '.error.candidates | length > 0' <"$EVAL_SANDBOX/k0-json.txt" >/dev/null 2>&1 \
  && jq -e '.error.message | contains("try: split the range")' <"$EVAL_SANDBOX/k0-json.txt" >/dev/null 2>&1; then
  good "--json refusal carries code + candidates + hint"
else
  bad "--json refusal shape: $(jq -c '.error | {code}' <"$EVAL_SANDBOX/k0-json.txt" 2>/dev/null)"
fi

# --- K2: precedence (flag > file > off); opted-in refuses as today + notice ---
run_review fetch-local acme/app chunkbig --chunk
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "chunking opted in (from --chunk)" "$EVAL_ERR" \
  && grep -q "exceeds cap 512000 bytes" "$EVAL_ERR" \
  && grep -q "try: split the range" "$EVAL_ERR"; then
  good "--chunk alone opts in: same refusal plus a --chunk notice"
else
  bad "--chunk exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
cp "$EVAL_ERR" "$EVAL_SANDBOX/k2-flag-err.txt"
run_review fetch-local acme/app chunkbig --chunk --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT")" = "diff-too-large" ] \
  && cmp -s <(jq -c '.error.candidates' <"$EVAL_OUT") <(jq -c '.error.candidates' <"$EVAL_SANDBOX/k0-json.txt") \
  && grep -q "chunking opted in (from --chunk)" "$EVAL_ERR"; then
  good "--chunk keeps code + candidates, notice on stderr only"
else
  bad "--chunk --json exits $EVAL_STATUS"
fi

printf 'true\n' > "$KNOB"
run_review fetch-local acme/app chunkbig
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "chunking opted in (from .review/chunk)" "$EVAL_ERR" \
  && grep -q "exceeds cap 512000 bytes" "$EVAL_ERR"; then
  good "knob file true alone opts in (notice names .review/chunk)"
else
  bad "knob-true exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi

printf 'false\n' > "$KNOB"
run_review fetch-local acme/app chunkbig --chunk
if [ "$EVAL_STATUS" -eq 1 ] && grep -q "chunking opted in (from --chunk)" "$EVAL_ERR"; then
  good "flag wins over a false knob file"
else
  bad "flag-vs-false-file exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
run_review fetch-local acme/app chunkbig
if [ "$EVAL_STATUS" -eq 1 ] && cmp -s "$EVAL_ERR" "$EVAL_SANDBOX/k0-err.txt" \
  && cmp -s "$EVAL_OUT" "$EVAL_SANDBOX/k0-out.txt"; then
  good "false knob file alone reads off (bytes identical to absent)"
else
  bad "false-file parity exits $EVAL_STATUS"
fi
: > "$KNOB"
run_review fetch-local acme/app chunkbig
if [ "$EVAL_STATUS" -eq 1 ] && cmp -s "$EVAL_ERR" "$EVAL_SANDBOX/k0-err.txt"; then
  good "empty knob file reads off"
else
  bad "empty-file parity exits $EVAL_STATUS"
fi
rm -f "$KNOB"

# --- K3: cap-override-wins (env and file forms) ---
REVIEW_MAX_BYTES=2000000 run_review fetch-local acme/app chunkbig --chunk
if [ "$EVAL_STATUS" -eq 0 ] \
  && ! grep -q "chunking" "$EVAL_ERR" \
  && [ -f "$EVAL_SANDBOX/.review/local-chunkbig/full.diff" ]; then
  good "raised cap + --chunk succeeds with no chunk notice (override wins)"
else
  bad "raised-cap exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
REVIEW_MAX_BYTES=100 run_review fetch-local acme/app chunkbig --chunk
if [ "$EVAL_STATUS" -eq 1 ] \
  && ! grep -q "chunking" "$EVAL_ERR" \
  && grep -q "exceeds cap 100 bytes" "$EVAL_ERR" \
  && grep -q "from REVIEW_MAX_BYTES" "$EVAL_OUT"; then
  good "still-over tiny cap + --chunk refuses with no chunk notice (override wins outright)"
else
  bad "tiny-cap exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
printf '2500000\n' > "$EVAL_SANDBOX/.review/max_bytes"
printf 'true\n' > "$KNOB"
run_review fetch-local acme/app chunkbig
if [ "$EVAL_STATUS" -eq 0 ] \
  && ! grep -q "chunking" "$EVAL_ERR" \
  && grep -q "from .review/max_bytes" "$EVAL_OUT"; then
  good "cap file + true knob file succeeds with no chunk notice (override wins)"
else
  bad "cap-file exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
rm -f "$EVAL_SANDBOX/.review/max_bytes" "$KNOB"

# --- K4: under-cap unaffected (flag vs file vs absent, same bytes) ---
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- chunkbig.txt
  printf 'alpha\nBETA\nGamma\ndelta\n' > chunksmall.txt
)
run_review fetch-local acme/app chunksmall --chunk
if [ "$EVAL_STATUS" -ne 0 ]; then
  bad "under-cap --chunk exits $EVAL_STATUS"
else
  good "under-cap --chunk succeeds"
fi
cp "$EVAL_OUT" "$EVAL_SANDBOX/k4-flag-out.txt"
cp "$EVAL_SANDBOX/.review/local-chunksmall/full.diff" "$EVAL_SANDBOX/k4-flag.diff"
run_review fetch-local acme/app chunksmall
if [ "$EVAL_STATUS" -eq 0 ] \
  && cmp -s "$EVAL_OUT" "$EVAL_SANDBOX/k4-flag-out.txt" \
  && cmp -s "$EVAL_SANDBOX/.review/local-chunksmall/full.diff" "$EVAL_SANDBOX/k4-flag.diff"; then
  good "under-cap absent reads the same bytes as flagged (stdout + full.diff)"
else
  bad "under-cap absent exits $EVAL_STATUS"
fi
printf 'true\n' > "$KNOB"
run_review fetch-local acme/app chunksmall
if [ "$EVAL_STATUS" -eq 0 ] \
  && cmp -s "$EVAL_OUT" "$EVAL_SANDBOX/k4-flag-out.txt" \
  && cmp -s "$EVAL_SANDBOX/.review/local-chunksmall/full.diff" "$EVAL_SANDBOX/k4-flag.diff" \
  && ! grep -qi "chunk" "$EVAL_ERR"; then
  good "under-cap filed knob succeeds with identical bytes, no chunk mention"
else
  bad "under-cap filed exits $EVAL_STATUS"
fi

# --- K5: bad knob refuses typed on the over-cap path only ---
printf 'maybe\n' > "$KNOB"
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- chunksmall.txt
  awk 'BEGIN{for(i=0;i<16000;i++) printf "changed line %06d completely new words words words\n", i}' > chunkbig.txt
)
run_review fetch-local acme/app chunkbig --json
if [ "$EVAL_STATUS" -eq 1 ] && [ "$(jq -r '.error.code' <"$EVAL_OUT")" = "bad-chunk-knob" ]; then
  good "bad knob refuses typed on the over-cap path"
else
  bad "bad knob exits $EVAL_STATUS"
fi
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- chunkbig.txt
  printf 'alpha\nBETA\nGamma\ndelta\n' > chunksmall.txt
)
run_review fetch-local acme/app chunksmall
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "under-cap never consults the knob (bad file, still succeeds)"
else
  bad "under-cap with bad knob exits $EVAL_STATUS"
fi

# --- cleanup: restore the shared sandbox (fixtures stay, probe state goes) ---
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- chunkbig.txt chunksmall.txt 2>/dev/null || true
)
rm -f "$KNOB" "$EVAL_SANDBOX/.review/max_bytes"
rm -rf "$EVAL_SANDBOX/oldrun" "$EVAL_SANDBOX/xdg-empty"
rm -f "$EVAL_SANDBOX"/k0-*.txt "$EVAL_SANDBOX"/k2-*.txt "$EVAL_SANDBOX"/k4-* "$EVAL_SANDBOX"/old-*.txt "$EVAL_SANDBOX"/old-status.txt
unset XDG_CONFIG_HOME || true

if [ "$fails" -ne 0 ]; then echo "case chunk-knob-parity: FAIL ($fails)"; exit 1; fi
echo "case chunk-knob-parity: PASS"
