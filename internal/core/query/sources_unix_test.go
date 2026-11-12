//go:build !windows

package query

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestInventoryDoesNotOpenAFIFO proves both inventory paths refuse a special
// file before opening it: the walk's type check covers the bundle entry, and
// add's Lstat covers the root override fragment.
func TestInventoryDoesNotOpenAFIFO(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, ".verdict", "state", "1234")
	overrideDir := filepath.Join(dir, ".verdict", "overrides")
	for _, path := range []string{bundleDir, overrideDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mkfifo(filepath.Join(bundleDir, "findings.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(overrideDir, "preamble.md"), 0o600); err != nil {
		t.Fatal(err)
	}
	layer := newLayer(dir)
	root := Root{ID: RootID(mustAbs(t, dir)), Path: mustAbs(t, dir)}
	done := make(chan int, 1)
	go func() {
		sources, _ := layer.inventory(root, "1234", bundleDir)
		done <- len(sources)
	}()
	select {
	case got := <-done:
		if got != 0 {
			t.Errorf("sources = %d, want no row for a FIFO", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("inventory blocked opening a FIFO")
	}
}
