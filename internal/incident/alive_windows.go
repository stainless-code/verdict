//go:build windows

package incident

import (
	"errors"
	"math"
	"syscall"
)

// errInvalidParameter is ERROR_INVALID_PARAMETER: OpenProcess on a pid that has
// already exited.
const errInvalidParameter syscall.Errno = 87

// pidAlive reports whether pid names a running process. A handle that opens is
// one; only "no such process" proves the process is gone, since a pid this user
// may not open belongs to somebody who is still running.
func pidAlive(pid int) bool {
	if pid <= 0 || int64(pid) > math.MaxUint32 {
		return false
	}
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return !errors.Is(err, errInvalidParameter)
	}
	defer func() { _ = syscall.CloseHandle(handle) }() //nolint:errcheck // the probe already answered
	return true
}
