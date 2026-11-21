//go:build unix

package repro

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stainless-code/verdict/internal/testenv"
)

func TestOpenReadRejectsFIFO(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "regular"), []byte("old bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(filepath.Join(dir, "regular"))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	checkFIFOOpen(t, root, before)
}

func checkFIFOOpen(t *testing.T, root *os.Root, before os.FileInfo) {
	t.Helper()
	done := make(chan error, 1)
	go func() { _, err := openRead(root, "pipe", before); done <- err }()
	var result error
	finished := false
	t.Cleanup(func() {
		if !finished {
			releaseFIFO(t, root, done)
		}
	})
	testenv.Until(t, func() bool {
		select {
		case result = <-done:
			finished = true
			return true
		default:
			return false
		}
	})
	if result == nil {
		t.Fatal("accepted FIFO as reproduction evidence")
	}
}

func releaseFIFO(t *testing.T, root *os.Root, done <-chan error) {
	t.Helper()
	testenv.Until(t, func() bool {
		select {
		case err := <-done:
			if err == nil {
				t.Error("unblocked FIFO read unexpectedly succeeded")
			}
			return true
		default:
		}
		connectFIFOWriter(t, root)
		return false
	})
}

func connectFIFOWriter(t *testing.T, root *os.Root) {
	t.Helper()
	writer, err := root.OpenFile("pipe", os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return
	}
	if err := writer.Close(); err != nil {
		t.Error(err)
	}
}
