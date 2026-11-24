package verifycore

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestSandboxGitDirsFollowsPointers pins the resolved control locations: a
// plain `.git` directory, a `.git` file naming a git directory (relative to
// the root), and the commondir a linked worktree's git directory names.
func TestSandboxGitDirsFollowsPointers(t *testing.T) {
	plain := t.TempDir()
	mustSandboxFS(t, os.Mkdir(filepath.Join(plain, ".git"), 0o755))
	if got, want := gitDirs(t, plain), []string{filepath.Join(plain, ".git")}; !slices.Equal(got, want) {
		t.Errorf("plain repo = %v, want %v", got, want)
	}

	linked := t.TempDir()
	gitdir := filepath.Join(linked, "meta", "worktrees", "wt")
	mustSandboxFS(t, os.MkdirAll(gitdir, 0o755))
	mustSandboxFS(t, os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: meta/worktrees/wt\n"), 0o600))
	mustSandboxFS(t, os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o600))
	want := []string{
		filepath.Join(linked, ".git"), filepath.Join(linked, "meta"), filepath.Join(linked, "meta", "worktrees"), gitdir,
	}
	if got := gitDirs(t, linked); !slices.Equal(got, want) {
		t.Errorf("linked worktree = %v, want %v", got, want)
	}

	if got := gitDirs(t, t.TempDir()); got != nil {
		t.Errorf("no repository = %v, want nil", got)
	}
}

// TestSandboxArgvMakesGitControlReadOnly pins the Linux shape: every git
// control directory inside the reviewed root gets a read-only bind right
// after the writable root bind (so it wins), and one outside the root adds
// nothing because `--ro-bind / /` already covers it.
func TestSandboxArgvMakesGitControlReadOnly(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	gitdir := filepath.Join(root, "meta", "worktrees", "wt")
	mustSandboxFS(t, os.MkdirAll(gitdir, 0o755))
	mustSandboxFS(t, os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: meta/worktrees/wt\n"), 0o600))
	mustSandboxFS(t, os.WriteFile(filepath.Join(gitdir, "commondir"), []byte(outside+"\n"), 0o600))
	argv, ref := bwrapArgv(root, homeInputs(t.TempDir()))
	if ref != nil {
		t.Fatalf("bwrapArgv: %v", ref)
	}
	// The git directory sits under the top-level `meta`, so `meta` is what
	// gets pinned: a bind of the deep directory alone would leave `meta`
	// renamable and replaceable by a link.
	want := []string{
		"--bind", root, root,
		"--ro-bind", filepath.Join(root, ".git"), filepath.Join(root, ".git"),
		"--ro-bind", filepath.Join(root, "meta"), filepath.Join(root, "meta"),
		"--unshare-net",
	}
	start := slices.Index(argv, "--bind")
	if start < 0 || len(argv) < start+len(want) || !slices.Equal(argv[start:start+len(want)], want) {
		t.Errorf("argv = %v, want %v after the repo bind", argv, want)
	}
}

// TestSandboxArgvRefusesARootWithoutGitDir pins the Linux refusal for a root
// with no `.git`: bwrap could protect `<root>/.git` only by creating a mount
// point in the host tree, so the run refuses instead of leaving the path
// creatable.
func TestSandboxArgvRefusesARootWithoutGitDir(t *testing.T) {
	root := t.TempDir()
	argv, ref := bwrapArgv(root, homeInputs(t.TempDir()))
	if ref == nil || ref.Code != codeSandboxUnavailable {
		t.Fatalf("argv=%v ref=%v, want sandbox-unavailable", argv, ref)
	}
	want := "sandbox unavailable: the reviewed root " + root + " has no .git, and bwrap can make only an existing " +
		"path read-only, so the command could create git hooks or config there that run later outside the " +
		"sandbox — verify from the top of a git checkout, or run the whole review in a throwaway VM or container"
	if ref.Message != want {
		t.Errorf("message = %q, want %q", ref.Message, want)
	}
}

// TestSandboxArgvRefusesUnprotectableGitPointers pins every `.git` pointer
// shape bwrap cannot make read-only: a `gitdir:` target or commondir that
// does not exist (the command could create it, hooks included), a `.git`
// file with no pointer line, and a git directory that is the root itself.
// Each refuses with its own cause instead of being skipped.
func TestSandboxArgvRefusesUnprotectableGitPointers(t *testing.T) {
	t.Run("missing gitdir target", func(t *testing.T) {
		root := t.TempDir()
		writeGitPointer(t, filepath.Join(root, ".git"), "gitdir: meta\n")
		requireGitRefusal(t, root, filepath.Join(root, ".git")+" names the git directory "+
			filepath.Join(root, "meta")+", which does not exist as a directory")
	})
	t.Run("missing commondir target", func(t *testing.T) {
		root := t.TempDir()
		mustSandboxFS(t, os.MkdirAll(filepath.Join(root, "meta"), 0o755))
		writeGitPointer(t, filepath.Join(root, ".git"), "gitdir: meta\n")
		writeGitPointer(t, filepath.Join(root, "meta", "commondir"), "../common\n")
		requireGitRefusal(t, root, filepath.Join(root, "meta", "commondir")+" names the git directory "+
			filepath.Join(root, "common")+", which does not exist as a directory")
	})
	t.Run("no pointer line", func(t *testing.T) {
		root := t.TempDir()
		writeGitPointer(t, filepath.Join(root, ".git"), "not a pointer\n")
		requireGitRefusal(t, root, filepath.Join(root, ".git")+" has no readable git pointer line")
	})
	t.Run("gitdir is the root", func(t *testing.T) {
		root := t.TempDir()
		writeGitPointer(t, filepath.Join(root, ".git"), "gitdir: .\n")
		requireGitRefusal(t, root, "the git location "+root+" is the reviewed root itself")
	})
}

// TestSandboxArgvRefusesAPointerWalkingThroughAMissingComponent pins the
// `..` escape: `gitdir: a/../g` cleans to `g`, but git resolves `a` first, so
// a command that creates `a` as a link sends git elsewhere. A missing in-root
// component on the raw walk refuses, for the gitdir and the commondir alike.
func TestSandboxArgvRefusesAPointerWalkingThroughAMissingComponent(t *testing.T) {
	t.Run("gitdir a/../g", func(t *testing.T) {
		root := t.TempDir()
		mustSandboxFS(t, os.Mkdir(filepath.Join(root, "g"), 0o755))
		writeGitPointer(t, filepath.Join(root, ".git"), "gitdir: a/../g\n")
		requireGitRefusal(t, root, "the git pointer "+filepath.Join(root, ".git")+" walks through "+
			filepath.Join(root, "a")+", which does not exist inside the reviewed root")
	})
	t.Run("commondir l/../shared", func(t *testing.T) {
		root := t.TempDir()
		mustSandboxFS(t, os.MkdirAll(filepath.Join(root, ".git", "shared"), 0o755))
		writeGitPointer(t, filepath.Join(root, ".git", "commondir"), "l/../shared\n")
		requireGitRefusal(t, root, "the git pointer "+filepath.Join(root, ".git", "commondir")+" walks through "+
			filepath.Join(root, ".git", "l")+", which does not exist inside the reviewed root")
	})
}

// TestSandboxArgvPinsEveryDirectoryAPointerWalks keeps `gitdir: a/../g` with
// `a` a real directory working, and sound: both `a` and `g` are pinned
// read-only, so `a` cannot later become a link that sends git elsewhere.
func TestSandboxArgvPinsEveryDirectoryAPointerWalks(t *testing.T) {
	root := t.TempDir()
	mustSandboxFS(t, os.Mkdir(filepath.Join(root, "a"), 0o755))
	mustSandboxFS(t, os.Mkdir(filepath.Join(root, "g"), 0o755))
	writeGitPointer(t, filepath.Join(root, ".git"), "gitdir: a/../g\n")
	argv, ref := bwrapArgv(root, homeInputs(t.TempDir()))
	if ref != nil {
		t.Fatalf("bwrapArgv: %v", ref)
	}
	want := []string{
		"--bind", root, root,
		"--ro-bind", filepath.Join(root, ".git"), filepath.Join(root, ".git"),
		"--ro-bind", filepath.Join(root, "a"), filepath.Join(root, "a"),
		"--ro-bind", filepath.Join(root, "g"), filepath.Join(root, "g"),
		"--unshare-net",
	}
	start := slices.Index(argv, "--bind")
	if start < 0 || len(argv) < start+len(want) || !slices.Equal(argv[start:start+len(want)], want) {
		t.Errorf("argv = %v, want %v after the repo bind", argv, want)
	}
}

// gitDirs is sandboxGitDirs for a layout that must not refuse.
func gitDirs(t *testing.T, root string) []string {
	t.Helper()
	dirs, ref := sandboxGitDirs(root)
	if ref != nil {
		t.Fatalf("sandboxGitDirs: %v", ref)
	}
	return dirs
}

// TestSandboxGitPointerKeepsGitsWhitespace pins git's own pointer format:
// only trailing CR and LF are stripped, so a git directory whose name ends in
// a space is the one protected — on Linux pinned, on macOS denied by path —
// not a trimmed neighbor.
func TestSandboxGitPointerKeepsGitsWhitespace(t *testing.T) {
	root := t.TempDir()
	spaced := filepath.Join(root, "g ")
	mustSandboxFS(t, os.Mkdir(spaced, 0o755))
	writeGitPointer(t, filepath.Join(root, ".git"), "gitdir: g \r\n")
	argv, ref := bwrapArgv(root, homeInputs(t.TempDir()))
	if ref != nil {
		t.Fatalf("bwrapArgv: %v", ref)
	}
	if !slices.Contains(argv, spaced) {
		t.Errorf("argv = %q, want %q pinned", argv, spaced)
	}
	if dirs := gitDirs(t, root); !slices.Contains(dirs, spaced) {
		t.Errorf("macOS git dirs = %q, want %q", dirs, spaced)
	}
}

// TestSandboxGitPointerRefusesAnOverCapPointer keeps a pointer file larger
// than the cap from being parsed by its prefix.
func TestSandboxGitPointerRefusesAnOverCapPointer(t *testing.T) {
	root := t.TempDir()
	mustSandboxFS(t, os.Mkdir(filepath.Join(root, "g"), 0o755))
	writeGitPointer(t, filepath.Join(root, ".git"), "gitdir: g\n"+strings.Repeat("x", sandboxGitPointerCap))
	requireGitRefusal(t, root, "could not read the git pointer "+filepath.Join(root, ".git"))
	if _, ref := sandboxGitDirs(root); ref == nil || !strings.Contains(ref.Message, "could not read the git pointer") {
		t.Errorf("macOS ref = %v, want the same refusal", ref)
	}
}

// TestSandboxHomeUsesTheLastHOME follows os/exec: the last of duplicate
// entries is what the child sees, so it is the home whose credentials hide.
func TestSandboxHomeUsesTheLastHOME(t *testing.T) {
	in := sandboxInputs{childEnv: []string{"HOME=/h/first", "HOME=/h/last"}}
	homes, ref := in.homes()
	if ref != nil || !slices.Equal(homes, []string{"/h/last"}) {
		t.Errorf("homes = %v, %v; want [/h/last]", homes, ref)
	}
}

// requireGitRefusal asserts the Linux argv refuses sandbox-unavailable with
// the literal cause as its message prefix.
func requireGitRefusal(t *testing.T, root, cause string) {
	t.Helper()
	argv, ref := bwrapArgv(root, homeInputs(t.TempDir()))
	if ref == nil || ref.Code != codeSandboxUnavailable {
		t.Fatalf("argv=%v ref=%v, want sandbox-unavailable", argv, ref)
	}
	if want := "sandbox unavailable: " + cause; !strings.HasPrefix(ref.Message, want) {
		t.Errorf("message = %q, want prefix %q", ref.Message, want)
	}
}

func writeGitPointer(t *testing.T, path, body string) {
	t.Helper()
	mustSandboxFS(t, os.WriteFile(path, []byte(body), 0o600))
}

// gitRoot is a reviewed root with a plain `.git` directory, the shape the
// Linux mechanism accepts.
func gitRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustSandboxFS(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	return root
}

func mustSandboxFS(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
