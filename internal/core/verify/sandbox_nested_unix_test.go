//go:build unix

package verifycore

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestSandboxGitlinksReadsEveryIndexFormat pins the index reader against
// real indexes git writes: version 2 and version 4 (prefix-compressed
// paths), SHA-256 repositories, and `index.skipHash` (a zeroed trailer).
// Each lists the one gitlink, among ordinary entries that share its prefix.
func TestSandboxGitlinksReadsEveryIndexFormat(t *testing.T) {
	for _, tc := range []struct {
		name     string
		initArgs []string
		setup    []string
	}{
		{name: "v2"},
		{name: "v4", setup: []string{"update-index", "--index-version", "4"}},
		{name: "sha256", initArgs: []string{"--object-format=sha256"}},
		{name: "skipHash", setup: []string{"config", "index.skipHash", "true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index := gitlinkIndex(t, tc.initArgs, tc.setup)
			got, present, reason := sandboxGitlinks(index)
			if !present || reason != "" || !slices.Equal(got, []string{"lib/sub"}) {
				t.Errorf("gitlinks = %q (present %v, reason %q), want [lib/sub]", got, present, reason)
			}
		})
	}
}

// gitlinkIndex builds a repository whose index holds the gitlink `lib/sub`
// among ordinary entries sharing its prefix, and returns the index path.
func gitlinkIndex(t *testing.T, initArgs, setup []string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	gitCommittedRepo(t, repo, initArgs...)
	if setup != nil {
		runGit(t, repo, setup...)
	}
	mustSandboxFS(t, os.MkdirAll(filepath.Join(repo, "lib"), 0o755))
	for _, name := range []string{"lib/a.txt", "lib/b.txt", "libz.txt"} {
		writeGitPointer(t, filepath.Join(repo, filepath.FromSlash(name)), name)
	}
	runGit(t, repo, "add", ".")
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+head+",lib/sub")
	return filepath.Join(repo, ".git", "index")
}

// TestSandboxGitlinksRefusesASplitIndex keeps a split index, whose entries
// live partly in a shared file, from being half-read.
func TestSandboxGitlinksRefusesASplitIndex(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	gitCommittedRepo(t, repo)
	runGit(t, repo, "update-index", "--split-index")
	if got, present, reason := sandboxGitlinks(filepath.Join(repo, ".git", "index")); !present || reason == "" {
		t.Errorf("gitlinks = %q (present %v), want a split-index refusal", got, present)
	}
}

// TestSandboxArgvHoldsANestedRepository pins the Linux shape for a nested
// repository with its own `.git` directory (a submodule checked out before
// git absorbed git directories, or any nested clone): the directories above
// it are self-bound so none can be renamed, then its `.git` is bound
// read-only. Before, only `<root>/.git` was bound, and the command could
// write `libs/sub/.git/hooks` for git to run in that tree.
func TestSandboxArgvHoldsANestedRepository(t *testing.T) {
	root := gitRoot(t)
	gitCommittedRepo(t, filepath.Join(root, "libs", "sub"))
	requireArgvTail(t, root, []string{
		"--bind", root, root,
		"--ro-bind", filepath.Join(root, ".git"), filepath.Join(root, ".git"),
		"--bind", filepath.Join(root, "libs"), filepath.Join(root, "libs"),
		"--bind", filepath.Join(root, "libs", "sub"), filepath.Join(root, "libs", "sub"),
		"--ro-bind", filepath.Join(root, "libs", "sub", ".git"), filepath.Join(root, "libs", "sub", ".git"),
		"--unshare-net",
	})
}

// TestSandboxArgvHoldsANonAbsorbedSubmodule reproduces the reported shape
// with real git: `git submodule add` of a repository already checked out in
// the tree keeps its `.git` directory in place, so the gitlink path holds a
// real git directory that `git status` in the superproject enters.
func TestSandboxArgvHoldsANonAbsorbedSubmodule(t *testing.T) {
	root := filepath.Join(t.TempDir(), "super")
	gitCommittedRepo(t, root)
	gitCommittedRepo(t, filepath.Join(root, "sub"))
	runGit(t, root, "submodule", "add", "-q", "./sub", "sub")
	if info, err := os.Lstat(filepath.Join(root, "sub", ".git")); err != nil || !info.IsDir() {
		t.Fatalf("sub/.git = %v (%v), want the non-absorbed git directory", info, err)
	}
	requireArgvTail(t, root, []string{
		"--bind", root, root,
		"--ro-bind", filepath.Join(root, ".git"), filepath.Join(root, ".git"),
		"--bind", filepath.Join(root, "sub"), filepath.Join(root, "sub"),
		"--ro-bind", filepath.Join(root, "sub", ".git"), filepath.Join(root, "sub", ".git"),
		"--unshare-net",
	})
}

// TestSandboxArgvHoldsAnUnpopulatedSubmodulePath pins the gitlink half: a
// submodule path not checked out yet is an empty directory, and a `.git`
// the command created there would make `git status` in the superproject
// enter it. The directory itself is held read-only; a missing one refuses.
func TestSandboxArgvHoldsAnUnpopulatedSubmodulePath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "super")
	gitCommittedRepo(t, root)
	head := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	runGit(t, root, "update-index", "--add", "--cacheinfo", "160000,"+head+",vendor/lib")
	mustSandboxFS(t, os.MkdirAll(filepath.Join(root, "vendor", "lib"), 0o755))
	requireArgvTail(t, root, []string{
		"--bind", root, root,
		"--ro-bind", filepath.Join(root, ".git"), filepath.Join(root, ".git"),
		"--bind", filepath.Join(root, "vendor"), filepath.Join(root, "vendor"),
		"--ro-bind", filepath.Join(root, "vendor", "lib"), filepath.Join(root, "vendor", "lib"),
		"--unshare-net",
	})
	mustSandboxFS(t, os.Remove(filepath.Join(root, "vendor", "lib")))
	requireGitRefusal(t, root, "the submodule path "+filepath.Join(root, "vendor", "lib")+" is missing")
}

// TestSandboxFollowsANestedGitdirFile pins nested `gitdir:` files on both
// platforms: the git directory one names under an ordinary in-root name
// (`meta-sub`) is pinned on Linux and write-denied on macOS, where only
// `.git`-named paths were denied before.
func TestSandboxFollowsANestedGitdirFile(t *testing.T) {
	root := gitRoot(t)
	mustSandboxFS(t, os.MkdirAll(filepath.Join(root, "meta-sub"), 0o755))
	mustSandboxFS(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	writeGitPointer(t, filepath.Join(root, "sub", ".git"), "gitdir: ../meta-sub\n")
	requireArgvTail(t, root, []string{
		"--bind", root, root,
		"--ro-bind", filepath.Join(root, ".git"), filepath.Join(root, ".git"),
		"--ro-bind", filepath.Join(root, "meta-sub"), filepath.Join(root, "meta-sub"),
		"--bind", filepath.Join(root, "sub"), filepath.Join(root, "sub"),
		"--ro-bind", filepath.Join(root, "sub", ".git"), filepath.Join(root, "sub", ".git"),
		"--unshare-net",
	})
	if dirs := gitDirs(t, root); !slices.Contains(dirs, filepath.Join(root, "meta-sub")) {
		t.Errorf("macOS git dirs = %q, want %s denied", dirs, filepath.Join(root, "meta-sub"))
	}
}

// TestSandboxRefusesASymlinkedNestedGitDir keeps a nested `.git` that is a
// symlink from being trusted on either platform: only its target could be
// protected, and the link could be swapped.
func TestSandboxRefusesASymlinkedNestedGitDir(t *testing.T) {
	root, elsewhere := gitRoot(t), t.TempDir()
	mustSandboxFS(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	mustSandboxFS(t, os.Symlink(elsewhere, filepath.Join(root, "sub", ".git")))
	cause := "the reviewed root's " + filepath.Join(root, "sub", ".git") + " is a symlink"
	requireGitRefusal(t, root, cause)
	requireGitDirsRefusal(t, root, cause)
}

// TestSandboxNestedWalkRefusesPastItsBound fails closed on a tree too large
// to search: past the directory cap the walk refuses rather than leaving the
// rest unsearched.
func TestSandboxNestedWalkRefusesPastItsBound(t *testing.T) {
	root := gitRoot(t)
	for i := range 5 {
		mustSandboxFS(t, os.Mkdir(filepath.Join(root, fmt.Sprintf("d%d", i)), 0o755))
	}
	gitRootID, ref := newSandboxGitRoot(root)
	if ref != nil {
		t.Fatal(ref)
	}
	_, ref = sandboxFindNested(gitRootID, filepath.Join(root, ".git"), 3)
	if want := "sandbox unavailable: the reviewed root " + root + " holds more than 3 directories"; ref == nil ||
		!strings.HasPrefix(ref.Message, want) {
		t.Errorf("ref = %v, want prefix %q", ref, want)
	}
	if _, ref := sandboxFindNested(gitRootID, filepath.Join(root, ".git"), 7); ref != nil {
		t.Errorf("within the bound: ref = %v, want none", ref)
	}
}

// TestSandboxGitScanIsLinearInSubmodules pins the scan's cost: the ancestry
// comparisons grow with the entries walked, not with entries times
// submodule git directories. It counts comparisons instead of timing, so it
// cannot flake; a longest-prefix search over every known git directory
// costs hundreds of thousands here.
func TestSandboxGitScanIsLinearInSubmodules(t *testing.T) {
	root := gitRoot(t)
	dotgit := filepath.Join(root, ".git")
	const modules = 300
	writeSubmoduleGitdirs(t, dotgit, modules)
	gitRootID, ref := newSandboxGitRoot(root)
	if ref != nil {
		t.Fatal(ref)
	}
	scan := newSandboxGitScanner(gitRootID, dotgit, nil)
	if ref := scan.run(); ref != nil {
		t.Fatal(ref)
	}
	if len(scan.gitdirs) != 1 && len(scan.gitdirs) > modules {
		t.Fatalf("git directory stack = %d, want it popped as the walk leaves each", len(scan.gitdirs))
	}
	// About 6 entries per module; allow 4 comparisons each.
	if limit := modules * 6 * 4; scan.checks > limit {
		t.Errorf("ancestry comparisons = %d, want at most %d (linear in entries)", scan.checks, limit)
	}
}

// writeSubmoduleGitdirs creates n minimal submodule git directories under
// dotgit/modules, each with a HEAD, a config, and one ref.
func writeSubmoduleGitdirs(t *testing.T, dotgit string, n int) {
	t.Helper()
	for i := range n {
		dir := filepath.Join(dotgit, "modules", fmt.Sprintf("m%03d", i))
		mustSandboxFS(t, os.MkdirAll(filepath.Join(dir, "refs"), 0o755))
		for _, name := range []string{"HEAD", "config", "refs/x"} {
			writeGitPointer(t, filepath.Join(dir, filepath.FromSlash(name)), "x")
		}
	}
}

// TestSandboxRefusesAHardlinkInANonFanoutObjectsDir pins the fan-out shape:
// only two lowercase hex digits name a loose-object directory, so a
// hard-linked file under `objects/abc/` is a control file like any other.
func TestSandboxRefusesAHardlinkInANonFanoutObjectsDir(t *testing.T) {
	for _, dir := range []string{"abc", "AB", "zz"} {
		t.Run(dir, func(t *testing.T) {
			root := gitRoot(t)
			file := filepath.Join(root, ".git", "objects", dir, "x")
			mustSandboxFS(t, os.MkdirAll(filepath.Dir(file), 0o755))
			writeGitPointer(t, file, "x")
			mustSandboxFS(t, os.Link(file, filepath.Join(root, "alias")))
			requireGitRefusal(t, root, "the git control file "+file+" has another hard link")
		})
	}
}

// TestSandboxAcceptsARecursiveLocalClone pins nested submodule git
// directories with real git: a `clone --local` of a superproject whose
// submodule has its own submodule puts the inner git directory at
// `.git/modules/mid/modules/leaf`, object payload hard-linked, and the scan
// must treat it as a git directory too — while its config stays checked.
func TestSandboxAcceptsARecursiveLocalClone(t *testing.T) {
	base := t.TempDir()
	leaf, mid, top, clone := filepath.Join(base, "leaf"), filepath.Join(base, "mid"), filepath.Join(base, "top"),
		filepath.Join(base, "clone")
	gitCommittedRepo(t, leaf)
	gitCommittedRepo(t, mid)
	runGit(t, mid, "submodule", "add", "-q", leaf, "leaf")
	runGit(t, mid, "commit", "-q", "-m", "leaf")
	gitCommittedRepo(t, top)
	runGit(t, top, "submodule", "add", "-q", mid, "mid")
	runGit(t, top, "commit", "-q", "-m", "mid")
	runGit(t, base, "clone", "-q", "--local", top, clone)
	runGit(t, clone, "submodule", "update", "-q", "--init", "--recursive")
	inner := filepath.Join(clone, ".git", "modules", "mid", "modules", "leaf")
	requireSubmoduleObjectsHardlinked(t, inner)
	requireGitAccepted(t, clone)

	alias := filepath.Join(clone, "cfg")
	mustSandboxFS(t, os.Link(filepath.Join(inner, "config"), alias))
	cause := "the git control file " + filepath.Join(inner, "config") + " has another hard link"
	requireGitRefusal(t, clone, cause)
	requireGitDirsRefusal(t, clone, cause)
}

// requireArgvTail asserts the Linux argv, from the root's writable bind
// through `--unshare-net`, is exactly want.
func requireArgvTail(t *testing.T, root string, want []string) {
	t.Helper()
	argv, ref := bwrapArgv(root, homeInputs(t.TempDir()))
	if ref != nil {
		t.Fatalf("bwrapArgv: %v", ref)
	}
	start := slices.Index(argv, "--bind")
	if start < 0 || len(argv) < start+len(want) || !slices.Equal(argv[start:start+len(want)], want) {
		t.Errorf("argv = %q\nwant %q after the repo bind", argv[max(start, 0):], want)
	}
}
