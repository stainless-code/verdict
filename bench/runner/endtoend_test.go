package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
		t.Fatalf("Run: %v (failures: %+v)", err, rep.Failures)
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
		K:       1,
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
		K:       1,
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

func requireShippedBinary(t *testing.T) (string, string) {
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
	return root, bin
}

func shippedLoopBadFixed(t *testing.T, bad, fixed string) (*Report, *Config) {
	t.Helper()
	root, bin := requireShippedBinary(t)
	restoreWD(t)
	t.Setenv("BENCH_TEST_BAD", bad)
	t.Setenv("BENCH_TEST_FIXED", fixed)
	agent := `if [ "${VERDICT_BENCH_ROUND:-1}" = "1" ]; then printf '%s' "$BENCH_TEST_BAD" > "$VERDICT_BENCH_OUT"; ` +
		`else printf '%s' "$BENCH_TEST_FIXED" > "$VERDICT_BENCH_OUT"; fi`
	cfg := &Config{K: 1,
		Root:    root,
		Corpus:  filepath.Join(root, "bench", "corpus"),
		Verdict: bin,
		WorkDir: t.TempDir(),
		Out:     filepath.Join(t.TempDir(), "run.json"),
		Agent:   agent,
		Model:   "fixture-good",
		Panel:   []string{"panel-alpha"},
		Cases:   []string{"off-by-one"},
		Runs:    1,
		Loop:    2,
		Timeout: 2 * time.Minute,
		Now:     time.Date(2026, 11, 5, 1, 2, 3, 0, time.UTC),
		Summary: io.Discard,
	}
	rep, err := Run(context.Background(), cfg)
	if err != nil {
		if raw, rerr := os.ReadFile(cfg.Out); rerr == nil && len(raw) > 0 {
			t.Logf("report: %.8000s", string(raw))
		}
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Failures) != 0 || len(rep.Loops) != 1 {
		t.Fatalf("failures=%v loops=%d, want one clean loop", rep.Failures, len(rep.Loops))
	}
	return rep, cfg
}

const shippedBody = `> Fixture review for the benchmark corpus: seeded defects reported, one deliberate false positive with enough substance.`

func shippedReview(comments string) string {
	return `{"schema_version":"1","body":"` + shippedBody + `","approved":false,"comments":[` + comments + `]}`
}

func findShippedRate(rep *Report, code, level string) *FixRate {
	for i := range rep.FixRates {
		if rep.FixRates[i].Code == code && rep.FixRates[i].Level == level {
			return &rep.FixRates[i]
		}
	}
	return nil
}

func checkShippedFixRate(t *testing.T, rep *Report, code, level string, shown, fixed, dropped int) {
	t.Helper()
	fr := findShippedRate(rep, code, level)
	if fr == nil {
		t.Fatalf("no %s/%s row in %+v", code, level, rep.FixRates)
	}
	if fr.Shown != shown || fr.Fixed != fixed || fr.Dropped != dropped {
		t.Errorf("rate = %+v, want shown %d fixed %d dropped %d", fr, shown, fixed, dropped)
	}
	if fr.Unattributed != 0 {
		t.Errorf("rate = %+v, want no unattributed", fr)
	}
}

// TestShippedLoopSameAnchorDeletionIsDrop keeps a deletion from borrowing.
func TestShippedLoopSameAnchorDeletionIsDrop(t *testing.T) {
	needPOSIX(t)
	keep := `{"path":"pagination.go","line":11,"side":"RIGHT","body":"The rename to renderRows reads well.","rigor":"walked","severity":"informational"}`
	bad := shippedReview(keep + `,{"path":"pagination.go","line":11,"side":"RIGHT","rigor":"walked","severity":"informational"}`)
	fixed := shippedReview(keep)
	rep, _ := shippedLoopBadFixed(t, bad, fixed)
	checkShippedFixRate(t, rep, "missing-field", "error", 1, 0, 1)
	if len(rep.Loops[0].Added) != 0 || len(rep.Loops[0].Dropped) != 1 {
		t.Errorf("added=%d dropped=%d, want 0 and 1", len(rep.Loops[0].Added), len(rep.Loops[0].Dropped))
	}
}

// TestShippedLoopWrongTypeKeepsSiblings keeps siblings on wrong type.
func TestShippedLoopWrongTypeKeepsSiblings(t *testing.T) {
	keep11 := `{"path":"pagination.go","line":11,"side":"RIGHT","body":"The rename to renderRows reads well.","rigor":"walked","severity":"informational"}`
	keep51 := `{"path":"pagination.go","line":51,"side":"RIGHT","body":"The fetchPage error is discarded.","rigor":"pointed","severity":"important"}`
	bad := shippedReview(keep11 + `,{"path":"pagination.go","line":42,"side":"RIGHT","body":42,"rigor":"walked","severity":"critical"},` + keep51)
	fixed := shippedReview(keep11 + `,` + keep51)
	rep, _ := shippedLoopBadFixed(t, bad, fixed)
	checkShippedFixRate(t, rep, "wrong-type", "error", 1, 0, 1)
	if len(rep.Loops[0].Added) != 0 || len(rep.Loops[0].Dropped) != 1 {
		t.Errorf("added=%d dropped=%d, want 0 and 1", len(rep.Loops[0].Added), len(rep.Loops[0].Dropped))
	}
}

// TestShippedLoopEmptyEntryKeepsIndex keeps an empty entry indexed.
func TestShippedLoopEmptyEntryKeepsIndex(t *testing.T) {
	keep11 := `{"path":"pagination.go","line":11,"side":"RIGHT","body":"The rename to renderRows reads well.","rigor":"walked","severity":"informational"}`
	keep42 := `{"path":"pagination.go","line":42,"side":"RIGHT","body":"Loop bound runs one past the slice.","rigor":"walked","severity":"critical"}`
	keep51 := `{"path":"pagination.go","line":51,"side":"RIGHT","body":"The fetchPage error is discarded.","rigor":"pointed","severity":"important"}`
	bad := shippedReview(`{},` + keep11 + `,` + keep42 + `,` + keep51)
	fixed := shippedReview(keep11 + `,` + keep42 + `,` + keep51)
	rep, _ := shippedLoopBadFixed(t, bad, fixed)
	checkShippedFixRate(t, rep, "missing-field", "error", 2, 0, 2)
	if len(rep.Loops[0].Added) != 0 || len(rep.Loops[0].Dropped) != 1 {
		t.Errorf("added=%d dropped=%d, want 0 and 1", len(rep.Loops[0].Added), len(rep.Loops[0].Dropped))
	}
}

// TestShippedLoopAliasDeleteIsDrop treats a deleted alias as a loss.
func TestShippedLoopAliasDeleteIsDrop(t *testing.T) {
	keep11 := `{"path":"pagination.go","line":11,"side":"RIGHT","body":"The rename to renderRows reads well.","rigor":"walked","severity":"informational"}`
	keep51 := `{"path":"pagination.go","line":51,"side":"RIGHT","body":"The fetchPage error is discarded.","rigor":"pointed","severity":"important"}`
	bad := shippedReview(keep11 + `,{"line":42,"side":"RIGHT","rigor":"walked","severity":"critical","file":"pagination.go"},` + keep51)
	fixed := shippedReview(keep11 + `,` + keep51)
	rep, _ := shippedLoopBadFixed(t, bad, fixed)
	checkShippedFixRate(t, rep, "missing-field", "error", 1, 0, 1)
	checkShippedFixRate(t, rep, "key-alias", "warning", 1, 0, 1)
	if len(rep.Loops[0].Added) != 0 || len(rep.Loops[0].Dropped) != 1 {
		t.Errorf("added=%d dropped=%d, want 0 and 1", len(rep.Loops[0].Added), len(rep.Loops[0].Dropped))
	}
}

func replayReviews(t *testing.T, cfg *Config, slug string) ([][]byte, []byte, []byte) {
	t.Helper()
	round1, err := os.ReadFile(filepath.Join(cfg.WorkDir, "runs", slug, "round-01", reviewJSONName))
	if err != nil {
		t.Fatalf("round-01 review: %v", err)
	}
	round2, err := os.ReadFile(filepath.Join(cfg.WorkDir, "runs", slug, "round-02", reviewJSONName))
	if err != nil {
		t.Fatalf("round-02 review: %v", err)
	}
	return [][]byte{round1, round2}, round1, round2
}

func checkReplayOutcome(t *testing.T, first *LoopOutcome, round1, round2 []byte) {
	t.Helper()
	if !first.FinalValid || first.RoundsToValid != 2 {
		t.Errorf("outcome = %+v, want valid on 2", first)
	}
	if !bytes.Equal(first.Attempts[0].ReviewBytes, round1) || !bytes.Equal(first.Attempts[1].ReviewBytes, round2) {
		t.Error("replay changed saved review bytes")
	}
}

func checkReplayRates(t *testing.T, first *LoopOutcome, rep *Report) {
	t.Helper()
	if !reflect.DeepEqual(first.Added, rep.Loops[0].Added) || !reflect.DeepEqual(first.Dropped, rep.Loops[0].Dropped) {
		t.Errorf("replay added/dropped = %v/%v, want %v/%v", first.Added, first.Dropped, rep.Loops[0].Added, rep.Loops[0].Dropped)
	}
	gotRates := fixRatesFromOutcomes([]*LoopOutcome{first})
	if len(gotRates) != len(rep.FixRates) {
		t.Fatalf("replay rates len %d, want %d", len(gotRates), len(rep.FixRates))
	}
	for i := range gotRates {
		if gotRates[i].Shown != rep.FixRates[i].Shown || gotRates[i].Fixed != rep.FixRates[i].Fixed || gotRates[i].Dropped != rep.FixRates[i].Dropped {
			t.Errorf("replay rate = %+v, want %+v", gotRates[i], rep.FixRates[i])
		}
	}
}

// TestReplayLoopReplaysShippedArtifacts replays saved agent outputs through
// the shipped record --dry-run, proving deterministic replay.
func TestReplayLoopReplaysShippedArtifacts(t *testing.T) {
	needPOSIX(t)
	keep11 := `{"path":"pagination.go","line":11,"side":"RIGHT","body":"The rename to renderRows reads well.","rigor":"walked","severity":"informational"}`
	keep42 := `{"path":"pagination.go","line":42,"side":"RIGHT","body":"Loop bound runs one past the slice.","rigor":"walked","severity":"critical"}`
	keep51 := `{"path":"pagination.go","line":51,"side":"RIGHT","body":"The fetchPage error is discarded.","rigor":"pointed","severity":"important"}`
	missing11 := `{"path":"pagination.go","line":11,"side":"RIGHT","rigor":"walked","severity":"informational"}`
	bad := shippedReview(missing11 + `,` + keep42 + `,` + keep51)
	fixed := shippedReview(keep11 + `,` + keep42 + `,` + keep51)
	rep, cfg := shippedLoopBadFixed(t, bad, fixed)
	checkShippedFixRate(t, rep, "missing-field", "error", 1, 1, 0)
	checkReplayFixedClock(t, rep)
	checkReplayFixtureCost(t, cfg, rep)
	reviews, round1, round2 := replayReviews(t, cfg, rep.Loops[0].Slug)
	slug := rep.Loops[0].Slug
	restoreBundleReview(t, cfg, slug)
	realDry := func(_ int, raw []byte) (DryRunResult, error) {
		return replayDryViaCLI(t, cfg, slug, raw), nil
	}
	first := replayLoop(reviews, realDry)
	second := replayLoop(reviews, realDry)
	checkReplayOutcome(t, &first, round1, round2)
	checkReplayDeterministic(t, &first, &second)
	for i := range reviews {
		checkReplayDryMatchesSaved(t, cfg, slug, rep, i+1, &first.Attempts[i].Dry)
	}
	checkReplayRates(t, &first, rep)
}

// checkReplayFixedClock pins the fixed runner clock: the report provenance
// derives from cfg.Now, not wall time.
func checkReplayFixedClock(t *testing.T, rep *Report) {
	t.Helper()
	if got := rep.GeneratedAt; got != "2026-11-05T01:02:03Z" {
		t.Errorf("generated_at = %q, want the fixed runner clock 2026-11-05T01:02:03Z", got)
	}
	if got := rep.RunID; got != "2026-11-05-fixture-good" {
		t.Errorf("run_id = %q, want the fixed-clock run id", got)
	}
}

// checkReplayFixtureCost pins the deterministic fixture spend: the agent
// writes no cost record, so both rounds stay unmeasured at zero.
func checkReplayFixtureCost(t *testing.T, cfg *Config, rep *Report) {
	t.Helper()
	if rep.LoopCost == nil {
		t.Fatal("loop_cost is nil, want two unmeasured attempts")
	}
	got := *rep.LoopCost
	if got.Attempts != 2 || got.WithEstimate != 0 || got.Usd != 0 || got.Measured || got.Incomplete != 0 {
		t.Errorf("loop_cost = %+v, want 2 attempts unmeasured at 0", got)
	}
	for _, round := range []int{1, 2} {
		checkReplayRoundCost(t, cfg, rep.Loops[0].Slug, round)
	}
}

// checkReplayRoundCost pins one round spend: no estimate, positive wall.
func checkReplayRoundCost(t *testing.T, cfg *Config, slug string, round int) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(cfg.WorkDir, "runs", slug, fmt.Sprintf(roundFmt, round), costJSONName))
	if err != nil {
		t.Fatalf("round-%02d cost: %v", round, err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("round-%02d cost: %v", round, err)
	}
	if _, ok := rec["est_usd"]; ok {
		t.Errorf("round-%02d cost carries est_usd, want no spend from the fixture", round)
	}
	checkReplayRoundWall(t, rec, round)
}

// checkReplayRoundWall asserts the volatile wall the comparison excludes.
func checkReplayRoundWall(t *testing.T, rec map[string]any, round int) {
	t.Helper()
	wall, ok := rec["wall_seconds"]
	if !ok {
		t.Errorf("round-%02d cost omits wall_seconds, want the real agent wall the test excludes", round)
		return
	}
	wallF, ok := wall.(float64)
	if !ok || wallF <= 0 {
		t.Errorf("round-%02d wall_seconds = %v, want a positive real wall excluded from the semantic comparison", round, wall)
	}
}

// restoreBundleReview saves the final bundle review and restores it after the
// test, so replay staging leaves the evidence intact.
func sameDir(a, b string) bool {
	resolvedA, errA := filepath.EvalSymlinks(a)
	resolvedB, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return resolvedA == resolvedB
	}
	return a == b
}

// restoreBundleReview saves the final bundle review and restores it after the
// test, so replay staging leaves the evidence intact.
func restoreBundleReview(t *testing.T, cfg *Config, slug string) {
	t.Helper()
	path := filepath.Join(cfg.WorkDir, reviewRoot, slug, reviewJSONName)
	orig, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("bundle review: %v", err)
	}
	t.Cleanup(func() {
		if err := os.WriteFile(path, orig, 0o644); err != nil {
			t.Errorf("restore bundle review: %v", err)
		}
	})
}

// replayDryViaCLI stages one saved agent output into the bundle and scores it
// with the shipped record --dry-run. It preserves the exact raw bytes.
func replayDryViaCLI(t *testing.T, cfg *Config, slug string, review []byte) DryRunResult {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if !sameDir(wd, cfg.WorkDir) {
		t.Fatalf("CWD = %q, want the run dir %q: replay must run where the bundle lives", wd, cfg.WorkDir)
	}
	bundleReview := filepath.Join(cfg.WorkDir, reviewRoot, slug, reviewJSONName)
	if err := os.WriteFile(bundleReview, review, 0o644); err != nil {
		t.Fatalf("stage replay review: %v", err)
	}
	grader := tool{bin: cfg.Verdict, repo: benchRepo}
	dry, err := grader.dryRun(context.Background(), slug)
	if err != nil {
		t.Fatalf("record --dry-run: %v", err)
	}
	return dry
}

// checkReplayDryMatchesSaved compares one replayed dry-run against the saved
// round artifacts, excluding only the volatile record timestamp.
func checkReplayDryMatchesSaved(t *testing.T, cfg *Config, slug string, rep *Report, round int, got *DryRunResult) {
	t.Helper()
	wantSum := rep.Loops[0].Attempts[round-1]
	checkReplayDryFields(t, round, &wantSum, got)
	checkReplayDryHuman(t, cfg, slug, round, got)
	checkReplayDryNormalized(t, cfg, slug, round, got)
	checkReplayDryEnvelope(t, cfg, slug, round, got)
}

// checkReplayDryFields pins the scoring fields the loop consumes.
func checkReplayDryFields(t *testing.T, round int, wantSum *LoopAttemptSummary, got *DryRunResult) {
	t.Helper()
	if got.Exit != wantSum.Exit {
		t.Errorf("round %d exit = %d, want %d from the shipped run", round, got.Exit, wantSum.Exit)
	}
	if !reflect.DeepEqual(got.Problems, wantSum.Problems) {
		t.Errorf("round %d problems = %+v, want %+v", round, got.Problems, wantSum.Problems)
	}
	if got.Truncated != wantSum.Truncated {
		t.Errorf("round %d truncated = %d, want %d", round, got.Truncated, wantSum.Truncated)
	}
	if !reflect.DeepEqual(got.Repairs, wantSum.Repairs) {
		t.Errorf("round %d repairs = %+v, want %+v", round, got.Repairs, wantSum.Repairs)
	}
	if !reflect.DeepEqual(got.Warnings, wantSum.Warnings) {
		t.Errorf("round %d warnings = %q, want %q", round, got.Warnings, wantSum.Warnings)
	}
}

// checkReplayDryHuman pins the stderr stream the CLI returned.
func checkReplayDryHuman(t *testing.T, cfg *Config, slug string, round int, got *DryRunResult) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join(cfg.WorkDir, "runs", slug, fmt.Sprintf(roundFmt, round), dryStderrFile))
	if err != nil {
		t.Fatalf("round-%02d stderr: %v", round, err)
	}
	if got.Human != string(want) {
		t.Errorf("round %d stderr = %q, want saved %q", round, got.Human, string(want))
	}
}

// checkReplayDryNormalized pins the accepted output: the normalized review
// the dry-run accepted, compared as JSON so key order cannot hide a change.
func checkReplayDryNormalized(t *testing.T, cfg *Config, slug string, round int, got *DryRunResult) {
	t.Helper()
	wantRaw := readSavedNormalized(t, cfg, slug, round)
	if isEmptyNormalized(wantRaw) {
		if len(got.Normalized) != 0 {
			t.Errorf("round %d normalized = %s, want empty for a refused round", round, got.Normalized)
		}
		return
	}
	compareNormalizedJSON(t, round, wantRaw, got.Normalized)
}

// readSavedNormalized reads one round accepted output.
func readSavedNormalized(t *testing.T, cfg *Config, slug string, round int) []byte {
	t.Helper()
	wantRaw, err := os.ReadFile(filepath.Join(cfg.WorkDir, "runs", slug, fmt.Sprintf(roundFmt, round), normFile))
	if err != nil {
		t.Fatalf("round-%02d normalized: %v", round, err)
	}
	return wantRaw
}

// isEmptyNormalized reports a refused round with no accepted output.
func isEmptyNormalized(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || string(trimmed) == "{}"
}

// compareNormalizedJSON compares accepted outputs as JSON.
func compareNormalizedJSON(t *testing.T, round int, wantRaw, gotRaw []byte) {
	t.Helper()
	var want, have any
	if err := json.Unmarshal(wantRaw, &want); err != nil {
		t.Fatalf("round-%02d normalized: %v", round, err)
	}
	if err := json.Unmarshal(gotRaw, &have); err != nil {
		t.Fatalf("round %d normalized: %v", round, err)
	}
	if !reflect.DeepEqual(have, want) {
		t.Errorf("round %d normalized differs from saved %s", round, normFile)
	}
}

// checkReplayDryEnvelope compares the raw record envelope, excluding only the
// volatile at timestamp: record stamps real wall time, which the fixed runner
// clock does not control, so two runs of the same bytes carry different at
// values while every scoring field stays identical.
func checkReplayDryEnvelope(t *testing.T, cfg *Config, slug string, round int, got *DryRunResult) {
	t.Helper()
	wantRaw, err := os.ReadFile(filepath.Join(cfg.WorkDir, "runs", slug, fmt.Sprintf(roundFmt, round), dryFile))
	if err != nil {
		t.Fatalf("round-%02d dryrun: %v", round, err)
	}
	wantAt := envelopeAt(t, wantRaw, round, "saved")
	gotAt := envelopeAt(t, got.Envelope, round, "replayed")
	for _, at := range []string{wantAt, gotAt} {
		if _, err := time.Parse(time.RFC3339, at); err != nil {
			t.Errorf("round %d at = %q, want RFC3339 wall time: %v", round, at, err)
		}
	}
	var want, have map[string]any
	if err := json.Unmarshal(wantRaw, &want); err != nil {
		t.Fatalf("round-%02d dryrun: %v", round, err)
	}
	if err := json.Unmarshal(got.Envelope, &have); err != nil {
		t.Fatalf("round %d envelope: %v", round, err)
	}
	delete(want, "at")
	delete(have, "at")
	if !reflect.DeepEqual(have, want) {
		t.Errorf("round %d envelope differs outside at: replay is not a deterministic rescore", round)
	}
}

// envelopeAt reads the volatile at timestamp from one record envelope.
func envelopeAt(t *testing.T, raw []byte, round int, which string) string {
	t.Helper()
	var env struct {
		At string `json:"at"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("round %d %s envelope: %v", round, which, err)
	}
	if env.At == "" {
		t.Fatalf("round %d %s envelope omits at, want the real wall time the test excludes", round, which)
	}
	return env.At
}

// checkReplayDeterministic proves two CLI replays of the same saved bytes
// agree on every scoring field, excluding only the volatile at timestamp and
// the agent wall replay skips by design.
func checkReplayDeterministic(t *testing.T, first, second *LoopOutcome) {
	t.Helper()
	if first.FinalValid != second.FinalValid || first.RoundsToValid != second.RoundsToValid {
		t.Errorf("replay validity = %+v vs %+v, want identical", first, second)
	}
	if len(first.Attempts) != len(second.Attempts) {
		t.Fatalf("replay attempts = %d vs %d, want identical", len(first.Attempts), len(second.Attempts))
	}
	for i := range first.Attempts {
		checkReplayAttempt(t, &first.Attempts[i], &second.Attempts[i])
	}
	if !reflect.DeepEqual(first.Added, second.Added) || !reflect.DeepEqual(first.Dropped, second.Dropped) {
		t.Errorf("replay added/dropped = %v/%v vs %v/%v, want identical", first.Added, first.Dropped, second.Added, second.Dropped)
	}
	checkReplayRatesEqual(t, first, second)
}

// checkReplayAttempt proves one round replays identically, excluding at/wall.
func checkReplayAttempt(t *testing.T, a, b *RoundAttempt) {
	t.Helper()
	if !bytes.Equal(a.ReviewBytes, b.ReviewBytes) {
		t.Errorf("round %d bytes differ between replays, want the preserved saved output", a.Round)
	}
	if a.Valid != b.Valid || a.Dry.Exit != b.Dry.Exit || a.Dry.Truncated != b.Dry.Truncated {
		t.Errorf("round %d scoring differs between replays: %+v vs %+v", a.Round, a.Dry, b.Dry)
	}
	if !reflect.DeepEqual(a.Dry.Problems, b.Dry.Problems) || !reflect.DeepEqual(a.Dry.Repairs, b.Dry.Repairs) || !reflect.DeepEqual(a.Dry.Warnings, b.Dry.Warnings) {
		t.Errorf("round %d problems/repairs/warnings differ between replays", a.Round)
	}
	checkReplayAttemptNormalized(t, a, b)
	checkReplayAttemptEnvelopes(t, a, b)
	checkReplayAttemptWall(t, a, b)
}

// checkReplayAttemptNormalized pins the accepted output across replays.
func checkReplayAttemptNormalized(t *testing.T, a, b *RoundAttempt) {
	t.Helper()
	var normA, normB any
	if len(a.Dry.Normalized) > 0 {
		if err := json.Unmarshal(a.Dry.Normalized, &normA); err != nil {
			t.Fatalf("round %d normalized: %v", a.Round, err)
		}
	}
	if len(b.Dry.Normalized) > 0 {
		if err := json.Unmarshal(b.Dry.Normalized, &normB); err != nil {
			t.Fatalf("round %d normalized: %v", a.Round, err)
		}
	}
	if !reflect.DeepEqual(normA, normB) {
		t.Errorf("round %d normalized differs between replays", a.Round)
	}
}

// checkReplayAttemptEnvelopes requires envelopes identical outside at.
func checkReplayAttemptEnvelopes(t *testing.T, a, b *RoundAttempt) {
	t.Helper()
	// The envelopes carry different at timestamps by design: record stamps
	// real wall time on every invocation. Assert both stamps exist and are
	// valid, then require the remainder identical.
	atA := envelopeAt(t, a.Dry.Envelope, a.Round, "first replay")
	atB := envelopeAt(t, b.Dry.Envelope, b.Round, "second replay")
	for _, at := range []string{atA, atB} {
		if _, err := time.Parse(time.RFC3339, at); err != nil {
			t.Errorf("round %d at = %q, want RFC3339: %v", a.Round, at, err)
		}
	}
	var envA, envB map[string]any
	if err := json.Unmarshal(a.Dry.Envelope, &envA); err != nil {
		t.Fatalf("round %d envelope: %v", a.Round, err)
	}
	if err := json.Unmarshal(b.Dry.Envelope, &envB); err != nil {
		t.Fatalf("round %d envelope: %v", b.Round, err)
	}
	delete(envA, "at")
	delete(envB, "at")
	if !reflect.DeepEqual(envA, envB) {
		t.Errorf("round %d envelopes differ outside at between replays", a.Round)
	}
}

// checkReplayAttemptWall asserts the wall gap replay leaves by design.
func checkReplayAttemptWall(t *testing.T, a, b *RoundAttempt) {
	t.Helper()
	// Replay skips the agent, so its wall stays zero while the shipped run
	// measured real agent time. Exclude wall by design and assert the gap.
	if a.Cost.Wall != 0 || b.Cost.Wall != 0 {
		t.Errorf("round %d replay wall = %v/%v, want zero: replay reuses saved bytes without re-running the agent", a.Round, a.Cost.Wall, b.Cost.Wall)
	}
}

// checkReplayRatesEqual requires identical fix rates across replays.
func checkReplayRatesEqual(t *testing.T, first, second *LoopOutcome) {
	t.Helper()
	firstRates := fixRatesFromOutcomes([]*LoopOutcome{first})
	secondRates := fixRatesFromOutcomes([]*LoopOutcome{second})
	if !reflect.DeepEqual(firstRates, secondRates) {
		t.Errorf("replay rates = %+v vs %+v, want identical", firstRates, secondRates)
	}
}
