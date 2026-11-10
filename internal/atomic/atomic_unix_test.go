//go:build !windows

package atomic_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/atomic"
)

// checkStoreMode pins the umask-022 store mode on platforms that
// model one.
func checkStoreMode(t *testing.T, fi os.FileInfo) {
	t.Helper()
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o; want 644", fi.Mode().Perm())
	}
}

// checkSidecarMode pins the owner-only stderr sidecar mode.
func checkSidecarMode(t *testing.T, fi os.FileInfo) {
	t.Helper()
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o; want 600", fi.Mode().Perm())
	}
}

// checkPayloadMode pins the explicit payload mode.
func checkPayloadMode(t *testing.T, fi os.FileInfo) {
	t.Helper()
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("mode = %o; want 755", fi.Mode().Perm())
	}
}

// seedWriteFailure seeds a store that the write path cannot replace: POSIX
// rename is gated by the directory, so a read-only parent dir fails the
// temp-file creation and leaves the original intact.
func seedWriteFailure(t *testing.T) (path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission gate does not fail for root")
	}
	dir := t.TempDir()
	path = filepath.Join(dir, "store.json")
	if err := atomic.WriteFile(path, []byte("original\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Errorf("restore perms: %v", err)
		}
	})
	return path
}
