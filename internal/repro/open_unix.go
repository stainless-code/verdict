//go:build unix

package repro

import "syscall"

const nonblockRead = syscall.O_NONBLOCK
