package coverage

import (
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/hunks"
)

// incrementalRows is the two-file fixture every incremental case shares:
// src/prior.ts 1-3 sits outside the delta, src/delta.ts 10-12 is the delta.
// Six changed lines total; `all` on one file attests three.
func incrementalRows() []hunks.Hunk {
	return []hunks.Hunk{
		{File: "src/prior.ts", Side: "NEW", Start: 1, End: 3},
		{File: "src/delta.ts", Side: "NEW", Start: 10, End: 12},
	}
}

// incrementalDiff touches only the delta file: the marker that turns the
// bundle into an incremental one. Its hunk lines are a subset of the full
// snapshot, so the full snapshot stays the denominator under test.
const incrementalDiff = "diff --git a/src/delta.ts b/src/delta.ts\n" +
	"@@ -10,3 +10,3 @@\n top\n-old line\n+new line\n bottom\n"

// priorAll records a prior round that attested the whole prior file.
const priorAll = `[{"file":"src/prior.ts","ranges":"1-3"}]`

// writeIncrementalBundle lays out the full snapshot and review, then puts
// the delta marker and the recorded prior (possibly empty) beside them.
func writeIncrementalBundle(t *testing.T, review, prior string) string {
	t.Helper()
	dir := writeBundle(t, incrementalRows())
	writeFile(t, dir+"/review.json", review)
	writeFile(t, dir+"/incremental.diff", incrementalDiff)
	if prior != "" {
		writeFile(t, dir+"/prior-coverage.json", prior)
	}
	return dir
}

// TestEvaluateIncrementalCreditsPrior proves the two STA-373 legs in one
// bundle: the prior range outside the delta is credited from the recorded
// attestation, so attesting the delta alone measures 100% — while the same
// attestation without the delta would leave the delta gap.
func TestEvaluateIncrementalCreditsPrior(t *testing.T) {
	dir := writeIncrementalBundle(t, `{"coverage":[{"file":"src/delta.ts","ranges":"10-12"}]}`, priorAll)
	got, warns, ref := Evaluate(dir, mustReview(t, `{"coverage":[{"file":"src/delta.ts","ranges":"10-12"}]}`), notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	if got.Attested != 6 || got.Total != 6 {
		t.Fatalf("attested/total = %d/%d, want 6/6 (prior credited)", got.Attested, got.Total)
	}
	if pct(&got) != "100" {
		t.Fatalf("percent = %v, want 100", pct(&got))
	}
	if got.PriorLines != 3 {
		t.Fatalf("prior credited = %d lines, want 3", got.PriorLines)
	}
	if !got.Incremental {
		t.Fatal("Incremental = false on a delta bundle")
	}
	assertWarnContains(t, warns, "carried 3 line(s)")
}

// assertWarnContains fails unless one warning names substr, so the credit
// and fail-closed cases pin the operator line, not just the number.
func assertWarnContains(t *testing.T, warns []string, substr string) {
	t.Helper()
	for _, w := range warns {
		if strings.Contains(w, substr) {
			return
		}
	}
	t.Fatalf("warnings = %v, want one containing %q", warns, substr)
}

// TestEvaluateIncrementalRefusesUnattestedDelta proves leg (a): an
// incremental range in the delta diff must be attested by the agent. With
// only a prior file in the bundle, the delta lines stay gaps and the
// percentage cannot reach 100%.
func TestEvaluateIncrementalRefusesUnattestedDelta(t *testing.T) {
	dir := writeIncrementalBundle(t, `{"coverage":[]}`, priorAll)
	got, _, ref := Evaluate(dir, mustReview(t, `{"coverage":[]}`), notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	if got.Attested != 3 || got.Total != 6 {
		t.Fatalf("attested/total = %d/%d, want 3/6 (prior only)", got.Attested, got.Total)
	}
	if pct(&got) != "50" {
		t.Fatalf("percent = %v, want 50", pct(&got))
	}
	if gaps := gapMap(&got); gaps["src/delta.ts"] != "10-12" {
		t.Fatalf("delta gap = %q, want 10-12", gaps["src/delta.ts"])
	}
}

// TestEvaluateIncrementalFailsClosedWithoutPrior proves an unrecorded prior
// gets no credit: the same delta-only attestation that passes with a
// recorded prior measures 50% here, because nothing was ever recorded.
func TestEvaluateIncrementalFailsClosedWithoutPrior(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prior  string
		needle string
	}{
		{name: "absent", prior: "", needle: "coverage: incremental bundle has no recorded prior coverage — grading against full hunks"},
		{name: "empty-array", prior: "[]", needle: "coverage: incremental bundle has no recorded prior coverage — grading against full hunks"},
		{name: "unreadable", prior: "{broken", needle: "coverage: recorded prior coverage unreadable — no prior ranges credited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertFailsClosed(t, tc.prior, tc.needle)
		})
	}
}

// assertFailsClosed grades one delta-only attestation that must earn no
// credit: 3 of 6 lines at 50% plus the expected warning, asserted as a
// full literal so mutating the warning text fails the test.
func assertFailsClosed(t *testing.T, prior, needle string) {
	t.Helper()
	dir := writeIncrementalBundle(t, `{"coverage":[{"file":"src/delta.ts","ranges":"10-12"}]}`, prior)
	got, warns, ref := Evaluate(dir, mustReview(t, `{"coverage":[{"file":"src/delta.ts","ranges":"10-12"}]}`), notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	if got.Attested != 3 || got.Total != 6 {
		t.Fatalf("attested/total = %d/%d, want 3/6 (no credit)", got.Attested, got.Total)
	}
	if pct(&got) != "50" {
		t.Fatalf("percent = %v, want 50", pct(&got))
	}
	assertWarnContains(t, warns, needle)
}

// TestEvaluateFullIgnoresPrior proves full-diff rounds are unchanged: with
// no delta marker the recorded prior credits nothing, so the delta-only
// attestation still measures 50%.
func TestEvaluateFullIgnoresPrior(t *testing.T) {
	dir := writeBundle(t, incrementalRows())
	writeFile(t, dir+"/prior-coverage.json", priorAll)
	got, _, ref := Evaluate(dir, mustReview(t, `{"coverage":[{"file":"src/delta.ts","ranges":"10-12"}]}`), notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	if got.Attested != 3 || pct(&got) != "50" {
		t.Fatalf("attested/percent = %d/%v, want 3/50 (prior ignored)", got.Attested, pct(&got))
	}
	if got.Incremental {
		t.Fatal("Incremental = true on a full bundle")
	}
	if got.PriorLines != 0 {
		t.Fatalf("prior credited = %d, want 0 on a full bundle", got.PriorLines)
	}
}

// TestEvaluateIncrementalPriorPartialLeavesGap proves a prior round that
// never covered a range leaves it uncovered: the prior attests 1-2 of the
// prior file, the agent attests the delta, and 3 remains the gap.
func TestEvaluateIncrementalPriorPartialLeavesGap(t *testing.T) {
	dir := writeIncrementalBundle(t,
		`{"coverage":[{"file":"src/delta.ts","ranges":"10-12"}]}`,
		`[{"file":"src/prior.ts","ranges":"1-2"}]`)
	got, _, ref := Evaluate(dir, mustReview(t, `{"coverage":[{"file":"src/delta.ts","ranges":"10-12"}]}`), notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	if got.Attested != 5 || pct(&got) != "83" {
		t.Fatalf("attested/percent = %d/%v, want 5/83", got.Attested, pct(&got))
	}
	if gaps := gapMap(&got); gaps["src/prior.ts"] != "3" {
		t.Fatalf("prior gap = %q, want 3", gaps["src/prior.ts"])
	}
}

// TestPersistPriorRoundTrips proves record's saved attestation is exactly
// what the next round credits: a summary evaluated over the delta-only
// claim persists both files, and a fresh bundle that records both reads
// back 100% without either ranging over the prior file by the agent.
func TestPersistPriorRoundTrips(t *testing.T) {
	dir := writeBundle(t, incrementalRows())
	got, _, ref := Evaluate(dir, mustReview(t,
		`{"coverage":[{"file":"src/prior.ts","ranges":"1-3"},{"file":"src/delta.ts","ranges":"10-12"}]}`), notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	if err := PersistPrior(dir, &got); err != nil {
		t.Fatalf("PersistPrior: %v", err)
	}
	writeFile(t, dir+"/incremental.diff", incrementalDiff)
	next, _, ref := Evaluate(dir, mustReview(t, `{"coverage":[{"file":"src/delta.ts","ranges":"10-12"}]}`), notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	if pct(&next) != "100" {
		t.Fatalf("next percent = %v, want 100 from the round-tripped prior", pct(&next))
	}
}

// writeOverlapBundle lays out a single-file snapshot with the given hunk
// rows, the delta marker, and the recorded prior (possibly empty) beside
// them, for the prior∩delta overlap cases.
func writeOverlapBundle(t *testing.T, rows []hunks.Hunk, delta, review, prior string) string {
	t.Helper()
	dir := writeBundle(t, rows)
	writeFile(t, dir+"/review.json", review)
	writeFile(t, dir+"/incremental.diff", delta)
	if prior != "" {
		writeFile(t, dir+"/prior-coverage.json", prior)
	}
	return dir
}

// assertCounts grades one overlap summary's four numbers.
func assertCounts(t *testing.T, got *Summary, attested, total int, percent string, priorLines int) {
	t.Helper()
	if got.Attested != attested || got.Total != total {
		t.Fatalf("attested/total = %d/%d, want %d/%d", got.Attested, got.Total, attested, total)
	}
	if pct(got) != percent {
		t.Fatalf("percent = %v, want %v", pct(got), percent)
	}
	if got.PriorLines != priorLines {
		t.Fatalf("prior credited = %d lines, want %d", got.PriorLines, priorLines)
	}
}

// assertGap grades one overlap summary's gap for a single file.
func assertGap(t *testing.T, got *Summary, file, want string) {
	t.Helper()
	if gaps := gapMap(got); gaps[file] != want {
		t.Fatalf("gap for %s = %q, want %q", file, gaps[file], want)
	}
}

// assertNoCarriedWarn fails if any warning names carried prior credit.
func assertNoCarriedWarn(t *testing.T, warns []string) {
	t.Helper()
	for _, w := range warns {
		if strings.Contains(w, "carried ") {
			t.Fatalf("warnings = %v, want no carried-credit line when nothing carried", warns)
		}
	}
}

// TestEvaluateIncrementalFullOverlapInvalidatesPrior proves the C1 fix: when
// the delta re-modifies exactly the lines the prior round attested, none of
// that credit survives. The prior covers src/overlap.ts 10-12, the delta
// re-modifies 10-12, and an empty attestation leaves the whole file as a gap
// with zero prior lines carried.
func TestEvaluateIncrementalFullOverlapInvalidatesPrior(t *testing.T) {
	rows := []hunks.Hunk{{File: "src/overlap.ts", Side: "NEW", Start: 10, End: 12}}
	delta := "diff --git a/src/overlap.ts b/src/overlap.ts\n" +
		"@@ -10,3 +10,3 @@\n top\n-old line\n+new line\n bottom\n"
	prior := `[{"file":"src/overlap.ts","ranges":"10-12"}]`
	dir := writeOverlapBundle(t, rows, delta, `{"coverage":[]}`, prior)
	got, warns, ref := Evaluate(dir, mustReview(t, `{"coverage":[]}`), notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	assertCounts(t, &got, 0, 3, "0", 0)
	assertGap(t, &got, "src/overlap.ts", "10-12")
	assertNoCarriedWarn(t, warns)
}

// TestEvaluateIncrementalPartialOverlapCarriesUnchanged proves the C1 fix's
// precision: prior 1-10 with the delta re-modifying only 5-7 carries 1-4
// and 8-10 (7 lines) while 5-7 stays a gap under an empty attestation, and
// re-attesting 5-7 reaches 100%. The delta's OLD side is 3-7 — subtracting
// OLD as well would drop to 5 carried lines, so 7 proves only the NEW side
// invalidates.
func TestEvaluateIncrementalPartialOverlapCarriesUnchanged(t *testing.T) {
	rows := []hunks.Hunk{{File: "src/overlap.ts", Side: "NEW", Start: 1, End: 10}}
	delta := "diff --git a/src/overlap.ts b/src/overlap.ts\n" +
		"@@ -3,5 +5,3 @@\n top\n-old line\n+new line\n bottom\n"
	prior := `[{"file":"src/overlap.ts","ranges":"1-10"}]`
	dir := writeOverlapBundle(t, rows, delta, `{"coverage":[]}`, prior)
	got, _, ref := Evaluate(dir, mustReview(t, `{"coverage":[]}`), notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	assertCounts(t, &got, 7, 10, "70", 7)
	assertGap(t, &got, "src/overlap.ts", "5-7")
	re := writeOverlapBundle(t, rows, delta, `{"coverage":[{"file":"src/overlap.ts","ranges":"5-7"}]}`, prior)
	full, _, ref := Evaluate(re, mustReview(t, `{"coverage":[{"file":"src/overlap.ts","ranges":"5-7"}]}`), notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	assertCounts(t, &full, 10, 10, "100", 7)
}

// assertWrongPriorCase grades one delta-only attestation over a corrupt
// prior: no credit, 3 of 6 at 50%, and the expected literal warning.
func assertWrongPriorCase(t *testing.T, prior, needle string) {
	t.Helper()
	dir := writeIncrementalBundle(t, `{"coverage":[{"file":"src/delta.ts","ranges":"10-12"}]}`, prior)
	got, warns, ref := Evaluate(dir, mustReview(t, `{"coverage":[{"file":"src/delta.ts","ranges":"10-12"}]}`), notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	if got.Attested != 3 || got.Total != 6 || pct(&got) != "50" {
		t.Fatalf("attested/total/percent = %d/%d/%v, want 3/6/50 (corrupt prior credits nothing)",
			got.Attested, got.Total, pct(&got))
	}
	if got.PriorLines != 0 {
		t.Fatalf("prior credited = %d lines, want 0", got.PriorLines)
	}
	assertWarnContains(t, warns, needle)
}

// TestEvaluateIncrementalWrongPriorShapesFailClosed proves a corrupt or
// off-snapshot prior credits nothing: each malformed file still leaves the
// delta-only attestation at 50% with its own literal warning. The missing,
// empty, and "all" ranges cases also prove the old whole-file expansion is
// gone — a machine-written file never widens that way.
func TestEvaluateIncrementalWrongPriorShapesFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prior  string
		needle string
	}{
		{name: "object", prior: "{}", needle: "coverage: recorded prior coverage unreadable — no prior ranges credited"},
		{name: "null", prior: "null", needle: "coverage: recorded prior coverage unreadable — no prior ranges credited"},
		{name: "string", prior: `"str"`, needle: "coverage: recorded prior coverage unreadable — no prior ranges credited"},
		{name: "missing-ranges", prior: `[{"file":"src/prior.ts"}]`, needle: "coverage: prior entry 0 has no ranges — ignored"},
		{name: "empty-ranges", prior: `[{"file":"src/prior.ts","ranges":""}]`, needle: "coverage: prior entry 0 has no ranges — ignored"},
		{name: "all-ranges", prior: `[{"file":"src/prior.ts","ranges":"all"}]`, needle: "coverage: prior entry 0 has no ranges — ignored"},
		{name: "unknown-file", prior: `[{"file":"src/gone.ts","ranges":"1-3"}]`, needle: `coverage: prior entry 0 names "src/gone.ts" with no hunks — ignored`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertWrongPriorCase(t, tc.prior, tc.needle)
		})
	}
}

// TestPersistPriorChunkedLeavesPriorUntouched proves a chunked round never
// rewrites the recorded prior: chunks[] is its attestation, and chain state
// stays untouched even when the chunked summary carries attested ranges.
// The chunked guard and the empty guard both leave the literal bytes alone.
func TestPersistPriorChunkedLeavesPriorUntouched(t *testing.T) {
	dir := writeBundle(t, incrementalRows())
	const literal = `[{"file":"src/prior.ts","ranges":"1-3"}]`
	writeFile(t, dir+"/prior-coverage.json", literal+"\n")
	chunked := &Summary{Chunked: true, AttestedRanges: []Gap{{File: "src/prior.ts", Ranges: "1-3"}}}
	if err := PersistPrior(dir, chunked); err != nil {
		t.Fatalf("PersistPrior chunked: %v", err)
	}
	raw, err := os.ReadFile(dir + "/prior-coverage.json")
	if err != nil {
		t.Fatalf("read prior after chunked persist: %v", err)
	}
	if string(raw) != literal+"\n" {
		t.Errorf("prior after chunked persist = %q, want the chunked round to leave it untouched", raw)
	}
	empty := &Summary{}
	if err := PersistPrior(dir, empty); err != nil {
		t.Fatalf("PersistPrior empty: %v", err)
	}
	raw, err = os.ReadFile(dir + "/prior-coverage.json")
	if err != nil {
		t.Fatalf("read prior after empty persist: %v", err)
	}
	if string(raw) != literal+"\n" {
		t.Errorf("prior after empty persist = %q, want an empty attestation to leave it untouched", raw)
	}
}
