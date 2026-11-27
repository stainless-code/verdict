//go:build unix

package verifycore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/hostsandbox"
)

// TestRemovePrivateDirRestoresPermissions proves a child that strips its own
// scratch tree of permissions cannot strand it on the host, and that the
// cleanup never follows a symlink out of the tree to re-permission a host
// directory.
func TestRemovePrivateDirRestoresPermissions(t *testing.T) {
	private, outside := lockedPrivateTree(t)
	if err := hostsandbox.RemovePrivateDir(private); err != nil {
		t.Fatalf("RemovePrivateDir: %v", err)
	}
	if _, err := os.Lstat(private); !os.IsNotExist(err) {
		t.Errorf("private tree survived (%v)", err)
	}
	info, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o500 {
		t.Errorf("the symlinked host directory mode = %o, want 500 untouched", got)
	}
}

// lockedPrivateTree builds a private tree whose nested directories lost their
// write bit, plus a symlink to a read-only host directory outside it.
func lockedPrivateTree(t *testing.T) (string, string) {
	t.Helper()
	private := filepath.Join(t.TempDir(), "private")
	locked := filepath.Join(private, "a", "locked")
	outside := t.TempDir()
	mustSandboxFS(t, os.MkdirAll(locked, 0o755))
	mustSandboxFS(t, os.WriteFile(filepath.Join(locked, "f"), []byte("x"), 0o600))
	mustSandboxFS(t, os.Symlink(outside, filepath.Join(private, "escape")))
	for _, dir := range []string{locked, filepath.Join(private, "a"), outside} {
		mustSandboxFS(t, os.Chmod(dir, 0o500))
	}
	t.Cleanup(func() {
		if err := os.Chmod(outside, 0o700); err != nil {
			t.Errorf("restore %s: %v", outside, err)
		}
	})
	return private, outside
}
