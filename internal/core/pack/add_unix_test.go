//go:build unix

package pack

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAddWarnsWhenBackupCleanupFails proves a re-add that cannot remove the
// old tree still succeeds: the new tree and lock are committed, and the
// response carries one warning naming the leftover sibling.
func TestAddWarnsWhenBackupCleanupFails(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	lensesSource(t, repo, "src-packs/lenses", "perf body\n")
	if _, ref := RunAdd(context.Background(), &AddRequest{Source: "src-packs/lenses"}); ref != nil {
		t.Fatalf("first RunAdd: %s: %s", ref.Code, ref.Message)
	}
	if err := os.Chmod(filepath.Join(".verdict", "packs", "lenses", "lenses"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { makeWritable(t, filepath.Join(".verdict", "packs")) })
	lensesSource(t, repo, "src-packs/lenses", "perf body v2\n")
	resp, ref := RunAdd(context.Background(), &AddRequest{Source: "src-packs/lenses"})
	if ref != nil {
		t.Fatalf("RunAdd = %s: %s, want success with a warning", ref.Code, ref.Message)
	}
	if !resp.Data.Replaced {
		t.Fatal("re-add did not report replaced")
	}
	if len(resp.Warnings) != 1 {
		t.Fatalf("warnings = %q, want one", resp.Warnings)
	}
	if !strings.Contains(resp.Warnings[0], ".verdict/packs/.lenses.old-") {
		t.Fatalf("warning = %q, want the leftover path", resp.Warnings[0])
	}
	assertFile(t, ".verdict/packs/lenses/lenses/perf.md", "perf body v2\n")
	assertFile(t, LockFile, fmt.Sprintf(addLockFmt, "dir:src-packs/lenses", addHashV2, addSigV2))
}

// makeWritable restores directory permissions under root so the test sandbox
// can be removed after a deliberate chmod 0555.
func makeWritable(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return err
		}
		return os.Chmod(path, packDirPerm)
	})
	if err != nil {
		t.Errorf("restore permissions under %s: %v", root, err)
	}
}
