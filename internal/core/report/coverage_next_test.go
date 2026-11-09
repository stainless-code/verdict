package reportcore_test

import (
	"os"
	"strconv"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/hunks"
)

// coverageDiff changes notes.txt across one hunk on both sides, so the
// evaluator unions OLD 1-3 with NEW 1-3 into three changed lines.
const coverageDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

// generatedDiff changes only a generated path: nothing is measurable, so
// `percent` must stay null, never 0.
const generatedDiff = "diff --git a/dist/app.min.js b/dist/app.min.js\nindex 3333333..4444444 100644\n" +
	"--- a/dist/app.min.js\n+++ b/dist/app.min.js\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

// coverageReview builds a review over path whose body clears the degenerate
// floor, with extra as the top-level fragment carrying coverage[].
func coverageReview(path, extra string) string {
	return `{"schema_version":"1","body":"> [!IMPORTANT]\n> Next coverage fixture with enough body.","approved":false,` +
		extra + `"comments":[{"path":"` + path + `","line":2,"side":"RIGHT",` +
		`"body":"Cited finding here with enough substance.","rigor":"walked","severity":"important"}]}`
}

// coverageNextRequest writes bundle 5101 from the given bundle-relative files
// and returns the `next --coverage` request over it. Each case states exactly
// the tree it needs, so a missing store is the fixture, not an accident.
func coverageNextRequest(t *testing.T, files map[string]string) *core.NextRequest {
	t.Helper()
	t.Chdir(t.TempDir())
	for rel, content := range files {
		writeFile(t, ".verdict/state/5101/"+rel, content)
	}
	return &core.NextRequest{
		//nolint:modernize // a struct field literal cannot elide its type (Go spec)
		ReadRequest: core.ReadRequest{Runtime: fixedRuntime(), Repo: "acme/app", Pr: "5101"},
	}
}

// coverageFiles is the measured fixture: the diff, its hunk snapshot, and the
// review (an empty review writes no review.json — the pre-review state).
// findings.json is deliberately absent — the read mode must work before record.
func coverageFiles(review string) map[string]string {
	files := map[string]string{
		"full.diff": coverageDiff,
		"hunks.tsv": hunks.Format(hunks.Parse(coverageDiff)),
	}
	if review != "" {
		files["review.json"] = review
	}
	return files
}

// percentOf renders a nullable percent for a failure message: a pointer
// printed with %v is an address, which hides the value that failed.
func percentOf(rep *core.CoverageReport) string {
	if rep == nil || rep.Percent == nil {
		return "null"
	}
	return strconv.Itoa(*rep.Percent)
}

// TestRunNextCoverageBeforeRecord pins criterion 9's pre-record leg: the
// coverage read answers with no findings.json in the bundle, so it works
// before record and cannot be blocked by a missing store.
func TestRunNextCoverageBeforeRecord(t *testing.T) {
	req := coverageNextRequest(t, coverageFiles(coverageReview("notes.txt", `"coverage":[{"file":"notes.txt","ranges":"1-2"}],`)))
	resp, ref := reportcore.RunNextCoverage(req)
	if ref != nil {
		t.Fatalf("RunNextCoverage refusal: %v", ref)
	}
	if _, err := os.Stat(".verdict/state/5101/findings.json"); !os.IsNotExist(err) {
		t.Fatal("the fixture has a findings.json; the pre-record leg proves nothing")
	}
	assertPartialCoverage(t, resp)
}

// TestRunNextCoverageMissingReview pins criterion 9's pre-review leg: an
// absent review.json is the normal first state, so the read reports 0% and
// succeeds instead of refusing like a corrupt one.
func TestRunNextCoverageMissingReview(t *testing.T) {
	resp, ref := reportcore.RunNextCoverage(coverageNextRequest(t, coverageFiles("")))
	if ref != nil {
		t.Fatalf("RunNextCoverage refusal: %v", ref)
	}
	if got := percentOf(resp.Coverage); got != "0" {
		t.Errorf("percent = %s, want 0 (a measured denominator, unattested)", got)
	}
	if resp.Coverage.Attested != 0 || resp.Coverage.Total != 3 {
		t.Errorf("attested/total = %d/%d, want 0/3", resp.Coverage.Attested, resp.Coverage.Total)
	}
	want := "coverage: 0% — no review.json yet\nunattested:\n- notes.txt:1-3"
	if resp.Human != want {
		t.Errorf("human = %q, want %q", resp.Human, want)
	}
}

// TestRunNextCoverageRefusesCorruptReview keeps the missing-review leg from
// widening: a review.json that exists but does not decode is a typed refusal,
// never a 0% reading.
func TestRunNextCoverageRefusesCorruptReview(t *testing.T) {
	files := coverageFiles("")
	files["review.json"] = "{broken"
	_, ref := reportcore.RunNextCoverage(coverageNextRequest(t, files))
	if ref == nil || ref.Code != "report-failed" {
		t.Fatalf("refusal = %v, want report-failed", ref)
	}
}

// TestRunNextCoverageChunked pins criterion 9's chunked leg: a chunked bundle
// reports its chunk count and is not evaluated, because chunks[] is its
// attestation.
func TestRunNextCoverageChunked(t *testing.T) {
	files := coverageFiles(coverageReview("notes.txt", `"coverage":[{"file":"notes.txt","ranges":"all"}],`))
	files["chunks/manifest.json"] = `{"chunks":[{"name":"chunks/01.diff"},{"name":"chunks/02.diff"}]}`
	files["chunks/01.diff"] = coverageDiff
	files["chunks/02.diff"] = coverageDiff
	resp, ref := reportcore.RunNextCoverage(coverageNextRequest(t, files))
	if ref != nil {
		t.Fatalf("RunNextCoverage refusal: %v", ref)
	}
	if !resp.Coverage.Chunked {
		t.Fatalf("chunked = false, want true")
	}
	if got := percentOf(resp.Coverage); got != "null" {
		t.Errorf("percent = %s on a chunked bundle, want null (not evaluated)", got)
	}
	want := "coverage: chunked bundle (2 chunks) — use the chunks[] attestation"
	if resp.Human != want {
		t.Errorf("human = %q, want %q", resp.Human, want)
	}
}

// TestRunNextCoverageNothingMeasured pins criterion 4's receipt half: an
// exempt-only bundle reads as unmeasured (`percent: null`), not as 0%.
func TestRunNextCoverageNothingMeasured(t *testing.T) {
	files := map[string]string{
		"full.diff":   generatedDiff,
		"hunks.tsv":   hunks.Format(hunks.Parse(generatedDiff)),
		"review.json": coverageReview("dist/app.min.js", `"coverage":[{"file":"dist/app.min.js","ranges":"all"}],`),
	}
	resp, ref := reportcore.RunNextCoverage(coverageNextRequest(t, files))
	if ref != nil {
		t.Fatalf("RunNextCoverage refusal: %v", ref)
	}
	if got := percentOf(resp.Coverage); got != "null" {
		t.Errorf("percent = %s, want null when nothing was measured", got)
	}
	if resp.Coverage.Exempt != 1 || resp.Coverage.Total != 0 || resp.Coverage.Files != 0 {
		t.Errorf("exempt/total/files = %d/%d/%d, want 1/0/0",
			resp.Coverage.Exempt, resp.Coverage.Total, resp.Coverage.Files)
	}
	want := "coverage: n/a — nothing changed to measure"
	if resp.Human != want {
		t.Errorf("human = %q, want %q", resp.Human, want)
	}
}

// TestRunNextCoverageCompleteNamesTheEmptyGapList pins the complete leg: a
// 100% reading still states that nothing is unattested, so a reader never
// confuses an empty list with a missing one.
func TestRunNextCoverageCompleteNamesTheEmptyGapList(t *testing.T) {
	req := coverageNextRequest(t, coverageFiles(coverageReview("notes.txt", `"coverage":[{"file":"notes.txt","ranges":"all"}],`)))
	resp, ref := reportcore.RunNextCoverage(req)
	if ref != nil {
		t.Fatalf("RunNextCoverage refusal: %v", ref)
	}
	if got := percentOf(resp.Coverage); got != "100" {
		t.Errorf("percent = %s, want 100", got)
	}
	want := "coverage: 100% (3/3 changed lines, 1 files)\nunattested: (none)"
	if resp.Human != want {
		t.Errorf("human = %q, want %q", resp.Human, want)
	}
}

// assertPartialCoverage pins the measured reading for the 2-of-3 fixture: the
// percentage, the counts, the single gap, and the rendered human block.
func assertPartialCoverage(t *testing.T, resp *core.NextResponse) {
	t.Helper()
	rep := resp.Coverage
	if rep == nil {
		t.Fatal("coverage report is nil")
	}
	if got := percentOf(rep); got != "66" {
		t.Errorf("percent = %s, want 66", got)
	}
	if rep.Attested != 2 || rep.Total != 3 || rep.Files != 1 {
		t.Errorf("attested/total/files = %d/%d/%d, want 2/3/1", rep.Attested, rep.Total, rep.Files)
	}
	if len(rep.Unattested) != 1 || rep.Unattested[0].File != "notes.txt" || rep.Unattested[0].Ranges != "3" {
		t.Errorf("unattested = %v, want one notes.txt:3 gap", rep.Unattested)
	}
	want := "coverage: 66% (2/3 changed lines, 1 files)\nunattested:\n- notes.txt:3"
	if resp.Human != want {
		t.Errorf("human = %q, want %q", resp.Human, want)
	}
}
