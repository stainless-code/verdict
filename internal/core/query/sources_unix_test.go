//go:build !windows

package query

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestInventoryNeverBlocksOnSwapToFIFO proves the production artifact open is
// the check: a regular file swapped for a FIFO between the pre-stat and that
// open is refused at once instead of waiting for a writer that never comes.
func TestInventoryNeverBlocksOnSwapToFIFO(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "full.diff")
	if err := os.WriteFile(path, []byte("diff --git a b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	swapped := false
	c := newSwappingCollector(dir, func(abs string) {
		mkfifo(t, abs)
		swapped = true
	})
	c.add("1234", path)
	if !swapped {
		t.Fatal("the seam did not run: the production open was never reached")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Type() != fs.ModeNamedPipe {
		t.Errorf("path mode = %v at open time, want a FIFO", info.Mode())
	}
	if !c.truncated {
		t.Error("truncated = false, want true for the refused artifact")
	}
	if len(c.sources) != 0 {
		t.Errorf("sources = %d, want none for a FIFO", len(c.sources))
	}
}

// newSwappingCollector returns a collector whose pre-open hook swaps the
// regular file for a FIFO, leaving the production reader to be the one
// exercised.
func newSwappingCollector(dir string, swap func(string)) *sourceCollector {
	c := &sourceCollector{
		root: Root{Path: dir}, rootPath: dir,
		maxEntries: maxInventoryEntries, maxBytes: maxInventoryBytes,
	}
	c.beforeOpen = swap
	return c
}

// mkfifo replaces one path with a FIFO.
func mkfifo(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestInventoryDoesNotOpenAFIFO proves both inventory paths refuse a special
// file before opening it: the walk's type check covers the bundle entry, and
// add's Lstat covers the root override fragment.
func TestInventoryDoesNotOpenAFIFO(t *testing.T) {
	t.Parallel()
	dir, bundleDir := fifoFixture(t)
	layer := newLayer(dir)
	root := Root{ID: RootID(mustAbs(t, dir)), Path: mustAbs(t, dir)}
	report := layer.inventory(root, "1234", bundleDir)
	if got := len(report.sources); got != 0 {
		t.Errorf("sources = %d, want no row for a FIFO", got)
	}
}

// fifoFixture builds the two special files the inventory paths must skip.
func fifoFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, ".verdict", "state", "1234")
	overrideDir := filepath.Join(dir, ".verdict", "overrides")
	for _, path := range []string{bundleDir, overrideDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		filepath.Join(bundleDir, "findings.json"),
		filepath.Join(overrideDir, "preamble.md"),
	} {
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, bundleDir
}
