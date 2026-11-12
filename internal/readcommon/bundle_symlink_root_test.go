package readcommon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWritableBundleLinkedRoot permits a symlink at the repository root.
func TestWritableBundleLinkedRoot(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "repo")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".verdict/state/42"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, ref := ResolveWritableBundleDir(link, "42"); ref != nil {
		t.Fatalf("linked repo root refused: %v", ref)
	}
}
