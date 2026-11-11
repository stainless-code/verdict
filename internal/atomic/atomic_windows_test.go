//go:build windows

package atomic_test

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
)

const (
	// openHandleHold is how long the exclusive handle stays open before the
	// retry loop can replace the file; well inside replaceFile's ~1.6s budget.
	openHandleHold = 200 * time.Millisecond
	// noShare denies every other handle while the test holds the store.
	noShare = 0
)

// checkStoreMode pins what a mode means on Windows: only the read-only
// attribute is tracked, so the 0644 contract reads "owner writable".
func checkStoreMode(t *testing.T, fi os.FileInfo) {
	t.Helper()
	if fi.Mode().Perm()&0o200 == 0 {
		t.Errorf("mode = %o; want owner-writable (read-only attribute clear)", fi.Mode().Perm())
	}
}

// checkSidecarMode skips the POSIX permission assertion on Windows, where
// [os.Chmod] does not model owner/group/other bits.
func checkSidecarMode(t *testing.T, _ os.FileInfo) {
	t.Helper()
	t.Log("skipping POSIX sidecar mode assertion on Windows")
}

// checkPayloadMode skips the POSIX executable-bit assertion on Windows, which
// does not model it.
func checkPayloadMode(t *testing.T, _ os.FileInfo) {
	t.Helper()
	t.Log("skipping POSIX payload mode assertion on Windows")
}

// seedWriteFailure seeds a store the write path cannot replace: Windows
// MoveFileEx(MOVEFILE_REPLACE_EXISTING) fails with access denied when the
// destination carries the read-only attribute.
func seedWriteFailure(t *testing.T) (path string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "store.json")
	if err := atomic.WriteFile(path, []byte("original\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Errorf("restore perms: %v", err)
		}
	})
	return path
}

// TestWriteFileRetriesWhileDestinationOpen proves the rename retry: an
// exclusive handle makes MoveFileEx fail with a sharing/access error, and
// the write succeeds once the outside handle closes, all within the
// bounded retry budget.
func TestWriteFileRetriesWhileDestinationOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	if err := atomic.WriteFile(path, []byte("old\n")); err != nil {
		t.Fatal(err)
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(
		name, syscall.GENERIC_READ, noShare, nil,
		syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0,
	)
	if err != nil {
		t.Fatalf("hold store handle: %v", err)
	}
	closeHandle := releaseOnce(t, handle)
	defer closeHandle()
	timer := time.AfterFunc(openHandleHold, closeHandle)
	defer timer.Stop()

	if err := atomic.WriteFile(path, []byte("new content here\n")); err != nil {
		t.Fatalf("write while the handle closes: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, []byte("new content here\n")) {
		t.Errorf("overwrite = %q; want new content", raw)
	}
}

// releaseOnce closes handle at most once, so the deferred call and the
// timer racing to release it cannot double-close. A failed close only leaks
// a handle the process reclaims at exit, so it goes to the test log.
func releaseOnce(t *testing.T, handle syscall.Handle) func() {
	t.Helper()
	var once sync.Once
	return func() {
		once.Do(func() {
			if err := syscall.CloseHandle(handle); err != nil {
				t.Logf("release store handle: %v", err)
			}
		})
	}
}
