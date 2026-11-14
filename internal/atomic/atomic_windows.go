//go:build windows

package atomic

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// Windows rename-over-existing is a MoveFileEx(MOVEFILE_REPLACE_EXISTING),
// so a plain replace works. What differs from POSIX is that the call fails
// while any other handle to the target is open (a sharing violation) or
// while an antivirus/indexer holds it (access denied), and those locks are
// transient. Two calls in this package meet them: the rename, and the read a
// second store's writer does of the same shared file while a rename lands.
const (
	// Windows error codes for a held handle; syscall exports only
	// ERROR_ACCESS_DENIED, so the same-family codes are spelled here.
	errSharingViolation syscall.Errno = 32
	errLockViolation    syscall.Errno = 33
)

// replaceFile renames the finished temp file over the store, retrying the
// transient Windows sharing/access errors with a bounded exponential
// backoff. Any other failure, or the last attempt, is returned unchanged.
func replaceFile(from, to string) error {
	wait := retryBackoff
	for attempt := 1; ; attempt++ {
		err := os.Rename(from, to)
		if err == nil || attempt == retryAttempts || !transientFileLock(err) {
			//nolint:wrapcheck // the caller seals the rename error in the atomic-write message (GO-ERRORS-03)
			return err
		}
		time.Sleep(wait)
		wait = min(wait*2, retryMaxBackoff)
	}
}

func replaceRootFile(root *os.Root, from, to string) error {
	wait := retryBackoff
	for attempt := 1; ; attempt++ {
		err := root.Rename(from, to)
		if err == nil || attempt == retryAttempts || !transientFileLock(err) {
			//nolint:wrapcheck // the caller seals the rename error in the atomic-write message (GO-ERRORS-03)
			return err
		}
		time.Sleep(wait)
		wait = min(wait*2, retryMaxBackoff)
	}
}

// transientFileLock reports whether a file call failed on a transient Windows
// lock — a sharing violation from another handle, an access denial from an
// antivirus/indexer, or a held byte range — rather than permanently (a missing
// directory, a read-only target, a cross-volume rename).
func transientFileLock(err error) bool {
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED) ||
		errors.Is(err, errSharingViolation) ||
		errors.Is(err, errLockViolation)
}
