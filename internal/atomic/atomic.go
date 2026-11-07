// Package atomic owns every store write: temp file in the target directory,
// fsync, close, rename over the target, then a best-effort directory fsync.
// Nothing is ever truncated in place, so a crash lands on either the old or
// the new bytes, never a torn middle. Every verb shares this one path.
package atomic

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/jq"
)

const storeFileMode = 0o644

const errSidecarMode = 0o600

const opWrite = "atomic write "

const opDirectWrite = "write "

// WriteFile writes data to path atomically with mode 0o644 (the
// umask-022 default for store files). The temp file lives in the target
// directory so the rename never crosses filesystems.
func WriteFile(path string, data []byte) error {
	return writeFile(path, data, storeFileMode, nil)
}

// WriteFileKeepingModTime writes data atomically and leaves the modification
// time the file already had, because some readers anchor on it: the measured
// usage join attaches the session whose window brackets a cost file's mtime, so
// a rewrite that moves the stamp can silently drop a paid measurement.
func WriteFileKeepingModTime(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return writeFile(path, data, storeFileMode, nil)
	}
	stamp := func(staged string) error {
		// Both stamps take the modification time: atime is not portable, and a
		// rewrite is not a read, so the value is only a carrier here.
		return os.Chtimes(staged, info.ModTime(), info.ModTime())
	}
	return writeFile(path, data, storeFileMode, stamp)
}

// WriteFileMode writes data to path atomically with an explicit mode, for
// payload files that must land executable (the skill shim, schema validator).
func WriteFileMode(path string, data []byte, mode os.FileMode) error {
	return writeFile(path, data, mode, nil)
}

// WriteErrSidecar writes one command-stderr debug sidecar atomically with
// mode 0o600: the bytes can carry token-adjacent forge diagnostics and are
// debug residue, never published bundle content.
func WriteErrSidecar(path string, data []byte) error {
	return writeFile(path, data, errSidecarMode, nil)
}

// writeFile stages data beside path and renames it into place. stamp, when set,
// runs on the staged file before the rename, so a caller can carry a property
// the rename would otherwise reset (a modification time) without ever exposing
// a moment where the file lacks it.
func writeFile(path string, data []byte, mode os.FileMode, stamp func(string) error) error {
	dir := filepath.Dir(path)
	tmpName, err := stage(path, dir, data, mode, stamp)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpName) }() // already renamed on the success path; cleanup rules
	if err := replaceFile(tmpName, path); err != nil {
		return jq.Sealed(opWrite+path, err)
	}
	fsyncDir(dir)
	return nil
}

// stage writes data into a fresh temp file in dir with mode and the optional
// stamp applied, returning its name. A failure removes the partial file and
// comes back sealed, so no caller inherits cleanup or wrapping.
func stage(path, dir string, data []byte, mode os.FileMode, stamp func(string) error) (string, error) {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", jq.Sealed(opWrite+path, err)
	}
	name := tmp.Name()
	if err := sealStaged(path, tmp, data, mode, stamp); err != nil {
		_ = os.Remove(name) // the staged bytes are dead; cleanup rules
		return "", err
	}
	return name, nil
}

// sealStaged finishes the staged file: the bytes, the sync, the mode, and the
// caller's stamp.
func sealStaged(path string, tmp *os.File, data []byte, mode os.FileMode, stamp func(string) error) error {
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close() // write error already rules; close adds nothing
		return jq.Sealed(opWrite+path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close() // sync error already rules; close adds nothing
		return jq.Sealed(opWrite+path, err)
	}
	if err := tmp.Close(); err != nil {
		return jq.Sealed(opWrite+path, err)
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return jq.Sealed(opWrite+path, err)
	}
	if stamp == nil {
		return nil
	}
	if err := stamp(tmp.Name()); err != nil {
		return jq.Sealed(opWrite+path, err)
	}
	return nil
}

func fsyncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer func() { _ = d.Close() }() // best-effort dir fsync; failure must not fail the write
	_ = d.Sync()                     //nolint:errcheck // best-effort dir fsync; failure must not fail the write
}

// Replace renames from onto to with the platform's replace semantics. Unix
// uses an atomic rename; Windows retries the transient sharing/access
// denials an antivirus or indexer can hold on the destination, then gives
// up. Callers stage on the destination filesystem first.
func Replace(from, to string) error { return replaceFile(from, to) }

// WriteThrough writes data atomically to a path the operator named, which may
// be a symlink or a node that cannot be replaced. A regular file — or the
// file a symlink chain ends at — gets the temp+rename path with its existing
// mode kept, so the link survives and only its target changes; a device, FIFO,
// socket, directory or dangling link keeps the direct write, because a rename
// would replace the node itself instead of writing through it.
func WriteThrough(path string, data []byte, mode os.FileMode) error {
	target, perm, ok := replaceable(path)
	if !ok {
		if err := os.WriteFile(path, data, mode); err != nil {
			return jq.Sealed(opDirectWrite+path, err)
		}
		return nil
	}
	if perm == 0 {
		perm = mode
	}
	return writeFile(target, data, perm, nil)
}

// replaceable reports the regular file a rename may replace: path itself, or
// the file a symlink chain ends at. perm is that file's mode, or zero when the
// path does not exist yet and the caller's mode applies. ok is false for
// every node a rename must not replace and for a link that leads nowhere.
func replaceable(path string) (target string, perm os.FileMode, ok bool) {
	fi, err := os.Lstat(path)
	if err != nil {
		return path, 0, errors.Is(err, fs.ErrNotExist)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		if path, err = filepath.EvalSymlinks(path); err != nil {
			return "", 0, false
		}
		if fi, err = os.Stat(path); err != nil {
			return "", 0, false
		}
	}
	if !fi.Mode().IsRegular() {
		return "", 0, false
	}
	return path, fi.Mode().Perm(), true
}
