//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package storelock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// tryLock claims the exclusive lock on f without waiting, reporting false while
// another process holds it. A signal interrupts the syscall before it answers,
// which reads as not held: the poll retries.
func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.EWOULDBLOCK), errors.Is(err, syscall.EINTR):
		return false, nil
	default:
		return false, fmt.Errorf("store lock %s: %w", f.Name(), err) //nolint:wrapcheck // the file names the lock
	}
}
