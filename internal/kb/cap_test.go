package kb

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// writePaddedKBStore writes row as one kb.jsonl record, padded with trailing
// whitespace to exactly size bytes under File. The padding parses, so the same
// bytes decode unless a size cap refuses them: an over-cap leg can only fail
// through the cap.
func writePaddedKBStore(t *testing.T, row string, size int64) {
	t.Helper()
	valid := row + "\n"
	if int64(len(valid)) > size {
		t.Fatalf("fixture is %d bytes, larger than the %d-byte store", len(valid), size)
	}
	if err := os.MkdirAll(filepath.Dir(File), 0o755); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, size)
	copy(data, valid)
	for i := len(valid); i < len(data); i++ {
		data[i] = ' '
	}
	if _, err := jq.DecodeJSONLOrdered(data); err != nil {
		t.Fatalf("padded kb store is not valid JSONL at %d bytes: %v", size, err)
	}
	if err := os.WriteFile(File, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// requireOverCap fails unless the store's own read path (jq.CappedFile)
// refuses path at limit with ErrTooLarge, so the over-cap leg cannot pass on
// any other failure.
func requireOverCap(t *testing.T, path string, limit int64) {
	t.Helper()
	if _, err := jq.CappedFile(path, limit); !errors.Is(err, jq.ErrTooLarge) {
		t.Fatalf("CappedFile(%s, %d) = %v, want ErrTooLarge", path, limit, err)
	}
}

// TestReadRowsStoreCap proves kb.jsonl slurps at the cap and reads unreadable
// (never allocated) one byte over it.
func TestReadRowsStoreCap(t *testing.T) {
	t.Chdir(t.TempDir())
	c := New(func(string) {})
	const row = `{"id":"r1","status":"active"}`
	writePaddedKBStore(t, row, jq.MaxKBBytes)
	rows, ok := c.readRows()
	if !ok || len(rows) != 1 {
		t.Fatalf("readRows at cap = %d rows, %v; want 1, true", len(rows), ok)
	}
	writePaddedKBStore(t, row, jq.MaxKBBytes+1)
	requireOverCap(t, File, jq.MaxKBBytes)
	if rows, ok := c.readRows(); ok || rows != nil {
		t.Errorf("readRows over cap = %v, %v; want nil, false", rows, ok)
	}
}
