package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	corpuscore "github.com/stainless-code/verdict/internal/core/corpus"
	"github.com/stainless-code/verdict/internal/golden"
)

func TestPublicationUsesInjectedDateAndDeterministicMarkdown(t *testing.T) {
	cfg := &Config{Now: time.Date(2040, 2, 3, 4, 5, 6, 0, time.UTC), K: 3, Runs: 3, Model: "fixture", Weights: config.SeverityWeights(nil)}
	r := &runner{cfg: cfg, commit: "abcdef", cases: []corpusCase{{id: "clean", manifest: []byte(`{"kind":"control","provenance":{"kind":"seeded"}}`)}}}
	rep := r.newReport()
	rep.ToolVersion = "verdict fixture-version"
	rep.PromptsHash = "fixture-prompts-hash"
	rep.Commands = [][]string{{"verdict", "trend", "acme/bench", "--json"}}
	rep.AnyOfKRecall = corpuscore.Measurement{Value: 0.6667, Numerator: 2, Denominator: 3, N: 3, Unit: "unique bugs"}
	if rep.Date != "2040-02-03" || rep.GeneratedAt != "2040-02-03T04:05:06Z" {
		t.Fatalf("date=%q generated=%q; want injected 2040-02-03T04:05:06Z", rep.Date, rep.GeneratedAt)
	}
	text := renderMarkdown(rep)
	for _, want := range []string{"not a model-quality claim", "fixture-version", "fixture-prompts-hash", "| any_of_k_recall | 0.6667 | 2 | 3 | 3 |", "Cost USD: null (n=0/0 samples)."} {
		if !strings.Contains(text, want) {
			t.Errorf("Markdown omits %q", want)
		}
	}
	if text != renderMarkdown(rep) {
		t.Fatal("render changed on repeat")
	}
	golden.Check(t, "publication-markdown", text)
}

func TestRenderOnlyNeedsPublication(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report with space.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"1","date":"2040-02-03","seeded":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := parseFlags([]string{"-render", path})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := renderFile(cfg.Render, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "# Benchmark 2040-02-03") {
		t.Fatalf("render=%q; want publication date", out.String())
	}
}

func TestSampleSpreadsSortCasesAndMetrics(t *testing.T) {
	zero := 0.0
	stat := Stat{N: 1, Median: &zero, Min: &zero, Max: &zero}
	a := CaseSpread{Case: "a", Metrics: map[string]Stat{"precision": stat, "control_fp_rate": stat}}
	b := CaseSpread{Case: "b", Metrics: map[string]Stat{"control_fp_rate": stat, "precision": stat}}
	var forward, reverse strings.Builder
	renderSampleSpreads(&forward, []CaseSpread{a, b})
	renderSampleSpreads(&reverse, []CaseSpread{b, a})
	if forward.String() != reverse.String() {
		t.Fatal("reversed cases changed static output")
	}
	want := "| a | control_fp_rate | 0.000000 | 0.000000 | 0.000000 | 1 |\n| a | precision |"
	if !strings.Contains(forward.String(), want) {
		t.Fatalf("sorted output lacks %q", want)
	}
}
