package readcommon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCostFilesListsBundleAndChunks pins the file list the rollup and the
// cost-name repair share: the bundle's own record first, then each chunk
// record once, sorted — a chunk named by the manifest and found by the glob
// must not be read twice, because pricing it twice would double real spend.
func TestCostFilesListsBundleAndChunks(t *testing.T) {
	dir := t.TempDir()
	writeCostFixture(t, dir, CostFile, `{"models":[]}`)
	writeCostFixture(t, dir, "chunks/manifest.json", `{"chunks":[{"name":"chunk-2"}]}`)
	writeCostFixture(t, dir, "chunks/chunk-1.cost.json", `{"models":[]}`)
	writeCostFixture(t, dir, "chunks/chunk-2.cost.json", `{"models":[]}`)
	writeCostFixture(t, dir, "chunks/notes.txt", "not a cost record")

	want := []string{
		filepath.Join(dir, CostFile),
		filepath.Join(dir, "chunks", "chunk-1.cost.json"),
		filepath.Join(dir, "chunks", "chunk-2.cost.json"),
	}
	got := CostFiles(dir)
	if len(got) != len(want) {
		t.Fatalf("CostFiles = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CostFiles[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// TestCostFilesWithoutChunks pins the list as candidates, not as existing
// files: the bundle's own record is named whether or not it is on disk, so a
// caller decides how to treat its absence, and a bundle with no chunk
// directory contributes no chunk paths at all.
func TestCostFilesWithoutChunks(t *testing.T) {
	for _, tt := range []struct {
		prepare func(t *testing.T, dir string)
		name    string
	}{
		{
			name:    "empty bundle",
			prepare: func(*testing.T, string) {},
		},
		{
			name: "bundle record only",
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, CostFile), []byte(`{"models":[]}`), 0o644); err != nil {
					t.Fatalf("write %s: %v", CostFile, err)
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.prepare(t, dir)
			got := CostFiles(dir)
			if len(got) != 1 || got[0] != filepath.Join(dir, CostFile) {
				t.Fatalf("CostFiles = %v, want only the bundle record path", got)
			}
		})
	}
}

// writeCostFixture writes one relative file under dir.
func writeCostFixture(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
