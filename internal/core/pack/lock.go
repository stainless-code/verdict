package pack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// LockFile is the repo layer's pin file: one entry per vendored pack.
const LockFile = ".verdict/verdict.lock"

// LockSchemaVersion is the only accepted verdict.lock format.
const LockSchemaVersion = "1"

// Lock refusal codes.
const (
	CodeLockMissing = "pack-lock-missing"
	CodeLockDrift   = "pack-lock-drift"
)

const sigLen = 64

// LockEntry pins one vendored pack: the manifest identity, where it came from,
// the canonical tree hash, and an integrity digest over those four fields.
type LockEntry struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Version string `json:"version"`
	Hash    string `json:"hash"`
	Sig     string `json:"sig"`
}

// Lock is the published verdict.lock document: strict JSON, entries sorted by
// name, one row per pack under .verdict/packs/.
type Lock struct {
	SchemaVersion string      `const:"1"    json:"schema_version"`
	Packs         []LockEntry `json:"packs"`
}

// LockSig digests one entry's identity fields. The encoding is
// "key=value" lines in name, source, version, hash order, so a hand edit to
// any field breaks the digest while the tree hash stays recomputable.
func LockSig(entry *LockEntry) string {
	h := sha256.New()
	fmt.Fprintf(h, "name=%s\nsource=%s\nversion=%s\nhash=%s\n",
		entry.Name, entry.Source, entry.Version, entry.Hash)
	return hex.EncodeToString(h.Sum(nil))
}

// CheckLock refuses a repo pack tree that does not match verdict.lock, or a
// lock entry whose pack is gone or edited. It reads both sides from the
// trusted-base controls snapshot, so all three verbs share one code path.
func CheckLock(snap *controls.Snapshot) *core.RefusalError {
	if snap == nil {
		return nil
	}
	names := snap.PackNames()
	raw, ok := snap.ReadFile(LockFile)
	if !ok {
		return checkUnlocked(snap, names)
	}
	lock, ref := parseLock(raw)
	if ref != nil {
		return ref
	}
	if ref := checkLockEntries(snap, lock); ref != nil {
		return ref
	}
	return checkLockedPacks(snap, lock, names)
}

// checkUnlocked refuses a repo layer with no lock file.
func checkUnlocked(snap *controls.Snapshot, names []string) *core.RefusalError {
	if len(names) == 0 {
		return nil
	}
	missing, ref := manifestNames(snap, names)
	if ref != nil {
		return ref
	}
	return lockMissing(missing)
}

// checkLockEntries verifies every lock row's directory, tree hash, and
// signature against the snapshot.
func checkLockEntries(snap *controls.Snapshot, lock *Lock) *core.RefusalError {
	for i := range lock.Packs {
		if ref := checkLockEntry(snap, &lock.Packs[i]); ref != nil {
			return ref
		}
	}
	return nil
}

// checkLockEntry verifies one row: its pack still exists, its tree hash
// matches, and its sig covers the recorded fields.
func checkLockEntry(snap *controls.Snapshot, entry *LockEntry) *core.RefusalError {
	origin, ref := lockedPack(snap, entry.Name)
	if ref != nil {
		return ref
	}
	if origin == nil {
		return lockDrift(entry.Name, "tree", entry.Hash, "no vendored pack")
	}
	if origin.Hash != entry.Hash {
		return lockDrift(entry.Name, "tree", entry.Hash, origin.Hash)
	}
	if want := LockSig(entry); entry.Sig != want {
		return lockDrift(entry.Name, "sig", entry.Sig, want)
	}
	return nil
}

// checkLockedPacks refuses a vendored pack no lock row names.
func checkLockedPacks(snap *controls.Snapshot, lock *Lock, names []string) *core.RefusalError {
	locked := map[string]bool{}
	for i := range lock.Packs {
		locked[lock.Packs[i].Name] = true
	}
	for _, name := range names {
		p, ref := LoadAt(snap.Dir, "packs/"+name)
		if ref != nil {
			return ref
		}
		if p != nil && !locked[p.Name] {
			return lockMissing([]string{p.Name})
		}
	}
	return nil
}

// manifestNames loads every vendored pack and returns its manifest identity,
// so a refusal names the pack rather than its directory slug.
func manifestNames(snap *controls.Snapshot, names []string) ([]string, *core.RefusalError) {
	out := make([]string, 0, len(names))
	for _, name := range names {
		p, ref := LoadAt(snap.Dir, "packs/"+name)
		if ref != nil {
			return nil, ref
		}
		if p != nil {
			out = append(out, p.Name)
		}
	}
	return out, nil
}

// lockedPack loads the vendored pack named by its manifest identity, or nil
// when the slug directory is absent.
func lockedPack(snap *controls.Snapshot, name string) (*Pack, *core.RefusalError) {
	p, ref := LoadAt(snap.Dir, "packs/"+packSlug(name))
	if ref != nil {
		return nil, ref
	}
	if p == nil || p.Name != name {
		return nil, nil
	}
	return p, nil
}

// lockDrift builds the drift refusal, naming the pack and both hashes.
func lockDrift(name, field, want, got string) *core.RefusalError {
	return &core.RefusalError{Code: CodeLockDrift, Message: fmt.Sprintf(
		"repo pack %s drift: %s records %s %s, found %s — re-add the pack or restore it",
		name, LockFile, field, want, got)}
}

// lockMissing builds the missing-entry refusal, naming every unlisted pack.
func lockMissing(names []string) *core.RefusalError {
	return &core.RefusalError{Code: CodeLockMissing, Message: fmt.Sprintf(
		"repo pack %s has no %s entry — run 'verdict pack add <dir>' to vendor and lock it",
		strings.Join(names, ", "), LockFile)}
}

// parseLock decodes verdict.lock strictly: exact lowercase keys, no unknown
// fields, a sorted unique pack list, and the published field formats.
func parseLock(raw []byte) (*Lock, *core.RefusalError) {
	if ref := checkLockKeys(raw); ref != nil {
		return nil, ref
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var lock Lock
	if err := dec.Decode(&lock); err != nil {
		return nil, badLock(fmt.Sprintf("%s is not a lock file: %v", LockFile, err))
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, badLock(LockFile + " carries trailing data after the lock object")
	}
	if ref := validateLock(&lock); ref != nil {
		return nil, ref
	}
	return &lock, nil
}

// checkLockKeys enforces the exact lowercase spellings encoding/json would
// otherwise match case-insensitively.
func checkLockKeys(raw []byte) *core.RefusalError {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		//nolint:nilerr // the strict decoder owns the syntax refusal
		return nil
	}
	if ref := checkExactKeys(top, []string{"schema_version", "packs"}, LockFile); ref != nil {
		return ref
	}
	var shape struct {
		Packs []map[string]json.RawMessage `json:"packs"`
	}
	if json.Unmarshal(raw, &shape) != nil {
		//nolint:nilerr // the strict decoder owns the syntax refusal
		return nil
	}
	for _, row := range shape.Packs {
		if ref := checkExactKeys(row, []string{"name", "source", "version", "hash", "sig"}, LockFile); ref != nil {
			return ref
		}
	}
	return nil
}

// validateLock checks the document header and every entry, in order.
func validateLock(lock *Lock) *core.RefusalError {
	if lock.SchemaVersion != LockSchemaVersion {
		return badLock(fmt.Sprintf("%s schema_version is %q, want %q",
			LockFile, lock.SchemaVersion, LockSchemaVersion))
	}
	for i := range lock.Packs {
		entry := &lock.Packs[i]
		if ref := validateLockEntry(entry); ref != nil {
			return ref
		}
		if i > 0 && lock.Packs[i-1].Name >= entry.Name {
			return badLock(fmt.Sprintf("%s pack entries are not sorted by unique name at %q", LockFile, entry.Name))
		}
	}
	return nil
}

// validateLockEntry checks one row's name, source, version, hash, and sig.
func validateLockEntry(entry *LockEntry) *core.RefusalError {
	if !nameRE.MatchString(entry.Name) {
		return badLock(fmt.Sprintf("%s pack name %q is not <namespace>/<slug>", LockFile, entry.Name))
	}
	if !validLockSource(entry.Source) {
		return badLock(fmt.Sprintf("%s pack %q source %q is not dir:<path> or git:<url>@<sha>",
			LockFile, entry.Name, redactURL(entry.Source)))
	}
	if !versionRE.MatchString(entry.Version) {
		return badLock(fmt.Sprintf("%s pack %q version %q is not semver", LockFile, entry.Name, entry.Version))
	}
	if !hex64(entry.Hash) {
		return badLock(fmt.Sprintf("%s pack %q hash %q is not a sha256", LockFile, entry.Name, entry.Hash))
	}
	if !hex64(entry.Sig) {
		return badLock(fmt.Sprintf("%s pack %q sig %q is not a sha256", LockFile, entry.Name, entry.Sig))
	}
	return nil
}

// validLockSource accepts the two published provenance spellings.
func validLockSource(source string) bool {
	if dir, ok := strings.CutPrefix(source, sourceDirPrefix); ok {
		return dir != ""
	}
	rest, ok := strings.CutPrefix(source, sourceGitPrefix)
	if !ok {
		return false
	}
	at := strings.LastIndex(rest, "@")
	return at > 0 && hex40(rest[at+1:]) && !userinfoPassword(rest[:at])
}

// hex64 reports whether s is a lowercase 64-hex digest.
func hex64(s string) bool {
	if len(s) != sigLen {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && s == strings.ToLower(s)
}

// hex40 reports whether s is a lowercase 40-hex commit.
func hex40(s string) bool {
	if len(s) != shaHexLen {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && s == strings.ToLower(s)
}

// badLock builds the malformed-lock refusal.
func badLock(reason string) *core.RefusalError {
	return &core.RefusalError{Code: CodeBadPack, Message: reason}
}

// loadLock reads the worktree's verdict.lock. A missing file reads nil.
func loadLock(root string) (*Lock, *core.RefusalError) {
	path := filepath.Join(root, filepath.FromSlash(LockFile))
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, badLock(fmt.Sprintf("inspect %s: %v", LockFile, err))
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, unsafePath(LockFile)
	}
	if info.Size() > MaxFileBytes {
		return nil, tooLarge(fmt.Sprintf("%s is %d bytes, over the %d-byte cap", LockFile, info.Size(), MaxFileBytes))
	}
	raw, err := jq.CappedFile(path, MaxFileBytes)
	if err != nil {
		return nil, badLock(fmt.Sprintf("read %s: %v", LockFile, err))
	}
	return parseLock(raw)
}

// writeLock writes the worktree's verdict.lock atomically with entries sorted
// by name.
func writeLock(root string, lock *Lock) *core.RefusalError {
	slices.SortFunc(lock.Packs, func(a, b LockEntry) int { return strings.Compare(a.Name, b.Name) })
	raw, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return badLock(fmt.Sprintf("encode %s: %v", LockFile, err))
	}
	path := filepath.Join(root, filepath.FromSlash(LockFile))
	if err := os.MkdirAll(filepath.Dir(path), packDirPerm); err != nil {
		return badLock(fmt.Sprintf("create %s: %v", filepath.Dir(LockFile), err))
	}
	if err := atomic.WriteFile(path, append(raw, '\n')); err != nil {
		return badLock(fmt.Sprintf("write %s: %v", LockFile, err))
	}
	return nil
}

// upsertLock returns the lock with entry added or replacing its same-name row.
func upsertLock(lock *Lock, entry *LockEntry) *Lock {
	if lock == nil {
		lock = &Lock{SchemaVersion: LockSchemaVersion, Packs: []LockEntry{}}
	}
	replaced := false
	for i := range lock.Packs {
		if lock.Packs[i].Name == entry.Name {
			lock.Packs[i] = *entry
			replaced = true
			break
		}
	}
	if !replaced {
		lock.Packs = append(lock.Packs, *entry)
	}
	slices.SortFunc(lock.Packs, func(a, b LockEntry) int { return strings.Compare(a.Name, b.Name) })
	return lock
}

// lockEntry returns the lock's row for name, or nil.
func lockEntry(lock *Lock, name string) *LockEntry {
	if lock == nil {
		return nil
	}
	for i := range lock.Packs {
		if lock.Packs[i].Name == name {
			return &lock.Packs[i]
		}
	}
	return nil
}
