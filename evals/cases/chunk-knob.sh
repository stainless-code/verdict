#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_BUDGET_USD REVIEW_MODEL || true

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  git config user.email "chunk-eval@example.com"
  git config user.name "chunk eval"
  if [ ! -f .gitignore ]; then
    cat > .gitignore <<'EOF'
.verdict/state/
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
(
  cd "$EVAL_SANDBOX" || exit 99
  awk 'BEGIN{for(i=0;i<16000;i++) printf "changed line %06d completely new words words words\n", i}' > chunkbig.txt
)

KNOB="$EVAL_SANDBOX/.verdict/config.json"
# knob_config writes the typed repository chunk default (the whole
# config the trusted-base read sees), so every case states one file.
knob_config() { printf '{"schema_version":"1","defaults":{"chunk":%s}}\n' "$1" >"$KNOB"; }

run_review fetch-local acme/app chunkbig
assert_exit "absent-knob baseline refuses" 1
assert_file_grep "absent knob names the cap" "$EVAL_ERR" "exceeds its cap 512000 bytes"
assert_file_grep "absent knob carries the split hint" "$EVAL_ERR" "try: split the range"
assert_file_grep "absent knob lists candidate scopes" "$EVAL_ERR" "candidates (suggested scopes"
assert_stream_nogrep "absent knob mentions no chunking" "$EVAL_ERR" "chunking"
cp "$EVAL_OUT" "$EVAL_SANDBOX/k0-out.txt"
cp "$EVAL_ERR" "$EVAL_SANDBOX/k0-err.txt"
run_review fetch-local acme/app chunkbig --json
cp "$EVAL_OUT" "$EVAL_SANDBOX/k0-json.txt"
K0_JSON_STATUS="$EVAL_STATUS"

if [ "$K0_JSON_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_SANDBOX/k0-json.txt")" = "diff-too-large" ] \
  && jq -e '.error.candidates | length > 0' <"$EVAL_SANDBOX/k0-json.txt" >/dev/null 2>&1 \
  && jq -e '.error.message | contains("try: split the range")' <"$EVAL_SANDBOX/k0-json.txt" >/dev/null 2>&1; then
  good "--json refusal carries code + candidates + hint"
else
  bad "--json refusal shape: $(jq -c '.error | {code}' <"$EVAL_SANDBOX/k0-json.txt" 2>/dev/null)"
fi

run_review fetch-local acme/app chunkbig --chunk
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "chunking opted in for group" "$EVAL_ERR" \
  && grep -q "single file over cap" "$EVAL_ERR" \
  && grep -q "exceeds its cap 512000 bytes" "$EVAL_ERR" \
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
  && grep -q "chunking opted in for group" "$EVAL_ERR"; then
  good "--chunk keeps code + candidates, notice on stderr only"
else
  bad "--chunk --json exits $EVAL_STATUS"
fi

knob_config true
eval_commit_controls
run_review fetch-local acme/app chunkbig
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "chunking opted in for group" "$EVAL_ERR" \
  && grep -q "single file over cap" "$EVAL_ERR" \
  && grep -q "exceeds its cap 512000 bytes" "$EVAL_ERR"; then
  good "typed chunk default alone opts in (notice names .verdict/config.json)"
else
  bad "chunk-default exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi

knob_config false
eval_commit_controls
run_review fetch-local acme/app chunkbig --chunk
if [ "$EVAL_STATUS" -eq 1 ] && grep -q "chunking opted in for group" "$EVAL_ERR"; then
  good "flag wins over a false typed default"
else
  bad "flag-vs-false-default exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
run_review fetch-local acme/app chunkbig
if [ "$EVAL_STATUS" -eq 1 ] && cmp -s "$EVAL_ERR" "$EVAL_SANDBOX/k0-err.txt" \
  && cmp -s "$EVAL_OUT" "$EVAL_SANDBOX/k0-out.txt"; then
  good "false typed default alone reads off (bytes identical to absent)"
else
  bad "false-file parity exits $EVAL_STATUS"
fi
printf '{"schema_version":"1"}\n' >"$KNOB"
eval_commit_controls
run_review fetch-local acme/app chunkbig
if [ "$EVAL_STATUS" -eq 1 ] && cmp -s "$EVAL_ERR" "$EVAL_SANDBOX/k0-err.txt"; then
  good "a config without a chunk key reads off"
else
  bad "absent-key parity exits $EVAL_STATUS"
fi
rm -f "$KNOB"
eval_commit_controls

REVIEW_MAX_BYTES=2000000 run_review fetch-local acme/app chunkbig --chunk
assert_exit "raised cap + --chunk succeeds" 0
assert_stream_nogrep "raised cap prints no chunk notice" "$EVAL_ERR" "chunking"
assert_file_grep "raised cap writes full.diff" "$EVAL_SANDBOX/.verdict/state/local-chunkbig/full.diff" "."
REVIEW_MAX_BYTES=100 run_review fetch-local acme/app chunkbig --chunk
assert_exit "tiny cap + --chunk refuses" 1
assert_stream_nogrep "tiny cap prints no chunk notice" "$EVAL_ERR" "chunking"
assert_file_grep "tiny cap names the overriding cap" "$EVAL_ERR" "exceeds its cap 100 bytes"
assert_file_grep "tiny cap names the override source" "$EVAL_OUT" "from REVIEW_MAX_BYTES"
printf '{"schema_version":"1","defaults":{"max_bytes":2500000,"chunk":true}}\n' >"$KNOB"
eval_commit_controls
run_review fetch-local acme/app chunkbig
assert_exit "typed cap + chunk default succeeds" 0
assert_stream_nogrep "typed cap prints no chunk notice" "$EVAL_ERR" "chunking"
assert_file_grep "typed cap names the override source" "$EVAL_OUT" "from .verdict/config.json"
rm -f "$KNOB"
eval_commit_controls

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
cp "$EVAL_SANDBOX/.verdict/state/local-chunksmall/full.diff" "$EVAL_SANDBOX/k4-flag.diff"
run_review fetch-local acme/app chunksmall
if [ "$EVAL_STATUS" -eq 0 ] \
  && cmp -s "$EVAL_OUT" "$EVAL_SANDBOX/k4-flag-out.txt" \
  && cmp -s "$EVAL_SANDBOX/.verdict/state/local-chunksmall/full.diff" "$EVAL_SANDBOX/k4-flag.diff"; then
  good "under-cap absent reads the same bytes as flagged (stdout + full.diff)"
else
  bad "under-cap absent exits $EVAL_STATUS"
fi
knob_config true
eval_commit_controls
run_review fetch-local acme/app chunksmall
assert_exit "under-cap chunk default succeeds" 0
assert_file_eq "under-cap chunk default keeps identical stdout bytes" "$EVAL_SANDBOX/k4-flag-out.txt" "$EVAL_OUT"
assert_file_eq "under-cap chunk default keeps identical full.diff" "$EVAL_SANDBOX/k4-flag.diff" "$EVAL_SANDBOX/.verdict/state/local-chunksmall/full.diff"
assert_stream_nogrep "under-cap chunk default mentions no chunk" "$EVAL_ERR" "chunk" "-i"

printf '{"schema_version":"1","defaults":{"chunk":"maybe"}}\n' >"$KNOB"
eval_commit_controls
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- chunksmall.txt
  awk 'BEGIN{for(i=0;i<16000;i++) printf "changed line %06d completely new words words words\n", i}' > chunkbig.txt
)
run_review fetch-local acme/app chunkbig --json
if [ "$EVAL_STATUS" -eq 1 ] && [ "$(jq -r '.error.code' <"$EVAL_OUT")" = "bad-config" ]; then
  good "a non-boolean chunk default refuses bad-config"
else
  bad "bad chunk default exits $EVAL_STATUS: $(head -c 300 "$EVAL_OUT")"
fi
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- chunkbig.txt
  printf 'alpha\nBETA\nGamma\ndelta\n' > chunksmall.txt
)
run_review fetch-local acme/app chunksmall --json
if [ "$EVAL_STATUS" -eq 1 ] && [ "$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)" = "bad-config" ]; then
  good "under-cap still validates the typed config and refuses bad-config"
else
  bad "under-cap with bad config exits $EVAL_STATUS"
fi

(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- chunkbig.txt chunksmall.txt 2>/dev/null || true
)
rm -f "$KNOB"
eval_commit_controls
rm -rf "$EVAL_SANDBOX/xdg-empty"
rm -f "$EVAL_SANDBOX"/k0-*.txt "$EVAL_SANDBOX"/k2-*.txt "$EVAL_SANDBOX"/k4-*
unset XDG_CONFIG_HOME || true

eval_end
