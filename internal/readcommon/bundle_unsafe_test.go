package readcommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnsafeBundleRefusalsNameTheRepoRelativePath pins the refusal's path
// spelling: relative to the repository and in slash form on every platform,
// as every bundle refusal names it — never the absolute OS path, which a
// Windows runner spells with a drive and backslashes.
func TestUnsafeBundleRefusalsNameTheRepoRelativePath(t *testing.T) {
	t.Parallel()
	root, dir := linkedBundle(t, "cost.json", "patch.json")
	walk := RefuseUnsafeBundle(root, dir)
	if walk == nil || !strings.HasPrefix(walk.Message, ".verdict/state/42/cost.json is a symlink inside the bundle;") {
		t.Errorf("walk refusal = %v, want it to name .verdict/state/42/cost.json first", walk)
	}
	named := RefuseUnsafeArtifacts(root, dir, "patch.json")
	if named == nil || !strings.HasPrefix(named.Message, ".verdict/state/42/patch.json is a symlink;") {
		t.Errorf("named refusal = %v, want it to name .verdict/state/42/patch.json first", named)
	}
}

// linkedBundle is a repository holding .verdict/state/42 with each of names
// a symlink to a file outside the bundle.
func linkedBundle(t *testing.T, names ...string) (root, dir string) {
	t.Helper()
	root = t.TempDir()
	dir = filepath.Join(root, ".verdict", "state", "42")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Skipf("symlink creation unavailable: %v", err)
		}
	}
	return root, dir
}

// TestHasHardLinksCountsLinks proves the link count is read on every platform
// that can make a hard link: a lone file has one name, a linked one two.
func TestHasHardLinksCountsLinks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(path); err != nil || HasHardLinks(path, info) {
		t.Fatalf("a lone file reads as hard-linked (%v)", err)
	}
	if err := os.Link(path, filepath.Join(dir, "second")); err != nil {
		t.Skipf("hard links unavailable on this filesystem: %v", err)
	}
	if info, err := os.Lstat(path); err != nil || !HasHardLinks(path, info) {
		t.Errorf("a file with a second name reads as singly linked (%v)", err)
	}
}
