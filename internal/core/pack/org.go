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
		return &core.RefusalError{Code: CodeLockMissing, Message: core.Msgf(
			"org pack %s@%s has no %s entry — run 'verdict pack sync' to vendor and lock it",
			pin.Repo, pin.Ref, LockFile)}
	}
	return verifyOrgEntry(root, entry, pin)
}

// validatePin re-checks one pin's shape, so a caller that built the struct
// directly cannot smuggle a traversal path or a floating ref past the
// config parser.
func validatePin(pin *OrgPin) *core.RefusalError {
	if strings.TrimSpace(pin.Repo) == "" || strings.HasPrefix(pin.Repo, "-") ||
		strings.ContainsAny(pin.Repo, "?#") || !cleanSource(pin.Repo) {
		return badPack(fmt.Sprintf("org pack repo %q is not a repository spelling", pin.Repo))
	}
	if _, ok := parseGitSource(pin.Repo); !ok && (looksLikeGitSource(pin.Repo) || secretShaped(pin.Repo)) {
		return badPackSource(fmt.Sprintf("org pack repo %q is not an allowlisted git remote", displaySource(pin.Repo)))
	}
	if !orgRefShape(pin.Ref) {
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
func verifyOrgEntry(root string, entry *LockEntry, pin *OrgPin) *core.RefusalError {
	p, ref := LoadDir(filepath.Join(root, filepath.FromSlash(RepoPacksDir), packSlug(entry.Name)))
	if ref != nil {
		return ref
	}
	if p == nil || p.Name != entry.Name {
		return &core.RefusalError{Code: CodeLockDrift, Message: core.Msgf(
			"org pack %s drift: %s records %s %s, found no vendored pack — run 'verdict pack sync'",
			entry.Name, LockFile, "tree", entry.Hash)}
	}
	if p.Hash != entry.Hash {
		return &core.RefusalError{Code: CodeLockDrift, Message: core.Msgf(
			"org pack %s drift: %s records %s %s, found %s — run 'verdict pack sync'",
			entry.Name, LockFile, "tree", entry.Hash, p.Hash)}
	}
	if want := LockSig(entry); entry.Sig != want {
		return &core.RefusalError{Code: CodeLockDrift, Message: core.Msgf(
			"org pack %s drift: %s records %s %s, found %s — run 'verdict pack sync'",
			entry.Name, LockFile, "sig", entry.Sig, want)}
	}
	if !orgPathMatch(entry, pin) {
		return &core.RefusalError{Code: CodeLockMissing, Message: core.Msgf(
			"org pack %s@%s path %q does not match lock — run 'verdict pack sync'",
			pin.Repo, pin.Ref, orgPinPath(pin))}
	}
	if !orgRefMatch(entry, pin) {
		return orgRefRefusal(entry, pin)
	}
	return nil
}

// orgPathMatch reports whether one lock row's recorded org path matches the
// pin. Legacy rows with no org_path read as the repository root.
func orgPathMatch(entry *LockEntry, pin *OrgPin) bool {
	want := orgPinPath(pin)
	got := entry.OrgPath
	if got == "" {
		got = rootSelf
	}
	return want == got
}

// orgRefMatch reports whether one lock row was synced from the pin's exact
// ref. A semver tag may resolve to any locked commit, so the recorded ref is
// the only thing that proves the pin still names the synced content: without
// it, bumping v1.0.0 to v1.1.0 would silently keep verifying the old tree.
func orgRefMatch(entry *LockEntry, pin *OrgPin) bool {
	return entry.OrgRef == pin.Ref
}

// orgRefRefusal names the ref the lock was synced at and the ref the pin now
// reads, so the operator knows the sync is what has to move.
func orgRefRefusal(entry *LockEntry, pin *OrgPin) *core.RefusalError {
	locked := entry.OrgRef
	if locked == "" {
		locked = "none"
	}
	return &core.RefusalError{Code: CodeLockMissing, Message: core.Msgf(
		"org pack %s drift: %s records org_ref %s, the pin reads %s — run 'verdict pack sync' to vendor and lock the new ref",
		entry.Name, LockFile, locked, pin.Ref)}
}

// orgRefShape reports whether one ref is an immutable pin: a 40-hex commit or
// a semver tag, never a branch name.
func orgRefShape(ref string) bool {
	return hex40(ref) || orgTagPattern.MatchString(ref)
}

// filepathSlash renders one path spelling with forward slashes.
func filepathSlash(path string) string {
	return strings.ReplaceAll(path, `\`, pathSep)
}
