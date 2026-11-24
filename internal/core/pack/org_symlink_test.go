package pack

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSyncRejectsIntermediateSymlink proves a pin whose path walks through a
// symlink refuses pack-unsafe-path before anything is vendored: the central
// repository's own tree cannot redirect the sync outside itself.
func TestSyncRejectsIntermediateSymlink(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	outside := t.TempDir()
	writeFixture(t, outside, map[string]string{
		"org/pack.json":      lockManifest,
		"org/lenses/perf.md": "perf body\n",
	})
	central := filepath.Join(repo, "central")
	if err := os.MkdirAll(central, 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, filepath.Join(outside, "org"), filepath.Join(central, "packs"))
	pin := &OrgPin{Repo: central, Ref: orgSha, Path: "packs/lenses"}
	_, ref := RunSync(context.Background(), &SyncRequest{OrgPin: pin})
	if ref == nil {
		t.Fatal("RunSync accepted a symlinked pin path")
	}
	if ref.Code != CodePackUnsafePath {
		t.Fatalf("refusal = %s: %s, want %s", ref.Code, ref.Message, CodePackUnsafePath)
	}
	if _, err := os.Stat(filepath.Join(repo, ".verdict", "packs")); !os.IsNotExist(err) {
		t.Fatalf("refused sync wrote %s (stat err %v)", RepoPacksDir, err)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(LockFile))); !os.IsNotExist(err) {
		t.Fatalf("refused sync wrote %s (stat err %v)", LockFile, err)
	}
}

// symlinkOrSkip creates one fixture symlink, skipping only on the platform
// that refuses the call without a privilege.
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("this OS refuses symlink creation without a privilege: %v", err)
		}
		t.Fatalf("symlink fixture: %v", err)
	}
}
