package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCreateBundleDirIsExclusive proves only the call that made the leaf
// owns it: a second create of the same name, as a racing run would issue,
// reads not-created and keeps what the first one wrote.
func TestCreateBundleDirIsExclusive(t *testing.T) {
	layout := NewLayout(t.TempDir())
	first, serr := layout.CreateBundleDir("local-mine")
	if serr != nil || !first {
		t.Fatalf("first create = %v, %v; want created", first, serr)
	}
	kept := filepath.Join(layout.StateDir(), "local-mine", "source.json")
	if err := os.WriteFile(kept, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, serr := layout.CreateBundleDir("local-mine")
	if serr != nil || second {
		t.Fatalf("second create = %v, %v; want existing, not created", second, serr)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("second create touched the bundle: %v", err)
	}
}

// TestCreateBundleDirRefusesALinkedBundle pins the symlink refusal on the
// exclusive create: an existing leaf that is a link refuses, never reads as
// an ordinary existing bundle.
func TestCreateBundleDirRefusesALinkedBundle(t *testing.T) {
	layout, target := symlinkedBundleFixture(t, "7")
	created, serr := layout.CreateBundleDir("7")
	if serr == nil || created {
		t.Fatalf("create on a linked leaf = %v, %v; want the symlink refusal", created, serr)
	}
	if ref := serr.Refusal(); ref == nil || ref.Message != bundleSymlinkMessage("7") {
		t.Fatalf("refusal = %v, want the symlink message", ref)
	}
	if got := dirEntries(t, target); len(got) != 0 {
		t.Fatalf("link target gained files: %v", got)
	}
}

// TestRemoveBundleDirRefusesALinkedBundle proves the removal never follows a
// leaf swapped for a symlink: it refuses, and the link and its target stay.
func TestRemoveBundleDirRefusesALinkedBundle(t *testing.T) {
	layout, target := symlinkedBundleFixture(t, "local-mine")
	if err := os.WriteFile(filepath.Join(target, "keep.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	serr := layout.RemoveBundleDir("local-mine")
	want := filepath.Join(".verdict", "state", "local-mine") + " is a symlink: refusing to remove through it"
	if serr == nil || serr.Error() != want {
		t.Fatalf("remove on a linked leaf = %v, want %q", serr, want)
	}
	if got := dirEntries(t, target); len(got) != 1 || got[0] != "keep.txt" {
		t.Fatalf("link target = %v, want keep.txt untouched", got)
	}
	if _, err := os.Lstat(filepath.Join(layout.StateDir(), "local-mine")); err != nil {
		t.Fatalf("the link itself was removed: %v", err)
	}
}

// TestRemoveBundleDirRemovesTheLeaf pins the ordinary path: the leaf and
// everything in it go, the state home stays.
func TestRemoveBundleDirRemovesTheLeaf(t *testing.T) {
	layout := NewLayout(t.TempDir())
	if _, serr := layout.CreateBundleDir("local-mine"); serr != nil {
		t.Fatal(serr)
	}
	if err := os.MkdirAll(filepath.Join(layout.StateDir(), "local-mine", "controls"), 0o755); err != nil {
		t.Fatal(err)
	}
	if serr := layout.RemoveBundleDir("local-mine"); serr != nil {
		t.Fatalf("RemoveBundleDir: %v", serr)
	}
	if _, err := os.Lstat(filepath.Join(layout.StateDir(), "local-mine")); !os.IsNotExist(err) {
		t.Fatalf("leaf survived the removal: %v", err)
	}
	if _, err := os.Stat(layout.StateDir()); err != nil {
		t.Fatalf("state home went with the leaf: %v", err)
	}
}
