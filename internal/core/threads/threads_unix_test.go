//go:build !windows

package threadscore_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	threadscore "github.com/stainless-code/verdict/internal/core/threads"
)

// TestReadFileNeverBlocksOnAFIFO proves the feed open is non-blocking: a FIFO
// in threads.json's place is refused at once instead of waiting for a writer
// that never comes.
func TestReadFileNeverBlocksOnAFIFO(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "threads.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := threadscore.ReadFile(path); err == nil {
		t.Fatal("ReadFile on a FIFO = nil error, want a refusal")
	}
}

// TestReadFileRefusesADirectory proves a directory in the feed's place is
// refused, never read as an empty document.
func TestReadFileRefusesADirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := threadscore.ReadFile(dir); err == nil {
		t.Fatal("ReadFile on a directory = nil error, want a refusal")
	}
}
