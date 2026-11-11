//go:build unix

package pack

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestPackFIFOBodyRefuses proves a real FIFO at a lens path refuses
// pack-unsafe-path instead of blocking the body read forever.
func TestPackFIFOBodyRefuses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.json"),
		`{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf"}]}`)
	if err := os.MkdirAll(filepath.Join(dir, "lenses"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "lenses", "perf.md"), 0o600); err != nil {
		t.Skipf("this platform cannot create a FIFO here: %v", err)
	}
	if _, ref := LoadDir(dir); ref == nil || ref.Code != CodePackUnsafePath {
		t.Fatalf("FIFO lens = %v, want pack-unsafe-path", ref)
	}
}
