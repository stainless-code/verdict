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
	if got, want := sandboxGitDirs(plain), []string{filepath.Join(plain, ".git")}; !slices.Equal(got, want) {
		t.Errorf("plain repo = %v, want %v", got, want)
	}

	linked := t.TempDir()
	gitdir := filepath.Join(linked, "meta", "worktrees", "wt")
	mustSandboxFS(t, os.MkdirAll(gitdir, 0o755))
	mustSandboxFS(t, os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: meta/worktrees/wt\n"), 0o600))
	mustSandboxFS(t, os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o600))
	want := []string{filepath.Join(linked, ".git"), gitdir, filepath.Join(linked, "meta")}
	if got := sandboxGitDirs(linked); !slices.Equal(got, want) {
		t.Errorf("linked worktree = %v, want %v", got, want)
	}

	if got := sandboxGitDirs(t.TempDir()); got != nil {
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
	want := []string{
		"--bind", root, root,
		"--ro-bind", filepath.Join(root, ".git"), filepath.Join(root, ".git"),
		"--ro-bind", gitdir, gitdir,
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
