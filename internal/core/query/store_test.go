package query

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
	if view.Errors[0].Code != "corrupt-store" || !strings.Contains(view.Errors[0].Message, ".verdict/state") {
		t.Errorf("error row = %+v; want corrupt-store naming the store", view.Errors[0])
	}
}
