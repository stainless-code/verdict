// Package storelock is the OS advisory lock that keeps one store's
// read-modify-write to one writer at a time: flock on Unix, LockFileEx on
// Windows.
package storelock

import (
	"errors"
	"fmt"
	"os"
	"time"
)

const (
	// LockSuffix names the file beside the store that carries the lock. It
	// outlives every write: a waiter already holds a descriptor on it, and
	// unlinking would let the next claimant lock a fresh file beside the one
	// the waiter is polling.
	LockSuffix = ".lock"
	// lockMode is the lock file's owner-only mode. It carries no state of its
	// own: the kernel holds the lock, not the bytes.
	lockMode = 0o600
	// DefaultWait bounds the wait for a concurrent write: longer than any
	// store write takes, shorter than an operator's patience.
	DefaultWait = 5 * time.Second
	// pollInterval is the retry interval while another writer holds the lock.
	pollInterval = 5 * time.Millisecond
)

// ErrBusy marks a store whose lock could not be taken inside the wait window.
// The holder is still running, so the caller may retry.
var ErrBusy = errors.New("store lock busy")

// LockPath names the lock file beside the store at path.
func LockPath(store string) string { return store + LockSuffix }

// Acquire takes the exclusive lock guarding store, waiting up to wait for the
// holder to finish. The kernel owns it: it is released when the holding
// process exits, however it exits, so a crashed writer leaves no stale lock to
// break and a paused one keeps the bytes it is writing.
func Acquire(store string, wait time.Duration) (func(), error) {
	file := LockPath(store)
	f, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR, lockMode)
	if err != nil {
		return nil, fmt.Errorf("store lock %s: %w", file, err) //nolint:wrapcheck // the file names the lock
	}
	if err := awaitLock(f, time.Now().Add(wait)); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}

// awaitLock polls the kernel for the lock until deadline, refusing [ErrBusy]
// while another writer holds it.
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
		time.Sleep(pollInterval)
	}
}
