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
	phys := physicalPath(t, root)
	requireArgvTail(t, root, []string{
		"--bind", root, root,
		"--ro-bind", filepath.Join(root, ".git"), filepath.Join(root, ".git"),
		"--bind", filepath.Join(phys, "libs"), filepath.Join(phys, "libs"),
		"--bind", filepath.Join(phys, "libs", "sub"), filepath.Join(phys, "libs", "sub"),
		"--ro-bind", filepath.Join(phys, "libs", "sub", ".git"), filepath.Join(phys, "libs", "sub", ".git"),
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
	phys := physicalPath(t, root)
	requireArgvTail(t, root, []string{
		"--bind", root, root,
		"--ro-bind", filepath.Join(root, ".git"), filepath.Join(root, ".git"),
		"--bind", filepath.Join(phys, "sub"), filepath.Join(phys, "sub"),
		"--ro-bind", filepath.Join(phys, "sub", ".git"), filepath.Join(phys, "sub", ".git"),
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
	phys := physicalPath(t, root)
	requireArgvTail(t, root, []string{
		"--bind", root, root,
		"--ro-bind", filepath.Join(root, ".git"), filepath.Join(root, ".git"),
		"--bind", filepath.Join(phys, "vendor"), filepath.Join(phys, "vendor"),
		"--ro-bind", filepath.Join(phys, "vendor", "lib"), filepath.Join(phys, "vendor", "lib"),
		"--unshare-net",
	})
	mustSandboxFS(t, os.Remove(filepath.Join(root, "vendor", "lib")))
	requireGitRefusal(t, root, "the submodule path "+filepath.Join(phys, "vendor", "lib")+" is missing")
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
	phys := physicalPath(t, root)
	requireArgvTail(t, root, []string{
		"--bind", root, root,
		"--ro-bind", filepath.Join(root, ".git"), filepath.Join(root, ".git"),
		"--ro-bind", filepath.Join(root, "meta-sub"), filepath.Join(root, "meta-sub"),
		"--bind", filepath.Join(phys, "sub"), filepath.Join(phys, "sub"),
		"--ro-bind", filepath.Join(phys, "sub", ".git"), filepath.Join(phys, "sub", ".git"),
		"--unshare-net",
	})
	if dirs := gitDirs(t, root); !slices.Contains(dirs, filepath.Join(phys, "meta-sub")) {
		t.Errorf("macOS git dirs = %q, want %s denied", dirs, filepath.Join(phys, "meta-sub"))
	}
}

// TestSandboxRefusesASymlinkedNestedGitDir keeps a nested `.git` that is a
// symlink from being trusted on either platform: only its target could be
// protected, and the link could be swapped.
func TestSandboxRefusesASymlinkedNestedGitDir(t *testing.T) {
	root, elsewhere := gitRoot(t), t.TempDir()
	mustSandboxFS(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	mustSandboxFS(t, os.Symlink(elsewhere, filepath.Join(root, "sub", ".git")))
	cause := "the reviewed root's " + filepath.Join(physicalPath(t, root), "sub", ".git") + " is a symlink"
	requireGitRefusal(t, root, cause)
	requireGitDirsRefusal(t, root, cause)
}

// physicalPath is path with every symlink resolved (/var -> /private/var on
// macOS): the nested walk runs on the physical root, so it names nested
// paths that way.
func physicalPath(t *testing.T, path string) string {
	t.Helper()
	phys, err := filepath.EvalSymlinks(path)
	mustSandboxFS(t, err)
	return phys
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
	_, ref = sandboxFindNested(gitRootID, "", 3)
	if want := "sandbox unavailable: the reviewed root " + root + " holds more than 3 directories"; ref == nil ||
		!strings.HasPrefix(ref.Message, want) {
		t.Errorf("ref = %v, want prefix %q", ref, want)
	}
	if _, ref := sandboxFindNested(gitRootID, "", 7); ref != nil {
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
	// Every submodule git directory is pushed once, and popped before the
	// next sibling is pushed: the stack never holds more than the location
	// and one submodule (a stack that never pops would reach 301).
	if scan.pushes != modules || scan.depth != 2 {
		t.Fatalf("pushes = %d, deepest stack = %d; want %d and 2", scan.pushes, scan.depth, modules)
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

// TestSandboxNestedWalkFollowsASymlinkedRoot pins the walk to the physical
// root. Reviewed through a symlink (`current -> checkouts/app`), WalkDir saw
// the root as a link and stopped there, so no nested `.git` was found: bwrap
// left `libs/sub/.git` and a submodule's `.git` pointer writable, and macOS
// missed the git directory a nested `gitdir:` file names.
func TestSandboxNestedWalkFollowsASymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	app := filepath.Join(base, "checkouts", "app")
	gitCommittedRepo(t, app)
	gitCommittedRepo(t, filepath.Join(app, "libs", "sub"))
	mustSandboxFS(t, os.MkdirAll(filepath.Join(app, "meta-sub"), 0o755))
	mustSandboxFS(t, os.MkdirAll(filepath.Join(app, "pointed"), 0o755))
	writeGitPointer(t, filepath.Join(app, "pointed", ".git"), "gitdir: ../meta-sub\n")
	link := filepath.Join(base, "current")
	mustSandboxFS(t, os.Symlink(app, link))
	phys := physicalPath(t, app)

	argv, ref := bwrapArgv(link, homeInputs(t.TempDir()))
	if ref != nil {
		t.Fatalf("bwrapArgv: %v", ref)
	}
	for _, held := range []string{filepath.Join(phys, "libs", "sub", ".git"), filepath.Join(phys, "pointed", ".git")} {
		if !slices.Contains(argv, held) {
			t.Errorf("argv = %q, want %s held read-only", argv, held)
		}
	}
	if dirs := gitDirs(t, link); !slices.Contains(dirs, filepath.Join(phys, "meta-sub")) {
		t.Errorf("macOS git dirs = %q, want %s denied", dirs, filepath.Join(phys, "meta-sub"))
	}
}

// TestSandboxGitDirsReadsNoIndex keeps the index out of the macOS path:
// Seatbelt's `.git` component rule already denies a `.git` at any
// submodule path, so a split index or a missing submodule path, which bwrap
// must refuse, protects nothing there and must not refuse.
func TestSandboxGitDirsReadsNoIndex(t *testing.T) {
	t.Run("split index", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "repo")
		gitCommittedRepo(t, root)
		runGit(t, root, "update-index", "--split-index")
		requireGitRefusal(t, root, "could not read the git index")
		if _, ref := sandboxGitDirs(root); ref != nil {
			t.Errorf("macOS ref = %v, want none", ref)
		}
	})
	t.Run("missing submodule path", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "repo")
		gitCommittedRepo(t, root)
		head := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
		runGit(t, root, "update-index", "--add", "--cacheinfo", "160000,"+head+",vendor/lib")
		requireGitRefusal(t, root, "the submodule path")
		if _, ref := sandboxGitDirs(root); ref != nil {
			t.Errorf("macOS ref = %v, want none", ref)
		}
	})
}

// TestSandboxGitDirsScansARootWithoutGit runs the macOS nested scan even
// when the root itself has no `.git`: a nested `gitdir:` file can still
// name an ordinary in-root directory, which used to stay writable because
// the scan returned before looking.
func TestSandboxGitDirsScansARootWithoutGit(t *testing.T) {
	root := t.TempDir()
	mustSandboxFS(t, os.MkdirAll(filepath.Join(root, "meta"), 0o755))
	mustSandboxFS(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	writeGitPointer(t, filepath.Join(root, "sub", ".git"), "gitdir: ../meta\n")
	if dirs := gitDirs(t, root); !slices.Contains(dirs, filepath.Join(physicalPath(t, root), "meta")) {
		t.Errorf("macOS git dirs = %q, want %s denied", dirs, filepath.Join(physicalPath(t, root), "meta"))
	}
}
