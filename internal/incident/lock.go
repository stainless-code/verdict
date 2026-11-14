package incident

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

const (
	// lockSuffix names the file beside the ledger that admits one attach at a
	// time: an attach is a read-modify-write of the whole file, so two running
	// together both read the same rows and the second write drops the first.
	lockSuffix = ".lock"
	// lockMode is the lock file's owner-only mode.
	lockMode = 0o600
	// lockWait bounds the wait for a concurrent attach: longer than any attach
	// takes, shorter than an operator's patience.
	lockWait = 5 * time.Second
	// lockPoll is the retry interval while an attach holds the lock.
	lockPoll = 5 * time.Millisecond
	// lockStale is the age at which a lock file is a killed attach's leftover:
	// the holder never spans a subprocess or a network call, so it holds the
	// lock for milliseconds.
	lockStale = time.Minute
)

// ErrBusy marks a ledger another attach held for the whole wait window. The
// holder is alive and wrote nothing, so the caller may retry.
var ErrBusy = errors.New("incident ledger busy")

// lock takes the ledger's exclusive lock, waiting up to wait for an attached
// writer to release it, and returns the release.
func lock(root string, wait time.Duration) (func(), error) {
	file := lockPath(root)
	deadline := time.Now().Add(wait)
	for {
		err := claimLock(file)
		if err == nil {
			return release(file), nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if !time.Now().Before(deadline) {
			return nil, ErrBusy //nolint:wrapcheck // the package's own sentinel
		}
		time.Sleep(lockPoll)
	}
}

// claimLock makes one attempt at the ledger lock: nil when this caller owns
// it, [fs.ErrExist] while another attach holds it, and the failure otherwise.
func claimLock(file string) error {
	err := claim(file)
	if !errors.Is(err, fs.ErrExist) {
		return err
	}
	if !stale(file) {
		return err
	}
	if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("incident ledger lock %s: %w", file, err) //nolint:wrapcheck // the held lock cannot be broken
	}
	return claim(file)
}

// claim creates the lock file, which carries the holder's pid and start so a
// leftover names the attach that died holding it.
func claim(file string) error {
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, lockMode)
	if err != nil {
		return fmt.Errorf("incident ledger lock %s: %w", file, err) //nolint:wrapcheck // the caller matches fs.ErrExist
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	return nil
}

// release drops the lock file; the ledger's rename already happened under it,
// so a failed removal leaves a lock the next attach breaks once it is stale.
func release(file string) func() {
	return func() { _ = os.Remove(file) }
}

// stale reports whether the lock file has aged past any real attach.
func stale(file string) bool {
	fi, err := os.Stat(file)
	return err == nil && time.Since(fi.ModTime()) > lockStale
}

// lockPath anchors the lock beside the ledger it guards.
func lockPath(root string) string {
	return path(root) + lockSuffix
}
