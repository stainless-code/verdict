package query

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestListBundlesRefusesAFileStore pins the listing order: a bundle home
// that is a regular file is refused even when ReadDir answers
// [fs.ErrNotExist], which is what Windows reports for a file opened as a
// directory. Keying on that error alone published an empty root for a store an
// operator can see is there.
func TestListBundlesRefusesAFileStore(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, ".verdict", "state")
	if err := os.WriteFile(state, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	layer := New(Snapshot{Roots: []Root{{Path: root}}}, Options{
		Stat:    os.Stat,
		ReadDir: func(string) ([]os.DirEntry, error) { return nil, fs.ErrNotExist },
	})
	view := layer.Roots(t.Context())
	if len(view.Errors) != 1 {
		t.Fatalf("errors = %+v; want one corrupt-store row", view.Errors)
	}
	want := "bundle store " + state + " is not a directory (move the file aside and re-fetch to rebuild the store)"
	if view.Errors[0].Code != "corrupt-store" || view.Errors[0].Message != want {
		t.Errorf("error row = %+v; want corrupt-store with %q", view.Errors[0], want)
	}
}
