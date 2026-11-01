#!/usr/bin/env bash
# Case chunk-incremental: incremental re-review composed over chunks.
#
# Grades (all hermetic — sandbox git repo, stub gh, no network, no model):
#   I1  delta-scoped re-read: an over-cap fixture fetched --chunk yields a
#       manifest; a one-file delta maps to a strict subset of manifest chunks;
#       the affected chunk diff carries the delta lines while unaffected
#       chunks name no delta file.
#   I2  prior-sig dedupe + delta reopen through the same merge: seeded open +
#       addressed findings merge by sig via record (the same validate_and_merge
#       post uses) — a restated open finding dedupes to one entry, a
#       delta-touched addressed finding reopens, and the new delta finding
#       appends.
#   I3  gate-still-applies with no extra code: the chunk-scoped incremental
#       review records through the unchanged verb (no chunk flags, exit 0); a
#       provably out-of-hunk anchor moves to the body under the keep-biased
#       rule; exactly one record.json lands beside the merged findings.json.
#   I4  prompt pins: incremental-review.md scopes the delta to affected chunk
#       diffs (unaffected chunks out of scope), dedupes by line-number-free
#       sig, carries evidence rungs + calibration over unchanged, and routes
#       the synthesis through the same post path under the cross-chunk
#       coverage gate.
# The agent loop itself is not executed here (no model in the harness): I4
# pins the contract text, I1-I3 grade the machine-readable bundle surface
# and the merge seam every incremental synthesis passes through.
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
# Explicit adds only — sibling cases' fixtures stay untracked and out of
# every diff; every diff below names its paths explicitly.
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  git config user.email "chunk-incremental@example.com"
  git config user.name "chunk incremental"
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
    awk -v f="$i" 'BEGIN{for(j=0;j<1500;j++) printf "base line %06d file%d padding padding padding padding\n", j, f}' > "cinc-f$i.txt"
  done
  git add cinc-f1.txt cinc-f2.txt cinc-f3.txt cinc-f4.txt cinc-f5.txt cinc-f6.txt .gitignore
  git commit -qm cinc-base 2>/dev/null || true
)
(
  cd "$EVAL_SANDBOX" || exit 99
  for i in 1 2 3 4 5 6; do
    awk -v f="$i" 'BEGIN{for(j=0;j<1500;j++) printf "changed line %06d file%d completely new words words words\n", j, f}' > "cinc-f$i.txt"
  done
)
CINC_FILES="cinc-f1.txt cinc-f2.txt cinc-f3.txt cinc-f4.txt cinc-f5.txt cinc-f6.txt"
# shellcheck disable=SC2086 # CINC_FILES is a validated space-separated path list
set -- $CINC_FILES
BUNDLE="$EVAL_SANDBOX/.review/local-cinc"
FULL_DIFF_BYTES="$(cd "$EVAL_SANDBOX" && git diff HEAD -- "$@" | wc -c | tr -d ' ')"
if [ "$FULL_DIFF_BYTES" -le 512000 ]; then
  bad "fixture diff $FULL_DIFF_BYTES bytes is not over the 512000 cap"
fi

# Chunked fetch: the over-cap "first review" bundle.
# shellcheck disable=SC2086 # CINC_FILES is a validated space-separated path list
run_review fetch-local acme/app cinc --chunk -- $CINC_FILES
if [ "$EVAL_STATUS" -eq 0 ] && [ -f "$BUNDLE/chunks/manifest.json" ]; then
  good "--chunk fetches the over-cap bundle with chunks + manifest"
else
  bad "--chunk exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
MAN="$BUNDLE/chunks/manifest.json"

# The "new commits" delta: file six changes again after the first review.
# Crafted as a one-file slice of the diff, exactly what fetch --base-sha
# writes when new commits touch one path (compare output, diff --git headers).
(cd "$EVAL_SANDBOX" && git diff HEAD -- cinc-f6.txt > "$BUNDLE/incremental.diff")
if [ -s "$BUNDLE/incremental.diff" ] \
  && [ "$(grep -c '^diff --git' "$BUNDLE/incremental.diff")" = "1" ]; then
  good "incremental.diff carries the one-file delta"
else
  bad "incremental.diff missing or not one file"
fi

# --- I1: the delta scopes to a strict subset of manifest chunks ---
N="$(jq -r '.chunk_count' <"$MAN" 2>/dev/null || echo 0)"
if [ "$N" -ge 2 ] 2>/dev/null; then
  good "manifest lists $N chunks (scoping is non-vacuous)"
else
  bad "chunk_count is '$N', want at least 2"
fi
DELTA_FILES="$(grep '^diff --git' "$BUNDLE/incremental.diff" | sed 's/.* b\///')"
if [ "$DELTA_FILES" = "cinc-f6.txt" ]; then
  good "delta touches exactly cinc-f6.txt"
else
  bad "delta files: '$DELTA_FILES'"
fi
if [ "$(jq -r --arg f "$DELTA_FILES" '[.chunks[].files[]] | map(select(. == $f)) | length' <"$MAN" 2>/dev/null)" = "1" ]; then
  good "the delta file lands in exactly one manifest chunk"
else
  bad "delta file chunk membership: $(jq -c '[.chunks[] | {name, files}]' <"$MAN" 2>/dev/null)"
fi
AFFECTED="$(jq -r --arg f "$DELTA_FILES" '.chunks | map(select(.files | index($f)) | .name) | join(" ")' <"$MAN" 2>/dev/null)"
AFFECTED_N="$(printf '%s' "$AFFECTED" | wc -w | tr -d ' ')"
if [ "$AFFECTED_N" -ge 1 ] 2>/dev/null && [ "$AFFECTED_N" -lt "$N" ] 2>/dev/null; then
  good "affected chunk set ($AFFECTED) is a strict subset of the $N-chunk bundle"
else
  bad "affected set '$AFFECTED' is not a strict subset of $N chunks"
fi
ADIFF_REF="$(jq -r --arg f "$DELTA_FILES" '.chunks | map(select(.files | index($f)))[0].diff' <"$MAN" 2>/dev/null)"
ADIFF="$EVAL_SANDBOX/$ADIFF_REF"
if grep -q "changed line 000007 file6" "$ADIFF" 2>/dev/null; then
  good "the affected chunk diff carries the delta lines (scoped re-read sees them)"
else
  bad "affected chunk diff $ADIFF_REF lacks the delta lines"
fi
stray=0
i=1
while [ "$i" -le "$N" ]; do
  name="$(printf 'chunk-%02d' "$i")"
  case " $AFFECTED " in
    *" $name "*) ;;
    *)
      if jq -e --arg n "$name" --arg f "$DELTA_FILES" '.chunks | map(select(.name == $n))[0].files | index($f)' <"$MAN" >/dev/null 2>&1; then
        stray=$((stray + 1))
      fi
      if grep -q "changed line 000007 file6" "$BUNDLE/chunks/$name.diff" 2>/dev/null; then
        stray=$((stray + 1))
      fi
      ;;
  esac
  i=$((i + 1))
done
if [ "$stray" -eq 0 ]; then
  good "no unaffected chunk names the delta file or carries its lines"
else
  bad "$stray unaffected-chunk reference(s) to the delta file"
fi

# NEW-side anchor lines for the record legs (first NEW hunk start per file).
new_start() { # $1 = path; prints the NEW start of its first hunk
  awk -v f="$1" '
    /^diff --git /{cur=$0; sub(/.* b\//, "", cur); next}
    cur==f && /^\@\@ /{h=$3; sub(/^\+/, "", h); sub(/,.*/, "", h); print h; exit}' \
    "$BUNDLE/full.diff"
}
F1L="$(new_start cinc-f1.txt)"
F6L="$(new_start cinc-f6.txt)"
if [[ "$F1L" =~ ^[0-9]+$ && "$F6L" =~ ^[0-9]+$ ]]; then
  good "anchor lines resolve inside reviewed hunks (f1:$F1L f6:$F6L)"
else
  bad "anchor lines unresolved (f1:'$F1L' f6:'$F6L')"
fi

# sig_of mirror (scripts/review.sh normalizes key whitespace, then sha1):
# sigKey for an inline comment is path + "\n" + heading("") + "\n" + body.
sig_for() { printf '%s\n%s\n%s' "$1" "" "$2" | tr -s '[:space:]' ' ' | sed -e 's/^ //' -e 's/ $//' | shasum -a 1 | cut -d' ' -f1; }
F1B="Prior note still stands on file one."
F6B="Prior addressed note on file six."
NEWB="Delta note: new behavior in file six needs a guard."
SIG1="$(sig_for cinc-f1.txt "$F1B")"
SIG6="$(sig_for cinc-f6.txt "$F6B")"
jq -n --arg s1 "$SIG1" --arg b1 "$F1B" --argjson l1 "$F1L" \
  --arg s6 "$SIG6" --arg b6 "$F6B" --argjson l6 "$F6L" \
  '[{kind:"inline",path:"cinc-f1.txt",side:"RIGHT",line:$l1,anchored:true,body:$b1,
      suggestion:null,kb:[],inBounds:true,rigor:"pointed",severity:"informational",
      sig:$s1,sig_version:"1",firstSeen:"0000000",lastSeen:"0000000",
      status:"open",rationale:"",history:[{at:"2026-01-01T00:00:00Z",event:"reported"}]},
    {kind:"inline",path:"cinc-f6.txt",side:"RIGHT",line:$l6,anchored:true,body:$b6,
      suggestion:null,kb:[],inBounds:true,rigor:"pointed",severity:"informational",
      sig:$s6,sig_version:"1",firstSeen:"0000000",lastSeen:"0000000",
      status:"addressed",rationale:"fixed earlier",history:[{at:"2026-01-01T00:00:00Z",event:"reported"}]}]' \
  > "$BUNDLE/findings.json"

# --- I2: restated + new delta findings merge by sig through record ---
cat > "$BUNDLE/review.json" <<EOF
{"schema_version":"1","body":"> \u2139\ufe0f One minor delta note; prior open finding restated.\n\n- Reworked the file six helper.\n\n> Basis: 0 reproduced \u00b7 0 ran \u00b7 0 walked \u00b7 2 pointed \u00b7 0 said \u00b7 0 rules cited \u00b7 0 verified fixes","approved":false,"comments":[{"path":"cinc-f1.txt","line":$F1L,"side":"RIGHT","body":"$F1B","rigor":"pointed","severity":"informational"},{"path":"cinc-f6.txt","line":$F6L,"side":"RIGHT","body":"$NEWB","rigor":"pointed","severity":"informational"}]}
EOF
run_review record acme/app local-cinc --json
if [ "$EVAL_STATUS" -eq 0 ] && jq -e '.error == null' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "chunk-scoped incremental review records exit 0 with a null error"
else
  bad "record exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
if [ "$(stdout_docs)" = "1" ]; then
  good "record emits exactly one JSON envelope"
else
  bad "record emits $(stdout_docs) JSON docs, want exactly 1"
fi
if [ "$(jq 'length' <"$BUNDLE/findings.json" 2>/dev/null)" = "3" ]; then
  good "store holds 3 entries (2 seeded + 1 new, restated merges instead of duplicating)"
else
  bad "store holds $(jq 'length' <"$BUNDLE/findings.json" 2>/dev/null) entries, want 3"
fi
if [ "$(jq --arg b "$F1B" '[.[] | select(.body == $b)] | length' <"$BUNDLE/findings.json" 2>/dev/null)" = "1" ] \
  && [ "$(jq --arg b "$F1B" '[.[] | select(.body == $b and .status == "open")] | length' <"$BUNDLE/findings.json" 2>/dev/null)" = "1" ] \
  && [ "$(jq --arg b "$F1B" -r '[.[] | select(.body == $b)][0].lastSeen' <"$BUNDLE/findings.json" 2>/dev/null)" != "0000000" ]; then
  good "restated prior finding dedupes by sig (one entry, still open, lastSeen bumped)"
else
  bad "prior-sig dedupe broke: $(jq --arg b "$F1B" -c '[.[] | select(.body == $b)]' <"$BUNDLE/findings.json" 2>/dev/null)"
fi
if [ "$(jq --arg b "$F6B" -r '[.[] | select(.body == $b)][0].status' <"$BUNDLE/findings.json" 2>/dev/null)" = "open" ] \
  && jq --arg b "$F6B" -e '[.[] | select(.body == $b)][0].history | map(select(.event == "reopened")) | length > 0' <"$BUNDLE/findings.json" >/dev/null 2>&1; then
  good "delta-touched addressed finding reopens (established incremental rule, chunked bundle)"
else
  bad "delta reopen broke: $(jq --arg b "$F6B" -c '[.[] | select(.body == $b) | {status, history}]' <"$BUNDLE/findings.json" 2>/dev/null)"
fi
if [ "$(jq --arg b "$NEWB" '[.[] | select(.body == $b and .status == "open")] | length' <"$BUNDLE/findings.json" 2>/dev/null)" = "1" ]; then
  good "new delta finding appends as open"
else
  bad "new delta finding missing"
fi
if [ -f "$BUNDLE/record.json" ] && [ "$(jq -r '.open' <"$BUNDLE/record.json" 2>/dev/null)" = "3" ]; then
  good "exactly one record.json lands with 3 open"
else
  bad "record.json missing or open count off"
fi
if [ ! -f "$BUNDLE/findings.dryrun.json" ]; then
  good "no dry-run findings file beside the merged store"
else
  bad "unexpected findings.dryrun.json"
fi

# --- I3: same-path gate, no extra code (separate bundle, out-of-hunk anchor) ---
GBUNDLE="$EVAL_SANDBOX/.review/local-cincgate"
# shellcheck disable=SC2086 # CINC_FILES is a validated space-separated path list
run_review fetch-local acme/app cincgate --chunk -- $CINC_FILES
if [ "$EVAL_STATUS" -eq 0 ] && [ -f "$GBUNDLE/chunks/manifest.json" ]; then
  good "gate bundle fetches chunked"
else
  bad "gate bundle fetch exits $EVAL_STATUS"
fi
GATEB="Gate probe: anchor outside every reviewed hunk."
cat > "$GBUNDLE/review.json" <<EOF
{"schema_version":"1","body":"> \u2139\ufe0f Gate probe on the chunked incremental path.\n\n- Checked the file six delta.\n\n> Basis: 0 reproduced \u00b7 0 ran \u00b7 0 walked \u00b7 1 pointed \u00b7 0 said \u00b7 0 rules cited \u00b7 0 verified fixes","approved":false,"comments":[{"path":"cinc-f6.txt","line":999999,"side":"RIGHT","body":"$GATEB","rigor":"pointed","severity":"informational"}]}
EOF
run_review record acme/app local-cincgate --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "unchanged record verb (no chunk flags) accepts the chunked input, exit 0"
else
  bad "gate record exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
if [ "$(jq -r '.data.anchors.moved' <"$EVAL_OUT" 2>/dev/null)" = "1" ] \
  && [ "$(jq -r '.data.anchors.kept' <"$EVAL_OUT" 2>/dev/null)" = "0" ]; then
  good "out-of-hunk anchor moves to the body under the keep-biased rule (gate applies)"
else
  bad "anchors: $(jq -c '.data.anchors' <"$EVAL_OUT" 2>/dev/null)"
fi
if [ "$(jq 'length' <"$GBUNDLE/findings.json" 2>/dev/null)" = "2" ] \
  && [ "$(jq '[.[] | select(.anchored == false)] | length' <"$GBUNDLE/findings.json" 2>/dev/null)" = "1" ]; then
  good "moved comment merges as unanchored (nothing posts as in-hunk)"
else
  bad "gate findings: $(jq -c 'map({anchored})' <"$GBUNDLE/findings.json" 2>/dev/null)"
fi
if [ -f "$GBUNDLE/record.json" ] && [ ! -f "$GBUNDLE/findings.dryrun.json" ] \
  && [ "$(stdout_docs)" = "1" ]; then
  good "exactly one record.json + findings.json, one envelope (terminal-write discipline holds)"
else
  bad "terminal-write discipline broke on the gate bundle"
fi

# --- I4: the Incremental prompt pins the chunk composition ---
PROMPT="$EVAL_ROOT/prompts/incremental-review.md"
if grep -q "chunks/manifest.json" "$PROMPT" \
  && grep -q "affected chunk" "$PROMPT"; then
  good "prompt scopes the delta to affected chunk diffs via the manifest"
else
  bad "prompt missing manifest + affected-chunk scoping"
fi
if grep -q "Unaffected chunks are out of scope" "$PROMPT"; then
  good "prompt forbids re-reviewing the whole bundle"
else
  bad "prompt missing the unaffected-chunks out-of-scope pin"
fi
if grep -q "line-number-free" "$PROMPT" && grep -q "Dedup against" "$PROMPT"; then
  good "prompt dedupes by sig with chunk re-slicing called out as safe"
else
  bad "prompt missing sig-dedupe pin"
fi
if grep -q "calibration grades the same scale" "$PROMPT"; then
  good "prompt carries evidence rungs + calibration over unchanged"
else
  bad "prompt missing rung-carryover pin"
fi
if grep -q "the same path as an unchunked delta" "$PROMPT" \
  && grep -q "coverage gate applies unchanged" "$PROMPT"; then
  good "prompt routes the synthesis through the same post path under the cross-chunk gate"
else
  bad "prompt missing same-path + coverage-gate pins"
fi

# --- cleanup: restore the shared sandbox (fixtures stay, probe state goes) ---
(
  cd "$EVAL_SANDBOX" || exit 99
  # shellcheck disable=SC2086 # CINC_FILES is a validated space-separated path list
  git checkout -q -- $CINC_FILES 2>/dev/null || true
)
rm -rf "$EVAL_SANDBOX/xdg-empty"
unset XDG_CONFIG_HOME || true

if [ "$fails" -ne 0 ]; then echo "case chunk-incremental: FAIL ($fails)"; exit 1; fi
echo "case chunk-incremental: PASS"
