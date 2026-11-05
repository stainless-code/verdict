package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/provenance"
)

// TestRunWithTheShippedBinary drives the real verbs: the bundle the runner
// writes must satisfy the same `record` the merge path uses, and `calibrate`
// must grade it to the corpus ground truth. The fake-grader tests pin the
// loop; this one pins the contract with the tool.
func TestRunWithTheShippedBinary(t *testing.T) {
	cfg := shippedFixture(t)
	rep, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Samples != 1 || len(rep.Failures) != 0 {
		t.Fatalf("samples=%d failures=%v, want one clean sample", rep.Samples, rep.Failures)
	}
	if want := provenance.PromptsHash(cfg.Root); rep.PromptsHash != want {
		t.Errorf("prompts_hash = %q, want the checkout's prompt set (%q): the run dir hash the binary sees must match what the agent read", rep.PromptsHash, want)
	}
	if rep.Cost.Measured {
		t.Error("cost is measured for a fixture agent that reports no spend")
	}
	checkCalibrate(t, rep, cfg.Model)
	checkTrend(t, rep)
	if rep.Citable {
		t.Errorf("a one-sample fixture run is citable: %q", rep.CitableReason)
	}
}

// shippedFixture is one run of the real corpus verb path, or a skip when the
// contract cannot be exercised offline.
func shippedFixture(t *testing.T) *Config {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("moduleRoot: %v", err)
	}
	bin := filepath.Join(root, "bin", "verdict")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("bin/verdict is not built: run make build")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	restoreWD(t)
	fixture := readJSON[struct {
		Model  string          `json:"model"`
		Panel  []string        `json:"panel"`
		Review json.RawMessage `json:"review"`
	}](t, filepath.Join(root, "bench", "corpus", "off-by-one", "agents", "fixture-good.json"))
	t.Setenv("BENCH_TEST_REVIEW", string(fixture.Review))
	return &Config{
		Root:    root,
		Corpus:  filepath.Join(root, "bench", "corpus"),
		Verdict: bin,
		WorkDir: t.TempDir(),
		Out:     filepath.Join(t.TempDir(), "run.json"),
		Agent:   agentCopiesReview,
		Model:   fixture.Model,
		Panel:   fixture.Panel,
		Cases:   []string{"off-by-one"},
		Runs:    1,
		Timeout: 2 * time.Minute,
		Now:     time.Date(2026, 11, 5, 1, 2, 3, 0, time.UTC),
		Summary: io.Discard,
	}
}

// checkCalibrate pins the ground truth the shipped calibrate verb scored.
func checkCalibrate(t *testing.T, rep *Report, model string) {
	t.Helper()
	cal := readJSON[struct {
		Case           string   `json:"case"`
		Model          string   `json:"model"`
		Panel          []string `json:"panel"`
		GroundTruth    int      `json:"ground_truth"`
		Reported       int      `json:"reported"`
		Matched        int      `json:"matched"`
		FalsePositives int      `json:"false_positives"`
		Missed         int      `json:"missed"`
	}](t, writeTemp(t, rep.Bundles[0].Calibrate))
	want := struct {
		Case           string
		Model          string
		GroundTruth    int
		Reported       int
		Matched        int
		FalsePositives int
		Missed         int
	}{"off-by-one", model, 2, 3, 2, 1, 0}
	if cal.Case != want.Case || cal.Model != want.Model || cal.GroundTruth != want.GroundTruth ||
		cal.Reported != want.Reported || cal.Matched != want.Matched ||
		cal.FalsePositives != want.FalsePositives || cal.Missed != want.Missed {
		t.Errorf("calibrate = %+v, want the seeded ground truth %+v", cal, want)
	}
	if len(cal.Panel) != 1 || cal.Panel[0] != "panel-alpha" {
		t.Errorf("calibrate panel = %v, want panel-alpha", cal.Panel)
	}
}

// checkTrend pins the corpus totals the shipped trend verb reported.
func checkTrend(t *testing.T, rep *Report) {
	t.Helper()
	var corpus trendRates
	if err := json.Unmarshal(rep.TrendCorpus, &corpus); err != nil {
		t.Fatalf("trend_corpus: %v", err)
	}
	if corpus.Bundles != 1 || corpus.Cases != 1 || corpus.Missed != 0 {
		t.Errorf("trend_corpus = %+v, want the one bundle the run recorded", corpus)
	}
}

// TestRunScoresAnOutOfHunkAnchorAsAFalsePositive drives the shipped record and
// calibrate over a review that cites a line no hunk displays. record keeps the
// old path and line but marks the finding unanchored, so calibrate must grade
// the body note, not the withdrawn line claim it still carries.
func TestRunScoresAnOutOfHunkAnchorAsAFalsePositive(t *testing.T) {
	cfg := outOfHunkFixture(t)
	rep, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Failures) != 0 {
		t.Fatalf("failures = %v, want a clean sample", rep.Failures)
	}
	for _, warning := range rep.Bundles[0].Warnings {
		if strings.Contains(warning, "no fetch-time hunk snapshot") {
			t.Errorf("record rebuilt the index instead of reading the bundle snapshot: %s", warning)
		}
	}
	bundle := filepath.Join(cfg.WorkDir, reviewRoot, "corpus-out-of-hunk-fixture-good-run1")
	checkBundleSnapshot(t, bundle)
	checkRecordedAnchors(t, filepath.Join(bundle, findingsName))
	checkOutOfHunkScore(t, rep)
}

// outOfHunkDiff is well formed: NEW 40-41, with the bug on the added line 41.
const outOfHunkDiff = `diff --git a/limit.go b/limit.go
--- a/limit.go
+++ b/limit.go
@@ -40,2 +40,2 @@ func page(xs []int) int {
 	limit := len(xs)
-	return xs[limit-1]
+	return xs[limit]
`

const outOfHunkCase = `{"schema_version":"1","id":"out-of-hunk","kind":"seeded",` +
	`"provenance":{"kind":"seeded","injected":"The bound skips the last element."},` +
	`"bugs":[{"path":"limit.go","line":41,"kind":"off-by-one","severity":"critical",` +
	`"note":"xs[limit] reads one past the slice"}]}`

// outOfHunkReview cites line 42 first (outside the only hunk, within the
// one-line window of the bug at 41) and the bug's own line second, so only the
// inline claim may score.
const outOfHunkReview = `{"schema_version":"1","body":"> verdict: request changes","approved":false,` +
	`"comments":[` +
	`{"path":"limit.go","line":42,"body":"The loop bound reads one past the slice at this line."},` +
	`{"path":"limit.go","line":41,"body":"off-by-one: xs[limit] reads one past the slice."}]}`

// outOfHunkFixture is the same shipped-binary run over one synthetic case, or a
// skip when the contract cannot be exercised offline.
func outOfHunkFixture(t *testing.T) *Config {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("moduleRoot: %v", err)
	}
	bin := filepath.Join(root, "bin", "verdict")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("bin/verdict is not built: run make build")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	corpus := filepath.Join(t.TempDir(), "corpus")
	writeFile(t, filepath.Join(corpus, "out-of-hunk", caseManifestName), []byte(outOfHunkCase))
	writeFile(t, filepath.Join(corpus, "out-of-hunk", fullDiffName), []byte(outOfHunkDiff))
	restoreWD(t)
	t.Setenv("BENCH_TEST_REVIEW", outOfHunkReview)
	return &Config{
		Root:    root,
		Corpus:  corpus,
		Verdict: bin,
		WorkDir: t.TempDir(),
		Out:     filepath.Join(t.TempDir(), "run.json"),
		Agent:   agentCopiesReview,
		Model:   "fixture-good",
		Panel:   []string{"panel-alpha"},
		Cases:   []string{"out-of-hunk"},
		Runs:    1,
		Timeout: 2 * time.Minute,
		Now:     time.Date(2026, 11, 5, 1, 2, 3, 0, time.UTC),
		Summary: io.Discard,
	}
}

// checkBundleSnapshot pins the bundle's hunk index to the one fetch writes.
func checkBundleSnapshot(t *testing.T, bundle string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(bundle, hunksTSVName))
	if err != nil {
		t.Fatalf("read bundle %s: %v", hunksTSVName, err)
	}
	if got, want := string(raw), hunks.Snapshot(outOfHunkDiff); got != want {
		t.Errorf("%s = %q, want the fetch-time index %q", hunksTSVName, got, want)
	}
}

// checkRecordedAnchors pins what record wrote: line 42 kept its old anchor
// fields but lost its inline standing, line 41 stayed inline. The moved
// section record appends is skipped by the scorer, not asserted here.
func checkRecordedAnchors(t *testing.T, path string) {
	t.Helper()
	entries := readJSON[[]struct {
		Line     *int `json:"line"`
		Anchored any  `json:"anchored"`
	}](t, path)
	want := map[int]bool{41: true, 42: false}
	seen := checkAnchoredEntries(t, entries, want)
	if seen != len(want) {
		t.Errorf("recorded %d anchored findings, want one kept and one moved", seen)
	}
}

func checkAnchoredEntries(t *testing.T, entries []struct {
	Line     *int `json:"line"`
	Anchored any  `json:"anchored"`
}, want map[int]bool,
) int {
	t.Helper()
	seen := 0
	for _, entry := range entries {
		if entry.Line == nil {
			continue
		}
		seen++
		anchored, ok := want[*entry.Line]
		if !ok {
			t.Errorf("unexpected recorded anchor line %d", *entry.Line)
			continue
		}
		if entry.Anchored != anchored {
			t.Errorf("line %d anchored = %v, want %v", *entry.Line, entry.Anchored, anchored)
		}
	}
	return seen
}

// gradedOutcome is the calibrate outcome block's grading fields.
type gradedOutcome struct {
	Line        *int   `json:"line"`
	MatchedLine *int   `json:"matched_line"`
	Result      string `json:"result"`
}

// checkOutOfHunkScore pins the graded outcome: only the inline claim is the
// catch, and the body note cannot take its place even though line 42 sits one
// line from the bug.
func checkOutOfHunkScore(t *testing.T, rep *Report) {
	t.Helper()
	cal := readJSON[struct {
		Outcomes       []gradedOutcome `json:"outcomes"`
		Reported       int             `json:"reported"`
		Matched        int             `json:"matched"`
		FalsePositives int             `json:"false_positives"`
		Missed         int             `json:"missed"`
	}](t, writeTemp(t, rep.Bundles[0].Calibrate))
	if cal.Reported != 2 || cal.Matched != 1 || cal.FalsePositives != 1 || cal.Missed != 0 {
		t.Fatalf("calibrate = reported %d matched %d false-positives %d missed %d, want 2/1/1/0",
			cal.Reported, cal.Matched, cal.FalsePositives, cal.Missed)
	}
	if moved := outcomeOn(t, cal.Outcomes, 42); moved.Result != "false-positive" || moved.MatchedLine != nil {
		t.Errorf("the out-of-hunk claim graded %+v, want a false positive with no matched line", moved)
	}
	if kept := outcomeOn(t, cal.Outcomes, 41); kept.Result != "true-positive" ||
		kept.MatchedLine == nil || *kept.MatchedLine != 41 {
		t.Errorf("the inline claim graded %+v, want the true positive on line 41", kept)
	}
}

// outcomeOn returns the one outcome anchored at line.
func outcomeOn(t *testing.T, outcomes []gradedOutcome, line int) gradedOutcome {
	t.Helper()
	for _, outcome := range outcomes {
		if outcome.Line != nil && *outcome.Line == line {
			return outcome
		}
	}
	t.Fatalf("no graded outcome on line %d (outcomes %+v)", line, outcomes)
	return gradedOutcome{}
}
