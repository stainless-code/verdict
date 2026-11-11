package initcore

import "testing"

// TestPresetGitignoreCoversBacktest pins the scaffolded ignore body: review
// state and mined backtest artifacts stay local, and the tracked configuration
// stays reviewable.
func TestPresetGitignoreCoversBacktest(t *testing.T) {
	t.Parallel()
	files := presetFiles(presets["standard"])
	want := "/state/\n/backtest/"
	if got := files[gitignoreName]; got != want {
		t.Errorf(".verdict/.gitignore body = %q; want %q", got, want)
	}
}
