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
