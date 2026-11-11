//go:build windows

package cli

import (
	"os"
	"syscall"
)

// IsTerminal reports whether f is a console. The console API is the Windows
// analog of the unix terminal ioctl: a character device alone is not enough
// (NUL is one but is never a TTY).
func IsTerminal(f *os.File) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode) == nil
}
