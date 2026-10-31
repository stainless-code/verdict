#!/usr/bin/env bash
# Case kboverlap: offline overlap audit over recalled x cited x outcomes.
#
# Grades (hermetic — crafted bundle fixtures in the sandbox; the
# network-dead gh stub stays first on PATH throughout):
#   O1  the three binding numbers over fixture bundles with known
#       recall/cite/outcome shapes: precision, coverage, outcome split,
#       plus the joins corroboration and per-bundle rows.
#   O2  empty-manifest edge (present but []: measured, precision null) and
#       uncited-bundle edge (no cites: measured, coverage null, precision 0).
#   O3  default trend stays byte-identical without --overlap (no overlap key
#       in the envelope, no overlap section in human output).
#   O4  no-manifest-at-all edge: precision/coverage null (unmeasured, never
#       zero), the outcome split still counted over cited findings.
#   O5  pure read: fixture bytes identical before/after, zero gh
#       interceptions (no network), corrupt joins warns and reads zeros.
#   O6  empty store: zero bundles read as nulls + zeros, never an error.
set -euo pipefail
HERE="$(dirname "$0")"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

# Cases share one sandbox: stash any bundles other cases left behind so the
# rollup below grades exactly this case's fixtures (restored on exit,
# even on failure — later cases must see their own stores untouched).
HOLD="$EVAL_SANDBOX/.review-hold-kbov"
mkdir -p "$EVAL_SANDBOX/.review" "$HOLD"
for e in "$EVAL_SANDBOX"/.review/*; do
  [ -e "$e" ] || continue
  mv "$e" "$HOLD/"
done
restore_kbov() {
  for e in "$HOLD"/*; do
    [ -e "$e" ] || continue
    mv "$e" "$EVAL_SANDBOX/.review/"
  done
  rmdir "$HOLD" 2>/dev/null || true
}
trap restore_kbov EXIT

mkf() { # $1 = pr, stdin = findings array
  local pr="$1"
  mkdir -p "$EVAL_SANDBOX/.review/$pr"
  cat > "$EVAL_SANDBOX/.review/$pr/findings.json"
}

F1="a111111111111111111111111111111111111111"
F2="a222222222222222222222222222222222222222"
F3="a333333333333333333333333333333333333333"
G1="b111111111111111111111111111111111111111"
G2="b222222222222222222222222222222222222222"
H1="c111111111111111111111111111111111111111"
I1="d111111111111111111111111111111111111111"
J1="e111111111111111111111111111111111111111"

# Bundle 4401: recalls r-a1 + r-a2; cites both (addressed + false-positive)
# plus one uncited open finding.
mkf 4401 <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"First cited finding here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","kb":["r-a1"],"sig":"$F1","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"addressed","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":3,"anchored":true,"body":"Second cited finding here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","kb":["r-a2"],"sig":"$F2","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"false-positive","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":4,"anchored":true,"body":"Uncited finding here for real.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"$F3","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
cat > "$EVAL_SANDBOX/.review/4401/kb-recall.json" <<'EOF'
[{"id":"r-a1","score":1,"hits":2,"judged":2},{"id":"r-a2","score":0.5,"hits":0,"judged":0}]
EOF

# Bundle 4402: recalls r-a2 + r-b1; cites r-a2 (addressed) and an invented
# rule r-out (open, never recalled). Carries a post.json so calibrate could
# settle it live.
mkf 4402 <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Adopted twin cited finding here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","kb":["r-a2"],"sig":"$G1","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"addressed","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":3,"anchored":true,"body":"Invented cite outside recall here.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":["r-out"],"sig":"$G2","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
cat > "$EVAL_SANDBOX/.review/4402/kb-recall.json" <<'EOF'
[{"id":"r-a2","score":0.5,"hits":0,"judged":0},{"id":"r-b1","score":0.5,"hits":0,"judged":0}]
EOF
cat > "$EVAL_SANDBOX/.review/4402/post.json" <<'EOF'
{"schema_version":"1","review":{"id":424242,"state":"COMMENTED"},"head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","posted_at":"2026-10-31T00:00:00Z","confidence":null,"security_summary":null}
EOF

# Bundle 4403: no manifest (unmeasured) with a cited addressed finding.
mkf 4403 <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Old bundle cited finding here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","kb":["r-a1"],"sig":"$H1","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"addressed","rationale":"","history":[]}]
EOF

# Bundle 4404: empty manifest (measured, nothing recalled) with one cite.
mkf 4404 <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Empty recall cited finding here.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":["r-d1"],"sig":"$I1","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
echo "[]" > "$EVAL_SANDBOX/.review/4404/kb-recall.json"

# Bundle 4405: recalls r-e1, cites nothing.
mkf 4405 <<EOF
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Uncited bundle finding here now.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"$J1","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
cat > "$EVAL_SANDBOX/.review/4405/kb-recall.json" <<'EOF'
[{"id":"r-e1","score":0.5,"hits":0,"judged":0}]
EOF

# Joins ledger: two credited flips on 4401, one adopted flip on 4402.
cat > "$EVAL_SANDBOX/.review/kb-joins.jsonl" <<EOF
{"at":"2026-10-31T00:00:01Z","sig":"$F1","rule":"r-a1","outcome":"addressed","actor":"human","verb":"triage","bundle":"4401","review_id":null}
{"at":"2026-10-31T00:00:02Z","sig":"$F2","rule":"r-a2","outcome":"false-positive","actor":"human","verb":"triage","bundle":"4401","review_id":null}
{"at":"2026-10-31T00:00:03Z","sig":"$G1","rule":"r-a2","outcome":"addressed","actor":"human","verb":"triage","bundle":"4402","review_id":424242}
EOF

# Fixture hashes + stub count before the audit runs (O5 pure-read proof).
before_hash="$(cd "$EVAL_SANDBOX" && find .review -type f | LC_ALL=C sort | xargs shasum -a 1)"
stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

run_review trend acme/app --overlap --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "trend --overlap exits 0"
else
  bad "trend --overlap exits $EVAL_STATUS"
fi

# --- O1: the three binding numbers ---
if jq -e '.data.overlap.precision == 0.5 and .data.overlap.coverage == 0.5' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "precision 0.5 (2/4) and coverage 0.5 (2/4)"
else
  bad "precision/coverage: $(jq -c '.data.overlap | {precision, coverage}' <"$EVAL_OUT" 2>/dev/null)"
fi
if jq -e '.data.overlap.recalled_rules == ["r-a1","r-a2","r-b1","r-e1"]
  and .data.overlap.cited_rules == ["r-a1","r-a2","r-d1","r-out"]
  and .data.overlap.overlap_rules == ["r-a1","r-a2"]' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "rule sets: recalled 4, cited 4 (measured only), overlap 2"
else
  bad "rule sets: $(jq -c '.data.overlap | {recalled_rules, cited_rules, overlap_rules}' <"$EVAL_OUT" 2>/dev/null)"
fi
if jq -e '.data.overlap.cited_findings == 6 and .data.overlap.addressed == 3
  and .data.overlap.false_positive == 1 and .data.overlap.pending == 2
  and .data.overlap.wont_fix == 0' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "outcome split: 6 cited — addressed 3, false-positive 1, pending 2"
else
  bad "outcome split: $(jq -c '.data.overlap | {cited_findings, addressed, false_positive, pending, wont_fix}' <"$EVAL_OUT" 2>/dev/null)"
fi
if jq -e '.data.overlap.joins == {rows: 3, addressed: 2, false_positive: 1}' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "joins corroboration: 3 rows (addressed 2, false-positive 1)"
else
  bad "joins: $(jq -c '.data.overlap.joins' <"$EVAL_OUT" 2>/dev/null)"
fi
if jq -e '.data.overlap.measured_bundles == 4
  and .data.overlap.unmeasured_bundles == 1
  and .data.overlap.unmeasured == ["4403"]
  and .data.overlap.calibrate_eligible_bundles == 1' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "measured 4 / unmeasured [4403]; one calibrate-eligible bundle"
else
  bad "bundle counts: $(jq -c '.data.overlap | {measured_bundles, unmeasured_bundles, unmeasured, calibrate_eligible_bundles}' <"$EVAL_OUT" 2>/dev/null)"
fi
if jq -e '.data.overlap.per_bundle | length == 5' <"$EVAL_OUT" >/dev/null 2>&1 \
  && jq -e '.data.overlap.per_bundle[] | select(.bundle == "4401") | .measured and .precision == 1 and .coverage == 1 and .joins_rows == 2' <"$EVAL_OUT" >/dev/null 2>&1 \
  && jq -e '.data.overlap.per_bundle[] | select(.bundle == "4402") | .precision == 0.5 and .coverage == 0.5 and .calibrate_eligible and .joins_rows == 1' <"$EVAL_OUT" >/dev/null 2>&1 \
  && jq -e '.data.overlap.per_bundle[] | select(.bundle == "4403") | (.measured | not) and .precision == null and .coverage == null and .addressed == 1' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "per-bundle rows: 4401 full marks, 4402 halves + eligible, 4403 unmeasured but counted"
else
  bad "per-bundle rows: $(jq -c '.data.overlap.per_bundle' <"$EVAL_OUT" 2>/dev/null)"
fi

# --- O2: empty-manifest + uncited-bundle edges ---
if jq -e '.data.overlap.per_bundle[] | select(.bundle == "4404") | .measured and .recalled == [] and .precision == null and .coverage == 0' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "empty manifest: measured, precision null, coverage 0"
else
  bad "4404 row: $(jq -c '.data.overlap.per_bundle[] | select(.bundle == "4404")' <"$EVAL_OUT" 2>/dev/null)"
fi
if jq -e '.data.overlap.per_bundle[] | select(.bundle == "4405") | .measured and .cited == [] and .precision == 0 and .coverage == null' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "uncited bundle: measured, precision 0, coverage null"
else
  bad "4405 row: $(jq -c '.data.overlap.per_bundle[] | select(.bundle == "4405")' <"$EVAL_OUT" 2>/dev/null)"
fi

# Human section carries the same three numbers.
run_review trend acme/app --overlap
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -qF "overlap: 4 measured, 1 unmeasured" "$EVAL_OUT" \
  && grep -qF "precision=0.5 coverage=0.5" "$EVAL_OUT" \
  && grep -qF "cited outcomes: 6 cited findings" "$EVAL_OUT"; then
  good "human section mirrors the three numbers"
else
  bad "human overlap section missing (exit $EVAL_STATUS)"
fi

# --- O3: default trend byte-identical without the flag ---
run_review trend acme/app --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && ! jq -e '.data | has("overlap")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "default envelope carries no overlap key"
else
  bad "default envelope grew an overlap key (exit $EVAL_STATUS)"
fi
run_review trend acme/app
if [ "$EVAL_STATUS" -eq 0 ] && ! grep -q "overlap:" "$EVAL_OUT"; then
  good "default human output has no overlap section"
else
  bad "default human output grew an overlap section"
fi

# --- O5: pure read (hashes stable, no network, corrupt joins warns+zeros) ---
after_hash="$(cd "$EVAL_SANDBOX" && find .review -type f | LC_ALL=C sort | xargs shasum -a 1)"
if [ "$before_hash" = "$after_hash" ]; then
  good "audit writes nothing (fixture bytes identical)"
else
  bad "fixture bytes moved under a pure read"
fi
stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
if [ "$stub_before" = "$stub_after" ]; then
  good "zero gh interceptions across the audit runs"
else
  bad "audit touched the network ($stub_before -> $stub_after)"
fi
echo "not jsonl at all" > "$EVAL_SANDBOX/.review/kb-joins.jsonl"
run_review trend acme/app --overlap --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.data.overlap.joins.rows == 0' <"$EVAL_OUT" >/dev/null 2>&1 \
  && grep -q "kb joins .review/kb-joins.jsonl unreadable" "$EVAL_ERR"; then
  good "corrupt joins warns and reads zeros (never fails)"
else
  bad "corrupt joins handling broke (exit $EVAL_STATUS)"
fi
cat > "$EVAL_SANDBOX/.review/kb-joins.jsonl" <<EOF
{"at":"2026-10-31T00:00:01Z","sig":"$F1","rule":"r-a1","outcome":"addressed","actor":"human","verb":"triage","bundle":"4401","review_id":null}
{"at":"2026-10-31T00:00:02Z","sig":"$F2","rule":"r-a2","outcome":"false-positive","actor":"human","verb":"triage","bundle":"4401","review_id":null}
{"at":"2026-10-31T00:00:03Z","sig":"$G1","rule":"r-a2","outcome":"addressed","actor":"human","verb":"triage","bundle":"4402","review_id":424242}
EOF

# --- O4: no-manifest-at-all edge ---
rm -f "$EVAL_SANDBOX"/.review/*/kb-recall.json
run_review trend acme/app --overlap --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.data.overlap.precision == null and .data.overlap.coverage == null
    and .data.overlap.measured_bundles == 0 and .data.overlap.unmeasured_bundles == 5
    and .data.overlap.cited_findings == 6 and .data.overlap.addressed == 3
    and .data.overlap.false_positive == 1' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "no manifests: precision/coverage null (never zero), split still counted"
else
  bad "no-manifest edge: $(jq -c '.data.overlap | {precision, coverage, measured_bundles, cited_findings, addressed}' <"$EVAL_OUT" 2>/dev/null)"
fi

# --- O6: empty store ---
mv "$EVAL_SANDBOX/.review" "$EVAL_SANDBOX/.review.hold"
mkdir -p "$EVAL_SANDBOX/.review"
run_review trend acme/app --overlap --json
empty_ok="false"
if [ "$EVAL_STATUS" -eq 0 ] \
  && jq -e '.data.overlap.bundles == 0 and .data.overlap.precision == null
    and .data.overlap.coverage == null and .data.overlap.cited_findings == 0
    and .data.overlap.joins.rows == 0' <"$EVAL_OUT" >/dev/null 2>&1; then
  empty_ok="true"
fi
rmdir "$EVAL_SANDBOX/.review"
mv "$EVAL_SANDBOX/.review.hold" "$EVAL_SANDBOX/.review"
if [ "$empty_ok" = "true" ]; then
  good "empty store: nulls + zeros, never an error"
else
  bad "empty store edge broke"
fi

if [ "$fails" -ne 0 ]; then echo "case kboverlap: FAIL ($fails)"; exit 1; fi
echo "case kboverlap: PASS"
