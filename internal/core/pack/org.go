package pack

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// orgTagPattern matches the accepted semver pin tags, with optional `v`.
var orgTagPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

// VerifyOrgPin proves the base-owned org pin is vendored and hash-locked.
// It performs no fetch and no network access: it reads the worktree's
// `verdict.lock` and the vendored tree only, so a review never drifts onto
// a mutable central ref. A missing lock row, a missing vendored pack, a
// content-hash drift, or a pin/lock mismatch all refuse typed — never a
// silent fallback to the previous pack set.
func VerifyOrgPin(root string, pin *OrgPin) *core.RefusalError {
	if pin == nil {
		return nil
	}
	if ref := validatePin(pin); ref != nil {
		return ref
	}
	lock, ref := loadLock(root)
	if ref != nil {
		return ref
	}
	entry := findOrgEntry(lock, pin)
	if entry == nil {
		return &core.RefusalError{Code: CodeLockMissing, Message: fmt.Sprintf(
			"org pack %s@%s has no %s entry — run 'verdict pack sync' to vendor and lock it",
			pin.Repo, pin.Ref, LockFile)}
	}
	return verifyOrgEntry(root, entry)
}

// validatePin re-checks one pin's shape, so a caller that built the struct
// directly cannot smuggle a traversal path or a floating ref past the
// config parser.
func validatePin(pin *OrgPin) *core.RefusalError {
	if strings.TrimSpace(pin.Repo) == "" || strings.HasPrefix(pin.Repo, "-") ||
		strings.ContainsAny(pin.Repo, "?#") || !cleanSource(pin.Repo) {
		return badPack(fmt.Sprintf("org pack repo %q is not a repository spelling", pin.Repo))
	}
	if !hex40(pin.Ref) && !orgTagPattern.MatchString(pin.Ref) {
		return badPack(fmt.Sprintf(
			"org pack ref %q is not immutable — use a 40-hex commit SHA or a semver tag like v1.2.3, never a branch name",
			pin.Ref))
	}
	if path := orgPinPath(pin); !confinedRoot(path) {
		return badPack(fmt.Sprintf("org pack path %q escapes the central repository", pin.Path))
	}
	return nil
}

// orgPinPath normalizes an empty pin path to the central repository root.
func orgPinPath(pin *OrgPin) string {
	if pin == nil || strings.TrimSpace(pin.Path) == "" {
		return rootSelf
	}
	return pin.Path
}

// findOrgEntry returns the lock row synced from pin's repository, or nil.
// A git pin matches its `git:<url>@<sha>` source; a SHA ref must match the
// recorded commit exactly, while a tag ref accepts any locked 40-hex
// commit — the tag resolved once, at sync time, and the hash-lock is what
// the review trusts.
func findOrgEntry(lock *Lock, pin *OrgPin) *LockEntry {
	if lock == nil {
		return nil
	}
	for i := range lock.Packs {
		entry := &lock.Packs[i]
		if orgSourceMatch(entry.Source, pin) {
			return entry
		}
	}
	return nil
}

// orgSourceMatch reports whether one lock source is the row `pack sync`
// wrote for pin.
func orgSourceMatch(source string, pin *OrgPin) bool {
	if g, ok := parseGitSource(pin.Repo); ok {
		return orgGitSourceMatch(source, g.url, pin.Ref)
	}
	return orgDirSourceMatch(source, pin.Repo)
}

// orgGitSourceMatch matches one git lock source against the pin's URL: the
// recorded commit must be a 40-hex sha, and a SHA ref must match it exactly.
func orgGitSourceMatch(source, url, ref string) bool {
	prefix := sourceGitPrefix + url + refSep
	if !strings.HasPrefix(source, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(source, prefix)
	if !hex40(suffix) {
		return false
	}
	return !hex40(ref) || suffix == ref
}

// orgDirSourceMatch matches one dir lock source against the pin's path.
func orgDirSourceMatch(source, repo string) bool {
	if !strings.HasPrefix(source, sourceDirPrefix) {
		return false
	}
	locked := strings.TrimPrefix(source, sourceDirPrefix)
	return locked == repo || locked == filepathSlash(repo)
}

// verifyOrgEntry proves the vendored tree under one lock row still hashes
// to the locked content: the tree exists, its name matches, its tree hash
// matches, and the row's signature covers its recorded fields.
func verifyOrgEntry(root string, entry *LockEntry) *core.RefusalError {
	p, ref := LoadDir(filepath.Join(root, filepath.FromSlash(RepoPacksDir), packSlug(entry.Name)))
	if ref != nil {
		return ref
	}
	if p == nil || p.Name != entry.Name {
		return &core.RefusalError{Code: CodeLockDrift, Message: fmt.Sprintf(
			"org pack %s drift: %s records %s %s, found no vendored pack — run 'verdict pack sync'",
			entry.Name, LockFile, "tree", entry.Hash)}
	}
	if p.Hash != entry.Hash {
		return &core.RefusalError{Code: CodeLockDrift, Message: fmt.Sprintf(
			"org pack %s drift: %s records %s %s, found %s — run 'verdict pack sync'",
			entry.Name, LockFile, "tree", entry.Hash, p.Hash)}
	}
	if want := LockSig(entry); entry.Sig != want {
		return &core.RefusalError{Code: CodeLockDrift, Message: fmt.Sprintf(
			"org pack %s drift: %s records %s %s, found %s — run 'verdict pack sync'",
			entry.Name, LockFile, "sig", entry.Sig, want)}
	}
	return nil
}

// filepathSlash renders one path spelling with forward slashes.
func filepathSlash(path string) string {
	return strings.ReplaceAll(path, `\`, pathSep)
}
