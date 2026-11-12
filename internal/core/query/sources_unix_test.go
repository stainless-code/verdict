//go:build !windows

package query

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// TestInventoryNeverBlocksOnSwapToFIFO proves the artifact open is the check:
// a regular file swapped for a FIFO between the pre-stat and the open is
// refused at once instead of waiting for a writer that never comes.
func TestInventoryNeverBlocksOnSwapToFIFO(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "full.diff")
	if err := os.WriteFile(path, []byte("diff --git a b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := newSwappingCollector(dir)
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.add("1234", path)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("inventory blocked after the swap: the open waited for a FIFO writer")
	}
	if !c.truncated {
		t.Error("truncated = false, want true for the refused artifact")
	}
	if len(c.sources) != 0 {
		t.Errorf("sources = %d, want none for a FIFO", len(c.sources))
	}
}

// newSwappingCollector returns a collector whose open seam swaps the regular
// file for a FIFO before the descriptor open.
func newSwappingCollector(dir string) *sourceCollector {
	c := &sourceCollector{
		root: Root{Path: dir}, rootPath: dir,
		maxEntries: maxInventoryEntries, maxBytes: maxInventoryBytes,
	}
	c.openArtifact = swapToFIFO
	return c
}

// swapToFIFO replaces path with a FIFO, then reads it through the race-free
// reader.
func swapToFIFO(path string, limit int64) ([]byte, error) {
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		return nil, err
	}
	return readcommon.ReadRegularCapped(path, limit)
}

// TestInventoryDoesNotOpenAFIFO proves both inventory paths refuse a special
// file before opening it: the walk's type check covers the bundle entry, and
// add's Lstat covers the root override fragment.
func TestInventoryDoesNotOpenAFIFO(t *testing.T) {
	t.Parallel()
	dir, bundleDir := fifoFixture(t)
	layer := newLayer(dir)
	root := Root{ID: RootID(mustAbs(t, dir)), Path: mustAbs(t, dir)}
	assertInventoryDoesNotBlock(t, layer, root, bundleDir)
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

// assertInventoryDoesNotBlock fails when the walk opens a FIFO.
func assertInventoryDoesNotBlock(t *testing.T, layer *Layer, root Root, bundleDir string) {
	t.Helper()
	done := make(chan int, 1)
	go func() {
		report := layer.inventory(root, "1234", bundleDir)
		done <- len(report.sources)
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
