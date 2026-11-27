package contenttree

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestGitAlias pins the names that may name a directory's .git: .git in any
// case everywhere, and on Windows a short (8.3) name such as GIT~1.
func TestGitAlias(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, goos string
		want       bool
	}{
		{".git", "linux", true},
		{".GiT", "darwin", true},
		{"GIT~1", "windows", true},
		{"git~12", "windows", true},
		{"GIT~1", "linux", false},
		{"GIT~", "windows", false},
		{"GIT~X", "windows", false},
		{"GITHUB", "windows", false},
	} {
		if got := gitAlias(tc.name, tc.goos); got != tc.want {
			t.Errorf("gitAlias(%q, %s) = %v, want %v", tc.name, tc.goos, got, tc.want)
		}
	}
}

// TestFillNeverLetsContentReplaceTheGitPointer proves content moving into a
// snapshot cannot replace its .git pointer: an entry naming .git refuses
// before anything moves, and the pointer git follows stays the one git wrote.
func TestFillNeverLetsContentReplaceTheGitPointer(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testenv.Checkout(t, root)
	dest := filepath.Join(t.TempDir(), "snap")
	run(t, root, "worktree", "add", "--no-checkout", "--detach", "--quiet", dest, "HEAD")
	before, err := os.ReadFile(filepath.Join(dest, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	staging := t.TempDir()
	if err := os.WriteFile(filepath.Join(staging, ".git"), []byte("gitdir: "+filepath.Join(root, ".git")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := Git{Cmdr: coreexec.Runner{}, Root: dest}
	if _, err := g.fill(t.Context(), staging, "linux"); err == nil {
		t.Error("content naming .git moved into the snapshot")
	}
	if after, err := os.ReadFile(filepath.Join(dest, ".git")); err != nil || !bytes.Equal(after, before) {
		t.Errorf("the snapshot's .git = %q (%v), want git's own pointer %q", after, err, before)
	}
}

// run runs one hermetic git command in dir.
func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = testenv.Base(nil)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
