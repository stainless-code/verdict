package trendcore

import (
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/corpus/corpustest"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// TestTrendFiltersDiffSpelledPhrase proves trend's corpus rollup grades through
// the bundle's own diff: the diff-spelled phrase is unavailable, and the same
// finding matches a control diff that does not carry it.
func TestTrendFiltersDiffSpelledPhrase(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) { checkTrendFilter(t, tc.diff, tc.body, tc.want) })
	}
}

func checkTrendFilter(t *testing.T, diff, body string, want int) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(readcommon.StateDir(root), "corpus-"+corpustest.CaseID)
	corpustest.Bundle(t, dir, diff, body)
	prog := &progressLog{}
	read := core.ReadRequest{Runtime: &core.Runtime{Progress: prog}, RepoRoot: root}
	resp, ref := Run(t.Context(), &Request{ReadRequest: read})
	if ref != nil {
		t.Fatalf("trend refusal %s: %s", ref.Code, ref.Message)
	}
	if resp.Data.Corpus == nil {
		t.Fatal("trend returned no corpus rollup")
	}
	if got := resp.Data.Corpus.total.matched; got != want {
		t.Fatalf("matched = %d, want %d", got, want)
	}
}
