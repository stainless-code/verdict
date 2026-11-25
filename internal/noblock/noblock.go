// Package noblock opens and reads regular files without ever waiting for a
// writer. A bundle path is operator-controlled: any local process can swap a
// file for a FIFO after the last stat, and a plain open of a FIFO then blocks
// the verb (and the store lock it holds) forever. Every open here is
// non-blocking and the opened descriptor is fstat-ed, so the check and the
// read refer to the same inode. It imports only the standard library so every
// package that reads a store file can share it.
package noblock

import (
	"errors"
	"io"
	"io/fs"
	"os"
)

// ErrNotRegular reports an opened descriptor that is not a regular file: a
// FIFO, device, socket or directory. A symlink to a regular file is followed
// and reads as that file.
var ErrNotRegular = errors.New("not a regular file")

// OpenRegular opens path without waiting for a writer and refuses anything
// that is not a regular file once opened. A missing file keeps
// [fs.ErrNotExist]; a refused file wraps [ErrNotRegular] in a [fs.PathError].
// The caller closes the returned file.
func OpenRegular(path string) (*os.File, error) {
	f, err := Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()   // the stat error rules
		return nil, err //nolint:wrapcheck // *fs.PathError already names the file
	}
	if !info.Mode().IsRegular() {
		_ = f.Close() // refused before any read
		return nil, &fs.PathError{Op: "open", Path: path, Err: ErrNotRegular}
	}
	return f, nil
}

// ReadFile is [os.ReadFile] that cannot block on a FIFO: a non-regular file is
// an error, never a wait. It is unbounded; callers with a size contract use
// jq.CappedFile instead.
func ReadFile(path string) ([]byte, error) {
	f, err := OpenRegular(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only handle; the read error rules
	return io.ReadAll(f)             //nolint:wrapcheck // *fs.PathError semantics are os.ReadFile's
}
