//go:build unix

package verifycore

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// TestSandboxArgvRefusesASymlinkedGitDir keeps the Linux shape from binding
// only a symlinked `.git`'s target while the link itself stays replaceable in
// the writable root.
func TestSandboxArgvRefusesASymlinkedGitDir(t *testing.T) {
	root, target := t.TempDir(), t.TempDir()
	mustSandboxFS(t, os.Symlink(target, filepath.Join(root, ".git")))
	argv, ref := bwrapArgv(root, homeInputs(t.TempDir()))
	if ref == nil || ref.Code != codeSandboxUnavailable {
		t.Fatalf("argv=%v ref=%v, want sandbox-unavailable", argv, ref)
	}
	want := "sandbox unavailable: the reviewed root's " + filepath.Join(root, ".git") + " is a symlink, and only " +
		"its target can be protected, so the link or the pointer it reaches could send git to a git directory " +
		"that runs later — replace the link with the directory or a `gitdir:` file, or verify a fresh clone"
	if ref.Message != want {
		t.Errorf("message = %q, want %q", ref.Message, want)
	}
}

// TestSandboxGitDirsRefusesADotGitLinkedToAPointerFile pins the macOS
// refusal for a `.git` that is a symlink to a `gitdir:` file elsewhere in
// the root: git reads the pointer through the link, but the walk does not,
// so the git directory it names (`evil`, an ordinary in-root name) would get
// no write denial. It used to refuse only by accident, when reading
// `.git/commondir` through the link failed with ENOTDIR; it now refuses by
// name, like Linux.
func TestSandboxGitDirsRefusesADotGitLinkedToAPointerFile(t *testing.T) {
	root := t.TempDir()
	mustSandboxFS(t, os.Mkdir(filepath.Join(root, "evil"), 0o755))
	writeGitPointer(t, filepath.Join(root, "pointer"), "gitdir: evil\n")
	mustSandboxFS(t, os.Symlink("pointer", filepath.Join(root, ".git")))
	requireGitDirsRefusal(t, root, "the reviewed root's "+filepath.Join(root, ".git")+" is a symlink")
}

// TestSandboxRefusesASymlinkedCommondir pins the shared refusal for a
// commondir that is a symlink, on both platforms: the pointer bytes come
// from the file it names, here a regular file in the writable root, so the
// command could rewrite it after setup and send git to a common directory
// nobody protected. Only the link sits in the protected git directory.
func TestSandboxRefusesASymlinkedCommondir(t *testing.T) {
	root := t.TempDir()
	mustSandboxFS(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	mustSandboxFS(t, os.Mkdir(filepath.Join(root, "shared"), 0o755))
	writeGitPointer(t, filepath.Join(root, "pointer"), "../shared\n")
	mustSandboxFS(t, os.Symlink(filepath.Join(root, "pointer"), filepath.Join(root, ".git", "commondir")))
	cause := "the git pointer " + filepath.Join(root, ".git", "commondir") + " is a symlink"
	requireGitRefusal(t, root, cause)
	requireGitDirsRefusal(t, root, cause)
}

// TestSandboxRefusesAnInRootLinkBehindAnOutsideRootLink pins the hop-by-hop
// walk: `.git` names a git directory through an outside-root symlink whose
// target is an in-root symlink. Resolving the outside link in one step
// never recorded the in-root one, so Linux did not refuse it and macOS did
// not deny it, and the command could repoint it at a directory of its own.
// Linux now refuses it; macOS write-denies it by its resolved spelling.
func TestSandboxRefusesAnInRootLinkBehindAnOutsideRootLink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	mustSandboxFS(t, os.MkdirAll(filepath.Join(root, "real", "gitdir"), 0o755))
	mustSandboxFS(t, os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "inlink")))
	mustSandboxFS(t, os.Symlink(filepath.Join(root, "inlink"), filepath.Join(outside, "hop")))
	writeGitPointer(t, filepath.Join(root, ".git"), "gitdir: "+filepath.Join(outside, "hop", "gitdir")+"\n")
	// The walk resolves the outside link to its target's physical spelling,
	// so on a host whose temp directory sits behind a link (/var on macOS)
	// the in-root link is named under the resolved root.
	realRoot, err := filepath.EvalSymlinks(root)
	mustSandboxFS(t, err)
	inlink := filepath.Join(realRoot, "inlink")
	requireGitRefusal(t, root, "the git pointer "+filepath.Join(root, ".git")+" passes through the symlink "+
		inlink+" inside the reviewed root")
	if dirs := gitDirs(t, root); !slices.Contains(dirs, inlink) {
		t.Errorf("macOS git dirs = %q, want the in-root link %q denied", dirs, inlink)
	}
}

// TestSandboxExecRemovesThePrivateDirWhenTheProfileRefuses keeps a profile
// refusal from leaking the private directory MkdirTemp already created: the
// deferred cleanup must remove the acquired path, not the named result the
// refusal return has cleared.
func TestSandboxExecRemovesThePrivateDirWhenTheProfileRefuses(t *testing.T) {
	root, tmpBase := t.TempDir(), t.TempDir()
	writeGitPointer(t, filepath.Join(root, ".git"), "gitdir: .\n")
	if _, ref := sandboxExecMechanism(root, homeInputs(t.TempDir()), tmpBase); ref == nil {
		t.Fatal("sandboxExecMechanism accepted a git directory that is the root")
	}
	entries, err := os.ReadDir(tmpBase)
	mustSandboxFS(t, err)
	if len(entries) != 0 {
		t.Errorf("tmpBase holds %v after the refusal, want the private directory removed", entries)
	}
}

// TestSandboxArgvRefusesAGitDirReachedThroughAnInRootSymlink keeps a
// `gitdir:` path that crosses a symlink inside the writable root from being
// bound by its target alone: the link could be replaced, and git follows it.
func TestSandboxArgvRefusesAGitDirReachedThroughAnInRootSymlink(t *testing.T) {
	root := t.TempDir()
	mustSandboxFS(t, os.MkdirAll(filepath.Join(root, "real", "gitdir"), 0o755))
	mustSandboxFS(t, os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")))
	mustSandboxFS(t, os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: link/gitdir\n"), 0o600))
	requireGitRefusal(t, root, "the git pointer "+filepath.Join(root, ".git")+
		" passes through the symlink "+filepath.Join(root, "link")+" inside the reviewed root")
}

// TestSandboxArgvRefusesAGitDirLinkedOutsideTheRoot pins the escape where the
// in-root link points outside the root: the target needs no bind (it is
// read-only already), but the link itself sits in the writable root, so the
// command could swap it for a directory with hooks. It refuses.
func TestSandboxArgvRefusesAGitDirLinkedOutsideTheRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	mustSandboxFS(t, os.Symlink(outside, filepath.Join(root, "link")))
	mustSandboxFS(t, os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: link\n"), 0o600))
	requireGitRefusal(t, root, "the git pointer "+filepath.Join(root, ".git")+
		" passes through the symlink "+filepath.Join(root, "link")+" inside the reviewed root")
}

// TestSandboxArgvRefusesACommondirLinkedOutsideTheRoot pins the same escape
// one pointer further: a commondir that reaches outside the root through an
// in-root symlink.
func TestSandboxArgvRefusesACommondirLinkedOutsideTheRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	mustSandboxFS(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	mustSandboxFS(t, os.Symlink(outside, filepath.Join(root, "shared")))
	mustSandboxFS(t, os.WriteFile(filepath.Join(root, ".git", "commondir"), []byte("../shared\n"), 0o600))
	requireGitRefusal(t, root, "the git pointer "+filepath.Join(root, ".git", "commondir")+
		" passes through the symlink "+filepath.Join(root, "shared")+" inside the reviewed root")
}

// TestSandboxArgvNamesTheRealInspectError keeps a `.git` that cannot be
// inspected from being reported as missing: a root that is a file fails
// Lstat with ENOTDIR, and the refusal names that error.
func TestSandboxArgvNamesTheRealInspectError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "file-root")
	mustSandboxFS(t, os.WriteFile(root, []byte("x"), 0o600))
	_, ref := bwrapArgv(root, homeInputs(t.TempDir()))
	if ref == nil || ref.Code != codeSandboxUnavailable {
		t.Fatalf("ref = %v, want sandbox-unavailable", ref)
	}
	want := "sandbox unavailable: could not inspect " + filepath.Join(root, ".git") + " (lstat " +
		filepath.Join(root, ".git") + ": not a directory), so it cannot be protected"
	if ref.Message != want {
		t.Errorf("message = %q, want %q", ref.Message, want)
	}
}

// TestSandboxRefusesARootThatResolvesToTheFilesystemRoot keeps a reviewed root
// that is a symlink to `/` from making the whole host writable on either
// platform.
func TestSandboxRefusesARootThatResolvesToTheFilesystemRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "slash")
	mustSandboxFS(t, os.Symlink("/", root))
	if _, ref := bwrapArgv(root, homeInputs(t.TempDir())); ref == nil ||
		ref.Message != "sandbox unavailable: the reviewed root "+root+" is the filesystem root /; refusing to bind "+
			"the whole host writable" {
		t.Errorf("linux ref = %v, want the filesystem-root refusal", ref)
	}
	if _, ref := sandboxExecMechanism(root, homeInputs(t.TempDir()), t.TempDir()); ref == nil ||
		ref.Message != "sandbox unavailable: the reviewed root "+root+" is the filesystem root /; refusing to allow "+
			"the whole host writable" {
		t.Errorf("macOS ref = %v, want the filesystem-root refusal", ref)
	}
}

// TestSandboxGitPointerRefusesAFIFOWithoutBlocking keeps a named pipe at
// commondir from hanging setup: it is refused as not a regular file, on both
// platforms, instead of waiting for a writer.
func TestSandboxGitPointerRefusesAFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	mustSandboxFS(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	mustSandboxFS(t, syscall.Mkfifo(filepath.Join(root, ".git", "commondir"), 0o600))
	requireGitRefusal(t, root, "could not read the git pointer "+filepath.Join(root, ".git", "commondir"))
	if _, ref := sandboxGitDirs(root); ref == nil || !strings.Contains(ref.Message, "could not read the git pointer") {
		t.Errorf("macOS ref = %v, want the same refusal", ref)
	}
}

// TestSandboxRefusesAGitDirInsideTheRootUnderAnotherCase pins identity, not
// spelling, as what decides "inside the root". On a case-insensitive volume
// `../proberoot` names the root `ProbeRoot`: the lexical check called every
// such path outside, so Linux pinned only `.git` and macOS denied only
// `.git`, and the command wrote `meta/git/hooks` and `meta/git/config`
// through the root's own spelling. Each shape now refuses on both platforms.
func TestSandboxRefusesAGitDirInsideTheRootUnderAnotherCase(t *testing.T) {
	t.Run("gitdir through a case alias of the root", func(t *testing.T) {
		parent, root := caseAliasRoot(t)
		mustSandboxFS(t, os.MkdirAll(filepath.Join(root, "meta", "git"), 0o755))
		writeGitPointer(t, filepath.Join(root, ".git"), "gitdir: ../proberoot/meta/git\n")
		cause := "the git pointer " + filepath.Join(root, ".git") + " reaches " +
			filepath.Join(parent, "proberoot", "meta") + ", which is inside the reviewed root " + root +
			" under another spelling"
		requireGitRefusal(t, root, cause)
		requireGitDirsRefusal(t, root, cause)
	})
	t.Run("gitdir is a case alias of the root", func(t *testing.T) {
		parent, root := caseAliasRoot(t)
		writeGitPointer(t, filepath.Join(root, ".git"), "gitdir: ../proberoot\n")
		cause := "the git location " + filepath.Join(parent, "proberoot") + " is the reviewed root or holds it"
		requireGitRefusal(t, root, cause)
		requireGitDirsRefusal(t, root, cause)
	})
	t.Run("commondir is a case alias of the root", func(t *testing.T) {
		parent, root := caseAliasRoot(t)
		mustSandboxFS(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
		writeGitPointer(t, filepath.Join(root, ".git", "commondir"), "../../proberoot\n")
		cause := "the git location " + filepath.Join(parent, "proberoot") + " is the reviewed root or holds it"
		requireGitRefusal(t, root, cause)
		requireGitDirsRefusal(t, root, cause)
	})
	t.Run("root inside its git directory through a case alias", func(t *testing.T) {
		parent, gitdir := caseAliasRoot(t)
		root := filepath.Join(gitdir, "hooks")
		mustSandboxFS(t, os.Mkdir(root, 0o755))
		writeGitPointer(t, filepath.Join(root, ".git"), "gitdir: ../../proberoot\n")
		cause := "the git location " + filepath.Join(parent, "proberoot") + " is the reviewed root or holds it"
		requireGitRefusal(t, root, cause)
		requireGitDirsRefusal(t, root, cause)
	})
}

// caseAliasRoot creates `<parent>/ProbeRoot` and skips unless the volume
// resolves `<parent>/proberoot` to it.
func caseAliasRoot(t *testing.T) (string, string) {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "ProbeRoot")
	mustSandboxFS(t, os.Mkdir(root, 0o755))
	want, err := os.Stat(root)
	mustSandboxFS(t, err)
	if got, err := os.Stat(filepath.Join(parent, "proberoot")); err != nil || !os.SameFile(got, want) {
		t.Skip("the temp volume is case-sensitive, so a case variant names a different file")
	}
	return parent, root
}

// TestSandboxRefusesAHardlinkedGitControlFile pins the setup check for a
// second name on a protected git file: Seatbelt denies writes by path and
// bwrap by mount, so `cfg` hard-linked to `.git/config` would let the command
// append `core.fsmonitor`, and a link to the `.git` pointer would let it
// repoint git. A git directory outside the root is checked too: its alias
// would sit in the writable root all the same.
func TestSandboxRefusesAHardlinkedGitControlFile(t *testing.T) {
	t.Run("config", func(t *testing.T) {
		root := gitRoot(t)
		config := filepath.Join(root, ".git", "config")
		writeGitPointer(t, config, "[core]\n")
		mustSandboxFS(t, os.Link(config, filepath.Join(root, "cfg")))
		cause := "the git control file " + config + " has another hard link"
		requireGitRefusal(t, root, cause)
		requireGitDirsRefusal(t, root, cause)
	})
	t.Run("the .git pointer", func(t *testing.T) {
		root, outside := t.TempDir(), t.TempDir()
		writeGitPointer(t, filepath.Join(root, ".git"), "gitdir: "+outside+"\n")
		mustSandboxFS(t, os.Link(filepath.Join(root, ".git"), filepath.Join(root, "pointer")))
		cause := "the git control file " + filepath.Join(root, ".git") + " has another hard link"
		requireGitRefusal(t, root, cause)
		requireGitDirsRefusal(t, root, cause)
	})
	t.Run("a hook in a common directory outside the root", func(t *testing.T) {
		root, common := gitRoot(t), t.TempDir()
		mustSandboxFS(t, os.Mkdir(filepath.Join(common, "hooks"), 0o755))
		hook := filepath.Join(common, "hooks", "pre-commit")
		writeGitPointer(t, hook, "#!/bin/sh\n")
		mustSandboxFS(t, os.Link(hook, filepath.Join(root, "hook.sh")))
		writeGitPointer(t, filepath.Join(root, ".git", "commondir"), common+"\n")
		// The walk reaches the common directory by its physical spelling.
		realCommon, err := filepath.EvalSymlinks(common)
		mustSandboxFS(t, err)
		cause := "the git control file " + filepath.Join(realCommon, "hooks", "pre-commit") + " has another hard link"
		requireGitRefusal(t, root, cause)
		requireGitDirsRefusal(t, root, cause)
	})
}

// TestSandboxAcceptsHardlinkedGitObjects keeps `git clone --local`
// checkouts working: their object files are hard-linked by design, and git
// never executes or configures from them.
func TestSandboxAcceptsHardlinkedGitObjects(t *testing.T) {
	root, elsewhere := gitRoot(t), t.TempDir()
	objects := filepath.Join(root, ".git", "objects", "ab")
	mustSandboxFS(t, os.MkdirAll(objects, 0o755))
	writeGitPointer(t, filepath.Join(elsewhere, "obj"), "x")
	mustSandboxFS(t, os.Link(filepath.Join(elsewhere, "obj"), filepath.Join(objects, "cdef")))
	if _, ref := bwrapArgv(root, homeInputs(t.TempDir())); ref != nil {
		t.Errorf("linux ref = %v, want hard-linked objects accepted", ref)
	}
	if dirs := gitDirs(t, root); !slices.Equal(dirs, []string{filepath.Join(root, ".git")}) {
		t.Errorf("macOS git dirs = %v, want only .git", dirs)
	}
}

// TestSandboxGitScanContinuesPastAVanishedEntry pins the walk-error policy:
// an entry that vanishes mid-walk has nothing left to alias, so the scan
// goes on to the control files after it. It used to stop there, and the
// caller read the error as "location absent", so a hard-linked `config`
// later in the walk went unchecked.
func TestSandboxGitScanContinuesPastAVanishedEntry(t *testing.T) {
	root := gitRoot(t)
	dotgit := filepath.Join(root, ".git")
	writeGitPointer(t, filepath.Join(dotgit, "a-vanishes"), "x")
	writeGitPointer(t, filepath.Join(dotgit, "config"), "[core]\n")
	mustSandboxFS(t, os.Link(filepath.Join(dotgit, "config"), filepath.Join(root, "cfg")))
	gitRootID, ref := newSandboxGitRoot(root)
	if ref != nil {
		t.Fatal(ref)
	}
	scan := sandboxGitScanner{root: gitRootID, location: dotgit, info: func(d fs.DirEntry) (fs.FileInfo, error) {
		if d.Name() == "a-vanishes" {
			return nil, fs.ErrNotExist
		}
		return d.Info()
	}}
	want := "sandbox unavailable: the git control file " + filepath.Join(dotgit, "config") + " has another hard link"
	if ref := scan.run(); ref == nil || !strings.HasPrefix(ref.Message, want) {
		t.Errorf("ref = %v, want prefix %q", ref, want)
	}
}

// TestSandboxGitScanRefusesAnUnreadableDirectory keeps every walk error
// other than a vanished entry a refusal: a directory in the git location
// that cannot be listed could hide a hard-linked control file.
func TestSandboxGitScanRefusesAnUnreadableDirectory(t *testing.T) {
	root := gitRoot(t)
	locked := filepath.Join(root, ".git", "info")
	mustSandboxFS(t, os.Mkdir(locked, 0o755))
	mustSandboxFS(t, os.Chmod(locked, 0))
	t.Cleanup(func() {
		if err := os.Chmod(locked, 0o755); err != nil {
			t.Errorf("restore %s: %v", locked, err)
		}
	})
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("running with privileges that read a mode-0 directory")
	}
	cause := "could not inspect " + locked
	requireGitRefusal(t, root, cause)
	requireGitDirsRefusal(t, root, cause)
}

// TestSandboxRefusesHardlinksInNestedObjectsDirs limits the clone --local
// exemption to a git directory's own top-level `objects/`: a directory that
// is merely named `objects` deeper in (under hooks, info, or a worktree's
// git directory) holds control files like any other.
func TestSandboxRefusesHardlinksInNestedObjectsDirs(t *testing.T) {
	for _, rel := range []string{"hooks/objects/pre-commit", "info/objects/exclude", "worktrees/wt/objects/gitdir"} {
		t.Run(rel, func(t *testing.T) {
			root := gitRoot(t)
			file := filepath.Join(root, ".git", filepath.FromSlash(rel))
			mustSandboxFS(t, os.MkdirAll(filepath.Dir(file), 0o755))
			writeGitPointer(t, file, "x")
			mustSandboxFS(t, os.Link(file, filepath.Join(root, "alias")))
			cause := "the git control file " + file + " has another hard link"
			requireGitRefusal(t, root, cause)
			requireGitDirsRefusal(t, root, cause)
		})
	}
}

// TestSandboxRefusesAControlFileSymlinkedIntoTheRoot pins the symlink half
// of the location scan: `.git/config -> ../cfg` made git read config the
// command writes (`core.fsmonitor` then runs on the operator's next git
// command), and the same for anything under `info/`, or a link whose
// target does not exist yet (the command could create it). The refusal
// names the link and its target.
func TestSandboxRefusesAControlFileSymlinkedIntoTheRoot(t *testing.T) {
	for _, tc := range []struct{ link, target string }{
		{"config", "../cfg"},
		{"info/exclude", "../../excludes"},
		{"config.worktree", "../not-yet-there"},
	} {
		t.Run(tc.link, func(t *testing.T) {
			root := gitRoot(t)
			writeGitPointer(t, filepath.Join(root, "cfg"), "[core]\n")
			writeGitPointer(t, filepath.Join(root, "excludes"), "x\n")
			link := filepath.Join(root, ".git", filepath.FromSlash(tc.link))
			mustSandboxFS(t, os.MkdirAll(filepath.Dir(link), 0o755))
			mustSandboxFS(t, os.Symlink(tc.target, link))
			cause := "the git control file " + link + " is a symlink to " + tc.target +
				", which the command could rewrite inside the reviewed root"
			requireGitRefusal(t, root, cause)
			requireGitDirsRefusal(t, root, cause)
		})
	}
}

// TestSandboxAcceptsControlFileSymlinksItCannotReach keeps the shapes the
// scan allows: a hook (or the whole hooks directory) linked into the tree —
// the operator's choice, like core.hooksPath — a config linked outside the
// root, and a symbolic-link HEAD inside the git directory itself.
func TestSandboxAcceptsControlFileSymlinksItCannotReach(t *testing.T) {
	t.Run("hook into the tree", func(t *testing.T) {
		root := gitRoot(t)
		mustSandboxFS(t, os.MkdirAll(filepath.Join(root, ".git", "hooks"), 0o755))
		mustSandboxFS(t, os.Symlink("../../scripts/pre-commit", filepath.Join(root, ".git", "hooks", "pre-commit")))
		requireGitAccepted(t, root)
	})
	t.Run("hooks directory into the tree", func(t *testing.T) {
		root := gitRoot(t)
		mustSandboxFS(t, os.Mkdir(filepath.Join(root, ".husky"), 0o755))
		mustSandboxFS(t, os.Symlink("../.husky", filepath.Join(root, ".git", "hooks")))
		requireGitAccepted(t, root)
	})
	t.Run("config outside the root", func(t *testing.T) {
		root := gitRoot(t)
		outside := filepath.Join(t.TempDir(), "cfg")
		writeGitPointer(t, outside, "[core]\n")
		mustSandboxFS(t, os.Symlink(outside, filepath.Join(root, ".git", "config")))
		requireGitAccepted(t, root)
	})
	t.Run("HEAD inside the git directory", func(t *testing.T) {
		root := gitRoot(t)
		mustSandboxFS(t, os.MkdirAll(filepath.Join(root, ".git", "refs", "heads"), 0o755))
		writeGitPointer(t, filepath.Join(root, ".git", "refs", "heads", "main"), "x\n")
		mustSandboxFS(t, os.Symlink("refs/heads/main", filepath.Join(root, ".git", "HEAD")))
		requireGitAccepted(t, root)
	})
}

// requireGitAccepted asserts both mechanisms accept the root's git layout.
func requireGitAccepted(t *testing.T, root string) {
	t.Helper()
	if _, ref := bwrapArgv(root, homeInputs(t.TempDir())); ref != nil {
		t.Errorf("linux ref = %v, want accepted", ref)
	}
	if _, ref := sandboxGitDirs(root); ref != nil {
		t.Errorf("macOS ref = %v, want accepted", ref)
	}
}
