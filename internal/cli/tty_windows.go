//go:build windows

package cli

import (
	"os"
	"syscall"
	"unsafe"
)

func IsTerminal(f *os.File) bool {
	var mode uint32
	r, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleMode").Call(
		uintptr(f.Fd()), uintptr(unsafe.Pointer(&mode)))
	return r != 0
}
