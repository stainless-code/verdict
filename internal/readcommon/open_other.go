//go:build !unix

package readcommon

import (
	"os"
)

// OpenNoBlock opens path. A non-unix filesystem carries no FIFO namespace
// (Windows named pipes live under \\.\pipe), so the plain open cannot wait for
// a writer; the caller still fstats and refuses a non-regular file.
func OpenNoBlock(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, openError("open", err)
	}
	return f, nil
}

// OpenNoBlockIn opens a file below root. A non-unix filesystem carries no
// FIFO namespace; the caller still fstats and refuses a non-regular file.
func OpenNoBlockIn(root *os.Root, name string) (*os.File, error) {
	f, err := root.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, openError("open", err)
	}
	return f, nil
}

func openNoFollowIn(root *os.Root, name string) (*os.File, error) {
	return OpenNoBlockIn(root, name)
}
