package kb_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/kb"
	"github.com/stainless-code/verdict/internal/storelock"
)

// creditSeed is one active rule at zero counters, so a committed credit moves
// hits to 1 and a dropped one leaves the bytes alone.
const creditSeed = `{"id":"rec-rule","rule":"Prefer table tests","scope":"internal","provenance":"manual","rigor":"walked","hits":0,"false_hits":0,"judged":0,"status":"active"}` + "\n"

// TestCreditWaitsForTheStoreLock proves a flip's credit rewrite is under the
// same lock as an append: while another writer holds it the store keeps its
// bytes — the unlocked rewrite landed here — and the credit commits once the
// holder releases.
func TestCreditWaitsForTheStoreLock(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, kb.File)
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, []byte(creditSeed), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := kb.New(func(string) {})
	ctx.Root = root
	ctx.Staged = []map[string]any{{"sig": "0f0f", "rule": "rec-rule", "outcome": "addressed"}}
	unlock, err := storelock.Acquire(store, storelock.DefaultWait)
	if err != nil {
		t.Fatalf("storelock.Acquire: %v", err)
	}
	committed := make(chan struct{})
	go func() {
		defer close(committed)
		ctx.CommitStaged("", "0000-00-00T00:00:00Z")
	}()
	select {
	case <-committed:
		unlock()
		t.Fatal("the credit rewrite landed while another writer held the lock")
	case <-time.After(200 * time.Millisecond):
	}
	if got := storeText(t, store); got != creditSeed {
		unlock()
		t.Fatalf("credit under a held lock moved the store: %q", got)
	}
	unlock()
	<-committed
	if got := storeText(t, store); !strings.Contains(got, `"hits":1`) {
		t.Fatalf("credit after the holder released = %q; want hits 1", got)
	}
}

// storeText reads the store back.
func storeText(t *testing.T, store string) string {
	t.Helper()
	raw, err := os.ReadFile(store)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
