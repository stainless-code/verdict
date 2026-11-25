//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package dashboard

import "syscall"

const (
	ioctlGetTermios = syscall.TIOCGETA
	ioctlSetTermios = syscall.TIOCSETA
)
