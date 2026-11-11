package checktreecore

import "testing"

// TestSplitScratchKeepsSharedControls proves review state and backtest
// artifacts are the only run scratch: a control file that is untracked,
// modified, or renamed out of those directories stays in the dirt set, so the
// gate cannot report clean for an uncommitted policy.
func TestSplitScratchKeepsSharedControls(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		line    string
		scratch bool
	}{
		{"untracked shared config", "?? .verdict/config.json", false},
		{"modified shared config", " M .verdict/config.json", false},
		{"untracked kb", "?? .verdict/kb.jsonl", false},
		{"tracked run state", " M .verdict/state/local-a/run.json", true},
		{"untracked run state", "?? .verdict/state/local-a/findings.json", true},
		{"rename out of state", "R  .verdict/state/local-a/run.json -> .verdict/config.json", false},
		{"rename shared config into state", "R  .verdict/config.json -> .verdict/state/local-a/run.json", false},
		{"rename within state", "R  .verdict/state/local-a/run.json -> .verdict/state/local-b/run.json", true},
		{"tracked backtest artifact", " M .verdict/backtest/run/mine.json", true},
		{"untracked backtest artifact", "?? .verdict/backtest/run/run.json", true},
		{"rename out of backtest", "R  .verdict/backtest/run/mine.json -> .verdict/config.json", false},
		{"rename config into backtest", "R  .verdict/config.json -> .verdict/backtest/run/mine.json", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertScratchClass(t, tc.line, tc.scratch)
		})
	}
}

// assertScratchClass checks the line lands in exactly one of the two sets.
func assertScratchClass(t *testing.T, line string, scratch bool) {
	t.Helper()
	own, kept := splitScratch(line)
	if got := own == 1; got != scratch {
		t.Fatalf("scratch = %v for %q; want %v", got, line, scratch)
	}
	if scratch != (kept == "") {
		t.Fatalf("kept = %q; dirt and scratch disagree for %q", kept, line)
	}
}
