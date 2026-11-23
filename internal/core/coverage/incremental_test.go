package coverage

import (
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
		name  string
		prior string
	}{
		{name: "absent", prior: ""},
		{name: "empty-array", prior: "[]"},
		{name: "unreadable", prior: "{broken"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertFailsClosed(t, tc.prior)
		})
	}
}

// assertFailsClosed grades one delta-only attestation that must earn no
// credit: 3 of 6 lines at 50% plus the no-prior or unreadable warning.
func assertFailsClosed(t *testing.T, prior string) {
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
	if len(warns) == 0 {
		t.Fatalf("warnings = %v, want the no-prior or unreadable warning", warns)
	}
	assertWarnContains(t, warns, priorWarnNeedle(warns[0]))
}

// priorWarnNeedle picks the expected needle for one warning: an undecodable
// prior names itself, every other unrecorded shape names the missing record.
func priorWarnNeedle(w string) string {
	if strings.Contains(w, "unreadable") {
		return "unreadable"
	}
	return "no recorded prior coverage"
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
