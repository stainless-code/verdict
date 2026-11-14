//go:build windows

package storelock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	// lockfileExclusiveLock and lockfileFailImmediately are the LockFileEx
	// flags: one exclusive, non-blocking claim on the range below.
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
	// lockBytes is the length of the range every writer locks. One byte at
	// offset zero locks the file for this protocol.
	lockBytes = 1
	// errLockViolation is ERROR_LOCK_VIOLATION: another handle holds the range.
	errLockViolation syscall.Errno = 33
)

// kernel32 and lockFileEx reach the one Win32 call the syscall package leaves
// out for files. A lazy lookup keeps the module graph at zero dependencies.
var (
	kernel32   = syscall.NewLazyDLL("kernel32.dll")
	lockFileEx = kernel32.NewProc("LockFileEx")
)

// tryLock claims the exclusive lock on f without waiting, reporting false while
// another process holds it.
func tryLock(f *os.File) (bool, error) {
	var rangeLock syscall.Overlapped
	held, _, err := lockFileEx.Call(f.Fd(), lockfileExclusiveLock|lockfileFailImmediately,
		0, lockBytes, 0, uintptr(unsafe.Pointer(&rangeLock)))
	if held != 0 {
		return true, nil
	}
	if errors.Is(err, errLockViolation) {
		return false, nil
	}
	return false, fmt.Errorf("store lock %s: %w", f.Name(), err) //nolint:wrapcheck // the file names the lock
}
