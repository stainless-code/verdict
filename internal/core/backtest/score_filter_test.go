package backtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/corpus/corpustest"
)

// TestScoreFiltersDiffSpelledPhrase proves backtest score grades through the
// replay bundle's diff: a finding that names only the phrase the diff carries
// is unmatched, while the same finding names the defect against a control diff.
func TestScoreFiltersDiffSpelledPhrase(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) { checkScoreFilter(t, tc.diff, tc.body, tc.want) })
	}
}

func checkScoreFilter(t *testing.T, diff, body string, want int) {
	t.Helper()
	root := t.TempDir()
	bundle := "local-bt-1-filter"
	corpustest.Review(t, filepath.Join(root, ".verdict", "state", bundle), diff, body)
	writeRaceScoreRun(t, root, bundle)
	resp, ref := RunScore(t.Context(), &ScoreRequest{
		Runtime: &core.Runtime{Now: scoreClock}, RepoRoot: root, Run: "s1",
	})
	if ref != nil {
		t.Fatalf("score refusal %s: %s", ref.Code, ref.Message)
	}
	if got := resp.Data.Matched; got != want {
		t.Fatalf("matched = %d, want %d", got, want)
	}
}

// writeRaceScoreRun plants one replayed candidate whose mined bug is a race on
// the fixture's added line.
func writeRaceScoreRun(t *testing.T, root, bundle string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".verdict", "backtest", "s1", "prs", "001"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, ".verdict", "backtest", "s1", "mine.json"),
		`{"schema_version":"1","method":"szz-blame-v1","candidates":[`+
			`{"head":"1111111111111111111111111111111111111111","base":"2222222222222222222222222222222222222222",`+
			`"landed_by":"3333333333333333333333333333333333333333","pr":2,"reconstruction":"merge-parent",`+
			`"head_source":"merge-time","merged_at":"2024-01-03T00:00:00Z","head_at":"2024-01-02T00:00:00Z",`+
			`"fixes":["4444444444444444444444444444444444444444"],"bugs":[{"path":"pool.go","line":45,`+
			`"kind":"race","signal":"fix","fix":"4444444444444444444444444444444444444444"}]}],"skipped":[]}`)
	writeTestFile(t, filepath.Join(root, ".verdict", "backtest", "s1", "prs", "001", "replay.json"),
		`{"schema_version":"1","run":"s1","head":"1111111111111111111111111111111111111111",`+
			`"base":"2222222222222222222222222222222222222222","agent":"claude","status":"ok",`+
			`"bundle":"`+bundle+`","at":"2024-02-01T12:00:00Z","pr":2,"cost_usd":null,"n":1,`+
			`"pass_through":[]}`)
}
