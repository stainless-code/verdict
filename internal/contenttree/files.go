package contenttree

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/jq"
)

// opCreate prefixes a failed create in a sealed error.
const opCreate = "create "

// The permissions of what a snapshot writes, and what RemoveTree restores so
// the owner can delete it.
const (
	dirPerm       os.FileMode = 0o755
	filePerm      os.FileMode = 0o644
	execPerm      os.FileMode = 0o755
	ownerDirPerm  os.FileMode = 0o700
	ownerFilePerm os.FileMode = 0o600
)

func perm(exec bool) os.FileMode {
	if exec {
		return execPerm
	}
	return filePerm
}

// makeDir creates rel under base, when base is set.
func makeDir(base, rel string) error {
	if base == "" {
		return nil
	}
	dir := filepath.Join(base, filepath.FromSlash(rel))
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return jq.Sealed(opCreate+dir, err)
	}
	return nil
}

// writeFile writes body to a new file at to, creating its parents.
func writeFile(to string, body []byte, exec bool) error {
	if err := os.MkdirAll(filepath.Dir(to), dirPerm); err != nil {
		return jq.Sealed(opCreate+filepath.Dir(to), err)
	}
	if err := os.WriteFile(to, body, perm(exec)); err != nil {
		return jq.Sealed("write "+to, err)
	}
	return nil
}

// copyFile copies from's bytes to a new file at to.
func copyFile(from, to string, exec bool) error {
	if err := os.MkdirAll(filepath.Dir(to), dirPerm); err != nil {
		return jq.Sealed(opCreate+filepath.Dir(to), err)
	}
	src, err := os.Open(from)
	if err != nil {
		return jq.Sealed("open "+from, err)
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm(exec))
	if err != nil {
		return jq.Sealed(opCreate+to, err)
	}
	_, cerr := io.Copy(dst, src)
	if err := errors.Join(cerr, dst.Close()); err != nil {
		return jq.Sealed("copy "+from, err)
	}
	return nil
}

// copyLinkTo recreates the symbolic link at from under base, when base is
// set; where links cannot be made (Windows without the privilege) it writes
// the target as a file, as git does with core.symlinks off.
func copyLinkTo(from, base, rel string) error {
	if base == "" {
		return nil
	}
	target, err := os.Readlink(from)
	if err != nil {
		return jq.Sealed("read link "+from, err)
	}
	to := filepath.Join(base, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(to), dirPerm); err != nil {
		return jq.Sealed(opCreate+filepath.Dir(to), err)
	}
	if os.Symlink(target, to) == nil {
		return nil
	}
	return writeFile(to, []byte(target), false)
}

// moveEntries moves every top-level entry of staging into dest.
func moveEntries(staging, dest string) error {
	names, err := os.ReadDir(staging)
	if err != nil {
		return jq.Sealed("read "+staging, err)
	}
	for _, d := range names {
		if err := os.Rename(filepath.Join(staging, d.Name()), filepath.Join(dest, d.Name())); err != nil {
			return jq.Sealed("move "+d.Name()+" into the snapshot", err)
		}
	}
	return nil
}

// RemoveTree removes dir after giving the owner back write access to every
// directory and file in it, so a command that chmodded its tree read-only
// cannot strand it (Windows also refuses to delete a read-only file).
// Symlinks are never followed.
func RemoveTree(dir string) error {
	var errs []error
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		errs = append(errs, ownerWritable(path, d, err))
		return nil
	})
	if err := os.RemoveAll(dir); err != nil {
		return jq.Sealed("remove "+dir, errors.Join(append([]error{err, walkErr}, errs...)...))
	}
	return nil
}

// ownerWritable restores the owner's access to one walked entry; a walk
// error or a link is left as found.
func ownerWritable(path string, d fs.DirEntry, walkErr error) error {
	switch {
	case walkErr != nil:
		return walkErr
	case d.IsDir():
		return os.Chmod(path, ownerDirPerm) //nolint:wrapcheck // joined and sealed by RemoveTree
	case d.Type().IsRegular():
		return os.Chmod(path, ownerFilePerm) //nolint:wrapcheck // joined and sealed by RemoveTree
	}
	return nil
}

// tempIndex is a fresh index path in a private directory, and its cleanup.
func tempIndex() (string, func(), error) {
	dir, err := os.MkdirTemp("", "verdict-index-")
	if err != nil {
		return "", nil, jq.Sealed("create a throwaway index", err)
	}
	return filepath.Join(dir, "index"), func() { _ = os.RemoveAll(dir) }, nil
}
