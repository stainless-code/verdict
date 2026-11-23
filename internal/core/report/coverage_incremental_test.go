package reportcore_test

import (
	"context"
	"strings"
	"testing"

	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/hunks"
)

// incrementalFullDiff changes notes.txt (the prior range) and delta.txt
// (the delta), each 1-3: six changed lines under the full snapshot.
const incrementalFullDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n" +
	"diff --git a/delta.txt b/delta.txt\nindex 3333333..4444444 100644\n" +
	"--- a/delta.txt\n+++ b/delta.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

// incrementalDeltaDiff touches only delta.txt: the fetch marker for a
// --since/--base-sha bundle. Its lines are a subset of the full snapshot.
const incrementalDeltaDiff = "diff --git a/delta.txt b/delta.txt\n" +
	"@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

// incrementalPrior records the prior round: the whole notes.txt file.
const incrementalPrior = `[{"file":"notes.txt","ranges":"1-3"}]`

// incrementalNextFiles is the two-file fixture the next --coverage read
// scores: the full snapshot, the delta marker, the review, and (when
// prior is non-empty) the recorded prior.
func incrementalNextFiles(review, prior string) map[string]string {
	files := map[string]string{
		"full.diff":        incrementalFullDiff,
		"hunks.tsv":        hunks.Format(hunks.Parse(incrementalFullDiff)),
		"review.json":      review,
		"incremental.diff": incrementalDeltaDiff,
	}
	if prior != "" {
		files["prior-coverage.json"] = prior
	}
	return files
}

// TestRunNextCoverageIncrementalCreditsPrior proves the report is
// incremental-aware: the agent attests only the delta, the recorded prior
// covers notes.txt, and next --coverage reports 100% with no gaps — the
// same numbers record's gate stores.
func TestRunNextCoverageIncrementalCreditsPrior(t *testing.T) {
	review := coverageReview("delta.txt", `"coverage":[{"file":"delta.txt","ranges":"all"}],`)
	req := coverageNextRequest(t, incrementalNextFiles(review, incrementalPrior))
	resp, ref := reportcore.RunNextCoverage(req)
	if ref != nil {
		t.Fatalf("RunNextCoverage refusal: %v", ref)
	}
	if got := percentOf(resp.Coverage); got != "100" {
		t.Errorf("percent = %s, want 100 (prior credited)", got)
	}
	if resp.Coverage.Attested != 6 || resp.Coverage.Total != 6 || len(resp.Coverage.Unattested) != 0 {
		t.Errorf("coverage = %+v, want 6/6 with no gaps", resp.Coverage)
	}
	assertNextCarriesPrior(t, resp.Warnings)
}

// assertNextCarriesPrior fails unless the read warned about the 3 prior
// lines it carried over from the recorded prior round.
func assertNextCarriesPrior(t *testing.T, warns []string) {
	t.Helper()
	for _, w := range warns {
		if strings.Contains(w, "carried 3 line(s)") {
			return
		}
	}
	t.Errorf("warnings = %v, want the prior-credit warning", warns)
}

// TestRunNextCoverageIncrementalFailsClosedWithoutPrior proves the read is
// honest about an unrecorded prior: the same delta-only attestation
// without the recorded file reports 50% and warns, never 100%.
func TestRunNextCoverageIncrementalFailsClosedWithoutPrior(t *testing.T) {
	review := coverageReview("delta.txt", `"coverage":[{"file":"delta.txt","ranges":"all"}],`)
	req := coverageNextRequest(t, incrementalNextFiles(review, ""))
	resp, ref := reportcore.RunNextCoverage(req)
	if ref != nil {
		t.Fatalf("RunNextCoverage refusal: %v", ref)
	}
	if got := percentOf(resp.Coverage); got != "50" {
		t.Errorf("percent = %s, want 50 (no prior recorded)", got)
	}
	warned := false
	for _, w := range resp.Warnings {
		if strings.Contains(w, "no recorded prior coverage") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("warnings = %v, want the no-prior warning", resp.Warnings)
	}
}

// gatedIncremental runs the merge gate over a stored receipt. The gate
// reads the store record's coverage object directly, so these cases grade
// the stored receipt the way record persisted it — including any prior
// credit it carries. No incremental.diff or prior-coverage.json is written:
// the gate never re-reads them, and writing them would pretend the verdict
// drives off files the gate ignores.
func gatedIncremental(t *testing.T, coverage string) (bool, string) {
	t.Helper()
	req := fixture(t, "[]")
	writeFile(t, ".verdict/state/5101/record.json",
		`{"schema_version":"1","status":"pr","head":"`+storeHeadSHA+`",`+
			`"evidence":{"head":"`+storeHeadSHA+`","diff_sha256":"`+strings.Repeat("0", 64)+`",`+
			`"receipts":{"total":0,"covered":0,"exempt":0,"complete":true},`+
			`"spec":{"total":0,"covered":0,"min":80,"met":true},"insufficient":false},`+
			`"coverage":`+coverage+`}`)
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "informational", Coverage: "require", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	var line string
	for _, l := range resp.CheckLines {
		if strings.Contains(l, "coverage") {
			line = l
		}
	}
	return resp.GateFailed, line
}

// TestRunNextCoverageIncrementalMarkerFlipsVerdict proves the incremental
// read grades live: the same delta-only attestation reports 100% while the
// delta marker and recorded prior are present, and drops to the full-diff
// 50% the moment the marker is absent — deleting incremental.diff flips
// the verdict instead of leaving it pinned.
func TestRunNextCoverageIncrementalMarkerFlipsVerdict(t *testing.T) {
	review := coverageReview("delta.txt", `"coverage":[{"file":"delta.txt","ranges":"all"}],`)
	withDelta := incrementalNextFiles(review, incrementalPrior)
	resp, ref := reportcore.RunNextCoverage(coverageNextRequest(t, withDelta))
	if ref != nil {
		t.Fatalf("RunNextCoverage refusal: %v", ref)
	}
	if got := percentOf(resp.Coverage); got != "100" {
		t.Errorf("with delta percent = %s, want 100 (prior credited)", got)
	}
	withoutDelta := incrementalNextFiles(review, incrementalPrior)
	delete(withoutDelta, "incremental.diff")
	req := coverageNextRequest(t, withoutDelta)
	resp, ref = reportcore.RunNextCoverage(req)
	if ref != nil {
		t.Fatalf("RunNextCoverage refusal: %v", ref)
	}
	if got := percentOf(resp.Coverage); got != "50" {
		t.Errorf("without delta percent = %s, want 50 (full grading, prior ignored)", got)
	}
}

// humanForNext runs the coverage read over the bundle files and returns
// its human block, failing the test on any refusal.
func humanForNext(t *testing.T, files map[string]string) string {
	t.Helper()
	resp, ref := reportcore.RunNextCoverage(coverageNextRequest(t, files))
	if ref != nil {
		t.Fatalf("RunNextCoverage refusal: %v", ref)
	}
	return resp.Human
}

// TestRunNextCoverageIncrementalHumanSuffix pins the A1 human suffix on
// the report surface: a delta round that carried prior credit names
// `(incremental)` in its summary line. Exact literal, so a missing suffix
// fails the case.
func TestRunNextCoverageIncrementalHumanSuffix(t *testing.T) {
	deltaOnly := coverageReview("delta.txt", `"coverage":[{"file":"delta.txt","ranges":"all"}],`)
	want := "coverage: 100% of full hunk spans, both sides and context (6/6 lines, 2 files) (incremental)\nunattested: (none)"
	if got := humanForNext(t, incrementalNextFiles(deltaOnly, incrementalPrior)); got != want {
		t.Errorf("human = %q, want %q", got, want)
	}
}

// TestRunNextCoverageFullRoundHasNoHumanSuffix pins the suffix guard: a
// full bundle grades the prior away, so the same full attestation without
// the delta marker reads the identical numbers with no suffix — full
// rounds never shift.
func TestRunNextCoverageFullRoundHasNoHumanSuffix(t *testing.T) {
	full := coverageReview("notes.txt", `"coverage":[{"file":"notes.txt","ranges":"all"},{"file":"delta.txt","ranges":"all"}],`)
	files := incrementalNextFiles(full, incrementalPrior)
	delete(files, "incremental.diff")
	want := "coverage: 100% of full hunk spans, both sides and context (6/6 lines, 2 files)\nunattested: (none)"
	if got := humanForNext(t, files); got != want {
		t.Errorf("human = %q, want %q", got, want)
	}
}

// TestRunNextCoverageIncrementalWithoutCarriedCreditHasNoSuffix pins the
// suffix guard's other leg: a delta bundle whose attestation already
// covered everything carries zero prior lines, so the suffix stays off
// even though Incremental is true — the guard is the credit, not the
// marker.
func TestRunNextCoverageIncrementalWithoutCarriedCreditHasNoSuffix(t *testing.T) {
	full := coverageReview("notes.txt", `"coverage":[{"file":"notes.txt","ranges":"all"},{"file":"delta.txt","ranges":"all"}],`)
	want := "coverage: 100% of full hunk spans, both sides and context (6/6 lines, 2 files)\nunattested: (none)"
	if got := humanForNext(t, incrementalNextFiles(full, "")); got != want {
		t.Errorf("human = %q, want %q", got, want)
	}
}

// TestRunGateCoverageIncrementalPassesWithPrior proves the gate honors the
// incremental-aware stored receipt: a 100% receipt whose prior ranges came
// from the recorded round passes --coverage require.
func TestRunGateCoverageIncrementalPassesWithPrior(t *testing.T) {
	failed, line := gatedIncremental(t, `{"percent":100,"attested":6,"total":6,"files":2,"exempt":0,"gaps":0}`)
	if failed {
		t.Fatalf("GateFailed = true, want false; line = %q", line)
	}
	if !strings.Contains(line, "coverage passes") {
		t.Errorf("coverage line = %q, want the passes line", line)
	}
}

// TestRunGateCoverageIncrementalFailsOnUnattestedDelta proves the gate
// still fails when the stored receipt names unattested delta ranges: a
// delta-only attestation stored without prior credit reads 50% and the
// gate refuses, never a pass.
func TestRunGateCoverageIncrementalFailsOnUnattestedDelta(t *testing.T) {
	failed, line := gatedIncremental(t, `{"percent":50,"attested":3,"total":6,"files":2,"exempt":0,"gaps":1}`)
	if !failed {
		t.Fatalf("GateFailed = false, want true; line = %q", line)
	}
	if !strings.Contains(line, "coverage fails") {
		t.Errorf("coverage line = %q, want the fails line", line)
	}
}
