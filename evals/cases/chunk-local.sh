#!/usr/bin/env bash
# Case chunk-local: local-bundle chunking parity with PR-bundle chunking.
#
# Ticket 06. The shared gate/writer (tickets 01+02) already slices fetch-local
# through the same code as fetch; this case proves the parity the ticket
# demands, side by side on one fixture (prior art: chunk-manifest.sh for the
# chunk layout, completeness.sh C8 for the local source record as authority):
#   L1  same cap, same knob, same shape on the local path: absent knob refuses
#       with no chunk mention and no chunks dir; --chunk and the knob file opt
#       in with the notice naming the source; the manifest carries schema 1,
#       cap 512000 + null source, totals, and ordered chunks each at or under
#       the cap concatenating byte-identically to full.diff; re-fetch
#       reproduces the manifest.
#   L2  source-HEAD authority: the local manifest head/base SHAs equal the
#       local source.json SHAs — null head on a dirty worktree, real commit
#       SHAs on a --range bundle.
#   L3  PR parity: the same diff bytes through fetch --chunk (canned gh) yield
#       the same chunk count, member files, and per-chunk bytes, the same
#       schema, and the additive data.chunk envelope key on both verbs.
#   L4  degenerate parity: one file over the cap refuses diff-too-large with
#       the single-file notice and no chunks dir on BOTH verbs.
#   L5  opted-out / under-cap byte-identity on the local path: under-cap
#       fetches read identical bytes with and without the knob and mint no
#       chunks; a false knob file on the over-cap path reads absent bytes.
#   L6  dump path: --show-prompts on a chunked local bundle embeds the full
#       diff bytes (== the ordered chunk concat), so the dump composes with
#       chunks instead of bypassing them.
# All hermetic — sandbox git repo, stub/canned gh, no network.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

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
  git config user.email "chunk-local@example.com"
  git config user.name "chunk local"
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
    awk -v f="$i" 'BEGIN{for(j=0;j<1500;j++) printf "base line %06d file%d padding padding padding padding\n", j, f}' > "cloc-f$i.txt"
  done
  printf 'alpha\nbeta\ngamma\n' > cloc-small.txt
  git add cloc-f1.txt cloc-f2.txt cloc-f3.txt cloc-f4.txt cloc-f5.txt cloc-f6.txt cloc-small.txt .gitignore
  git commit -qm cloc-base 2>/dev/null || true
)
(
  cd "$EVAL_SANDBOX" || exit 99
  for i in 1 2 3 4 5 6; do
    awk -v f="$i" 'BEGIN{for(j=0;j<1500;j++) printf "changed line %06d file%d completely new words words words\n", j, f}' > "cloc-f$i.txt"
  done
)
CLOC_FILES="cloc-f1.txt cloc-f2.txt cloc-f3.txt cloc-f4.txt cloc-f5.txt cloc-f6.txt"
# shellcheck disable=SC2086 # CLOC_FILES is a validated space-separated path list
set -- $CLOC_FILES
CLOC_BYTES="$(cd "$EVAL_SANDBOX" && git diff HEAD -- "$@" | wc -c | tr -d ' ')"
if [ "$CLOC_BYTES" -le 512000 ]; then
  bad "fixture diff $CLOC_BYTES bytes is not over the 512000 cap"
fi
BUNDLE="$EVAL_SANDBOX/.review/local-clocfix"
KNOB="$EVAL_SANDBOX/.review/chunk"

# --- L1: same cap, same knob, same shape on the local path ---
# shellcheck disable=SC2086 # CLOC_FILES is a validated space-separated path list
run_review fetch-local acme/app clocfix -- $CLOC_FILES
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "exceeds cap 512000 bytes" "$EVAL_ERR" \
  && grep -q "try: split the range" "$EVAL_ERR" \
  && ! grep -qi "chunk" "$EVAL_ERR" \
  && [ ! -d "$BUNDLE/chunks" ]; then
  good "absent knob: over-cap local refuses diff-too-large, no chunk mention, no chunks dir"
else
  bad "absent baseline exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
# shellcheck disable=SC2086 # CLOC_FILES is a validated space-separated path list
run_review fetch-local acme/app clocfix --chunk -- $CLOC_FILES
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "chunking opted in (from --chunk)" "$EVAL_ERR" \
  && grep -q "chunked: .* chunk(s) at or under 512000 bytes" "$EVAL_ERR"; then
  good "local --chunk succeeds with the opt-in + chunked notices"
else
  bad "local --chunk exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
printf 'true\n' > "$KNOB"
# shellcheck disable=SC2086 # CLOC_FILES is a validated space-separated path list
run_review fetch-local acme/app clocfix -- $CLOC_FILES
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "chunking opted in (from .review/chunk)" "$EVAL_ERR"; then
  good "knob file alone opts the local path in (notice names .review/chunk)"
else
  bad "knob-file exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
rm -f "$KNOB"
MAN="$BUNDLE/chunks/manifest.json"
N="$(jq -r '.chunk_count' <"$MAN" 2>/dev/null || echo 0)"
if [ -f "$MAN" ] \
  && [ "$(jq -r '.schema_version' <"$MAN" 2>/dev/null)" = "1" ] \
  && [ "$(jq -r '.cap.bytes' <"$MAN" 2>/dev/null)" = "512000" ] \
  && [ "$(jq -r '.cap.source' <"$MAN" 2>/dev/null)" = "null" ] \
  && [ "$(jq -r '.total_bytes' <"$MAN" 2>/dev/null)" = "$CLOC_BYTES" ] \
  && [ "$N" -ge 2 ] 2>/dev/null \
  && [ "$(jq -r '.chunks | length' <"$MAN" 2>/dev/null)" = "$N" ] \
  && [ "$(jq -r '[.chunks[].files[]] | join(" ")' <"$MAN" 2>/dev/null)" = "$CLOC_FILES" ]; then
  good "local manifest: schema 1, cap 512000 + null source, totals, $N ordered chunks partitioning the diff"
else
  bad "local manifest: $(jq -c '{schema_version,cap,total_bytes,chunk_count}' <"$MAN" 2>/dev/null || echo MISSING)"
fi
over=0
i=1
while [ "$i" -le "$N" ]; do
  name="$(printf 'chunk-%02d' "$i")"
  sz="$(wc -c <"$BUNDLE/chunks/$name.diff" 2>/dev/null | tr -d ' ' || echo 999999999)"
  case "$sz" in ''|*[!0-9]*) sz=999999999;; esac
  if [ "$sz" -gt 512000 ]; then over=$((over + 1)); fi
  [ -s "$BUNDLE/chunks/$name.toc.txt" ] || { bad "$name misses its checklist"; break; }
  [ -s "$BUNDLE/chunks/$name.roles.txt" ] || { bad "$name misses its roles"; break; }
  [ "$(jq -r ".chunks[$((i - 1))].bytes" <"$MAN" 2>/dev/null)" = "$sz" ] || bad "$name manifest bytes mismatch"
  i=$((i + 1))
done
[ "$over" -eq 0 ] && good "every local chunk stays at or under the cap with checklist + roles" || bad "$over chunk(s) over cap"
rm -f "$EVAL_SANDBOX/cloc-concat.diff"
: > "$EVAL_SANDBOX/cloc-concat.diff"
i=1
while [ "$i" -le "$N" ]; do
  cat "$BUNDLE/chunks/$(printf 'chunk-%02d' "$i").diff" >> "$EVAL_SANDBOX/cloc-concat.diff"
  i=$((i + 1))
done
if cmp -s "$EVAL_SANDBOX/cloc-concat.diff" "$BUNDLE/full.diff"; then
  good "ordered local chunk diffs concatenate byte-identically to full.diff"
else
  bad "local chunk concat differs from full.diff"
fi
cp "$MAN" "$EVAL_SANDBOX/cloc-manifest-1.json"
# shellcheck disable=SC2086 # CLOC_FILES is a validated space-separated path list
run_review fetch-local acme/app clocfix --chunk -- $CLOC_FILES
if [ "$EVAL_STATUS" -eq 0 ] && cmp -s "$EVAL_SANDBOX/cloc-manifest-1.json" "$MAN"; then
  good "local re-fetch reproduces the identical manifest (deterministic ordering)"
else
  bad "local re-fetch exits $EVAL_STATUS / manifest differs"
fi

# --- L2: the local source record is the HEAD authority ---
if [ "$(jq -r '.head_sha' <"$MAN" 2>/dev/null)" = "$(jq -r '.head_sha' <"$BUNDLE/source.json" 2>/dev/null)" ] \
  && [ "$(jq -r '.head_sha' <"$MAN" 2>/dev/null)" = "null" ] \
  && [ "$(jq -r '.base_sha' <"$MAN" 2>/dev/null)" = "$(jq -r '.base_sha' <"$BUNDLE/source.json" 2>/dev/null)" ] \
  && [ "$(jq -r '.base_sha' <"$MAN" 2>/dev/null)" != "null" ]; then
  good "worktree manifest SHAs match source.json (dirty head reads null, base is HEAD)"
else
  bad "worktree SHAs: manifest $(jq -c '{head_sha,base_sha}' <"$MAN" 2>/dev/null)"
fi
(cd "$EVAL_SANDBOX" && git diff HEAD -- $CLOC_FILES > "$EVAL_SANDBOX/cloc-canned.diff")
(
  cd "$EVAL_SANDBOX" || exit 99
  # shellcheck disable=SC2086 # CLOC_FILES is a validated space-separated path list
  git add $CLOC_FILES && git commit -qm cloc-head
)
CLOC_A="$(cd "$EVAL_SANDBOX" && git rev-parse HEAD~1)"
CLOC_B="$(cd "$EVAL_SANDBOX" && git rev-parse HEAD)"
# shellcheck disable=SC2086 # CLOC_FILES is a validated space-separated path list
run_review fetch-local acme/app clocrange --range "$CLOC_A..$CLOC_B" --chunk -- $CLOC_FILES
RBUNDLE="$EVAL_SANDBOX/.review/local-clocrange"
RMAN="$RBUNDLE/chunks/manifest.json"
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.head_sha' <"$RMAN" 2>/dev/null)" = "$CLOC_B" ] \
  && [ "$(jq -r '.base_sha' <"$RMAN" 2>/dev/null)" = "$CLOC_A" ] \
  && [ "$(jq -r '.head_sha' <"$RMAN" 2>/dev/null)" = "$(jq -r '.head_sha' <"$RBUNDLE/source.json" 2>/dev/null)" ] \
  && [ "$(jq -r '.base_sha' <"$RMAN" 2>/dev/null)" = "$(jq -r '.base_sha' <"$RBUNDLE/source.json" 2>/dev/null)" ]; then
  good "range manifest records the range head/base SHAs straight from source.json"
else
  bad "range SHAs: manifest $(jq -c '{head_sha,base_sha}' <"$RMAN" 2>/dev/null || echo MISSING)"
fi

# --- L3: the same diff bytes chunk identically down the PR path ---
CLOC_FAKEBIN="$EVAL_SANDBOX/fakebin"
mkdir -p "$CLOC_FAKEBIN"
jq -n --argjson files "$(printf '%s\n' $CLOC_FILES | jq -R . | jq -s 'map({path:.})')" \
  '{state:"OPEN",headRefOid:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",baseRefOid:"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",author:{login:"teammate"},files:$files}' \
  > "$EVAL_SANDBOX/cloc-pr.json"
cat > "$CLOC_FAKEBIN/gh" <<'EOF'
#!/usr/bin/env bash
# Case-local gh shim: serve the canned PR payload, log every interception.
echo "fake-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then cat "$EVAL_SANDBOX/cloc-pr.json"; exit 0; fi
if [ "$1" = "pr" ] && [ "$2" = "diff" ]; then cat "$EVAL_SANDBOX/cloc-canned.diff"; exit 0; fi
if [ "$1" = "api" ]; then echo "[]"; exit 0; fi
echo "fake-gh: unexpected call ($*)" >&2; exit 1
EOF
chmod +x "$CLOC_FAKEBIN/gh"
run_review_fake() {
  set +e
  (
    cd "$EVAL_SANDBOX" || exit 99
    PATH="$CLOC_FAKEBIN:$EVAL_STUBBIN:$PATH" bash "$EVAL_ROOT/scripts/review.sh" "$@" \
      >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  EVAL_STATUS=$?
  set -e
  return 0
}
run_review_fake fetch acme/app 4421 --chunk
PMAN="$EVAL_SANDBOX/.review/4421/chunks/manifest.json"
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.chunk_count' <"$PMAN" 2>/dev/null)" = "$N" ] \
  && [ "$(jq -r '[.chunks[].files[]] | join(" ")' <"$PMAN" 2>/dev/null)" = "$CLOC_FILES" ] \
  && [ "$(jq -r '[.chunks[].bytes] | join(" ")' <"$PMAN" 2>/dev/null)" = "$(jq -r '[.chunks[].bytes] | join(" ")' <"$MAN" 2>/dev/null)" ] \
  && [ "$(jq -r '.schema_version' <"$PMAN" 2>/dev/null)" = "1" ] \
  && [ "$(jq -r '.head_sha' <"$PMAN" 2>/dev/null)" = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" ]; then
  good "PR fetch chunks the same bytes identically (count, files, sizes, schema) under PR HEAD authority"
else
  bad "PR parity exits $EVAL_STATUS: $(jq -c '{chunk_count,head_sha}' <"$PMAN" 2>/dev/null || echo MISSING)"
fi
# shellcheck disable=SC2086 # CLOC_FILES is a validated space-separated path list
run_review fetch-local acme/app clocrange --range "$CLOC_A..$CLOC_B" --chunk --json -- $CLOC_FILES
local_chunk="$(jq -c '.data.chunk' <"$EVAL_OUT" 2>/dev/null || echo absent)"
run_review_fake fetch acme/app 4421 --chunk --json
pr_chunk="$(jq -c '.data.chunk' <"$EVAL_OUT" 2>/dev/null || echo absent)"
if [ "$(jq -r '.count' <<<"$local_chunk" 2>/dev/null)" = "$N" ] \
  && [ "$(jq -r '.count' <<<"$pr_chunk" 2>/dev/null)" = "$N" ] \
  && jq -e '.error == null' <"$EVAL_OUT" >/dev/null 2>&1 \
  && [ "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null)" = "at data error next pr repo schema_version verb warnings" ]; then
  good "both verbs carry the additive data.chunk summary with their envelope keys intact"
else
  bad "envelope parity: local $(jq -c '.count // "absent"' <<<"$local_chunk" 2>/dev/null)"
fi

# --- L4: degenerate single-file behavior matches on both verbs ---
(
  cd "$EVAL_SANDBOX" || exit 99
  awk 'BEGIN{for(i=0;i<16000;i++) printf "base line %06d padding padding padding padding padding\n", i}' > cloc-huge.txt
  git add cloc-huge.txt
  git commit -qm cloc-huge-base 2>/dev/null || true
  awk 'BEGIN{for(i=0;i<16000;i++) printf "changed line %06d completely new words words words words\n", i}' > cloc-huge.txt
)
run_review fetch-local acme/app clochuge --chunk -- cloc-huge.txt
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "single file over cap" "$EVAL_ERR" \
  && grep -q "exceeds cap 512000 bytes" "$EVAL_ERR" \
  && [ ! -d "$EVAL_SANDBOX/.review/local-clochuge/chunks" ]; then
  good "local single-file keeps the advisory refusal with no chunks dir"
else
  bad "local degenerate exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
(cd "$EVAL_SANDBOX" && git diff HEAD -- cloc-huge.txt > "$EVAL_SANDBOX/cloc-canned.diff")
run_review_fake fetch acme/app 4422 --chunk
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "single file over cap" "$EVAL_ERR" \
  && grep -q "exceeds cap 512000 bytes" "$EVAL_ERR" \
  && [ ! -d "$EVAL_SANDBOX/.review/4422/chunks" ]; then
  good "PR single-file keeps the same advisory refusal with no chunks dir"
else
  bad "PR degenerate exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
run_review fetch-local acme/app clochuge --chunk --json -- cloc-huge.txt
local_code="$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null || echo none)"
run_review_fake fetch acme/app 4422 --chunk --json
pr_code="$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null || echo none)"
if [ "$local_code" = "diff-too-large" ] && [ "$pr_code" = "diff-too-large" ]; then
  good "both verbs refuse the degenerate input with diff-too-large in --json"
else
  bad "degenerate codes: local $local_code / pr $pr_code"
fi

# --- L5: opted-out and under-cap local fetches behave exactly as today ---
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- cloc-huge.txt 2>/dev/null || true
  printf 'alpha\nBETA\nGamma\ndelta\n' > cloc-small.txt
)
run_review fetch-local acme/app clocsmall --chunk -- cloc-small.txt
if [ "$EVAL_STATUS" -ne 0 ]; then
  bad "under-cap --chunk exits $EVAL_STATUS"
else
  good "under-cap local --chunk succeeds"
fi
cp "$EVAL_OUT" "$EVAL_SANDBOX/cloc-under-out.txt"
cp "$EVAL_SANDBOX/.review/local-clocsmall/full.diff" "$EVAL_SANDBOX/cloc-under.diff"
if grep -qi "chunk" "$EVAL_ERR"; then
  bad "under-cap --chunk mentions chunking: $(head -c 200 "$EVAL_ERR")"
else
  good "under-cap --chunk stays silent about chunking"
fi
run_review fetch-local acme/app clocsmall -- cloc-small.txt
if [ "$EVAL_STATUS" -eq 0 ] \
  && cmp -s "$EVAL_OUT" "$EVAL_SANDBOX/cloc-under-out.txt" \
  && cmp -s "$EVAL_SANDBOX/.review/local-clocsmall/full.diff" "$EVAL_SANDBOX/cloc-under.diff" \
  && [ ! -d "$EVAL_SANDBOX/.review/local-clocsmall/chunks" ] \
  && ! jq -e '.data | has("chunk")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "under-cap absent reads identical bytes, mints no chunks, envelope gains no data.chunk"
else
  bad "under-cap absent parity exits $EVAL_STATUS"
fi
printf 'false\n' > "$KNOB"
(
  cd "$EVAL_SANDBOX" || exit 99
  for i in 1 2 3 4 5 6; do
    awk -v f="$i" 'BEGIN{for(j=0;j<1500;j++) printf "parity line %06d file%d different words again again\n", j, f}' > "cloc-f$i.txt"
  done
)
# shellcheck disable=SC2086 # CLOC_FILES is a validated space-separated path list
run_review fetch-local acme/app clocfix -- $CLOC_FILES
false_status="$EVAL_STATUS"
cp "$EVAL_ERR" "$EVAL_SANDBOX/cloc-false-err.txt"
cp "$EVAL_OUT" "$EVAL_SANDBOX/cloc-false-out.txt"
rm -f "$KNOB"
# shellcheck disable=SC2086 # CLOC_FILES is a validated space-separated path list
run_review fetch-local acme/app clocfix -- $CLOC_FILES
if [ "$false_status" -eq 1 ] && [ "$EVAL_STATUS" -eq 1 ] \
  && cmp -s "$EVAL_ERR" "$EVAL_SANDBOX/cloc-false-err.txt" \
  && cmp -s "$EVAL_OUT" "$EVAL_SANDBOX/cloc-false-out.txt"; then
  good "false knob file on the over-cap path reads absent bytes"
else
  bad "false-knob parity broke"
fi

# --- L6: the prompt-dump path composes with chunks ---
# shellcheck disable=SC2086 # CLOC_FILES is a validated space-separated path list
run_review fetch-local acme/app clocrange --range "$CLOC_A..$CLOC_B" --chunk --show-prompts -- $CLOC_FILES
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(grep -c '^diff --git' "$EVAL_OUT")" = "$(grep -c '^diff --git' "$RBUNDLE/full.diff")" ] \
  && grep -q "$(head -n 1 "$RBUNDLE/full.diff")" "$EVAL_OUT" \
  && grep -q "$(tail -n 1 "$RBUNDLE/full.diff")" "$EVAL_OUT"; then
  good "--show-prompts embeds the full diff bytes (== the ordered chunk concat)"
else
  bad "dump exits $EVAL_STATUS with $(grep -c '^diff --git' "$EVAL_OUT" 2>/dev/null) headers"
fi

# --- cleanup: restore the shared sandbox (fixtures stay, probe state goes) ---
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- $CLOC_FILES cloc-small.txt cloc-huge.txt 2>/dev/null || true
)
rm -rf "$CLOC_FAKEBIN" "$EVAL_SANDBOX/xdg-empty"
rm -f "$KNOB" "$EVAL_SANDBOX"/cloc-concat.diff "$EVAL_SANDBOX"/cloc-manifest-1.json
rm -f "$EVAL_SANDBOX"/cloc-canned.diff "$EVAL_SANDBOX"/cloc-pr.json
rm -f "$EVAL_SANDBOX"/cloc-under-out.txt "$EVAL_SANDBOX"/cloc-under.diff
rm -f "$EVAL_SANDBOX"/cloc-false-err.txt "$EVAL_SANDBOX"/cloc-false-out.txt
unset XDG_CONFIG_HOME || true

if [ "$fails" -ne 0 ]; then echo "case chunk-local: FAIL ($fails)"; exit 1; fi
echo "case chunk-local: PASS"
