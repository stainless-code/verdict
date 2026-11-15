package calibratecore

import (
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/corpus/corpustest"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// TestCalibrateFiltersDiffSpelledPhrase proves calibrate grades through the
// bundle's own diff: a finding that names only the phrase the diff carries is
// unmatched, while the same finding names the defect against a diff without it.
func TestCalibrateFiltersDiffSpelledPhrase(t *testing.T) {
	tests := []struct {
		name string
		diff string
		body string
		want int
	}{
		{name: "diff spells the phrase", diff: corpustest.SpelledDiff, body: corpustest.Finding},
		{name: "control diff leaves the phrase", diff: corpustest.CleanDiff, body: corpustest.CleanBody, want: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { checkCalibrateFilter(t, tc.diff, tc.body, tc.want) })
	}
}

func checkCalibrateFilter(t *testing.T, diff, body string, want int) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(readcommon.StateDir(root), "corpus-"+corpustest.CaseID)
	corpustest.Bundle(t, dir, diff, body)
	read := core.ReadRequest{RepoRoot: root, Pr: "corpus-" + corpustest.CaseID}
	resp, ref := Run(t.Context(), &Request{ReadRequest: read})
	if ref != nil {
		t.Fatalf("calibrate refusal %s: %s", ref.Code, ref.Message)
	}
	if resp.Data.corpus == nil {
		t.Fatal("calibrate returned no corpus result")
	}
	if got := resp.Data.corpus.report.Matched; got != want {
		t.Fatalf("matched = %d, want %d", got, want)
	}
}
