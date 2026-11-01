#!/usr/bin/env bash
# Case chunk-envelope: machine-envelope chunk summary (additive data.chunk)
# + prompt-dump concatenation of chunk diffs in manifest order.
#
# Grades (all hermetic — sandbox git repo, stub gh unless a canned-gh leg
# overrides it, no network):
#   E1  additive envelope (fetch-local): --chunk --json on the multi-file
#       over-cap fixture succeeds; the top-level key set is exactly the
#       documented envelope; data.chunk carries exactly count/manifest/
#       total_bytes with values matching the manifest and full.diff; error
#       null; existing data fields keep parsing (parser-compat probe).
#   E2  dump order (fetch-local): --chunk --show-prompts stdout equals the
#       assembled files with chunk diffs concatenated in manifest order; the
#       diff section is byte-equal to full.diff; the reverse-order control
#       differs (the order grade is non-vacuous).
#   E3  --json coexistence: --chunk --show-prompts --json carries both
#       data.chunk and data.prompt_dump, the dump equal to the E2 bytes.
#   E4  non-chunked parity: under-cap --show-prompts stdout equals the
#       full.diff concatenation byte-identically; plain --json carries no
#       data.chunk key and no prompt_dump key; the absent-knob over-cap
#       --json refusal stays {data null, diff-too-large} with no chunk key.
#   E5  PR fetch path: canned-gh fetch --chunk --json keeps the envelope key
#       set with additive data.chunk, and --show-prompts concatenates chunk
#       diffs in manifest order.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

WANT_KEYS="at data error next pr repo schema_version verb warnings"

# Deterministic surface: no settings file, no ambient REVIEW_* knobs.
mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_BUDGET_USD REVIEW_MODEL || true

# Scratch git repo at the sandbox root (run_review runs with cwd=sandbox).
# Explicit adds only — earlier cases' fixtures stay untracked and out of
# every diff; every fetch below names its paths explicitly.
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  git config user.email "chunk-envelope@example.com"
  git config user.name "chunk envelope"
  if [ ! -f .gitignore ]; then
    cat > .gitignore <<'EOF'
.review/
stubbin/
fakebin/
*.log
eval-out.bin
eval-err.txt
case-results.txt
EOF
  fi
  for i in 1 2 3 4 5 6; do
    awk -v f="$i" 'BEGIN{for(j=0;j<1500;j++) printf "base line %06d file%d padding padding padding padding\n", j, f}' > "cenv-f$i.txt"
  done
  printf 'tiny alpha\n' > cenv-small.txt
  git add cenv-f1.txt cenv-f2.txt cenv-f3.txt cenv-f4.txt cenv-f5.txt cenv-f6.txt cenv-small.txt .gitignore
  git commit -qm cenv-base 2>/dev/null || true
)
(
  cd "$EVAL_SANDBOX" || exit 99
  for i in 1 2 3 4 5 6; do
    awk -v f="$i" 'BEGIN{for(j=0;j<1500;j++) printf "changed line %06d file%d completely new words words words\n", j, f}' > "cenv-f$i.txt"
  done
)
CENV_FILES="cenv-f1.txt cenv-f2.txt cenv-f3.txt cenv-f4.txt cenv-f5.txt cenv-f6.txt"
# shellcheck disable=SC2086 # CENV_FILES is a validated space-separated path list
set -- $CENV_FILES
BUNDLE="$EVAL_SANDBOX/.review/local-cenvfix"
FULL_DIFF_BYTES="$(cd "$EVAL_SANDBOX" && git diff HEAD -- "$@" | wc -c | tr -d ' ')"
if [ "$FULL_DIFF_BYTES" -le 512000 ]; then
  bad "fixture diff $FULL_DIFF_BYTES bytes is not over the 512000 cap"
fi
MAN="$BUNDLE/chunks/manifest.json"

# dump_expected_chunks <bundle-dir> <out> — the documented dump with the
# diff section built from chunk diffs in manifest order, independently of
# render_prompt_dump (missing/optional files skipped).
dump_expected_chunks() {
  (
    cd "$EVAL_SANDBOX" || exit 99
    local dir="$1" meta="$1/source.json" f
    [ -f "$1/PR.json" ] && meta="$1/PR.json"
    [ -f "$1/source.json" ] && meta="$1/source.json"
    : > "$2"
    for f in SKILL.md prompts/system.md prompts/review.md "$meta" "$dir/toc.txt" "$dir/roles.txt"; do
      [ -f "$f" ] || continue
      cat -- "$f" >> "$2"
    done
    while IFS= read -r cf; do
      [ -n "$cf" ] || continue
      cat -- "$cf" >> "$2"
    done < <(jq -r '.chunks | sort_by(.index) | .[].diff // empty' "$dir/chunks/manifest.json")
    for f in "$dir/kb.txt" "$dir/sensitive.txt" "$dir/panel"; do
      [ -f "$f" ] || continue
      cat -- "$f" >> "$2"
    done
  )
}

# dump_expected_full <bundle-dir> <out> — the pre-chunk concatenation with
# full.diff, the byte-identical contract for non-chunked dumps.
dump_expected_full() {
  (
    cd "$EVAL_SANDBOX" || exit 99
    local dir="$1" meta="$1/source.json" f
    [ -f "$1/PR.json" ] && meta="$1/PR.json"
    [ -f "$1/source.json" ] && meta="$1/source.json"
    : > "$2"
    for f in SKILL.md prompts/system.md prompts/review.md "$meta" "$dir/toc.txt" "$dir/roles.txt" "$dir/full.diff" "$dir/kb.txt" "$dir/sensitive.txt" "$dir/panel"; do
      [ -f "$f" ] || continue
      cat -- "$f" >> "$2"
    done
  )
}

# --- E1: additive envelope on the chunked fetch-local path ---
# shellcheck disable=SC2086 # CENV_FILES is a validated space-separated path list
run_review fetch-local acme/app cenvfix --chunk --json -- $CENV_FILES
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.chunk.count' <"$EVAL_OUT" 2>/dev/null)" -ge 2 ] 2>/dev/null \
  && [ "$(jq -r '.data.chunk.manifest' <"$EVAL_OUT" 2>/dev/null)" = ".review/local-cenvfix/chunks/manifest.json" ] \
  && [ "$(jq -r '.data.chunk.total_bytes' <"$EVAL_OUT" 2>/dev/null)" = "$FULL_DIFF_BYTES" ]; then
  good "--chunk --json carries data.chunk with count, manifest, total bytes"
else
  bad "--chunk --json exits $EVAL_STATUS: $(jq -c '.data.chunk // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi
if [ "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null)" = "$WANT_KEYS" ] \
  && jq -e '.error == null' <"$EVAL_OUT" >/dev/null 2>&1 \
  && [ "$(jq -r '[.data.chunk | keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null)" = "count manifest total_bytes" ]; then
  good "envelope keeps exactly the documented top-level keys; data.chunk carries exactly count/manifest/total_bytes"
else
  bad "envelope shape: top='$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null)' chunk='$(jq -r '[.data.chunk // {} | keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null)'"
fi
if [ "$(jq -r '.data.chunk.count' <"$EVAL_OUT" 2>/dev/null)" = "$(jq -r '.chunk_count' <"$MAN" 2>/dev/null)" ] \
  && [ "$(jq -r '.data.chunk.total_bytes' <"$EVAL_OUT" 2>/dev/null)" = "$(jq -r '.total_bytes' <"$MAN" 2>/dev/null)" ] \
  && [ "$(jq -r '.data.dir' <"$EVAL_OUT" 2>/dev/null)" = ".review/local-cenvfix" ] \
  && [ "$(jq -r '.data.diff_bytes' <"$EVAL_OUT" 2>/dev/null)" = "$FULL_DIFF_BYTES" ]; then
  good "chunk summary matches the manifest; existing data fields parse as today (parser-compat)"
else
  bad "chunk/manifest/data agreement broke"
fi

# --- E2: dump order on the chunked fetch-local path ---
# shellcheck disable=SC2086 # CENV_FILES is a validated space-separated path list
run_review fetch-local acme/app cenvfix --chunk --show-prompts -- $CENV_FILES
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "--chunk --show-prompts exits 0"
else
  bad "--chunk --show-prompts exits $EVAL_STATUS"
fi
dump_expected_chunks ".review/local-cenvfix" "$EVAL_SANDBOX/cenv-expected.txt"
if cmp -s "$EVAL_OUT" "$EVAL_SANDBOX/cenv-expected.txt"; then
  good "chunked dump equals the assembled files with chunk diffs in manifest order"
else
  bad "chunked dump differs from the manifest-order concatenation"
fi
# Diff section of the dump is byte-equal to full.diff (chunks partition it).
dump_expected_full ".review/local-cenvfix" "$EVAL_SANDBOX/cenv-fullconcat.txt"
if cmp -s "$EVAL_SANDBOX/cenv-expected.txt" "$EVAL_SANDBOX/cenv-fullconcat.txt"; then
  good "manifest-order chunk concat is byte-equal to the full.diff concatenation"
else
  bad "chunk concat differs from full.diff bytes"
fi
# Reverse-order control: concat in reverse manifest order must differ, so the
# order grade above is sensitive to ordering (non-vacuous with N >= 2).
(
  cd "$EVAL_SANDBOX" || exit 99
  : > "$EVAL_SANDBOX/cenv-reversed.diff"
  while IFS= read -r cf; do
    [ -n "$cf" ] || continue
    cat -- "$cf" >> "$EVAL_SANDBOX/cenv-reversed.diff"
  done < <(jq -r '.chunks | sort_by(.index) | reverse | .[].diff // empty' ".review/local-cenvfix/chunks/manifest.json")
)
(
  cd "$EVAL_SANDBOX" || exit 99
  : > "$EVAL_SANDBOX/cenv-forward.diff"
  while IFS= read -r cf; do
    [ -n "$cf" ] || continue
    cat -- "$cf" >> "$EVAL_SANDBOX/cenv-forward.diff"
  done < <(jq -r '.chunks | sort_by(.index) | .[].diff // empty' ".review/local-cenvfix/chunks/manifest.json")
)
if ! cmp -s "$EVAL_SANDBOX/cenv-forward.diff" "$EVAL_SANDBOX/cenv-reversed.diff"; then
  good "reverse-order control differs (order grade is non-vacuous)"
else
  bad "reverse-order concat matches forward (fixture has < 2 effective chunks?)"
fi

# --- E3: --json coexistence of both additives ---
# shellcheck disable=SC2086 # CENV_FILES is a validated space-separated path list
run_review fetch-local acme/app cenvfix --chunk --show-prompts --json -- $CENV_FILES
dump_expected_chunks ".review/local-cenvfix" "$EVAL_SANDBOX/cenv-expected.txt"
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.data.chunk.count >= 2' <"$EVAL_OUT" >/dev/null 2>&1 \
  && jq -j '.data.prompt_dump // empty' <"$EVAL_OUT" | cmp -s - "$EVAL_SANDBOX/cenv-expected.txt"; then
  good "--json carries both data.chunk and data.prompt_dump equal to the ordered dump"
else
  bad "--json coexistence exits $EVAL_STATUS"
fi
if [ "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null)" = "$WANT_KEYS" ]; then
  good "coexistence envelope keeps exactly the documented top-level keys"
else
  bad "coexistence envelope keys broke: '$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null)'"
fi

# --- E4: non-chunked parity ---
run_review fetch-local acme/app cenvsmall --show-prompts -- cenv-small.txt
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "under-cap --show-prompts exits 0"
else
  bad "under-cap --show-prompts exits $EVAL_STATUS"
fi
dump_expected_full ".review/local-cenvsmall" "$EVAL_SANDBOX/cenv-small-expected.txt"
if cmp -s "$EVAL_OUT" "$EVAL_SANDBOX/cenv-small-expected.txt"; then
  good "non-chunked dump is byte-identical to the full.diff concatenation"
else
  bad "non-chunked dump differs from the full.diff concatenation"
fi
run_review fetch-local acme/app cenvsmall --json -- cenv-small.txt
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null)" = "$WANT_KEYS" ] \
  && [ "$(jq -r '.data | has("chunk")' <"$EVAL_OUT" 2>/dev/null)" = "false" ] \
  && [ "$(jq -r '.data | has("prompt_dump")' <"$EVAL_OUT" 2>/dev/null)" = "false" ]; then
  good "plain --json keeps the key set with no data.chunk and no prompt_dump key"
else
  bad "plain --json broke: keys='$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null)'"
fi
# shellcheck disable=SC2086 # CENV_FILES is a validated space-separated path list
run_review fetch-local acme/app cenvfix --json -- $CENV_FILES
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)" = "diff-too-large" ] \
  && jq -e '.data == null' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "absent-knob over-cap --json refusal stays {data null, diff-too-large} with no chunk key"
else
  bad "absent-knob --json refusal exits $EVAL_STATUS"
fi

# --- E5: PR fetch path via a canned gh shim ---
# shellcheck disable=SC2086 # CENV_FILES is a validated space-separated path list
(cd "$EVAL_SANDBOX" && git diff HEAD -- $CENV_FILES > "$EVAL_SANDBOX/cenv-canned.diff")
CENV_FAKEBIN="$EVAL_SANDBOX/fakebin"
mkdir -p "$CENV_FAKEBIN"
# shellcheck disable=SC2086 # CENV_FILES is a validated space-separated path list
jq -n --argjson files "$(printf '%s\n' $CENV_FILES | jq -R . | jq -s 'map({path:.})')" \
  '{state:"OPEN",headRefOid:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",baseRefOid:"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",author:{login:"teammate"},files:$files}' \
  > "$EVAL_SANDBOX/cenv-pr.json"
cat > "$CENV_FAKEBIN/gh" <<'EOF'
#!/usr/bin/env bash
# Case-local gh shim: serve the canned PR payload, log every interception.
echo "fake-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then cat "$EVAL_SANDBOX/cenv-pr.json"; exit 0; fi
if [ "$1" = "pr" ] && [ "$2" = "diff" ]; then cat "$EVAL_SANDBOX/cenv-canned.diff"; exit 0; fi
if [ "$1" = "api" ]; then echo "[]"; exit 0; fi
echo "fake-gh: unexpected call ($*)" >&2; exit 1
EOF
chmod +x "$CENV_FAKEBIN/gh"
run_review_fake() {
  set +e
  (
    cd "$EVAL_SANDBOX" || exit 99
    PATH="$CENV_FAKEBIN:$EVAL_STUBBIN:$PATH" bash "$EVAL_ROOT/scripts/review.sh" "$@" \
      >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  EVAL_STATUS=$?
  set -e
  return 0
}
run_review_fake fetch acme/app 4399 --chunk --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null)" = "$WANT_KEYS" ] \
  && jq -e '.data.chunk.count >= 2 and .error == null' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "PR --chunk --json keeps the key set with additive data.chunk"
else
  bad "PR --chunk --json exits $EVAL_STATUS: $(jq -c '.data.chunk // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi
run_review_fake fetch acme/app 4399 --chunk --show-prompts
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "PR --chunk --show-prompts exits 0"
else
  bad "PR --chunk --show-prompts exits $EVAL_STATUS"
fi
dump_expected_chunks ".review/4399" "$EVAL_SANDBOX/cenv-99-expected.txt"
if cmp -s "$EVAL_OUT" "$EVAL_SANDBOX/cenv-99-expected.txt"; then
  good "PR-path chunked dump concatenates chunk diffs in manifest order"
else
  bad "PR-path chunked dump differs from the manifest-order concatenation"
fi

# --- cleanup: restore the shared sandbox (fixtures stay, probe state goes) ---
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- cenv-f1.txt cenv-f2.txt cenv-f3.txt cenv-f4.txt cenv-f5.txt cenv-f6.txt cenv-small.txt 2>/dev/null || true
)
rm -rf "$CENV_FAKEBIN" "$EVAL_SANDBOX/xdg-empty"
rm -f "$EVAL_SANDBOX"/cenv-expected.txt "$EVAL_SANDBOX"/cenv-fullconcat.txt
rm -f "$EVAL_SANDBOX"/cenv-forward.diff "$EVAL_SANDBOX"/cenv-reversed.diff
rm -f "$EVAL_SANDBOX"/cenv-small-expected.txt
rm -f "$EVAL_SANDBOX"/cenv-99-expected.txt "$EVAL_SANDBOX"/cenv-canned.diff "$EVAL_SANDBOX"/cenv-pr.json
unset XDG_CONFIG_HOME || true

if [ "$fails" -ne 0 ]; then echo "case chunk-envelope: FAIL ($fails)"; exit 1; fi
echo "case chunk-envelope: PASS"
