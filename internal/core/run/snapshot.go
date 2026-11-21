package run

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/journal"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// snapshotAbsent is the digest a name that does not exist at all is recorded
// under, so a file appearing later still reads as a change.
const snapshotAbsent = "absent"

type executionSnapshot map[string]string

func captureSnapshot(dir string) (executionSnapshot, *core.RefusalError) {
	snapshot := executionSnapshot{}
	for _, name := range []string{
		"PR.json", "source.json", "full.diff", "hunks.tsv", "run.json",
		"context.txt", "procedure.md", "roles.txt", "toc.txt",
	} {
		digest, ref := snapshotFileDigest(dir, name)
		if ref != nil {
			return nil, ref
		}
		snapshot[name] = digest
	}

	if ref := snapshot.bindInputs(dir); ref != nil {
		return nil, ref
	}
	return snapshot, nil
}

func (snapshot executionSnapshot) check(dir string) *core.RefusalError {
	if ref := snapshot.checkInputInventory(dir); ref != nil {
		return ref
	}
	for name, hash := range snapshot {
		digest, ref := snapshotFileDigest(dir, name)
		if ref != nil {
			return ref
		}
		if digest != hash {
			return &core.RefusalError{Code: codeStaleArtifact, Message: "execution snapshot changed: " + name}
		}
	}
	return nil
}

func phaseDirectory(dir, name string) (string, *core.RefusalError) {
	path := filepath.Join(dir, name)
	if err := os.Mkdir(path, roundsDirMode); err == nil {
		return path, nil
	}

	return "", &core.RefusalError{Code: codeRecordFailed, Message: "cannot create fresh phase directory: " + name}
}

func writeCoordinator(dir, name string, value any) *core.RefusalError {
	if err := atomic.WriteFileMode(filepath.Join(dir, name), coordinatorBytes(value), roundEvidenceMode); err != nil {
		return &core.RefusalError{Code: codeRecordFailed, Message: "cannot write execution artifact: " + name}
	}
	return nil
}

func coordinatorBytes(value any) []byte { return []byte(jq.PrettyOrdered(value) + "\n") }

// snapshotFileDigest streams one snapshot file's digest. The checkpoint covers
// every artifact the snapshot names, including a finder's auxiliary files,
// which carry no size ceiling: only the digest is needed, so the file is never
// loaded whole. The leaf is confined to the bundle and refused when it is not a
// regular file, so a swapped symlink cannot make the checkpoint hash a file
// outside the bundle.
func snapshotFileDigest(dir, name string) (string, *core.RefusalError) {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return snapshotAbsent, nil
	}
	if err != nil {
		return "", snapshotUnreadable(name)
	}
	defer func() { _ = root.Close() }()
	file, ref := openSnapshotLeaf(root, name)
	if ref != nil {
		return "", ref
	}
	if file == nil {
		return snapshotAbsent, nil
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", snapshotUnreadable(name)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// openSnapshotLeaf opens a bound leaf without following it. A nil file and a
// nil refusal mean the leaf is absent, which the checkpoint records as such.
func openSnapshotLeaf(root *os.Root, name string) (*os.File, *core.RefusalError) {
	leaf := filepath.ToSlash(name)
	before, err := root.Lstat(leaf)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, snapshotUnreadable(name)
	}
	if !before.Mode().IsRegular() {
		return nil, snapshotUnsafe(name)
	}
	file, err := readcommon.OpenNoFollowIn(root, leaf)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, snapshotUnreadable(name)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, snapshotUnsafe(name)
	}
	return file, nil
}

func snapshotUnsafe(name string) *core.RefusalError {
	return &core.RefusalError{Code: codeStaleArtifact, Message: "snapshot file is unsafe: " + name}
}

func snapshotUnreadable(name string) *core.RefusalError {
	return &core.RefusalError{Code: codeMissingBundle, Message: "snapshot file unreadable: " + name}
}

// snapshotBytes reads one snapshot file whole for a consumer that needs its
// contents, never for a digest.
func snapshotBytes(dir, name string) ([]byte, *core.RefusalError) {
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, snapshotUnsafe(name)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, snapshotUnreadable(name)
	}
	return raw, nil
}

func (snapshot executionSnapshot) bind(dir string, names ...string) *core.RefusalError {
	for _, name := range names {
		digest, ref := snapshotFileDigest(dir, name)
		if ref != nil {
			return ref
		}
		snapshot[name] = digest
	}
	return nil
}

func outputNamespace(name string) bool {
	first, _, _ := strings.Cut(filepath.ToSlash(name), "/")
	return strings.HasPrefix(first, "x-") || first == "verification" || first == launcherFile ||
		first == reviewFile || first == "budget.json" || strings.HasPrefix(first, ".run")
}

func (snapshot executionSnapshot) bindInputs(dir string) *core.RefusalError {
	files, err := journal.Sweep(dir)
	if err != nil {
		return &core.RefusalError{Code: codeStaleArtifact, Message: "cannot snapshot evidence inventory"}
	}
	for _, file := range files.Files {
		if !outputNamespace(file.Path) {
			snapshot[filepath.FromSlash(file.Path)] = file.SHA256
		}
	}
	return nil
}

func (snapshot executionSnapshot) bindPhase(dir, name string) *core.RefusalError {
	files, err := journal.Sweep(filepath.Join(dir, name))
	if err != nil {
		return &core.RefusalError{Code: codeRecordFailed, Message: "cannot bind phase artifacts"}
	}
	for _, file := range files.Files {
		snapshot[filepath.Join(name, filepath.FromSlash(file.Path))] = file.SHA256
	}
	return rejectPhaseLinks(filepath.Join(dir, name))
}

func rejectPhaseLinks(dir string) *core.RefusalError {
	err := filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return jq.Sealed("phase inventory", err)
		}
		if !entry.IsDir() && !entry.Type().IsRegular() {
			return jq.NewErr("unsafe phase artifact")
		}
		return nil
	})
	if err != nil {
		return &core.RefusalError{Code: codeRecordFailed, Message: "phase contains an unsafe artifact"}
	}
	return nil
}

func (snapshot executionSnapshot) checkInputInventory(dir string) *core.RefusalError {
	current := executionSnapshot{}
	if ref := current.bindInputs(dir); ref != nil {
		return ref
	}
	for name := range current {
		if _, exists := snapshot[name]; !exists {
			return &core.RefusalError{Code: codeStaleArtifact, Message: "execution evidence inventory changed"}
		}
	}
	return nil
}
