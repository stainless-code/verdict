package corpuscore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestDiffSpelledBuiltinPhrasesAreFilteredFromGrading pins the filter: a
// built-in phrase the case's own diff carries must not name the defect, while
// a non-spelled phrase or the curator's keyword still does.
func TestDiffSpelledBuiltinPhrasesAreFilteredFromGrading(t *testing.T) {
	c := raceCase("data race")
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "diff comment names only goroutine", body: "The goroutine closes the conn after the pool takes it back."},
		{name: "non-spelled category phrase", body: "This is a race between the pool and the borrower.", want: true},
		{name: "curator keyword", body: "Two borrowers share a data race on the conn.", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := evaluate(c, []any{entry("pool.go", 45, tc.body)}, []byte(raceDiff))
			if got := rep.Matched == 1; got != tc.want {
				t.Fatalf("matched = %v, want %v (outcomes %+v)", got, tc.want, rep.Outcomes)
			}
		})
	}
}

// TestEvaluateDirFiltersFromBundleDiff proves the bundle path calibrate and
// trend use reads the diff beside corpus.json, not only the diff-less case.
func TestEvaluateDirFiltersFromBundleDiff(t *testing.T) {
	dir := t.TempDir()
	c := raceCase("data race")
	writeManifest(t, dir, c)
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(raceDiff), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, ref := EvaluateDir(dir, c, []any{entry("pool.go", 45, "The goroutine closes the conn after the pool takes it back.")})
	if ref != nil {
		t.Fatalf("ref = %v, want the bundle's diff to be read", ref)
	}
	if rep.Matched != 0 {
		t.Fatalf("matched = %d, want 0: the diff's own goroutine must not name the defect", rep.Matched)
	}
}

// TestEvaluateDirRefusesARealCaseWithoutDiff is the fail-closed guard: a real
// bundle that lost its full.diff, or kept only blank bytes, must not grade with
// the unfiltered built-ins.
func TestEvaluateDirRefusesARealCaseWithoutDiff(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		write bool
	}{
		{name: "missing"},
		{name: "empty", write: true},
		{name: "whitespace", body: "\n\n \t\n", write: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeManifest(t, dir, raceCase("data race"))
			c, ref := Load(dir)
			if ref != nil {
				t.Fatalf("Load: %v", ref)
			}
			if tc.write {
				if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(tc.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			rep, ref := EvaluateDir(dir, c, []any{entry("pool.go", 45, "The goroutine closes the conn.")})
			if ref == nil || ref.Code != codeCorpusInvalid {
				t.Fatalf("EvaluateDir = %v, %v; want a corpus-invalid refusal", rep, ref)
			}
			if !strings.Contains(ref.Message, "full.diff") {
				t.Errorf("message %q does not name full.diff", ref.Message)
			}
		})
	}
}

// TestEvaluateDirKeepsSeededWithoutDiff pins the exception: a seeded case has
// no reviewed diff to filter, so its full vocabulary still grades.
func TestEvaluateDirKeepsSeededWithoutDiff(t *testing.T) {
	dir := t.TempDir()
	c := raceCase("data race")
	c.Kind = kindSeeded
	c.Provenance.Kind = kindSeeded
	rep, ref := EvaluateDir(dir, c, []any{entry("pool.go", 45, "This is a race between the pool and the borrower.")})
	if ref != nil {
		t.Fatalf("ref = %v, want a seeded case to keep grading", ref)
	}
	if rep.Matched != 1 {
		t.Fatalf("matched = %d, want 1", rep.Matched)
	}
}

func writeManifest(t *testing.T, dir string, c *core.CorpusCase) {
	t.Helper()
	manifest, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "corpus.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
}

func raceCase(keywords ...string) *core.CorpusCase {
	pr := 2319
	return &core.CorpusCase{
		SchemaVersion: "1",
		ID:            "race-filter",
		Kind:          kindReal,
		Provenance: core.CorpusProvenance{
			Kind: kindReal, Repo: "urfave/cli", PR: &pr,
			BaseSHA: strings.Repeat("1", 40), HeadSHA: strings.Repeat("2", 40), FixCommit: strings.Repeat("3", 40),
		},
		Bugs: []core.CorpusBug{{
			Path: "pool.go", Line: 45, Kind: "race",
			Severity: "important", Keywords: keywords,
		}},
	}
}

// evaluateAll grades with the unfiltered vocabulary. Tests that predate the
// diff filter use it; production has no diff-less entry point.
func evaluateAll(c *core.CorpusCase, entries []any) *Report {
	return evaluate(c, entries, nil)
}
