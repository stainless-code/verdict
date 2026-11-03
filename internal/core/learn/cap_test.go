package learncore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
)

// writePaddedKB writes the row padded with JSON whitespace to exactly size
// bytes in the learn store.
func writePaddedKB(t *testing.T, row string, size int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(kbFile), 0o755); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, size)
	copy(data, row)
	for i := len(row); i < len(data); i++ {
		data[i] = ' '
	}
	if err := os.WriteFile(kbFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// checkLearnReadsAtCap proves every read path accepts the store at the cap.
func checkLearnReadsAtCap(t *testing.T) {
	t.Helper()
	st := &state{req: &Request{}}
	if rows, err := kb.ReadRows(); err != nil || len(rows) != 1 {
		t.Fatalf("kb.ReadRows at cap = %d rows, %v; want 1, nil", len(rows), err)
	}
	if rows, ref := st.readRowsStrict("r1"); ref != nil || len(rows) != 1 {
		t.Fatalf("readRowsStrict at cap = %d rows, %v; want 1, nil", len(rows), ref)
	}
	if rows, ref := st.readStatusRows(); ref != nil || len(rows) != 1 {
		t.Fatalf("readStatusRows at cap = %d rows, %v; want 1, nil", len(rows), ref)
	}
}

// checkLearnReadsOverCap proves every read path refuses typed corrupt-store
// one byte over the cap.
func checkLearnReadsOverCap(t *testing.T) {
	t.Helper()
	st := &state{req: &Request{}}
	if _, err := kb.ReadRows(); !errors.Is(err, kb.ErrCorrupt) {
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
	t.Chdir(t.TempDir())
	writePaddedKB(t, `{"id":"r1","rule":"keep it small"}`, jq.MaxKBBytes)
	checkLearnReadsAtCap(t)
	if err := os.Truncate(kbFile, jq.MaxKBBytes+1); err != nil {
		t.Fatal(err)
	}
	checkLearnReadsOverCap(t)
}
