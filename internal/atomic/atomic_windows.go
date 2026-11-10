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
// transient. Store readers and the write path never overlap by design, so
// the retry only covers the outside-world locks.
const (
	// replaceAttempts bounds the retry: a lock that outlives it is a real
	// failure, never a hang.
	replaceAttempts = 20
	// replaceBackoff is the first wait between attempts; it doubles up to
	// replaceMaxBackoff (~1.6s total across replaceAttempts).
	replaceBackoff = 5 * time.Millisecond
	// replaceMaxBackoff caps the exponential wait.
	replaceMaxBackoff = 100 * time.Millisecond
)

// Windows error codes for a held handle; syscall exports only
// ERROR_ACCESS_DENIED, so the same-family codes are spelled here.
const (
	errSharingViolation syscall.Errno = 32
	errLockViolation    syscall.Errno = 33
)

// replaceFile renames the finished temp file over the store, retrying the
// transient Windows sharing/access errors with a bounded exponential
// backoff. Any other failure, or the last attempt, is returned unchanged.
func replaceFile(from, to string) error {
	wait := replaceBackoff
	for attempt := 1; ; attempt++ {
		err := os.Rename(from, to)
		if err == nil || attempt == replaceAttempts || !retryableReplace(err) {
			return err
		}
		time.Sleep(wait)
		wait = min(wait*2, replaceMaxBackoff)
	}
}

func replaceRootFile(root *os.Root, from, to string) error {
	wait := replaceBackoff
	for attempt := 1; ; attempt++ {
		err := root.Rename(from, to)
		if err == nil || attempt == replaceAttempts || !retryableReplace(err) {
			return err
		}
		time.Sleep(wait)
		wait = min(wait*2, replaceMaxBackoff)
	}
}

// retryableReplace reports whether a rename failure is a transient Windows
// sharing/access denial rather than a permanent one (a missing directory,
// a read-only target, a cross-volume rename).
func retryableReplace(err error) bool {
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED) ||
		errors.Is(err, errSharingViolation) ||
		errors.Is(err, errLockViolation)
}
