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

// TestEvaluateIncrementalFullOverlapInvalidatesPrior proves the coordinate
// rule at full overlap: when the delta re-modifies exactly the lines the prior
// round attested, none of that credit survives, because a re-modified line has
// no current-head counterpart to map to. The prior covers src/overlap.ts
// 10-12, the delta re-modifies 10-12, and an empty attestation leaves the
// whole file a gap with zero prior lines carried.
func TestEvaluateIncrementalFullOverlapInvalidatesPrior(t *testing.T) {
	rows := []hunks.Hunk{{File: "src/overlap.ts", Side: "NEW", Start: 10, End: 12}}
	delta := "diff --git a/src/overlap.ts b/src/overlap.ts\n" +
		"@@ -10,3 +10,3 @@\n-a\n-b\n-c\n+x\n+y\n+z\n"
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

// TestEvaluateIncrementalPartialOverlapCarriesUnchanged proves the same rule's
// precision: prior 1-10 with the delta re-modifying only 5-7 carries 1-4 and
// 8-10 (7 lines) while 5-7 stays a gap under an empty attestation, and
// re-attesting 5-7 reaches 100%. The context lines either side of the rewrite
// are what the translation keeps; the removed 5-7 are what it drops.
func TestEvaluateIncrementalPartialOverlapCarriesUnchanged(t *testing.T) {
	rows := []hunks.Hunk{{File: "src/overlap.ts", Side: "NEW", Start: 1, End: 10}}
	delta := "diff --git a/src/overlap.ts b/src/overlap.ts\n" +
		"@@ -3,7 +3,7 @@\n c3\n c4\n-o5\n-o6\n-o7\n+n5\n+n6\n+n7\n c8\n c9\n"
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

// priorLiteral is the recorded prior two files share in the persistence and
// translation cases: a.ts is the file the delta re-modifies and b.ts is the
// file it leaves alone.
const priorLiteral = `[{"file":"a.ts","ranges":"1-3"},{"file":"b.ts","ranges":"1-3"}]`

// aOnlyPrior is the recorded prior that a re-modifying delta wipes out.
const aOnlyPrior = `[{"file":"a.ts","ranges":"1-3"}]`

// rewriteDelta re-modifies a.ts 1-3 in place: a well-formed hunk that removes
// all three lines and adds three new ones, so no prior credit on them survives
// — a rewrite that only replaced the middle line would leave its context lines
// legitimately credited.
const rewriteDelta = "diff --git a/a.ts b/a.ts\n" +
	"@@ -1,3 +1,3 @@\n-o1\n-o2\n-o3\n+n1\n+n2\n+n3\n"

// readPriorText reads prior-coverage.json, failing the test when the file is
// gone: a case that expects removal checks with absent() instead.
func readPriorText(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(dir + "/prior-coverage.json")
	if err != nil {
		t.Fatalf("read prior-coverage.json: %v", err)
	}
	return string(raw)
}

// assertAbsent fails unless the file at path does not exist.
func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s exists (err=%v), want it removed", path, err)
	}
}

// TestPersistPriorEmptyRoundDropsStaleCredit proves an empty non-chunked round
// removes the recorded prior: AttestedRanges already carries whatever survived
// the delta, so an empty union means nothing is validly attested and an older
// round's numbers must not credit lines this round re-modified.
func TestPersistPriorEmptyRoundDropsStaleCredit(t *testing.T) {
	dir := writeBundle(t, incrementalRows())
	writeFile(t, dir+"/prior-coverage.json", priorLiteral+"\n")
	if err := PersistPrior(dir, &Summary{}); err != nil {
		t.Fatalf("PersistPrior empty: %v", err)
	}
	assertAbsent(t, dir+"/prior-coverage.json")
	// Removing a prior that is already gone is the expected case, not an error.
	if err := PersistPrior(dir, &Summary{}); err != nil {
		t.Fatalf("PersistPrior empty with no prior: %v", err)
	}
}

// TestPersistPriorWritesAttestedUnion proves a non-chunked round writes the
// union it graded, so unchanged prior lines keep their credit across rounds.
func TestPersistPriorWritesAttestedUnion(t *testing.T) {
	dir := writeBundle(t, incrementalRows())
	writeFile(t, dir+"/prior-coverage.json", priorLiteral+"\n")
	sum := &Summary{AttestedRanges: []Gap{{File: "b.ts", Ranges: "1-3"}}}
	if err := PersistPrior(dir, sum); err != nil {
		t.Fatalf("PersistPrior: %v", err)
	}
	want := `[
  {
    "file": "b.ts",
    "ranges": "1-3"
  }
]
`
	if got := readPriorText(t, dir); got != want {
		t.Errorf("prior = %q, want %q", got, want)
	}
}

// TestPersistPriorChunkedFullRoundClears proves a chunked round with no delta
// drops the recorded prior: its chunks[] attestation says nothing about lines,
// and an earlier head's credit must not leak into the next grading.
func TestPersistPriorChunkedFullRoundClears(t *testing.T) {
	dir := writeBundle(t, incrementalRows())
	writeFile(t, dir+"/prior-coverage.json", priorLiteral+"\n")
	if err := PersistPrior(dir, &Summary{Chunked: true, Chunks: 2, ChunksAttested: 2}); err != nil {
		t.Fatalf("PersistPrior chunked: %v", err)
	}
	assertAbsent(t, dir+"/prior-coverage.json")
}

// TestPersistPriorChunkedIncrementalTranslates proves an incremental chunked
// round rewrites the recorded prior through its delta: the re-modified file's
// credit goes, the untouched file's stays, so the next round credits only what
// this head still holds.
func TestPersistPriorChunkedIncrementalTranslates(t *testing.T) {
	dir := writeBundle(t, incrementalRows())
	writeFile(t, dir+"/prior-coverage.json", priorLiteral+"\n")
	writeFile(t, dir+"/incremental.diff", rewriteDelta)
	if err := PersistPrior(dir, &Summary{Chunked: true, Chunks: 2, ChunksAttested: 1}); err != nil {
		t.Fatalf("PersistPrior chunked incremental: %v", err)
	}
	want := `[
  {
    "file": "b.ts",
    "ranges": "1-3"
  }
]
`
	if got := readPriorText(t, dir); got != want {
		t.Errorf("prior = %q, want %q (a.ts credit dropped, b.ts kept)", got, want)
	}
}

// TestPersistPriorChunkedIncrementalAllInvalidatedClears proves the same path
// removes the file when the delta invalidates every recorded line.
func TestPersistPriorChunkedIncrementalAllInvalidatedClears(t *testing.T) {
	dir := writeBundle(t, incrementalRows())
	writeFile(t, dir+"/prior-coverage.json", aOnlyPrior+"\n")
	writeFile(t, dir+"/incremental.diff", rewriteDelta)
	if err := PersistPrior(dir, &Summary{Chunked: true, Chunks: 1, ChunksAttested: 1}); err != nil {
		t.Fatalf("PersistPrior chunked incremental: %v", err)
	}
	assertAbsent(t, dir+"/prior-coverage.json")
}

// TestPersistPriorChunkedCorruptPriorClears proves a prior the persistence
// path cannot read is removed rather than left for the next round to trust.
func TestPersistPriorChunkedCorruptPriorClears(t *testing.T) {
	dir := writeBundle(t, incrementalRows())
	writeFile(t, dir+"/prior-coverage.json", `[{"file":"a.ts","ranges":"all"}]`)
	writeFile(t, dir+"/incremental.diff", rewriteDelta)
	if err := PersistPrior(dir, &Summary{Chunked: true, Chunks: 1, ChunksAttested: 1}); err != nil {
		t.Fatalf("PersistPrior chunked incremental: %v", err)
	}
	assertAbsent(t, dir+"/prior-coverage.json")
}

// shiftCase is one coordinate-translation fixture: the rows the current head's
// snapshot holds, the delta from the prior head, the recorded prior, and the
// summary the translation must produce.
func shiftCase(t *testing.T, rows []hunks.Hunk, delta, prior string) (Summary, []string) {
	t.Helper()
	dir := writeBundle(t, rows)
	writeFile(t, dir+"/incremental.diff", delta)
	if prior != "" {
		writeFile(t, dir+"/prior-coverage.json", prior)
	}
	got, warns, ref := Evaluate(dir, mustReview(t, `{"coverage":[]}`), notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	return got, warns
}

// TestEvaluateIncrementalInsertionShiftsPrior proves the translation across an
// insertion: one line added above line 1 moves prior 10-12 to 11-13, so the
// attested range follows its content instead of staying pinned to line
// numbers. Line 10 is the proof — it is the line an untranslated prior would
// have falsely credited.
func TestEvaluateIncrementalInsertionShiftsPrior(t *testing.T) {
	delta := "diff --git a/src/prior.ts b/src/prior.ts\n" +
		"@@ -0,0 +1,1 @@\n+inserted\n"
	got, warns := shiftCase(t, []hunks.Hunk{{File: "src/prior.ts", Side: "NEW", Start: 10, End: 13}}, delta, aTenTo12)
	assertCounts(t, &got, 3, 4, "75", 3)
	assertGap(t, &got, "src/prior.ts", "10")
	assertWarnContains(t, warns, "carried 3 line(s)")
}

// aTenTo12 is the recorded prior the coordinate cases share: lines 10-12 of the
// previous head's src/prior.ts.
const aTenTo12 = `[{"file":"src/prior.ts","ranges":"10-12"}]`

// TestEvaluateIncrementalDeletionShiftsPrior proves the translation across a
// deletion: removing line 5 moves prior 10-12 up to 9-11, so line 12 is the
// unattested line and 9-11 the credit.
func TestEvaluateIncrementalDeletionShiftsPrior(t *testing.T) {
	delta := "diff --git a/src/prior.ts b/src/prior.ts\n" +
		"@@ -5,1 +4,0 @@\n-gone\n"
	got, _ := shiftCase(t, []hunks.Hunk{{File: "src/prior.ts", Side: "NEW", Start: 9, End: 12}}, delta, aTenTo12)
	assertCounts(t, &got, 3, 4, "75", 3)
	assertGap(t, &got, "src/prior.ts", "12")
}

// TestEvaluateIncrementalHunkDropsRewrittenLines proves the inside-hunk rule:
// a line the delta rewrites carries no credit, while the context lines after it
// still shift by the hunk's net growth. Prior 4-6 maps to 4 and 7: line 5 was
// rewritten, line 6 became 7.
func TestEvaluateIncrementalHunkDropsRewrittenLines(t *testing.T) {
	delta := "diff --git a/src/prior.ts b/src/prior.ts\n" +
		"@@ -4,3 +4,4 @@\n c4\n-o5\n+n5\n+n6\n c6\n"
	got, _ := shiftCase(t, []hunks.Hunk{{File: "src/prior.ts", Side: "NEW", Start: 4, End: 7}}, delta,
		`[{"file":"src/prior.ts","ranges":"4-6"}]`)
	assertCounts(t, &got, 2, 4, "50", 2)
	assertGap(t, &got, "src/prior.ts", "5-6")
	if got.AttestedRanges[0].Ranges != "4,7" {
		t.Errorf("persisted ranges = %q, want %q", got.AttestedRanges[0].Ranges, "4,7")
	}
}

// TestEvaluateIncrementalUntouchedFileCarries proves a prior file the delta
// never names keeps its own coordinates: the translation is per file, so an
// insertion in another file cannot move it.
func TestEvaluateIncrementalUntouchedFileCarries(t *testing.T) {
	delta := "diff --git a/src/other.ts b/src/other.ts\n" +
		"@@ -0,0 +1,2 @@\n+one\n+two\n"
	got, _ := shiftCase(t, []hunks.Hunk{{File: "src/prior.ts", Side: "NEW", Start: 10, End: 12}}, delta, aTenTo12)
	assertCounts(t, &got, 3, 3, "100", 3)
	if len(got.Unattested) != 0 {
		t.Errorf("gaps = %v, want none (the untouched file keeps every attested line)", got.Unattested)
	}
}

// TestEvaluateIncrementalUnmappableFileWithholdsCredit proves the fail-closed
// branch: a file whose delta section is binary has no coordinates to map, so
// its prior credit is withheld with a warning naming it instead of being
// credited at stale line numbers.
func TestEvaluateIncrementalUnmappableFileWithholdsCredit(t *testing.T) {
	delta := "diff --git a/src/prior.ts b/src/prior.ts\n" +
		"Binary files a/src/prior.ts and b/src/prior.ts differ\n"
	got, warns := shiftCase(t, []hunks.Hunk{{File: "src/prior.ts", Side: "NEW", Start: 10, End: 12}}, delta, aTenTo12)
	assertCounts(t, &got, 0, 3, "0", 0)
	assertGap(t, &got, "src/prior.ts", "10-12")
	assertWarnContains(t, warns, "prior credit withheld for that file")
}

// TestEvaluateIncrementalMalformedHunkWithholdsCredit proves a hunk whose body
// disagrees with its header fails closed too: a body that does not add up
// cannot place any line, so guessing would credit unattested lines.
func TestEvaluateIncrementalMalformedHunkWithholdsCredit(t *testing.T) {
	delta := "diff --git a/src/prior.ts b/src/prior.ts\n" +
		"@@ -3,5 +5,3 @@\n top\n-old line\n+new line\n bottom\n"
	got, warns := shiftCase(t, []hunks.Hunk{{File: "src/prior.ts", Side: "NEW", Start: 10, End: 12}}, delta, aTenTo12)
	assertCounts(t, &got, 0, 3, "0", 0)
	assertWarnContains(t, warns, "prior credit withheld for that file")
}

// TestEvaluateIncrementalDeletedFileWithholdsCredit proves a file the delta
// removes carries no prior credit: the head holds none of its lines, so the
// recorded numbers would point at whatever took their place. The snapshot still
// names the file through full.diff's OLD side — the PR base still had it — so
// the prior entry survives loadPrior and reaches the translation, which must
// refuse it rather than map it onto the current head.
func TestEvaluateIncrementalDeletedFileWithholdsCredit(t *testing.T) {
	delta := "diff --git a/src/prior.ts b/src/prior.ts\n" +
		"deleted file mode 100644\n--- a/src/prior.ts\n+++ /dev/null\n" +
		"@@ -10,3 +0,0 @@\n-a\n-b\n-c\n"
	got, warns := shiftCase(t, []hunks.Hunk{{File: "src/prior.ts", Side: "OLD", Start: 10, End: 12}}, delta, aTenTo12)
	if len(got.AttestedRanges) != 0 {
		t.Errorf("attested ranges = %v, want none (the file is gone)", got.AttestedRanges)
	}
	assertWarnContains(t, warns, "prior credit withheld for that file")
}
