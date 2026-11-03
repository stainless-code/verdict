package provenance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestCostRecordsStoreCap proves cost.json decodes at the cap and reads as
// absent (never allocated) when oversized.
func TestCostRecordsStoreCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cost.json")
	row := `[{"est_usd":1}]`
	data := make([]byte, jq.MaxCostBytes)
	copy(data, row)
	for i := len(row); i < len(data); i++ {
		data[i] = ' '
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if recs := costRecords(dir); len(recs) != 1 {
		t.Fatalf("costRecords at cap = %d records, want 1", len(recs))
	}
	if err := os.Truncate(path, jq.MaxCostBytes+1); err != nil {
		t.Fatal(err)
	}
	if recs := costRecords(dir); recs != nil {
		t.Errorf("costRecords over cap = %v, want nil", recs)
	}
}
