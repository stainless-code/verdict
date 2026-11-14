package initcore

import "testing"

// TestPresetGitignoreCoversBacktest pins the scaffolded ignore body: review
// state, mined backtest artifacts, the local index, and the incident ledger's
// lock stay local, and the tracked configuration stays reviewable.
func TestPresetGitignoreCoversBacktest(t *testing.T) {
	t.Parallel()
	files := presetFiles(presets["standard"])
	want := "/state/\n/backtest/\n/index/\nincidents.jsonl.lock\nkb.jsonl.lock\nreverts.jsonl.lock"
	if got := files[gitignoreName]; got != want {
		t.Errorf(".verdict/.gitignore body = %q; want %q", got, want)
	}
}
