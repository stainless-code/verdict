//go:build unix

package serve

import (
	"crypto/sha256"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestHashDiffRefusesAFIFOWithoutBlocking pins the non-blocking guard: a
// full.diff that is a FIFO with no writer would block an open forever while the
// findings-store lock is held, so hashDiff refuses it before opening.
func TestHashDiffRefusesAFIFOWithoutBlocking(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "full.diff")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- hashDiff(sha256.New(), path) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("hashDiff accepted a FIFO, want a refusal")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hashDiff blocked on a FIFO: it opened a non-regular file")
	}
}
