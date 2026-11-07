package readcommon_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// gitIn runs one git command in dir with a fixed identity and no system
// config, so the fixture is reproducible on any machine.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// committedRepo seeds a checkout with one commit and returns its real path,
// symlinks resolved, which is what RepoRoot answers from a subdirectory.
func committedRepo(t *testing.T) (root, want string) {
	t.Helper()
	root = t.TempDir()
	gitIn(t, root, "init", "-q")
	gitIn(t, root, "commit", "-q", "--allow-empty", "-m", "base")
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, want
}

// TestRepoRoot pins the reviewed-root resolution with a real repository:
// the root reads repo-relative, a subdirectory resolves to the same
// top-level directory itself (so a bundle lands at the Git root), and a
// directory outside any repository reads cwd-relative.
func TestRepoRoot(t *testing.T) {
	root, want := committedRepo(t)

	t.Chdir(root)
	if got, err := readcommon.RepoRoot(t.Context()); err != nil || got != "." {
		t.Errorf("RepoRoot at the repository root = %q, %v; want repo-relative", got, err)
	}

	sub := filepath.Join(root, "sub", "dir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	if got, err := readcommon.RepoRoot(t.Context()); err != nil || got != want {
		t.Errorf("RepoRoot from a subdirectory = %q, %v; want %q", got, err, want)
	}

	t.Chdir(t.TempDir())
	if got, err := readcommon.RepoRoot(t.Context()); err != nil || got != "." {
		t.Errorf("RepoRoot outside a repository = %q, %v; want cwd-relative", got, err)
	}
}

// TestRepoRootHonoursCancellation pins the propagation: from a repository
// subdirectory a live context resolves the top level, while a canceled one
// aborts the probe and reports ErrRootUnresolved instead of falling back to
// the cwd-relative answer — a canceled probe must never read as a verified
// absent checkout.
func TestRepoRootHonoursCancellation(t *testing.T) {
	root := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "git", "init", "-q")
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	if got, err := readcommon.RepoRoot(t.Context()); err != nil || got != want {
		t.Fatalf("RepoRoot with a live context = %q, %v; want %q", got, err, want)
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	got, err := readcommon.RepoRoot(canceled)
	if !errors.Is(err, readcommon.ErrRootUnresolved) {
		t.Fatalf("RepoRoot with a canceled context = (%q, %v); want ErrRootUnresolved: a canceled probe must never read as a verified absent checkout", got, err)
	}
}
