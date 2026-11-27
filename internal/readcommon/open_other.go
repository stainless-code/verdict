//go:build !unix

package readcommon

import (
	"errors"
	"io/fs"
	"os"

	"github.com/stainless-code/verdict/internal/noblock"
)

// OpenNoBlock opens path. A non-unix filesystem carries no FIFO namespace
// (Windows named pipes live under \\.\pipe), so the plain open cannot wait for
// a writer; the caller still fstats and refuses a non-regular file.
func OpenNoBlock(path string) (*os.File, error) {
	f, err := noblock.Open(path)
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

// OpenNoFollowIn opens a file below root without following a final link. A
// non-unix filesystem carries no FIFO namespace and no O_NOFOLLOW, so the
// leaf is lstat-checked before the open and the opened file must be that
// same file afterwards (its handle's identity against the lstat): a link
// swapped in between is refused, never read through.
func OpenNoFollowIn(root *os.Root, name string) (*os.File, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, openError("lstat", err)
	}
	if before.Mode()&fs.ModeSymlink != 0 {
		return nil, openError("open without following symlinks", errLinkedLeaf)
	}
	f, err := OpenNoBlockIn(root, name)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		_ = f.Close()
		return nil, openError("open without following symlinks", errLinkedLeaf)
	}
	return f, nil
}

// errLinkedLeaf is a leaf that was a link, or changed between its lstat and
// its open.
var errLinkedLeaf = errors.New("the file is a link or changed while it was opened")
