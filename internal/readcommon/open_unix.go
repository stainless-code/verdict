//go:build unix

package readcommon

import (
	"os"
	"syscall"
)

// OpenNoBlock opens path without waiting for a writer. A path the operator
// controls can become a FIFO after any earlier stat, and a blocking open would
// wait for a writer that never comes. The caller must fstat the descriptor and
// refuse anything that is not a regular file before reading.
// Missing files preserve [fs.ErrNotExist].
func OpenNoBlock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, openError("open without blocking", err)
	}
	return f, nil
}

// OpenNoBlockIn opens a file below root without waiting for a writer. The
// caller must fstat the descriptor and refuse anything but a regular file.
func OpenNoBlockIn(root *os.Root, name string) (*os.File, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, openError("open without blocking", err)
	}
	return f, nil
}

// OpenNoFollowIn opens a file below root neither following a final symlink nor
// waiting for a writer (O_NOFOLLOW|O_NONBLOCK). The caller still fstats the
// descriptor and refuses anything but a regular file, and on platforms without
// O_NOFOLLOW it must lstat the leaf first. Missing files preserve
// [fs.ErrNotExist].
func OpenNoFollowIn(root *os.Root, name string) (*os.File, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, openError("open without following symlinks", err)
	}
	return f, nil
}
