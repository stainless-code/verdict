package contenttree_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/contenttree"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
)

func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// repo is a committed checkout: a tracked file and a .gitignore.
func repo(t *testing.T) (string, contenttree.Git) {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-q")
	git(t, root, "config", "user.email", "t@example.com")
	git(t, root, "config", "user.name", "t")
	write(t, root, "app.go", "package app\n")
	write(t, root, ".gitignore", "build/\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "c")
	return root, contenttree.Git{Cmdr: coreexec.Runner{}, Root: root}
}

// TestWorktreeNamesTheCheckoutContent pins what the tree id covers: a clean
// checkout is HEAD's tree; an unstaged edit and an untracked file each change
// it; an ignored file and verdict's own state do not; and computing it leaves
// the user's index byte for byte as it was.
func TestWorktreeNamesTheCheckoutContent(t *testing.T) {
	root, g := repo(t)
	ctx := context.Background()
	head := git(t, root, "rev-parse", "HEAD^{tree}")
	clean := mustTree(t, g)
	if clean != head {
		t.Fatalf("clean tree = %s, want HEAD's %s", clean, head)
	}
	write(t, root, "build/out.bin", "ignored\n")
	write(t, root, ".verdict/state/7/patch.json", "{}\n")
	write(t, root, ".verdict/loop/1/round.json", "{}\n")
	write(t, root, ".verdict/.gitignore", "/state/\n")
	if got := mustTree(t, g); got != head {
		t.Fatalf("tree with only ignored files and verdict state = %s, want %s", got, head)
	}
	indexBefore := readIndex(t, root)
	write(t, root, "app.go", "package app // edited\n")
	edited := mustTree(t, g)
	write(t, root, "new.go", "package app\n")
	untracked := mustTree(t, g)
	if edited == head || untracked == edited {
		t.Fatalf("trees head=%s edited=%s untracked=%s, want all distinct", head, edited, untracked)
	}
	if !bytes.Equal(indexBefore, readIndex(t, root)) {
		t.Fatal("computing the tree rewrote the user's index")
	}
	if _, err := g.Commit(ctx, "0123456789012345678901234567890123456789"); !errors.Is(err, contenttree.ErrUnresolved) {
		t.Fatalf("unknown commit: err = %v, want ErrUnresolved", err)
	}
}

// TestWorktreeOnAnUnbornBranch pins the first-commit case: there is no HEAD
// to start from, so the tree is the checkout's files alone, and verdict's
// state stays out even when nothing ignores it.
func TestWorktreeOnAnUnbornBranch(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q")
	write(t, root, "app.go", "package app\n")
	write(t, root, ".verdict/state/7/patch.json", "{}\n")
	tree := mustTree(t, contenttree.Git{Cmdr: coreexec.Runner{}, Root: root})
	if files := git(t, root, "ls-tree", "-r", "--name-only", tree); files != "app.go" {
		t.Fatalf("unborn tree holds %q, want only app.go", files)
	}
}

// TestCheckoutRebuildsTheTree pins that the snapshot a command runs in is
// exactly the tree, as a working git checkout: the edited and untracked files
// are there, the ignored file and verdict state are not, git sees a clean
// tree, and RemoveCheckout leaves no worktree registered.
func TestCheckoutRebuildsTheTree(t *testing.T) {
	root, g := repo(t)
	write(t, root, "app.go", "package app // edited\n")
	write(t, root, "new.go", "package app\n")
	write(t, root, "build/out.bin", "ignored\n")
	write(t, root, ".verdict/state/7/patch.json", "{}\n")
	tree := mustTree(t, g)
	dest := filepath.Join(t.TempDir(), "snap")
	ctx := context.Background()
	if err := g.Checkout(ctx, tree, dest); err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	requireSnapshotFiles(t, dest)
	if got := git(t, dest, "rev-parse", "HEAD^{tree}"); got != tree {
		t.Errorf("snapshot HEAD tree = %s, want %s", got, tree)
	}
	if status := git(t, dest, "status", "--porcelain"); status != "" {
		t.Errorf("snapshot status = %q, want clean", status)
	}
	if err := g.RemoveCheckout(ctx, dest); err != nil {
		t.Fatalf("RemoveCheckout: %v", err)
	}
	requireNoTrace(t, root)
}

// requireSnapshotFiles checks the snapshot holds the edited and untracked
// files and neither the ignored file nor verdict state.
func requireSnapshotFiles(t *testing.T, dest string) {
	t.Helper()
	for rel, want := range map[string]string{"app.go": "package app // edited\n", "new.go": "package app\n"} {
		if got, err := os.ReadFile(filepath.Join(dest, rel)); err != nil || string(got) != want {
			t.Errorf("%s = %q (%v), want %q", rel, got, err, want)
		}
	}
	for _, rel := range []string{"build/out.bin", ".verdict/state/7/patch.json"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); !os.IsNotExist(err) {
			t.Errorf("%s exists in the snapshot (%v)", rel, err)
		}
	}
}

// requireNoTrace checks a removed snapshot left no worktree or branch.
func requireNoTrace(t *testing.T, root string) {
	t.Helper()
	if list := git(t, root, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		t.Errorf("worktrees after removal:\n%s", list)
	}
	if branches := git(t, root, "branch", "--list"); strings.Count(branches, "\n") != 0 {
		t.Errorf("a snapshot created a branch: %q", branches)
	}
}

func readIndex(t *testing.T, root string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mustTree(t *testing.T, g contenttree.Git) string {
	t.Helper()
	tree, err := g.Worktree(context.Background())
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	return tree
}
