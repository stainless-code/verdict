package verifycore

import (
	"os"
	"path/filepath"
	"slices"
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

func mustSandboxFS(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
