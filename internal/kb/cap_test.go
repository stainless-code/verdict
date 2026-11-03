package kb

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// writePaddedKBStore writes the row padded with JSON whitespace to exactly
// size bytes under dir.
func writePaddedKBStore(t *testing.T, row string, size int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(File), 0o755); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, size)
	copy(data, row)
	for i := len(row); i < len(data); i++ {
		data[i] = ' '
	}
	if err := os.WriteFile(File, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestReadRowsStoreCap proves kb.jsonl slurps at the cap and reads unreadable
// (never allocated) one byte over it.
func TestReadRowsStoreCap(t *testing.T) {
	t.Chdir(t.TempDir())
	c := New(func(string) {})
	writePaddedKBStore(t, `{"id":"r1","status":"active"}`, jq.MaxKBBytes)
	rows, ok := c.readRows()
	if !ok || len(rows) != 1 {
		t.Fatalf("readRows at cap = %d rows, %v; want 1, true", len(rows), ok)
	}
	if err := os.Truncate(File, jq.MaxKBBytes+1); err != nil {
		t.Fatal(err)
	}
	if rows, ok := c.readRows(); ok || rows != nil {
		t.Errorf("readRows over cap = %v, %v; want nil, false", rows, ok)
	}
}
