//go:build unix

package run

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestSnapshotCheckRefusesALeafThatIsNotARegularFile proves the checkpoint
// hashes only a leaf it can open without following a link or blocking: an
// artifact swapped for a symlink after the bind is refused instead of hashing
// the file it points at, and one swapped for a FIFO is refused without waiting
// for a writer. Both swapped leaves carry the bound content, so a reader that
// follows the link or drains the pipe sees no change at all.
func TestSnapshotCheckRefusesALeafThatIsNotARegularFile(t *testing.T) {
	t.Parallel()
	for name, swap := range map[string]func(t *testing.T, path, target string){
		"symlink": swapForSymlink,
		"fifo":    swapForFIFO,
	} {
		t.Run(name, func(t *testing.T) { checkSwappedLeaf(t, swap) })
	}
}

func checkSwappedLeaf(t *testing.T, swap func(t *testing.T, path, target string)) {
	t.Helper()
	dir, leaf, outside := boundPhaseLeaf(t)
	snapshot := boundSnapshot(t, dir, leaf)
	if err := os.Remove(filepath.Join(dir, leaf)); err != nil {
		t.Fatal(err)
	}
	swap(t, filepath.Join(dir, leaf), outside)
	ref := snapshot.check(dir)
	if ref == nil || ref.Code != "stale-review-artifact" || ref.Message != "snapshot file is unsafe: "+leaf {
		t.Fatalf("swapped leaf = %v", ref)
	}
}

func swapForSymlink(t *testing.T, path, target string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func swapForFIFO(t *testing.T, path, _ string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

// boundPhaseLeaf writes one phase artifact plus an outside file holding the
// same bytes, so following the leaf would hash an identical digest.
func boundPhaseLeaf(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	leaf := filepath.Join("x-finder-1", "procedure.md")
	if err := os.MkdirAll(filepath.Join(dir, "x-finder-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, leaf), outside} {
		if err := os.WriteFile(path, []byte("bound content\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, leaf, outside
}

func boundSnapshot(t *testing.T, dir, leaf string) executionSnapshot {
	t.Helper()
	snapshot := executionSnapshot{}
	if ref := snapshot.bind(dir, leaf); ref != nil {
		t.Fatal(ref)
	}
	return snapshot
}

// TestSnapshotInventorySkipsOutputNamespaces proves the evidence inventory
// never descends into an output namespace. A walk that hashes every regular
// file it meets fails the whole checkpoint on a retirement holding an
// unreadable artifact; the skip keeps history, and a sibling phase a parallel
// worker is writing, out of the pinned input set entirely.
func TestSnapshotInventorySkipsOutputNamespaces(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeUnder(t, dir, "full.diff", fixtureDiff)
	writeUnder(t, filepath.Join(dir, "x-superseded", "legacy"), "review.json", `{"retired":true}`)
	unreadable := filepath.Join(dir, "x-superseded", "legacy", "aux.txt")
	if err := os.WriteFile(unreadable, []byte("retired bytes"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o600) }) //nolint:errcheck // leaves the temp dir removable
	snapshot, ref := captureSnapshot(dir)
	if ref != nil {
		t.Fatalf("capture = %v, want the output namespace skipped", ref)
	}
	if ref := snapshot.check(dir); ref != nil {
		t.Fatalf("check = %v, want the output namespace skipped", ref)
	}
	if _, bound := snapshot[filepath.Join("x-superseded", "legacy", "review.json")]; bound {
		t.Fatal("inventory bound an artifact under a retired execution")
	}
	if _, bound := snapshot["full.diff"]; !bound {
		t.Fatal("inventory dropped a pinned input")
	}
}
