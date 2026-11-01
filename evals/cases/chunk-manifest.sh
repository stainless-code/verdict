#!/usr/bin/env bash
# Case chunk-manifest: chunked fetch for over-cap bundles (writer + manifest
# + degenerate fallback).
#
# Grades (all hermetic — sandbox git repo, stub gh, no network):
#   M1  absent-knob parity: the same multi-file over-cap fixture without the
#       knob refuses diff-too-large with no chunk mention and no chunks dir.
#   M2  chunked fetch-local: --chunk on the multi-file over-cap fixture
#       succeeds; every chunk diff stays at or under the cap; the ordered
#       chunk diffs concatenate byte-identically to full.diff; the manifest
#       carries schema_version 1, fetched SHAs, cap bytes + source, total
#       bytes, and the ordered chunk file lists with sizes and coverage refs.
#   M3  determinism: re-fetch reproduces the identical chunk map.
#   M4  envelope additive: the chunked --json envelope carries data.chunk
#       with all existing keys intact.
#   M5  single-file fallback: one file over the cap keeps today's advisory
#       refusal with no chunks dir and no manifest (no hang, no half-write).
#   M6  PR fetch path: fetch --chunk on a canned over-cap PR writes the same
#       chunk layout with PR head/base SHAs and the additive envelope key.
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

# Scratch git repo at the sandbox root (run_review runs with cwd=sandbox).
# Explicit adds only — earlier cases' fixtures stay untracked and out of
# every diff; every fetch below names its paths explicitly.
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  git config user.email "chunk-manifest@example.com"
  git config user.name "chunk manifest"
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
    awk -v f="$i" 'BEGIN{for(j=0;j<1500;j++) printf "base line %06d file%d padding padding padding padding\n", j, f}' > "cman-f$i.txt"
  done
  git add cman-f1.txt cman-f2.txt cman-f3.txt cman-f4.txt cman-f5.txt cman-f6.txt .gitignore
  git commit -qm cman-base 2>/dev/null || true
)
(
  cd "$EVAL_SANDBOX" || exit 99
  for i in 1 2 3 4 5 6; do
    awk -v f="$i" 'BEGIN{for(j=0;j<1500;j++) printf "changed line %06d file%d completely new words words words\n", j, f}' > "cman-f$i.txt"
  done
)
CMAN_FILES="cman-f1.txt cman-f2.txt cman-f3.txt cman-f4.txt cman-f5.txt cman-f6.txt"
# shellcheck disable=SC2086 # CMAN_FILES is a validated space-separated path list
set -- $CMAN_FILES
BUNDLE="$EVAL_SANDBOX/.review/local-cmanfix"
FULL_DIFF_BYTES="$(cd "$EVAL_SANDBOX" && git diff HEAD -- "$@" | wc -c | tr -d ' ')"
if [ "$FULL_DIFF_BYTES" -le 512000 ]; then
  bad "fixture diff $FULL_DIFF_BYTES bytes is not over the 512000 cap"
fi

# --- M1: absent-knob parity on the multi-file over-cap fixture ---
# shellcheck disable=SC2086 # CMAN_FILES is a validated space-separated path list
run_review fetch-local acme/app cmanfix -- $CMAN_FILES
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "exceeds cap 512000 bytes" "$EVAL_ERR" \
  && grep -q "try: split the range" "$EVAL_ERR" \
  && ! grep -qi "chunk" "$EVAL_ERR"; then
  good "absent knob: multi-file over-cap refuses diff-too-large, no chunk mention"
else
  bad "absent baseline exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
if [ ! -d "$BUNDLE/chunks" ]; then
  good "absent refusal writes no chunks dir"
else
  bad "absent refusal left a chunks dir"
fi

# --- M2: chunked fetch-local ---
# shellcheck disable=SC2086 # CMAN_FILES is a validated space-separated path list
run_review fetch-local acme/app cmanfix --chunk -- $CMAN_FILES
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "chunking opted in (from --chunk)" "$EVAL_ERR" \
  && grep -q "chunked: .* chunk(s) at or under 512000 bytes" "$EVAL_ERR"; then
  good "--chunk succeeds with the opt-in + chunked notices"
else
  bad "--chunk exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
MAN="$BUNDLE/chunks/manifest.json"
if [ -f "$MAN" ] \
  && [ "$(jq -r '.schema_version' <"$MAN" 2>/dev/null)" = "1" ] \
  && [ "$(jq -r '.cap.bytes' <"$MAN" 2>/dev/null)" = "512000" ] \
  && [ "$(jq -r '.cap.source' <"$MAN" 2>/dev/null)" = "null" ] \
  && [ "$(jq -r '.total_bytes' <"$MAN" 2>/dev/null)" = "$FULL_DIFF_BYTES" ] \
  && [ "$(jq -r '.total_bytes' <"$MAN" 2>/dev/null)" = "$(wc -c <"$BUNDLE/full.diff" | tr -d ' ')" ]; then
  good "manifest carries schema 1, cap 512000 + null source, total bytes matching full.diff"
else
  bad "manifest header: $(jq -c '{schema_version,cap,total_bytes}' <"$MAN" 2>/dev/null || echo MISSING)"
fi
N="$(jq -r '.chunk_count' <"$MAN" 2>/dev/null || echo 0)"
if [ "$N" -ge 2 ] 2>/dev/null \
  && [ "$(jq -r '.chunks | length' <"$MAN" 2>/dev/null)" = "$N" ]; then
  good "manifest lists $N chunks (ordered, more than one)"
else
  bad "chunk_count is '$N'"
fi
over=0
i=1
while [ "$i" -le "$N" ]; do
  name="$(printf 'chunk-%02d' "$i")"
  sz="$(wc -c <"$BUNDLE/chunks/$name.diff" 2>/dev/null | tr -d ' ' || echo 999999999)"
  case "$sz" in ''|*[!0-9]*) sz=999999999;; esac
  if [ "$sz" -gt 512000 ]; then over=$((over + 1)); fi
  if [ ! -s "$BUNDLE/chunks/$name.toc.txt" ] || [ ! -s "$BUNDLE/chunks/$name.roles.txt" ]; then
    bad "$name misses its checklist or roles file"
  fi
  if [ "$(jq -r ".chunks[$((i - 1))].bytes" <"$MAN" 2>/dev/null)" != "$sz" ]; then
    bad "$name manifest bytes mismatch on-disk size"
  fi
  i=$((i + 1))
done
if [ "$over" -eq 0 ]; then
  good "every chunk diff stays at or under the cap"
else
  bad "$over chunk(s) exceed the cap"
fi
if [ "$(jq -r '[.chunks[].files[]] | join(" ")' <"$MAN" 2>/dev/null)" = "$CMAN_FILES" ]; then
  good "manifest file lists partition the diff in order"
else
  bad "manifest files: $(jq -r '[.chunks[].files[]] | join(" ")' <"$MAN" 2>/dev/null)"
fi
rm -f "$EVAL_SANDBOX/cman-concat.diff"
: > "$EVAL_SANDBOX/cman-concat.diff"
i=1
while [ "$i" -le "$N" ]; do
  name="$(printf 'chunk-%02d' "$i")"
  cat "$BUNDLE/chunks/$name.diff" >> "$EVAL_SANDBOX/cman-concat.diff"
  i=$((i + 1))
done
if cmp -s "$EVAL_SANDBOX/cman-concat.diff" "$BUNDLE/full.diff"; then
  good "ordered chunk diffs concatenate byte-identically to full.diff"
else
  bad "chunk concat differs from full.diff"
fi
if [ "$(cd "$EVAL_SANDBOX" && git diff --name-only HEAD -- $CMAN_FILES | LC_ALL=C sort | tr '\n' ' ')" = "$(jq -r '[.chunks[].files[]] | sort | join(" ") | . + " "' <"$MAN" 2>/dev/null)" ]; then
  good "every diff file appears in exactly one chunk"
else
  bad "chunk coverage differs from the diff file set"
fi
if [ -f "$BUNDLE/full.diff" ] && [ -f "$BUNDLE/toc.txt" ] && [ -f "$BUNDLE/roles.txt" ] \
  && [ "$(jq -r '.head_sha' <"$MAN" 2>/dev/null)" = "null" ] \
  && [ "$(jq -r '.base_sha' <"$MAN" 2>/dev/null)" = "$(jq -r '.base_sha' <"$BUNDLE/source.json" 2>/dev/null)" ] \
  && [ -n "$(jq -r '.base_sha' <"$MAN" 2>/dev/null)" ] \
  && [ "$(jq -r '.base_sha' <"$MAN" 2>/dev/null)" != "null" ]; then
  good "top-level bundle keeps the full diff; manifest SHAs match source.json (dirty worktree head reads null)"
else
  bad "bundle SHAs: manifest $(jq -c '{head_sha,base_sha}' <"$MAN" 2>/dev/null)"
fi
missing_refs=0
i=1
while [ "$i" -le "$N" ]; do
  idx=$((i - 1))
  for key in diff toc roles; do
    ref="$(jq -r ".chunks[$idx].$key" <"$MAN" 2>/dev/null)"
    if [ ! -s "$EVAL_SANDBOX/$ref" ]; then missing_refs=$((missing_refs + 1)); fi
  done
  i=$((i + 1))
done
if [ "$missing_refs" -eq 0 ]; then
  good "every manifest coverage reference resolves to a non-empty file"
else
  bad "$missing_refs manifest reference(s) missing on disk"
fi

# --- M3: determinism (re-fetch reproduces the identical chunk map) ---
cp "$MAN" "$EVAL_SANDBOX/cman-manifest-1.json"
i=1
while [ "$i" -le "$N" ]; do
  name="$(printf 'chunk-%02d' "$i")"
  cp "$BUNDLE/chunks/$name.diff" "$EVAL_SANDBOX/cman-$name-1.diff"
  i=$((i + 1))
done
# shellcheck disable=SC2086 # CMAN_FILES is a validated space-separated path list
run_review fetch-local acme/app cmanfix --chunk -- $CMAN_FILES
if [ "$EVAL_STATUS" -eq 0 ] && cmp -s "$EVAL_SANDBOX/cman-manifest-1.json" "$MAN"; then
  good "re-fetch reproduces the identical manifest"
else
  bad "re-fetch exits $EVAL_STATUS / manifest differs"
fi
same=0
i=1
while [ "$i" -le "$N" ]; do
  name="$(printf 'chunk-%02d' "$i")"
  if cmp -s "$EVAL_SANDBOX/cman-$name-1.diff" "$BUNDLE/chunks/$name.diff"; then
    same=$((same + 1))
  fi
  i=$((i + 1))
done
if [ "$same" -eq "$N" ]; then
  good "re-fetch reproduces every chunk diff byte-identically"
else
  bad "re-fetch chunk diffs differ ($same/$N same)"
fi

# --- M4: additive envelope ---
# shellcheck disable=SC2086 # CMAN_FILES is a validated space-separated path list
run_review fetch-local acme/app cmanfix --chunk --json -- $CMAN_FILES
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.chunk.count' <"$EVAL_OUT" 2>/dev/null)" = "$N" ] \
  && [ "$(jq -r '.data.chunk.manifest' <"$EVAL_OUT" 2>/dev/null)" = ".review/local-cmanfix/chunks/manifest.json" ] \
  && [ "$(jq -r '.data.chunk.total_bytes' <"$EVAL_OUT" 2>/dev/null)" = "$FULL_DIFF_BYTES" ]; then
  good "--json carries the additive data.chunk summary"
else
  bad "--json chunk summary: $(jq -c '.data.chunk // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi
if [ "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null)" = "at data error next pr repo schema_version verb warnings" ] \
  && jq -e '.error == null' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "--json keeps the envelope keys with chunk added under data"
else
  bad "--json envelope shape broke: $(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null)"
fi

# Save the multi-file diff for the canned PR leg before restoring the tree.
(cd "$EVAL_SANDBOX" && git diff HEAD -- $CMAN_FILES > "$EVAL_SANDBOX/cman-canned.diff")

# --- M5: single-file over-cap fallback ---
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- $CMAN_FILES
  awk 'BEGIN{for(i=0;i<16000;i++) printf "base line %06d padding padding padding padding padding\n", i}' > cman-huge.txt
  git add cman-huge.txt
  git commit -qm cman-huge-base 2>/dev/null || true
  awk 'BEGIN{for(i=0;i<16000;i++) printf "changed line %06d completely new words words words words\n", i}' > cman-huge.txt
)
run_review fetch-local acme/app cmanhuge --chunk -- cman-huge.txt
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "chunking opted in (from --chunk)" "$EVAL_ERR" \
  && grep -q "single file over cap" "$EVAL_ERR" \
  && grep -q "exceeds cap 512000 bytes" "$EVAL_ERR" \
  && grep -q "try: split the range" "$EVAL_ERR"; then
  good "single over-cap file keeps the advisory refusal with the opted-in notice"
else
  bad "single-file fallback exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
if [ ! -d "$EVAL_SANDBOX/.review/local-cmanhuge/chunks" ] \
  && [ ! -f "$EVAL_SANDBOX/.review/local-cmanhuge/chunks/manifest.json" ]; then
  good "fallback writes no chunks dir and no manifest (no half-write)"
else
  bad "fallback left chunk output behind"
fi
run_review fetch-local acme/app cmanhuge --chunk --json -- cman-huge.txt
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT")" = "diff-too-large" ] \
  && jq -e '.error.candidates | length > 0' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "fallback --json keeps code + candidates"
else
  bad "fallback --json exits $EVAL_STATUS"
fi
if [ ! -d "$EVAL_SANDBOX/.review/local-cmanhuge/chunks" ]; then
  good "fallback --json writes no chunks dir either"
else
  bad "fallback --json left a chunks dir"
fi

# --- M6: PR fetch path via a canned gh shim ---
CMAN_FAKEBIN="$EVAL_SANDBOX/fakebin"
mkdir -p "$CMAN_FAKEBIN"
jq -n --argjson files "$(printf '%s\n' $CMAN_FILES | jq -R . | jq -s 'map({path:.})')" \
  '{state:"OPEN",headRefOid:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",baseRefOid:"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",author:{login:"teammate"},files:$files}' \
  > "$EVAL_SANDBOX/cman-pr.json"
cat > "$CMAN_FAKEBIN/gh" <<'EOF'
#!/usr/bin/env bash
# Case-local gh shim: serve the canned PR payload, log every interception.
echo "fake-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then cat "$EVAL_SANDBOX/cman-pr.json"; exit 0; fi
if [ "$1" = "pr" ] && [ "$2" = "diff" ]; then cat "$EVAL_SANDBOX/cman-canned.diff"; exit 0; fi
if [ "$1" = "api" ]; then echo "[]"; exit 0; fi
echo "fake-gh: unexpected call ($*)" >&2; exit 1
EOF
chmod +x "$CMAN_FAKEBIN/gh"
run_review_fake() {
  set +e
  (
    cd "$EVAL_SANDBOX" || exit 99
    PATH="$CMAN_FAKEBIN:$EVAL_STUBBIN:$PATH" bash "$EVAL_ROOT/scripts/review.sh" "$@" \
      >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  EVAL_STATUS=$?
  set -e
  return 0
}
run_review_fake fetch acme/app 4399 --chunk
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "chunked: .* chunk(s) at or under 512000 bytes" "$EVAL_ERR" \
  && [ -f "$EVAL_SANDBOX/.review/4399/chunks/manifest.json" ]; then
  good "PR fetch --chunk succeeds with chunks + manifest"
else
  bad "PR fetch --chunk exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
if [ "$(jq -r '.head_sha' <"$EVAL_SANDBOX/.review/4399/chunks/manifest.json" 2>/dev/null)" = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" ] \
  && [ "$(jq -r '.base_sha' <"$EVAL_SANDBOX/.review/4399/chunks/manifest.json" 2>/dev/null)" = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" ] \
  && [ "$(jq -r '.total_bytes' <"$EVAL_SANDBOX/.review/4399/chunks/manifest.json" 2>/dev/null)" = "$FULL_DIFF_BYTES" ]; then
  good "PR manifest records the PR head/base SHAs and total bytes"
else
  bad "PR manifest: $(jq -c '{head_sha,base_sha,total_bytes}' <"$EVAL_SANDBOX/.review/4399/chunks/manifest.json" 2>/dev/null)"
fi
pr_concat_bytes=0
pr_over=0
for f in "$EVAL_SANDBOX"/.review/4399/chunks/chunk-*.diff; do
  sz="$(wc -c <"$f" | tr -d ' ')"
  pr_concat_bytes=$((pr_concat_bytes + sz))
  if [ "$sz" -gt 512000 ]; then pr_over=$((pr_over + 1)); fi
done
if [ "$pr_over" -eq 0 ] && [ "$pr_concat_bytes" = "$FULL_DIFF_BYTES" ]; then
  good "PR chunks each stay under the cap and sum to the full diff"
else
  bad "PR chunks: $pr_over over cap, $pr_concat_bytes bytes vs $FULL_DIFF_BYTES"
fi
run_review_fake fetch acme/app 4399 --chunk --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.data.chunk.count >= 2' <"$EVAL_OUT" >/dev/null 2>&1 \
  && jq -e '.error == null' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "PR --json carries the additive chunk summary with a null error"
else
  bad "PR --json exits $EVAL_STATUS: $(jq -c '.data.chunk // "absent"' <"$EVAL_OUT" 2>/dev/null)"
fi

# --- cleanup: restore the shared sandbox (fixtures stay, probe state goes) ---
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- cman-f1.txt cman-f2.txt cman-f3.txt cman-f4.txt cman-f5.txt cman-f6.txt cman-huge.txt 2>/dev/null || true
)
rm -rf "$CMAN_FAKEBIN" "$EVAL_SANDBOX/xdg-empty"
rm -f "$EVAL_SANDBOX"/cman-concat.diff "$EVAL_SANDBOX"/cman-manifest-1.json "$EVAL_SANDBOX"/cman-chunk-*.diff
rm -f "$EVAL_SANDBOX"/cman-canned.diff "$EVAL_SANDBOX"/cman-pr.json
unset XDG_CONFIG_HOME || true

if [ "$fails" -ne 0 ]; then echo "case chunk-manifest: FAIL ($fails)"; exit 1; fi
echo "case chunk-manifest: PASS"
