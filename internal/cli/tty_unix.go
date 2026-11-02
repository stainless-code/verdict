//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package cli

import (
	"os"
	"syscall"
	"unsafe"
)

// IsTerminal reports whether f is a terminal, mirroring the shell's
// `[ -t 0 ]`. A character device alone is not enough (/dev/null is one but
// is never a TTY), so this issues the terminal ioctl directly instead of
// checking the file mode.
func IsTerminal(f *os.File) bool {
	var buf [termiosSize]byte
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), ioctlGetTermios,
		uintptr(unsafe.Pointer(&buf[0])))
	return errno == 0
}
