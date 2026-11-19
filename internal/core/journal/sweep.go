package journal

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/stainless-code/verdict/internal/storelock"
)

// Snapshot is the canonical content-digest manifest of one bundle directory
// (J3: `path\0size\0sha256(content)\n`, sorted by path, excluding advisory
// *.lock files).
type Snapshot struct {
	Digest string
	Files  []FileRecord
	Exists bool
}

// Sweep walks dir and hashes every regular file (excluding *.lock) to produce
// the canonical content-digest manifest. A missing directory yields
// Snapshot{Exists: false} and a nil error.
func Sweep(dir string) (Snapshot, error) {
	if dir == "" {
		return Snapshot{}, nil
	}
	fi, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return Snapshot{}, nil
		}
		return Snapshot{}, wrapErr("stat bundle "+dir, err)
	}
	if !fi.IsDir() {
		return Snapshot{}, nil
	}
	files, err := collectFiles(dir)
	if err != nil {
		return Snapshot{}, err
	}
	slices.SortFunc(files, func(a, b FileRecord) int {
		return cmp.Compare(a.Path, b.Path)
	})
	return Snapshot{
		Digest: manifestDigest(files),
		Files:  files,
		Exists: true,
	}, nil
}

func collectFiles(dir string) ([]FileRecord, error) {
	var files []FileRecord
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		return visitBundleEntry(dir, path, d, walkErr, &files)
	})
	if err != nil {
		return nil, wrapErr("walk bundle "+dir, err)
	}
	return files, nil
}

func visitBundleEntry(dir, path string, d fs.DirEntry, walkErr error, files *[]FileRecord) error {
	if walkErr != nil {
		if errors.Is(walkErr, fs.ErrNotExist) {
			return nil
		}
		return walkErr
	}
	rec, keep, err := sweepEntry(dir, path, d)
	if err != nil || !keep {
		return err
	}
	*files = append(*files, rec)
	return nil
}

func sweepEntry(dir, path string, d fs.DirEntry) (FileRecord, bool, error) {
	relSlash, ok, err := regularBundleRelPath(dir, path, d)
	if err != nil || !ok {
		return FileRecord{}, false, err
	}
	sum, n, err := hashFileStream(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return FileRecord{}, false, nil
		}
		return FileRecord{}, false, err
	}
	return FileRecord{Path: relSlash, SHA256: sum, Bytes: n}, true, nil
}

func regularBundleRelPath(dir, path string, d fs.DirEntry) (string, bool, error) {
	if d == nil || d.IsDir() || !d.Type().IsRegular() {
		return "", false, nil
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return "", false, wrapErr("relpath "+path, err)
	}
	if rel == "." {
		return "", false, nil
	}
	relSlash := filepath.ToSlash(rel)
	if strings.HasSuffix(d.Name(), storelock.LockSuffix) {
		return "", false, nil
	}
	return relSlash, true, nil
}

func hashFileStream(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, wrapErr("open "+path, err)
	}
	h := sha256.New()
	n, copyErr := io.Copy(h, f)
	closeErr := f.Close()
	if copyErr != nil {
		return "", 0, wrapErr("hash "+path, copyErr)
	}
	if closeErr != nil {
		return "", 0, wrapErr("close "+path, closeErr)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// manifestDigest computes sha256 over `path\0size\0sha256(content)\n` for every
// entry in sorted order (AMENDMENTS J3).
func manifestDigest(files []FileRecord) string {
	h := sha256.New()
	for i := range files {
		fmt.Fprintf(h, "%s\x00%d\x00%s\n", files[i].Path, files[i].Bytes, files[i].SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Diff returns the files created or whose content changed between pre and post,
// and the paths present in pre and absent from post. Both slices are sorted by
// path and non-nil.
func Diff(pre, post Snapshot) (wrote []FileRecord, removed []string) {
	preMap := indexFiles(pre.Files)
	postMap := indexFiles(post.Files)
	return diffWrote(preMap, post.Files), diffRemoved(pre.Files, postMap)
}

func indexFiles(files []FileRecord) map[string]FileRecord {
	idx := make(map[string]FileRecord, len(files))
	for i := range files {
		idx[files[i].Path] = files[i]
	}
	return idx
}

func diffWrote(preMap map[string]FileRecord, postFiles []FileRecord) []FileRecord {
	wrote := []FileRecord{}
	for i := range postFiles {
		f := postFiles[i]
		prev, ok := preMap[f.Path]
		if !ok || prev.SHA256 != f.SHA256 || prev.Bytes != f.Bytes {
			wrote = append(wrote, f)
		}
	}
	return wrote
}

func diffRemoved(preFiles []FileRecord, postMap map[string]FileRecord) []string {
	removed := []string{}
	for i := range preFiles {
		if _, ok := postMap[preFiles[i].Path]; !ok {
			removed = append(removed, preFiles[i].Path)
		}
	}
	return removed
}
