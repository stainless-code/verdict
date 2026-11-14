package learncore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/kb"
	"github.com/stainless-code/verdict/internal/storelock"
)

const seededKBRow = `{"id":"rec-seeded","status":"active"}` + "\n"

// TestRuleAppendRefusesAStoreBusy proves a rule append serializes on the KB's
// OS lock: while another writer holds it the append waits its bounded window,
// refuses store-busy, and leaves the store bytes alone; the retry appends once
// the holder releases.
func TestRuleAppendRefusesAStoreBusy(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	store := seedKBStore(t, root)
	req := &Request{Rule: "Prefer literal argv", Scope: "internal", Rigor: "walked"}
	req.Runtime = &core.Runtime{}
	unlock := holdStoreLock(t, store)
	_, ref := Run(context.Background(), req)
	if ref == nil || ref.Code != codeStoreBusy {
		t.Fatalf("append under a held lock = %v; want store-busy", ref)
	}
	if want := "another write holds the store lock on .verdict/kb.jsonl (retry in a moment)"; ref.Message != want {
		t.Fatalf("store-busy message = %q; want %q", ref.Message, want)
	}
	if got := storeBytes(t, store); got != seededKBRow {
		t.Fatalf("a refused append moved the store: %q", got)
	}
	unlock()
	if _, ref := Run(context.Background(), req); ref != nil {
		t.Fatalf("append after the holder released: %v", ref)
	}
	if got := storeBytes(t, store); !strings.HasPrefix(got, seededKBRow) || len(got) <= len(seededKBRow) {
		t.Fatalf("retried append = %q; want the seeded row plus the new one", got)
	}
}

// seedKBStore writes the one seeded row and returns the store path.
func seedKBStore(t *testing.T, root string) string {
	t.Helper()
	store := filepath.Join(root, kb.File)
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, []byte(seededKBRow), 0o600); err != nil {
		t.Fatal(err)
	}
	return store
}

// holdStoreLock takes the store's lock and returns its release.
func holdStoreLock(t *testing.T, store string) func() {
	t.Helper()
	unlock, err := storelock.Acquire(store, storelock.DefaultWait)
	if err != nil {
		t.Fatalf("storelock.Acquire: %v", err)
	}
	return unlock
}

// storeBytes reads the store back.
func storeBytes(t *testing.T, store string) string {
	t.Helper()
	raw, err := os.ReadFile(store)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
