//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package incident

import (
	"errors"
	"syscall"
)

// pidAlive reports whether pid names a running process. Only ESRCH proves the
// process is gone: a permission error is somebody else's live process, and no
// lock is broken on an unproven death.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return !errors.Is(err, syscall.ESRCH)
}
