package incident

import (
	"errors"
	"fmt"
	"os"
	"time"
)

const (
	// lockSuffix names the file beside the ledger that admits one attach at a
	// time: an attach is a read-modify-write of the whole file, so two running
	// together both read the same rows and the second write drops the first.
	// It outlives every attach — see [lock].
	lockSuffix = ".lock"
	// lockMode is the lock file's owner-only mode. It carries no state of its
	// own: the kernel holds the lock, not the bytes.
	lockMode = 0o600
	// lockWait bounds the wait for a concurrent attach: longer than any attach
	// takes, shorter than an operator's patience.
	lockWait = 5 * time.Second
	// lockPoll is the retry interval while another attach holds the lock.
	lockPoll = 5 * time.Millisecond
)

// ErrBusy marks a ledger whose lock this attach could not take inside the wait
// window. The holder is still running, so the caller may retry.
var ErrBusy = errors.New("incident ledger busy")

// lock takes the ledger's exclusive advisory lock, waiting up to wait for the
// holder to finish. The kernel owns it: it is released when the holding process
// exits, however it exits, so a crashed attach leaves no stale lock to break
// and a paused one keeps the rows it is writing. The lock file itself is never
// removed: a waiter already holds a descriptor on it, and unlinking would let
// the next claimant lock a fresh file beside the one the waiter is polling.
func lock(root string, wait time.Duration) (func(), error) {
	file := lockPath(root)
	f, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR, lockMode)
	if err != nil {
		return nil, fmt.Errorf("incident ledger lock %s: %w", file, err) //nolint:wrapcheck // the file names the lock
	}
	if err := awaitLock(f, time.Now().Add(wait)); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}

// awaitLock polls the kernel for the lock until deadline, refusing [ErrBusy]
// while another process holds it.
func awaitLock(f *os.File, deadline time.Time) error {
	for {
		held, err := tryLock(f)
		if err != nil {
			return err
		}
		if held {
			return nil
		}
		if !time.Now().Before(deadline) {
			return ErrBusy //nolint:wrapcheck // the package's own sentinel
		}
		time.Sleep(lockPoll)
	}
}

// lockPath anchors the lock beside the ledger it guards.
func lockPath(root string) string {
	return path(root) + lockSuffix
}
