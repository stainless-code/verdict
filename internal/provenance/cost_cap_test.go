package provenance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// writePaddedStore writes valid JSON padded with trailing whitespace to
// exactly size bytes. The padding parses, so the same bytes decode unless a
// size cap refuses them: an over-cap leg can only fail through the cap.
func writePaddedStore(t *testing.T, path, content string, size int64) {
	t.Helper()
	if int64(len(content)) > size {
		t.Fatalf("fixture is %d bytes, larger than the %d-byte store", len(content), size)
	}
	data := make([]byte, size)
	copy(data, content)
	for i := len(content); i < len(data); i++ {
		data[i] = ' '
	}
	if !json.Valid(data) {
		t.Fatalf("padded store %q is not valid JSON at %d bytes", content, size)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
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

// TestCostRecordsStoreCap proves cost.json decodes at the cap and reads as
// absent (never allocated) one byte over it.
func TestCostRecordsStoreCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, readcommon.CostFile)
	const row = `[{"est_usd":1}]`
	writePaddedStore(t, path, row, jq.MaxCostBytes)
	if recs := readcommon.CostRecords(dir); len(recs) != 1 {
		t.Fatalf("CostRecords at cap = %d records, want 1", len(recs))
	}
	writePaddedStore(t, path, row, jq.MaxCostBytes+1)
	requireOverCap(t, path, jq.MaxCostBytes)
	if recs := readcommon.CostRecords(dir); recs != nil {
		t.Errorf("CostRecords over cap = %v, want nil", recs)
	}
}
