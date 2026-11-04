//go:build !windows

package atomic_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/atomic"
)

// TestWriteThroughWritesThroughANode checks a device target keeps the direct
// write: the device stays a device and the bytes are discarded, where a
// rename would try to replace the node itself.
func TestWriteThroughWritesThroughANode(t *testing.T) {
	if err := atomic.WriteThrough(os.DevNull, []byte("discard\n"), 0o600); err != nil {
		t.Fatalf("WriteThrough(%s) = %v; want the direct write", os.DevNull, err)
	}
	fi, err := os.Stat(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeDevice == 0 {
		t.Errorf("%s mode = %v; want the device left in place", os.DevNull, fi.Mode())
	}
}

// TestWriteThroughFailureKeepsTheOldBytes injects a write failure (an
// unwritable directory) and checks the previous report survives byte for byte
// with no staged temp beside it: a reader never sees a partial file.
func TestWriteThroughFailureKeepsTheOldBytes(t *testing.T) {
	path, dir := seedUnwritable(t)
	if err := atomic.WriteThrough(path, []byte("new content that must not land\n"), 0o600); err == nil {
		t.Fatal("want a failed write in a read-only directory, got nil")
	}
	checkContent(t, path, "old\n")
	checkNoTemp(t, dir)
}

// seedUnwritable writes path with known bytes and makes its directory deny
// creation, so the staged temp cannot be made. The directory mode is restored
// at cleanup, which t.TempDir needs to remove it.
func seedUnwritable(t *testing.T) (path, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir = t.TempDir()
	path = filepath.Join(dir, "report.md")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Errorf("restore directory mode: %v", err)
		}
	})
	return path, dir
}

// checkNoTemp fails on any staged atomic temp left in dir.
func checkNoTemp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("temp leftover %q after the failed write", e.Name())
		}
	}
}
