package backtest

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/golden"
	"github.com/stainless-code/verdict/internal/jq"
)

// scoreRun is the fixture run every scoring case reads.
const scoreRun = "s1"

// scoreClock pins the response timestamp.
func scoreClock() time.Time { return time.Date(2024, 2, 1, 12, 0, 0, 0, time.UTC) }

// scoreFixture assembles the checked-in run tree into a temp repository:
// scoring writes report.json, so the fixture itself stays pristine.
func scoreFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	copyScoreTree(t, filepath.Join("testdata", "score", "run"), filepath.Join(root, ".verdict", "backtest", scoreRun))
	copyScoreTree(t, filepath.Join("testdata", "score", "state"), filepath.Join(root, ".verdict", "state"))
	return root
}

// copyScoreTree copies every file under src into the same relative path
// under dst.
func copyScoreTree(t *testing.T, src, dst string) {
	t.Helper()
	walk := func(path string, entry fs.DirEntry, err error) error {
		return copyScoreEntry(src, dst, path, entry, err)
	}
	if err := filepath.WalkDir(src, walk); err != nil {
		t.Fatalf("copy the score fixture: %v", err)
	}
}

// copyScoreEntry copies one walked entry into the mirrored path.
func copyScoreEntry(src, dst, path string, entry fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(src, path)
	if err != nil {
		return err
	}
	target := filepath.Join(dst, rel)
	if entry.IsDir() {
		return os.MkdirAll(target, 0o755)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(target, raw, 0o644)
}

// runScoreOK runs the service against one copied fixture.
func runScoreOK(t *testing.T, root string, rec *recorder) *ScoreResponse {
	t.Helper()
	resp, ref := RunScore(t.Context(), &ScoreRequest{
		Runtime:  &core.Runtime{Now: scoreClock, Progress: rec},
		RepoRoot: root, Run: scoreRun,
	})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	return resp
}

// readScoreFile reads one artifact a scoring pass wrote.
func readScoreFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// scoreReportPath is one run's report.json.
func scoreReportPath(root, run string) string {
	return filepath.Join(root, ".verdict", "backtest", run, "report.json")
}

// TestScoreGoldenRun pins report.json and the human lines against the
// checked-in fixture, and proves the envelope data is the artifact's own
// object.
func TestScoreGoldenRun(t *testing.T) {
	root := scoreFixture(t)
	rec := &recorder{}
	resp := runScoreOK(t, root, rec)
	report := readScoreFile(t, scoreReportPath(root, scoreRun))
	golden.Check(t, "backtest-score-report", report)
	golden.Check(t, "backtest-score-human", strings.Join(rec.lines, "\n")+"\n")
	if got := jq.PrettyOrdered(resp.Data.Wire()); got != report {
		t.Errorf("envelope data and report.json differ:\n%s\n---\n%s", got, report)
	}
	if want := ".verdict/backtest/s1/report.json"; !strings.Contains(resp.Next, want) {
		t.Errorf("next = %q, want a pointer to %s", resp.Next, want)
	}
}

// scoreRefusalCase is one unscoreable run and the refusal it must produce.
type scoreRefusalCase struct {
	name   string
	run    string
	mutate func(t *testing.T, root string)
	code   string
	part   string
}

// TestScoreRefusesBadRuns proves each unscoreable run refuses with the code an
// operator can act on.
func TestScoreRefusesBadRuns(t *testing.T) {
	tests := []scoreRefusalCase{
		{name: "no run id", run: "", code: "bad-flags", part: "--run <id>"},
		{name: "bad slug", run: "../s1", code: "bad-slug", part: "lowercase letters"},
		{name: "unmined", run: "s2", code: "missing-bundle", part: "no mine.json"},
		{name: "unreplayed", run: "s1", mutate: removeScoreRows, code: "missing-bundle", part: "no replayed reviews"},
		{name: "corrupt row", run: "s1", mutate: corruptScoreRow, code: "backtest-row-stale", part: "unreadable"},
		{name: "pre-signal mine", run: "s1", mutate: stripScoreSignal, code: "backtest-run-stale", part: "before bugs recorded a seed signal"},
		{name: "row names no bundle", run: "s1", mutate: stripScoreBundle, code: "backtest-row-stale", part: "names no bundle"},
		{name: "missing findings", run: "s1", mutate: removeScoreFindings, code: "backtest-row-stale", part: "missing or unreadable"},
		{name: "corrupt findings", run: "s1", mutate: corruptScoreFindings, code: "backtest-row-stale", part: "missing or unreadable"},
		{name: "re-mined candidate", run: "s1", mutate: remineScoreRow, code: "backtest-run-stale", part: "no longer carries"},
		{name: "foreign row", run: "s1", mutate: foreignScoreRow, code: "backtest-row-stale", part: "not 's1'"},
		{name: "repeated row", run: "s1", mutate: repeatScoreRow, code: "backtest-row-stale", part: "already scored"},
		{name: "row names a path", run: "s1", mutate: stripScoreBundleToPath, code: "backtest-row-stale", part: "not one path element"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { checkScoreRefusal(t, tc) })
	}
}

// checkScoreRefusal proves one refusal's code, named cause, and that no
// report was written.
func checkScoreRefusal(t *testing.T, tc scoreRefusalCase) {
	t.Helper()
	root := scoreFixture(t)
	if tc.mutate != nil {
		tc.mutate(t, root)
	}
	_, ref := RunScore(t.Context(), &ScoreRequest{
		Runtime:  &core.Runtime{Now: scoreClock, Progress: &recorder{}},
		RepoRoot: root, Run: tc.run,
	})
	if ref == nil {
		t.Fatal("no refusal")
	}
	if ref.Code != tc.code || !strings.Contains(ref.Message, tc.part) {
		t.Fatalf("refusal = %s: %s; want %s naming %q", ref.Code, ref.Message, tc.code, tc.part)
	}
	if _, err := os.Stat(scoreReportPath(root, scoreRun)); !os.IsNotExist(err) {
		t.Errorf("a refused score wrote report.json: %v", err)
	}
}

// removeScoreRows deletes the replay records, leaving a mined but unreplayed
// run.
func removeScoreRows(t *testing.T, root string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(root, ".verdict", "backtest", scoreRun, "prs")); err != nil {
		t.Fatal(err)
	}
}

// TestScoreRefusalHintsNameTheBacktestStep proves the two score refusals do
// not borrow the shared run-mismatch/corrupt-store hints: each printed step
// rebuilds a backtest artifact and never deletes the run's replay bundles.
func TestScoreRefusalHintsNameTheBacktestStep(t *testing.T) {
	tests := []struct{ code, hint string }{
		{
			code: "backtest-row-stale",
			hint: "try: delete the named prs/<NNN>/ directory, then replay the run so it records a fresh row",
		},
		{
			code: "backtest-run-stale",
			hint: "try: re-mine the run in place with the same --run and window, then replay it again",
		},
	}
	for _, tc := range tests {
		if got := core.HintFor(tc.code); got != tc.hint {
			t.Errorf("HintFor(%q) = %q, want %q", tc.code, got, tc.hint)
		}
	}
}

// stripScoreSignal rewrites the mine as one written before bugs recorded a
// seed signal.
func stripScoreSignal(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".verdict", "backtest", scoreRun, "mine.json")
	body := strings.Replace(readScoreFile(t, path), `"signal": "fix",`+"\n", "", 1)
	writeTestFile(t, path, body)
}

// stripScoreBundle rewrites the first row so it names no bundle.
func stripScoreBundle(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".verdict", "backtest", scoreRun, "prs", "001", "replay.json")
	body := strings.Replace(readScoreFile(t, path), `"bundle": "local-bt-1-s1"`, `"bundle": ""`, 1)
	writeTestFile(t, path, body)
}

// stripScoreBundleToPath rewrites the first row so the bundle it names is a
// traversal instead of one path element.
func stripScoreBundleToPath(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".verdict", "backtest", scoreRun, "prs", "001", "replay.json")
	body := strings.Replace(readScoreFile(t, path), `"bundle": "local-bt-1-s1"`, `"bundle": "../escaped"`, 1)
	writeTestFile(t, path, body)
}

// removeScoreFindings deletes the first row's recorded review, leaving its
// resume record pointing at a bundle with no findings.
func removeScoreFindings(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".verdict", "state", "local-bt-1-s1", "findings.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// corruptScoreFindings writes bytes the findings store cannot decode.
func corruptScoreFindings(t *testing.T, root string) {
	t.Helper()
	writeTestFile(t, filepath.Join(root, ".verdict", "state", "local-bt-1-s1", "findings.json"), "{")
}

// corruptScoreRow truncates one resume record.
func corruptScoreRow(t *testing.T, root string) {
	t.Helper()
	writeTestFile(t, filepath.Join(root, ".verdict", "backtest", scoreRun, "prs", "001", "replay.json"), "{")
}

// remineScoreRow rewrites the head a recorded review names, so the mine no
// longer carries its candidate.
func remineScoreRow(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".verdict", "backtest", scoreRun, "prs", "001", "replay.json")
	body := strings.Replace(readScoreFile(t, path), "1111111111111111111111111111111111111111", "9999", 1)
	writeTestFile(t, path, body)
}

// repeatScoreRow copies one resume record beside itself, so two rows name the
// same candidate.
func repeatScoreRow(t *testing.T, root string) {
	t.Helper()
	src := filepath.Join(root, ".verdict", "backtest", scoreRun, "prs", "001", "replay.json")
	body := strings.Replace(readScoreFile(t, src), `"n": 1`, `"n": 3`, 1)
	dst := filepath.Join(root, ".verdict", "backtest", scoreRun, "prs", "003")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dst, "replay.json"), body)
}

// foreignScoreRow rewrites one record's run id, so it belongs to another run.
func foreignScoreRow(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".verdict", "backtest", scoreRun, "prs", "001", "replay.json")
	body := strings.Replace(readScoreFile(t, path), `"run": "s1"`, `"run": "s2"`, 1)
	writeTestFile(t, path, body)
}

// TestScoreUnclassifiedBugIsNotMatchable proves the documented guarantee: a
// finding that names the sentinel cannot turn an unclassified bug into a true
// positive. The fixture already carries one at b.go:9, so the probe adds the
// one finding that would match it.
func TestScoreUnclassifiedBugIsNotMatchable(t *testing.T) {
	root := scoreFixture(t)
	addUnclassifiedFinding(t, root)
	data := runScoreOK(t, root, &recorder{}).Data
	if data.Bugs != 3 || data.Matched != 2 {
		t.Errorf("bugs/matched = %d/%d; want 3/2", data.Bugs, data.Matched)
	}
	if data.CatchRate == nil || *data.CatchRate != 0.6667 {
		t.Errorf("catch_rate = %v; want 0.6667", data.CatchRate)
	}
	if data.Signals[1].CatchRate == nil || *data.Signals[1].CatchRate != 0.5 {
		t.Errorf("revert rate = %v; want 0.5 (1 of 2)", data.Signals[1].CatchRate)
	}
}

// addUnclassifiedFinding appends a finding on the unclassified bug's line,
// naming the sentinel the way the matcher would read it.
func addUnclassifiedFinding(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".verdict", "state", "local-bt-2-s1", "findings.json")
	const entry = `  {
    "kind": "inline",
    "path": "b.go",
    "side": "RIGHT",
    "line": 9,
    "anchored": true,
    "body": "This unclassified branch is never reached.",
    "suggestion": null,
    "rigor": "walked",
    "severity": "important",
    "sig": "dddddddddddddddddddddddddddddddddddddddd",
    "status": "open"
  }`
	body := strings.Replace(readScoreFile(t, path), "\n]", ",\n"+entry+"\n]", 1)
	writeTestFile(t, path, body)
}

// TestScoreEmptyMineReportsZero proves a mined run with no candidates scores
// as a zero report instead of refusing: replay writes no prs directory for an
// empty candidate list, so "never replayed" would misread a completed run.
func TestScoreEmptyMineReportsZero(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, ".verdict", "backtest", "empty")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(runDir, "mine.json"),
		`{"schema_version":"1","method":"szz-blame-v1","candidates":[],"skipped":[]}`)
	rec := &recorder{}
	resp, ref := RunScore(t.Context(), &ScoreRequest{
		Runtime: &core.Runtime{Now: scoreClock, Progress: rec}, RepoRoot: root, Run: "empty",
	})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	data := resp.Data
	if data.PRs != 0 || data.NotReplayed != 0 || data.Bugs != 0 {
		t.Errorf("counts = %+v; want zero everywhere", data)
	}
	checkUnmeasured(t, data)
	if _, err := os.Stat(scoreReportPath(root, "empty")); err != nil {
		t.Errorf("an empty run wrote no report.json: %v", err)
	}
	if want := "backtest score empty  (0 PRs, 0 bugs)"; len(rec.lines) == 0 || rec.lines[0] != want {
		t.Errorf("header = %v; want %q", rec.lines, want)
	}
}

// TestScoreNoReviewsReportsUnmeasured proves a run whose every row failed
// writes a report of nulls and counts the candidates it could not score.
func TestScoreNoReviewsReportsUnmeasured(t *testing.T) {
	root := scoreFixture(t)
	failAllRows(t, root)
	rec := &recorder{}
	data := runScoreOK(t, root, rec).Data
	if data.PRs != 0 || data.NotReplayed != 2 || data.Bugs != 0 || data.Reported != 0 {
		t.Errorf("counts = %+v; want no scored PRs and two not replayed", data)
	}
	checkUnmeasured(t, data)
	want := []string{
		"backtest score s1  (0 PRs, 0 bugs, 2 not replayed)",
		"  catch rate: n/a (0/0 bugs)",
		"  off-target: n/a per PR (0 reported, 0 matched, 0 PRs)",
		"  cost: n/a per review (0 priced reviews, 0 unpriced)",
		"  unclassified: 0 of 0 bugs (kind matched no rule)",
		"  signal fix: n/a (0/0)",
		"  signal revert: n/a (0/0)",
		"not citable — SZZ-derived: a score grades a blame-derived guess, not a verified defect",
	}
	if !slices.Equal(rec.lines, want) {
		t.Errorf("human lines =\n%s\nwant\n%s", strings.Join(rec.lines, "\n"), strings.Join(want, "\n"))
	}
}

// checkUnmeasured proves every rate and the spend read null, never zero.
func checkUnmeasured(t *testing.T, data *ScoreReport) {
	t.Helper()
	if data.CatchRate != nil || data.OffTargetPerPR != nil || data.CostPerReviewUSD != nil || data.Spend.USD != nil {
		t.Errorf("rates = %+v; an unmeasured rate is null, never zero", data)
	}
	for i := range data.Signals {
		if data.Signals[i].CatchRate != nil {
			t.Errorf("signal %s rate = %v, want null", data.Signals[i].Signal, *data.Signals[i].CatchRate)
		}
	}
}

// TestScoreUnpricedReviewsNullTheCost proves a run that priced nothing reports
// an unknown cost per review and a null spend total, with the unpriced count
// left as the mark that a later partial sum is a lower bound.
func TestScoreUnpricedReviewsNullTheCost(t *testing.T) {
	root := scoreFixture(t)
	path := filepath.Join(root, ".verdict", "backtest", scoreRun, "prs", "001", "replay.json")
	body := strings.Replace(readScoreFile(t, path), `"cost_usd": 0.5`, `"cost_usd": null`, 1)
	writeTestFile(t, path, body)
	rec := &recorder{}
	data := runScoreOK(t, root, rec).Data
	if data.CostPerReviewUSD != nil || data.Spend.USD != nil {
		t.Errorf("cost = %v spend = %v; want both null with no priced review", data.CostPerReviewUSD, data.Spend.USD)
	}
	if data.Spend.PricedReviews != 0 || data.Spend.UnpricedReviews != 2 {
		t.Errorf("spend = %+v; want two unpriced reviews", data.Spend)
	}
	if want := "  cost: n/a per review (0 priced reviews, 2 unpriced)"; !slices.Contains(rec.lines, want) {
		t.Errorf("human lines = %q, want %q", rec.lines, want)
	}
}

// TestScoreIsAPureReadOfTheRun proves scoring writes report.json and nothing
// else: no bundle, no corpus-* store, no fixture byte touched.
func TestScoreIsAPureReadOfTheRun(t *testing.T) {
	root := scoreFixture(t)
	before := scoreTree(t, root)
	runScoreOK(t, root, &recorder{})
	after := scoreTree(t, root)
	report := ".verdict/backtest/s1/report.json"
	delete(after, report)
	if !mapsEqual(before, after) {
		t.Errorf("scoring changed the run tree:\nbefore %v\nafter %v", before, after)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".verdict", "state"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "corpus-") {
			t.Errorf("scoring wrote corpus bundle %s", entry.Name())
		}
	}
}

// scoreTree snapshots every file's bytes under root.
func scoreTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	walk := func(path string, entry fs.DirEntry, err error) error {
		return scoreEntry(root, out, path, entry, err)
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return out
}

// scoreEntry records one walked file's bytes.
func scoreEntry(root string, out map[string]string, path string, entry fs.DirEntry, err error) error {
	if err != nil || entry.IsDir() {
		return err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out[filepath.ToSlash(rel)] = string(raw)
	return nil
}

// mapsEqual compares two snapshots by key and bytes.
func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

// failAllRows rewrites every resume record as an agent failure, the shape a
// replay writes when no review was recorded.
func failAllRows(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".verdict", "backtest", scoreRun, "prs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name(), "replay.json")
		body := strings.Replace(readScoreFile(t, path), `"status": "ok"`, `"status": "agent-failed"`, 1)
		writeTestFile(t, path, body)
	}
}

// TestScoreHonorsCancellationDuringThePass proves a cancel after the start
// check stops the pass: the first row is refused before its review is graded,
// and a cancel after the last review suppresses the report. The first case
// carries a missing findings file, so grading it would refuse with
// backtest-row-stale unless the per-row cancel check fires first.
func TestScoreHonorsCancellationDuringThePass(t *testing.T) {
	t.Run("between reviews", func(t *testing.T) { checkScoreCanceled(t, 2, removeScoreFindings) })
	t.Run("before the report", func(t *testing.T) { checkScoreCanceled(t, 4, nil) })
}

// checkScoreCanceled proves one mid-pass cancel refuses with the frozen score
// text and writes no report.
func checkScoreCanceled(t *testing.T, after int, mutate func(t *testing.T, root string)) {
	t.Helper()
	root := scoreFixture(t)
	if mutate != nil {
		mutate(t, root)
	}
	ctx := &cancelAfterContext{after: after}
	_, ref := RunScore(ctx, &ScoreRequest{
		Runtime: &core.Runtime{Now: scoreClock}, RepoRoot: root, Run: scoreRun,
	})
	if ref == nil || ref.Code != "canceled" || ref.Message != msgScoreCanceled {
		t.Fatalf("refusal = %+v; want the frozen score cancel", ref)
	}
	if _, err := os.Stat(scoreReportPath(root, scoreRun)); !os.IsNotExist(err) {
		t.Errorf("a canceled score wrote report.json: %v", err)
	}
}

// cancelAfterContext reports cancellation on the Nth Err call, so a test can
// interrupt a pass without racing a goroutine. It is [context.Background]
// with Err overridden; no pass in the test reads the other three methods.
type cancelAfterContext struct {
	after int
	calls int
}

// Deadline implements [context.Context].
func (c *cancelAfterContext) Deadline() (time.Time, bool) { return time.Time{}, false }

// Done implements [context.Context].
func (c *cancelAfterContext) Done() <-chan struct{} { return nil }

// Value implements [context.Context].
func (c *cancelAfterContext) Value(any) any { return nil }

// Err implements [context.Context].
func (c *cancelAfterContext) Err() error {
	c.calls++
	if c.calls >= c.after {
		return context.Canceled
	}
	return nil
}

// TestScoreCancelReportsTheScoreVerb proves an interrupted pass reports the
// score's own cancel text and writes no report.
func TestScoreCancelReportsTheScoreVerb(t *testing.T) {
	root := scoreFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, ref := RunScore(ctx, &ScoreRequest{Runtime: &core.Runtime{Now: scoreClock}, RepoRoot: root, Run: scoreRun})
	if ref == nil || ref.Code != "canceled" || ref.Message != "canceled before backtest score completed" {
		t.Fatalf("refusal = %+v; want the frozen score cancel", ref)
	}
}
