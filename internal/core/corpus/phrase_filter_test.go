package corpuscore

import (
	"encoding/json"
	"os"
	"path/filepath"
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
			rep := EvaluateWithDiff(c, []any{entry("pool.go", 45, tc.body)}, []byte(raceDiff))
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
	manifest, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "corpus.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(raceDiff), 0o600); err != nil {
		t.Fatal(err)
	}
	rep := EvaluateDir(dir, c, []any{entry("pool.go", 45, "The goroutine closes the conn after the pool takes it back.")})
	if rep.Matched != 0 {
		t.Fatalf("matched = %d, want 0: the diff's own goroutine must not name the defect", rep.Matched)
	}
}

func raceCase(keywords ...string) *core.CorpusCase {
	return &core.CorpusCase{
		SchemaVersion: "1",
		ID:            "race-filter",
		Kind:          kindReal,
		Provenance:    core.CorpusProvenance{Kind: kindReal},
		Bugs: []core.CorpusBug{{
			Path: "pool.go", Line: 45, Kind: "race",
			Severity: "important", Keywords: keywords,
		}},
	}
}
