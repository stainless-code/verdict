// Package atomic owns every store write: temp file in the target directory,
// fsync, close, rename over the target, then a best-effort directory fsync.
// Nothing is ever truncated in place, so a crash lands on either the old or
// the new bytes, never a torn middle. This folds audit 19 in as structure:
// the shell keeps its own writes, and every Go-ported verb shares this path.
package atomic

import (
	"fmt"
	"os"
	"path/filepath"
)

// storeFileMode is the shell's umask-022 default for store files.
const storeFileMode = 0o644

// opWrite names the atomic-write operation in sealed errors.
const opWrite = "atomic write "

// WriteFile writes data to path atomically with mode 0o644 (the shell's
// umask-022 default for store files). The temp file lives in the target
// directory so the rename never crosses filesystems.
func WriteFile(path string, data []byte) error {
	return writeFile(path, data, storeFileMode)
}

// writeFile is the shared temp+fsync+rename+dir-fsync path.
func writeFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return sealed(opWrite+path, err)
	}
	tmpName := tmp.Name()
	// Best-effort temp cleanup: the rename already replaced the target, or
	// the write below failed and its error carries the outcome.
	defer func() { _ = os.Remove(tmpName) }() // cleanup has no recovery path; the write error rules
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close() // write error already rules; close adds nothing
		return sealed(opWrite+path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close() // sync error already rules; close adds nothing
		return sealed(opWrite+path, err)
	}
	if err := tmp.Close(); err != nil {
		return sealed(opWrite+path, err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return sealed(opWrite+path, err)
	}
	if err := replaceFile(tmpName, path); err != nil {
		return sealed(opWrite+path, err)
	}
	fsyncDir(dir)
	return nil
}

// sealed annotates err without exposing it, per GO-ERRORS-03 (see jq.Sealed).
func sealed(op string, err error) error {
	//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
	return fmt.Errorf("%s: %v", op, err)
}

// fsyncDir persists the rename itself. Best effort: a directory that cannot
// be opened (permissions, exotic filesystems) must not fail the write.
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
