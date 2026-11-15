package incident

import (
	"time"

	"github.com/stainless-code/verdict/internal/storelock"
)

// ErrBusy marks a ledger whose lock this attach could not take inside the wait
// window. The holder is still running, so the caller may retry.
var ErrBusy = storelock.ErrBusy

// lockWait is the shared bounded wait for a concurrent writer. The parallel
// attach test raises it: sixteen waiters can starve one past a budget sized for
// a single operator write while the machine is loaded.
var lockWait = storelock.DefaultWait

// lock takes the ledger's exclusive advisory lock, waiting up to wait for the
// holder to finish. The kernel owns it: it is released when the holding process
// exits, however it exits, so a crashed attach leaves no stale lock to break
// and a paused one keeps the rows it is writing. The lock file itself is never
// removed: a waiter already holds a descriptor on it, and unlinking would let
// the next claimant lock a fresh file beside the one the waiter is polling.
func lock(root string, wait time.Duration) (func(), error) {
	return storelock.Acquire(path(root), wait) //nolint:wrapcheck // the shared lock already names the ledger it guards
}
