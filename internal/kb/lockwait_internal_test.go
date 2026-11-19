package kb

import (
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/storelock"
)

// TestLockWaitForTestSetsAndRestoresTheWait proves the seam an out-of-package
// test raises reaches the OS lock and that its restore hands the shipped budget
// back. The budget is read where [storelock.Acquire] receives it, so a hook
// nothing reads fails here instead of in a wall-clock window.
func TestLockWaitForTestSetsAndRestoresTheWait(t *testing.T) {
	waits := recordLockWaits(t)
	root := t.TempDir()
	restore := LockWaitForTest(time.Minute)
	if got := waits.at(t, root); got != time.Minute {
		t.Fatalf("Lock passed %v while LockWaitForTest said %v", got, time.Minute)
	}
	restore()
	if got := waits.at(t, root); got != storelock.DefaultWait {
		t.Fatalf("Lock passed %v after the restore; want the shipped %v", got, storelock.DefaultWait)
	}
}

// lockWaits records the budget [Lock] hands the OS, in call order.
type lockWaits struct {
	seen []time.Duration
}

// recordLockWaits swaps [acquireLock] for this recorder, which takes no real
// lock, and restores the seam when the test ends.
func recordLockWaits(t *testing.T) *lockWaits {
	t.Helper()
	waits := new(lockWaits)
	prev := acquireLock
	acquireLock = func(_ string, wait time.Duration) (func(), error) {
		waits.seen = append(waits.seen, wait)
		return func() {}, nil
	}
	t.Cleanup(func() { acquireLock = prev })
	return waits
}

// at locks the store over root and reports the wait that one call handed the
// OS, failing when the call never reached the seam.
func (w *lockWaits) at(t *testing.T, root string) time.Duration {
	t.Helper()
	before := len(w.seen)
	unlock, err := Lock(root)
	if err != nil {
		t.Fatalf("Lock(%s): %v", root, err)
	}
	unlock()
	if len(w.seen) != before+1 {
		t.Fatalf("Lock handed the OS %d waits; want one per call", len(w.seen)-before)
	}
	return w.seen[before]
}
