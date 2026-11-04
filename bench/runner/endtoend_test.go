package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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
