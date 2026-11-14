package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// stateLines are the contract lines an ignore file must end up carrying once,
// whatever order the writers landed in.
var stateLines = []string{
	gitignoreStateLine, gitignoreBacktestLine, gitignoreIndexLine,
	gitignoreIncidentLock, gitignoreKBLock,
}

// TestEnsureStateIgnoreKeepsOneContractAcrossWriters proves the ignore contract
// survives concurrent first writers: every store's writer calls it, the KB and
// the ledger hold different locks, and each call must come back clean with one
// copy of every line. The atomic replace of that one file denies a reader's
// open for a moment on Windows, so an unretried read refused here instead.
func TestEnsureStateIgnoreKeepsOneContractAcrossWriters(t *testing.T) {
	root := t.TempDir()
	repeatWriters(t, root)
	raw, err := os.ReadFile(filepath.Join(root, ".verdict", ".gitignore"))
	if err != nil {
		t.Fatalf("read ignore: %v", err)
	}
	for _, line := range stateLines {
		if got := strings.Count(string(raw), line); got != 1 {
			t.Fatalf("ignore carries %q %d times; want 1\n%s", line, got, raw)
		}
	}
}

// repeatWriters runs one first-time EnsureStateIgnore per writer and fails on
// the first writer that did not come back clean.
func repeatWriters(t *testing.T, root string) {
	t.Helper()
	const writers = 16
	errs := make([]*stateError, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() { errs[i] = NewLayout(root).EnsureStateIgnore() })
	}
	wg.Wait()
	for i, serr := range errs {
		if serr != nil {
			t.Fatalf("writer %d: %v", i, serr)
		}
	}
}

// TestEnsureStateIgnoreLeavesAPresentContractAlone proves the check before the
// write: once every line is present the call rewrites nothing, so a settled
// checkout gains no churn — and, on Windows, no rename window for another
// store's reader to trip on. The stamp is the evidence: an atomic write would
// hand the file a fresh modification time.
func TestEnsureStateIgnoreLeavesAPresentContractAlone(t *testing.T) {
	root := t.TempDir()
	layout := NewLayout(root)
	if serr := layout.EnsureStateIgnore(); serr != nil {
		t.Fatalf("EnsureStateIgnore: %v", serr)
	}
	stamp := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	ignore := layout.GitignoreFile()
	if err := os.Chtimes(ignore, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if serr := layout.EnsureStateIgnore(); serr != nil {
		t.Fatalf("second EnsureStateIgnore: %v", serr)
	}
	info, err := os.Stat(ignore)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(stamp) {
		t.Fatalf("ignore mtime = %s; want the untouched %s", info.ModTime(), stamp)
	}
}
