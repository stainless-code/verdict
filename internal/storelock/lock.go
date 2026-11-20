// Package storelock is the OS advisory lock that keeps one store's
// read-modify-write to one writer at a time: flock on Unix, LockFileEx on
// Windows.
package storelock

import (
	"context"
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
	return acquire(store, wait, func() error {
		time.Sleep(pollInterval)
		return nil
	})
}

// AcquireContext takes the same lock and stops waiting when ctx is canceled.
func AcquireContext(ctx context.Context, store string, wait time.Duration) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return acquire(store, wait, func() error {
		select {
		case <-ctx.Done():
		case <-time.After(pollInterval):
		}
		return ctx.Err()
	})
}

func acquire(store string, wait time.Duration, pause func() error) (func(), error) {
	file := LockPath(store)
	f, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR, lockMode)
	if err != nil {
		return nil, fmt.Errorf("store lock %s: %w", file, err) //nolint:wrapcheck // the file names the lock
	}
	if err := awaitLock(f, time.Now().Add(wait), pause); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}

// awaitLock polls the kernel for the lock until deadline, refusing [ErrBusy]
// while another writer holds it.
func awaitLock(f *os.File, deadline time.Time, pause func() error) error {
	var waitErr error
	for ; waitErr == nil; waitErr = pause() {
		held, err := tryLock(f)
		if held || err != nil {
			return err
		}
		if !time.Now().Before(deadline) {
			return ErrBusy //nolint:wrapcheck // the package's own sentinel
		}
	}
	return waitErr
}
