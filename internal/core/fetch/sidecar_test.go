package fetchcore

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestWriteErrFileOwnerOnly proves a fetch stderr sidecar lands owner-only:
// the bytes can carry token-adjacent forge diagnostics.
func TestWriteErrFileOwnerOnly(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".diff.err")
	const stderr = "HTTP 401: bad credentials\n"
	if err := writeErrFile(path, stderr); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != stderr {
		t.Errorf("sidecar = %q; want %q", raw, stderr)
	}
	if runtime.GOOS == "windows" {
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o; want 600", fi.Mode().Perm())
	}
}
