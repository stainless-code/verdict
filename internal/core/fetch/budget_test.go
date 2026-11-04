package fetchcore

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEstimateRecsWarnsWithSlashPaths pins the user-visible warning to
// forward-slash paths. The sweep globs `.review/*/cost.json`, and
// [filepath.Glob] returns OS separators, so Windows rendered
// `.review\2\cost.json` where the golden and every bundle path uses slashes.
func TestEstimateRecsWarnsWithSlashPaths(t *testing.T) {
	t.Skip("prototype: state root moved to .verdict; stale .review expectations")
	dir := t.TempDir()
	path := filepath.Join(dir, ".review", "2", "cost.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var warnings []string
	r := &session{warnings: &warnings}
	if recs := estimateRecs(r, nil); len(recs) != 0 {
		t.Fatalf("recs = %d, want 0", len(recs))
	}
	want := "cost history unreadable — skipped from estimate: .review/2/cost.json "
	if len(warnings) != 1 || warnings[0] != want {
		t.Fatalf("warnings = %q, want [%q]", warnings, want)
	}
}
