package learncore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
)

func writePaddedKB(t *testing.T, root, row string, size int64) {
	t.Helper()
	path := filepath.Join(root, kb.File)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, size)
	copy(data, row)
	for i := len(row); i < len(data); i++ {
		data[i] = ' '
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func checkLearnReadsAtCap(t *testing.T, root string) {
	t.Helper()
	st := &state{req: &Request{}, root: root}
	if rows, err := kb.ReadRows(root); err != nil || len(rows) != 1 {
		t.Fatalf("kb.ReadRows at cap = %d rows, %v; want 1, nil", len(rows), err)
	}
	if rows, ref := st.readRowsStrict("r1"); ref != nil || len(rows) != 1 {
		t.Fatalf("readRowsStrict at cap = %d rows, %v; want 1, nil", len(rows), ref)
	}
	if rows, ref := st.readStatusRows(); ref != nil || len(rows) != 1 {
		t.Fatalf("readStatusRows at cap = %d rows, %v; want 1, nil", len(rows), ref)
	}
}

func checkLearnReadsOverCap(t *testing.T, root string) {
	t.Helper()
	st := &state{req: &Request{}, root: root}
	if _, err := kb.ReadRows(root); !errors.Is(err, kb.ErrCorrupt) {
		t.Errorf("kb.ReadRows over cap = %v, want ErrCorrupt", err)
	}
	if _, ref := st.readRowsStrict("r1"); ref == nil || ref.Code != codeCorruptStore {
		t.Errorf("readRowsStrict over cap = %v, want corrupt-store", ref)
	}
	if _, ref := st.readStatusRows(); ref == nil || ref.Code != codeCorruptStore {
		t.Errorf("readStatusRows over cap = %v, want corrupt-store", ref)
	}
}

// TestKBStoreReadsRefuseOversizedInput proves every learn read path caps
// kb.jsonl: at the cap it decodes, one byte over it reads corrupt-store
// without allocating the store.
func TestKBStoreReadsRefuseOversizedInput(t *testing.T) {
	root := t.TempDir()
	writePaddedKB(t, root, `{"id":"r1","rule":"keep it small"}`, jq.MaxKBBytes)
	checkLearnReadsAtCap(t, root)
	if err := os.Truncate(filepath.Join(root, kb.File), jq.MaxKBBytes+1); err != nil {
		t.Fatal(err)
	}
	checkLearnReadsOverCap(t, root)
}
