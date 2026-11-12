//go:build !windows

package readcommon_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// TestReadDirLimitNeverBlocksOnAFIFO proves the directory open is non-blocking:
// a FIFO in the directory's place is refused at once instead of waiting for a
// writer that never comes.
func TestReadDirLimitNeverBlocksOnAFIFO(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "chunks")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	entries, more, err := readcommon.ReadDirLimit(path, 2)
	if err == nil {
		t.Fatalf("ReadDirLimit on a FIFO = %v entries, more=%v, nil error; want a refusal", entries, more)
	}
	if len(entries) != 0 || more {
		t.Errorf("ReadDirLimit on a FIFO = %v entries, more=%v; want none", entries, more)
	}
}

// TestReadDirLimitRefusesANonDirectory proves a regular file in a directory's
// place is refused by the descriptor check, not by a failed read.
func TestReadDirLimitRefusesANonDirectory(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readcommon.ReadDirLimit(path, 2); err == nil {
		t.Fatal("ReadDirLimit on a regular file = nil error, want a refusal")
	}
}
