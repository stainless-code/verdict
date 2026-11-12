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

	"github.com/stainless-code/verdict/internal/core"
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

// TestAddNamesTheStaleSweepFailure proves a leftover the sweep cannot remove
// refuses as pack-staging-stale, names the path to delete, and carries the
// staging hint instead of the generic pack.json one.
func TestAddNamesTheStaleSweepFailure(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	lensesSource(t, repo, "src-packs/lenses", "perf body\n")
	const stale = ".verdict/packs/.lenses.old-locked"
	if err := os.MkdirAll(filepath.Join(stale, "inner"), packDirPerm); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, repo, map[string]string{stale + "/inner/junk": "old\n"})
	if err := os.Chmod(filepath.Join(stale, "inner"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { makeWritable(t, filepath.Join(repo, ".verdict", "packs", ".lenses.old-locked")) })
	_, ref := RunAdd(context.Background(), &AddRequest{Source: "src-packs/lenses"})
	if ref == nil || ref.Code != CodePackStagingStale {
		t.Fatalf("RunAdd = %v, want pack-staging-stale", ref)
	}
	if !strings.Contains(ref.Message, stale) {
		t.Fatalf("message = %q, want it to name %s", ref.Message, stale)
	}
	const wantHint = "try: delete the dot-prefixed sibling under .verdict/packs/ that the message " +
		"names by hand (its permissions block the sweep), then re-run"
	if got := core.HintFor(ref.Code); got != wantHint {
		t.Fatalf("hint = %q, want %q", got, wantHint)
	}
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
