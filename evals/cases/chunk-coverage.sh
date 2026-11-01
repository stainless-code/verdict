#!/usr/bin/env bash
# Case chunk-coverage: post/record cross-chunk merge validation.
#
# Grades (all hermetic — crafted chunked bundles, stub gh, no network):
#   C1  missing-chunk refusal (post): an attested synthesis omitting a
#       manifest chunk is refused with typed missing-chunk-coverage naming
#       the missing chunk plus the re-fetch remediation; nothing merges
#       (no findings.json, no dry-run file, no post.json).
#   C2  missing-chunk refusal (record): the same gate fires through record
#       on a local chunked bundle (omitting the other chunk).
#   C3  missing snapshot refusal: a manifest chunk whose pinned diff is
#       gone is refused as its own typed state (missing-chunk-snapshot)
#       naming the chunk with the re-fetch remediation, never a partial post.
#   C4  drift surfaces pinned: a mid-review push rewriting full.diff warns
#       of drift while anchors still validate against the pinned per-chunk
#       snapshots (kept, never silently mis-anchored).
#   C5  happy-path exactly-one-post + dedupe: a fully attested synthesis
#       posts exactly one review (one reviews call, one payload, one
#       envelope), merges exactly one findings file with today's sig dedupe
#       (restated prior entry merges, new entry appends), and lands post.json.
#   C6  unattested warn-and-keep: a review without the chunks claim on a
#       chunked bundle still validates exactly as today (exit 0,
#       keep-biased anchors, no basis warning) but now warns loudly naming
#       the manifest, so silent partial synthesis is impossible; delta-scoped
#       and single-anchor inputs keep their exits.
# The agent loop itself is not executed here (no model in the harness): the
# prompt attestation contract is pinned by chunk-synthesize S1-adjacent text
# in prompts/review.md; C1-C6 grade the merge seam every synthesis passes
# through. This case carries the dedicated missing-chunk refusal grade that
# the incremental slice (chunk-incremental.sh) deferred to this slice.
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

HEADA="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
BASEB="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

# mkcovbundle <pr> — crafted two-chunk PR bundle (a.txt in chunk-01,
# b.txt in chunk-02), same on-disk shape fetch --chunk writes.
mkcovbundle() { # $1 = pr number
  local pr="$1" dir="$EVAL_SANDBOX/.review/$1"
  local total="" b1="" b2=""
  mkdir -p "$dir/chunks"
  cat > "$dir/PR.json" <<EOF
{"state":"OPEN","headRefOid":"$HEADA","baseRefOid":"$BASEB","author":{"login":"teammate"},"files":[{"path":"a.txt","additions":1,"deletions":0},{"path":"b.txt","additions":1,"deletions":0}]}
EOF
  cat > "$dir/full.diff" <<'EOF'
diff --git a/a.txt b/a.txt
index 1111111..2222222 100644
--- a/a.txt
+++ b/a.txt
@@ -1 +1,2 @@
 top
+line-a
diff --git a/b.txt b/b.txt
index 3333333..4444444 100644
--- a/b.txt
+++ b/b.txt
@@ -1 +1,2 @@
 top
+line-b
EOF
  head -n 7 "$dir/full.diff" > "$dir/chunks/chunk-01.diff"
  tail -n +8 "$dir/full.diff" > "$dir/chunks/chunk-02.diff"
  cat > "$dir/chunks/chunk-01.toc.txt" <<'EOF'
# Chunk 1/2 — read ranges as coverage checklist

diff --git a/a.txt b/a.txt

## files
- a.txt
EOF
  cat > "$dir/chunks/chunk-02.toc.txt" <<'EOF'
# Chunk 2/2 — read ranges as coverage checklist

diff --git a/b.txt b/b.txt

## files
- b.txt
EOF
  printf '## source\n- a.txt\n' > "$dir/chunks/chunk-01.roles.txt"
  printf '## source\n- b.txt\n' > "$dir/chunks/chunk-02.roles.txt"
  printf 'a.txt\tNEW\t1\t2\nb.txt\tNEW\t1\t2\n' > "$dir/hunks.tsv"
  total="$(wc -c < "$dir/full.diff" | tr -d ' ')"
  b1="$(wc -c < "$dir/chunks/chunk-01.diff" | tr -d ' ')"
  b2="$(wc -c < "$dir/chunks/chunk-02.diff" | tr -d ' ')"
  jq -n --arg hs "$HEADA" --arg bs "$BASEB" --arg p "$pr" \
    --argjson total "$total" --argjson b1 "$b1" --argjson b2 "$b2" \
    '{schema_version:"1",bundle:(".review/" + $p),
      head_sha:$hs,base_sha:$bs,cap:{bytes:512000,source:null},
      total_bytes:$total,chunk_count:2,
      chunks:[{index:1,name:"chunk-01",files:["a.txt"],estimated_bytes:$b1,bytes:$b1,
        diff:(".review/" + $p + "/chunks/chunk-01.diff"),toc:(".review/" + $p + "/chunks/chunk-01.toc.txt"),roles:(".review/" + $p + "/chunks/chunk-01.roles.txt")},
        {index:2,name:"chunk-02",files:["b.txt"],estimated_bytes:$b2,bytes:$b2,
        diff:(".review/" + $p + "/chunks/chunk-02.diff"),toc:(".review/" + $p + "/chunks/chunk-02.toc.txt"),roles:(".review/" + $p + "/chunks/chunk-02.roles.txt")}]}' \
    > "$dir/chunks/manifest.json"
}

# mkcovlocal <slug> — same two-chunk shape as a local bundle for record.
mkcovlocal() { # $1 = local slug (without the local- prefix)
  local slug="$1" dir="$EVAL_SANDBOX/.review/local-$1"
  local total="" b1="" b2=""
  mkdir -p "$dir/chunks"
  cat > "$dir/source.json" <<EOF
{"head_sha":"$HEADA","base_sha":"$BASEB","head_ref":"feature"}
EOF
  cat > "$dir/full.diff" <<'EOF'
diff --git a/a.txt b/a.txt
index 1111111..2222222 100644
--- a/a.txt
+++ b/a.txt
@@ -1 +1,2 @@
 top
+line-a
diff --git a/b.txt b/b.txt
index 3333333..4444444 100644
--- a/b.txt
+++ b/b.txt
@@ -1 +1,2 @@
 top
+line-b
EOF
  head -n 7 "$dir/full.diff" > "$dir/chunks/chunk-01.diff"
  tail -n +8 "$dir/full.diff" > "$dir/chunks/chunk-02.diff"
  printf '# Chunk 1/2\n\ndiff --git a/a.txt b/a.txt\n\n## files\n- a.txt\n' > "$dir/chunks/chunk-01.toc.txt"
  printf '# Chunk 2/2\n\ndiff --git a/b.txt b/b.txt\n\n## files\n- b.txt\n' > "$dir/chunks/chunk-02.toc.txt"
  printf '## source\n- a.txt\n' > "$dir/chunks/chunk-01.roles.txt"
  printf '## source\n- b.txt\n' > "$dir/chunks/chunk-02.roles.txt"
  printf 'a.txt\tNEW\t1\t2\nb.txt\tNEW\t1\t2\n' > "$dir/hunks.tsv"
  total="$(wc -c < "$dir/full.diff" | tr -d ' ')"
  b1="$(wc -c < "$dir/chunks/chunk-01.diff" | tr -d ' ')"
  b2="$(wc -c < "$dir/chunks/chunk-02.diff" | tr -d ' ')"
  jq -n --arg hs "$HEADA" --arg bs "$BASEB" --arg s "$slug" \
    --argjson total "$total" --argjson b1 "$b1" --argjson b2 "$b2" \
    '{schema_version:"1",bundle:(".review/local-" + $s),
      head_sha:$hs,base_sha:$bs,cap:{bytes:512000,source:null},
      total_bytes:$total,chunk_count:2,
      chunks:[{index:1,name:"chunk-01",files:["a.txt"],estimated_bytes:$b1,bytes:$b1,
        diff:(".review/local-" + $s + "/chunks/chunk-01.diff"),toc:(".review/local-" + $s + "/chunks/chunk-01.toc.txt"),roles:(".review/local-" + $s + "/chunks/chunk-01.roles.txt")},
        {index:2,name:"chunk-02",files:["b.txt"],estimated_bytes:$b2,bytes:$b2,
        diff:(".review/local-" + $s + "/chunks/chunk-02.diff"),toc:(".review/local-" + $s + "/chunks/chunk-02.toc.txt"),roles:(".review/local-" + $s + "/chunks/chunk-02.roles.txt")}]}' \
    > "$dir/chunks/manifest.json"
}

# --- C1: attested-but-partial synthesis is refused on post ---
mkcovbundle 4641
eval_write_review 4641 <<'EOF'
{"schema_version":"1","chunks":["chunk-01"],"body":"> ✅ Chunk-01 reviewed, one note.\n> Basis: 0 reproduced · 1 ran · 0 walked · 0 pointed · 0 said · 0 rules cited · 0 verified fixes","approved":false,"comments":[{"path":"a.txt","line":2,"side":"RIGHT","body":"Chunk-01 note: clarify this line.","rigor":"ran","severity":"informational"}]}
EOF
run_review post acme/app 4641 --dry-run --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)" = "missing-chunk-coverage" ]; then
  good "partial attestation refuses typed missing-chunk-coverage (exit 1)"
else
  bad "partial post gave exit $EVAL_STATUS code '$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)'"
fi
if jq -r '.error.message' <"$EVAL_OUT" 2>/dev/null | grep -q "chunk-02" \
  && jq -r '.error.message' <"$EVAL_OUT" 2>/dev/null | grep -q "b.txt" \
  && jq -r '.error.message' <"$EVAL_OUT" 2>/dev/null | grep -q "try: re-fetch"; then
  good "refusal names the missing chunk (chunk-02, b.txt) with the re-fetch remediation"
else
  bad "refusal message misses the chunk/remediation: $(jq -r '.error.message' <"$EVAL_OUT" 2>/dev/null | head -c 300)"
fi
if [ ! -f "$EVAL_SANDBOX/.review/4641/findings.json" ] \
  && [ ! -f "$EVAL_SANDBOX/.review/4641/findings.dryrun.json" ] \
  && [ ! -f "$EVAL_SANDBOX/.review/4641/post.json" ]; then
  good "refusal merges nothing (no findings, no dry-run file, no post record)"
else
  bad "refusal left store output behind"
fi

# --- C2: the same gate fires through record on a local bundle ---
mkcovlocal covrec
cat > "$EVAL_SANDBOX/.review/local-covrec/review.json" <<'EOF'
{"schema_version":"1","chunks":["chunk-02"],"body":"> ✅ Chunk-02 reviewed, one note.\n> Basis: 0 reproduced · 0 ran · 1 walked · 0 pointed · 0 said · 0 rules cited · 0 verified fixes","approved":false,"comments":[{"path":"b.txt","line":2,"side":"RIGHT","body":"Chunk-02 note: clarify this line.","rigor":"walked","severity":"informational"}]}
EOF
run_review record acme/app local-covrec --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)" = "missing-chunk-coverage" ] \
  && jq -r '.error.message' <"$EVAL_OUT" 2>/dev/null | grep -q "chunk-01"; then
  good "record refuses the partial attestation naming chunk-01"
else
  bad "record gave exit $EVAL_STATUS code '$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)'"
fi
if [ ! -f "$EVAL_SANDBOX/.review/local-covrec/findings.json" ] \
  && [ ! -f "$EVAL_SANDBOX/.review/local-covrec/record.json" ]; then
  good "record refusal merges nothing (no findings, no record)"
else
  bad "record refusal left store output behind"
fi

# --- C3: a missing pinned snapshot is its own typed refusal ---
mkcovbundle 4642
rm "$EVAL_SANDBOX/.review/4642/chunks/chunk-02.diff"
eval_write_review 4642 <<'EOF'
{"schema_version":"1","chunks":["chunk-01","chunk-02"],"body":"> ✅ Both chunks reviewed, one note.\n> Basis: 0 reproduced · 1 ran · 0 walked · 0 pointed · 0 said · 0 rules cited · 0 verified fixes","approved":false,"comments":[{"path":"a.txt","line":2,"side":"RIGHT","body":"Chunk-01 note: clarify this line.","rigor":"ran","severity":"informational"}]}
EOF
run_review post acme/app 4642 --dry-run --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)" = "missing-chunk-snapshot" ] \
  && jq -r '.error.message' <"$EVAL_OUT" 2>/dev/null | grep -q "chunk-02" \
  && jq -r '.error.message' <"$EVAL_OUT" 2>/dev/null | grep -q "try: re-fetch"; then
  good "missing pinned diff refuses typed missing-chunk-snapshot naming chunk-02 + re-fetch"
else
  bad "snapshot leg gave exit $EVAL_STATUS code '$(jq -r '.error.code' <"$EVAL_OUT" 2>/dev/null)'"
fi
if [ ! -f "$EVAL_SANDBOX/.review/4642/findings.json" ] \
  && [ ! -f "$EVAL_SANDBOX/.review/4642/post.json" ]; then
  good "snapshot refusal posts nothing partial"
else
  bad "snapshot refusal left store output behind"
fi

# --- C4: mid-review drift warns while anchors stay pinned ---
mkcovbundle 4643
# Simulate a push landing after the fetch: full.diff grows (a.txt hunk now
# covers NEW 1-1 only), while the pinned chunk diffs still say NEW 1-2.
{
  echo 'diff --git a/a.txt b/a.txt'
  echo 'index 1111111..2222222 100644'
  echo '--- a/a.txt'
  echo '+++ b/a.txt'
  echo '@@ -1 +1 @@'
  echo ' top'
  tail -n +8 "$EVAL_SANDBOX/.review/4643/full.diff"
} > "$EVAL_SANDBOX/.review/4643/full.diff.new"
mv "$EVAL_SANDBOX/.review/4643/full.diff.new" "$EVAL_SANDBOX/.review/4643/full.diff"
eval_write_review 4643 <<'EOF'
{"schema_version":"1","body":"> ✅ One note on the pinned line.\n> Basis: 0 reproduced · 0 ran · 1 walked · 0 pointed · 0 said · 0 rules cited · 0 verified fixes","approved":false,"comments":[{"path":"a.txt","line":2,"side":"RIGHT","body":"Pinned note: clarify this line.","rigor":"walked","severity":"informational"}]}
EOF
run_review post acme/app 4643 --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.anchors.kept' <"$EVAL_OUT" 2>/dev/null)" = "1" ] \
  && [ "$(jq -r '.data.anchors.moved' <"$EVAL_OUT" 2>/dev/null)" = "0" ]; then
  good "drifted anchor stays kept against the pinned per-chunk snapshot (never silently mis-anchored)"
else
  bad "drift leg gave exit $EVAL_STATUS anchors $(jq -c '.data.anchors' <"$EVAL_OUT" 2>/dev/null || echo unreadable)"
fi
if grep -q "drifted" "$EVAL_ERR" && grep -q "re-fetch" "$EVAL_ERR"; then
  good "drift surfaces loudly naming the re-fetch re-pin"
else
  bad "drift warning missing: $(head -c 200 "$EVAL_ERR")"
fi

# --- C5: a complete attested synthesis posts exactly once with sig dedupe ---
mkcovbundle 4644
# sig_of mirror (scripts/review.sh normalizes key whitespace, then sha1):
# sigKey for an inline comment is path + "\n" + heading("") + "\n" + body.
sig_for() { printf '%s\n%s\n%s' "$1" "" "$2" | tr -s '[:space:]' ' ' | sed -e 's/^ //' -e 's/ $//' | shasum -a 1 | cut -d' ' -f1; }
ABODY="Prior note on file one still stands."
BBODY="New note on file two from this synthesis."
SIGA="$(sig_for a.txt "$ABODY")"
jq -n --arg s "$SIGA" --arg b "$ABODY" \
  '[{kind:"inline",path:"a.txt",side:"RIGHT",line:2,anchored:true,body:$b,
      suggestion:null,kb:[],inBounds:true,rigor:"pointed",severity:"informational",
      sig:$s,sig_version:"1",firstSeen:"0000000",lastSeen:"0000000",
      status:"open",rationale:"",history:[{at:"2026-01-01T00:00:00Z",event:"reported"}]}]' \
  > "$EVAL_SANDBOX/.review/4644/findings.json"
eval_write_review 4644 <<EOF
{"schema_version":"1","chunks":["chunk-01","chunk-02"],"body":"> ✅ Both chunks reviewed, two notes.\n> Basis: 0 reproduced · 0 ran · 0 walked · 2 pointed · 0 said · 0 rules cited · 0 verified fixes","approved":false,"comments":[{"path":"a.txt","line":2,"side":"RIGHT","body":"$ABODY","rigor":"pointed","severity":"informational"},{"path":"b.txt","line":2,"side":"RIGHT","body":"$BBODY","rigor":"pointed","severity":"informational"}]}
EOF
# Real post through a canned gh shim (payload saved, head reads fetched).
STUB_SAVED="$EVAL_SANDBOX/gh-stub-orig"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
restore_stub() { cp "$STUB_SAVED" "$EVAL_STUBBIN/gh"; }
trap restore_stub EXIT
cat > "$EVAL_STUBBIN/gh" <<EOF
#!/usr/bin/env bash
echo "stub-gh \$*" >> "\${EVAL_STUBLOG:-/dev/null}"
case "\$*" in
  *reviews*)
    if [ -n "\${EVAL_SAVE_PAYLOAD:-}" ]; then cat > "\$EVAL_SAVE_PAYLOAD"; fi
    printf '%s' '{"id":101,"state":"commented","html_url":"https://example.invalid/r/101","submitted_at":"2026-01-01T00:00:00Z"}'
    exit 0;;
  *pulls*)
    printf '$HEADA'; exit 0;;
  *user*)
    printf 'someone-else'; exit 0;;
esac
echo "evals-stub-gh: network disabled (\$*)" >&2
exit 1
EOF
chmod +x "$EVAL_STUBBIN/gh"
COVLOG="$EVAL_SANDBOX/cov-post.log"
COVLOG_ORIG="$EVAL_STUBLOG"
export EVAL_STUBLOG="$COVLOG"
: > "$COVLOG"
export EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/posted-4644.json"
rm -f "$EVAL_SAVE_PAYLOAD"
run_review post acme/app 4644 --json
unset EVAL_SAVE_PAYLOAD
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.error == null' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "complete synthesis posts (exit 0, null error)"
else
  bad "happy post gave exit $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi
if [ "$(grep -c "reviews" "$COVLOG" 2>/dev/null || true)" = "1" ]; then
  good "exactly one reviews call leaves the tool (one terminal write)"
else
  bad "reviews calls: $(grep -c "reviews" "$COVLOG" 2>/dev/null || true), want exactly 1"
fi
if [ "$(jq '.comments | length' <"$EVAL_SANDBOX/posted-4644.json" 2>/dev/null)" = "2" ] \
  && [ "$(jq -r '.comments[0].path' <"$EVAL_SANDBOX/posted-4644.json" 2>/dev/null)" = "a.txt" ] \
  && [ "$(jq -r '.comments[1].path' <"$EVAL_SANDBOX/posted-4644.json" 2>/dev/null)" = "b.txt" ]; then
  good "payload carries both chunk findings inline (one review, one document)"
else
  bad "payload comments: $(jq -c '.comments' <"$EVAL_SANDBOX/posted-4644.json" 2>/dev/null || echo unreadable)"
fi
if [ "$(jq 'length' <"$EVAL_SANDBOX/.review/4644/findings.json" 2>/dev/null)" = "2" ] \
  && [ "$(jq --arg b "$ABODY" '[.[] | select(.body == $b)] | length' <"$EVAL_SANDBOX/.review/4644/findings.json" 2>/dev/null)" = "1" ] \
  && [ "$(jq --arg b "$ABODY" -r '[.[] | select(.body == $b)][0].lastSeen' <"$EVAL_SANDBOX/.review/4644/findings.json" 2>/dev/null)" != "0000000" ] \
  && [ "$(jq --arg b "$BBODY" '[.[] | select(.body == $b and .status == "open")] | length' <"$EVAL_SANDBOX/.review/4644/findings.json" 2>/dev/null)" = "1" ]; then
  good "findings merge dedupes by sig (restated entry merges, new entry appends)"
else
  bad "findings merge broke: $(jq -c 'map({sig: .sig[0:8], body})' <"$EVAL_SANDBOX/.review/4644/findings.json" 2>/dev/null || echo unreadable)"
fi
if [ "$(jq -r '.head' <"$EVAL_SANDBOX/.review/4644/post.json" 2>/dev/null)" = "$HEADA" ] \
  && [ "$(stdout_docs)" = "1" ] \
  && [ "$(jq -r '.data.event' <"$EVAL_OUT" 2>/dev/null)" = "COMMENT" ]; then
  good "one post.json on the reviewed head, one envelope, one event"
else
  bad "post record/envelope broke"
fi
if [ -z "$(ls "$EVAL_SANDBOX/.review/4644"/chunks/*.review.json 2>/dev/null || true)" ]; then
  good "no per-chunk review files beside the single synthesis"
else
  bad "per-chunk review files leaked"
fi
restore_stub
trap - EXIT
export EVAL_STUBLOG="$COVLOG_ORIG"

# --- C6: unattested reviews warn loudly but validate as today ---
eval_write_review 4641 <<'EOF'
{"schema_version":"1","body":"> ✅ One note on chunk-01.\n> Basis: 0 reproduced · 1 ran · 0 walked · 0 pointed · 0 said · 0 rules cited · 0 verified fixes","approved":false,"comments":[{"path":"a.txt","line":2,"side":"RIGHT","body":"Chunk-01 note: clarify this line.","rigor":"ran","severity":"informational"}]}
EOF
run_review post acme/app 4641 --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.anchors.kept' <"$EVAL_OUT" 2>/dev/null)" = "1" ] \
  && ! grep -q "basis line counts" "$EVAL_ERR"; then
  good "unattested partial review passes as today (exit 0, kept, no basis warning)"
else
  bad "unattested leg gave exit $EVAL_STATUS"
fi
if grep -q "no chunks attestation" "$EVAL_ERR" \
  && grep -q "chunks/manifest.json" "$EVAL_ERR"; then
  good "unattested chunked review warns loudly naming the manifest"
else
  bad "unattested warn missing: $(head -c 300 "$EVAL_ERR")"
fi
if jq -e '.warnings | join(" ") | contains("no chunks attestation")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "the warn rides the JSON envelope warnings (machine-visible)"
else
  bad "envelope warnings miss the attestation warn: $(jq -c '.warnings' <"$EVAL_OUT" 2>/dev/null)"
fi

# --- cleanup: probe state goes, fixtures stay out of later cases ---
rm -rf "$EVAL_SANDBOX/.review/4641" "$EVAL_SANDBOX/.review/4642" \
  "$EVAL_SANDBOX/.review/4643" "$EVAL_SANDBOX/.review/4644" \
  "$EVAL_SANDBOX/.review/local-covrec" "$EVAL_SANDBOX/xdg-empty"
rm -f "$EVAL_SANDBOX"/posted-4644.json "$EVAL_SANDBOX"/cov-post.log
unset XDG_CONFIG_HOME EVAL_SAVE_PAYLOAD || true

if [ "$fails" -ne 0 ]; then echo "case chunk-coverage: FAIL ($fails)"; exit 1; fi
echo "case chunk-coverage: PASS"
