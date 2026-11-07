package kb

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadFileResolvesThroughRoot pins the read/write split: a Ctx rooted at
// the repository top reads the same store it rewrites, even when the command
// runs from a subdirectory. Before Root existed both sides were cwd-relative
// and agreed; once writes anchored at Root, an unrooted reader silently
// missed the store and dropped cited credit with an "unreadable" warning
// that named the wrong cause.
func TestReadFileResolvesThroughRoot(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, filepath.FromSlash(File))
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	row := `{"id":"rec-rule","rule":"Cite the rule","scope":"*","status":"active"}` + "\n"
	if err := os.WriteFile(store, []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())

	c := New(func(string) {})
	c.Root = root
	if !c.ruleActive("rec-rule") {
		t.Fatal("ruleActive read below the cwd instead of through Root")
	}
	if rows, ok := c.readRows(); !ok || len(rows) != 1 {
		t.Fatalf("readRows = %d rows, %v; want the rooted store", len(rows), ok)
	}
}
