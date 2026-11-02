//go:build windows

package cli

import (
	"os"
	"syscall"
	"unsafe"
)

// IsTerminal reports whether f is a console, mirroring the shell's
// `[ -t 0 ]` on Windows.
func IsTerminal(f *os.File) bool {
	var mode uint32
	r, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleMode").Call(
		uintptr(f.Fd()), uintptr(unsafe.Pointer(&mode)))
	return r != 0
}
