//go:build !windows

package verify_test

// End-to-end proof of what a patch-path receipt binds: the real watchdog,
// real git, and a receipt naming the tree of the content the command ran on —
// a snapshot checkout, not the user's checkout.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/verify"
)

// TestVerifyPatchReceiptNamesContent pins what the receipt's tree covers:
// verdict's own state (bundle, loop receipts, the generated .gitignore)
// leaves it at HEAD's tree, while reviewed input under .verdict/ — a
// regression test, the KB — is content and changes it.
func TestVerifyPatchReceiptNamesContent(t *testing.T) {
	for _, tc := range []struct {
		name, extra string // extra is a .verdict-relative file written untracked
		isHeadTree  bool
	}{
		{name: "state-and-generated-gitignore", extra: ".gitignore", isHeadTree: true},
		{name: "loop-round-receipts", extra: "loop/20261125T000000Z/round-1.json", isHeadTree: true},
		{name: "untracked-regression-test", extra: "tests/regress_test.go"},
		{name: "untracked-kb", extra: "kb.jsonl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := committedRepo(t)
			writeTreeFile(t, root, ".verdict/"+tc.extra, "x\n")
			receipt := runPatchVerify(t, root)
			headTree := gitOrFatal(t, root, "rev-parse", "HEAD^{tree}")
			if receipt.Tree == "" || (receipt.Tree == headTree) != tc.isHeadTree || receipt.Tree != contentTree(t, root) {
				t.Fatalf("receipt tree = %q, HEAD tree %s, content tree %s; want it to be HEAD's: %t",
					receipt.Tree, headTree, contentTree(t, root), tc.isHeadTree)
			}
		})
	}
}

// TestVerifyPatchReceiptCountsHiddenUntracked pins that status config cannot
// hide content: with status.showUntrackedFiles=no an untracked source file is
// still in the tree the receipt names.
func TestVerifyPatchReceiptCountsHiddenUntracked(t *testing.T) {
	root := committedRepo(t)
	gitOrFatal(t, root, "config", "status.showUntrackedFiles", "no")
	writeTreeFile(t, root, "helper.go", "package main\n")
	receipt := runPatchVerify(t, root)
	if receipt.Tree == gitOrFatal(t, root, "rev-parse", "HEAD^{tree}") || receipt.Tree != contentTree(t, root) {
		t.Fatalf("receipt tree = %q, want the content tree with helper.go (%s)", receipt.Tree, contentTree(t, root))
	}
}

// TestVerifyRunsOnSnapshotContent pins that the command tests the content the
// receipt names, uncommitted edits included, in a snapshot: a command that
// checks out and restores the file there still sees the edit, and the user's
// checkout keeps it — nothing the command does changes what was tested.
func TestVerifyRunsOnSnapshotContent(t *testing.T) {
	root := committedRepo(t)
	writeTreeFile(t, root, "marker.txt", "uncommitted edit\n")
	writeTreeFile(t, root, ".verdict/state/5201/patch.json",
		`{"verifyCmd":"git checkout -- marker.txt && cat marker.txt && test ! -e `+root+`/should-not-exist"}`)
	receipt := runPatchVerify(t, root)
	out, err := os.ReadFile(filepath.Join(root, ".verdict", "state", "5201", ".verify.out"))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Exit != 0 || !strings.Contains(string(out), "uncommitted edit") {
		t.Fatalf("exit %d, output %q; want the snapshot to hold the uncommitted edit", receipt.Exit, out)
	}
	if got, err := os.ReadFile(filepath.Join(root, "marker.txt")); err != nil || string(got) != "uncommitted edit\n" {
		t.Errorf("user checkout marker.txt = %q (%v), want the edit untouched", got, err)
	}
	if receipt.Tree != contentTree(t, root) {
		t.Errorf("receipt tree = %s, want the content tree %s", receipt.Tree, contentTree(t, root))
	}
	if list := gitOrFatal(t, root, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		t.Errorf("the snapshot stayed registered:\n%s", list)
	}
}

// committedRepo is a repository with one committed marker.txt and a
// patch.json whose verify command passes.
func committedRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitOrFatal(t, root, "init", "-q")
	gitOrFatal(t, root, "config", "user.email", "t@example.com")
	gitOrFatal(t, root, "config", "user.name", "t")
	writeTreeFile(t, root, "marker.txt", "root\n")
	gitOrFatal(t, root, "add", "marker.txt")
	gitOrFatal(t, root, "commit", "-q", "-m", "c")
	writeTreeFile(t, root, ".verdict/state/5201/patch.json", `{"verifyCmd":"true"}`)
	return root
}

// contentTree computes the checkout's content tree with plain git — HEAD
// updated by `git add -A`, verdict's state restored to HEAD — through a
// throwaway index, independently of the code under test.
func contentTree(t *testing.T, root string) string {
	t.Helper()
	index := filepath.Join(t.TempDir(), "index")
	run := func(args ...string) string {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+index)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("read-tree", "HEAD")
	run("add", "-A", "--", ".")
	run("reset", "-q", "HEAD", "--", ".verdict/state", ".verdict/loop", ".verdict/.gitignore")
	return run("write-tree")
}

func writeTreeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runPatchVerify runs the patch-path CLI at root and returns the receipt it
// wrote.
func runPatchVerify(t *testing.T, root string) core.VerifyReceipt {
	t.Helper()
	t.Chdir(root)
	var out, diag strings.Builder
	r := &cli.Runner{
		Cmd: "verify", Repo: "acme/app", Pr: "5201",
		Out: &out, Diag: &diag, Human: &out,
		Now: time.Now, Effective: cli.Defaults(), EnvNoColor: true,
	}
	code := verify.Run(context.Background(), r, nil, verify.Request{
		Env:  map[string]string{"HOME": t.TempDir(), "PATH": os.Getenv("PATH")},
		Repo: "acme/app", Pr: "5201", Root: root, RepoRoot: root,
	})
	if code != 0 {
		t.Fatalf("verify exit = %d\nstdout: %s\nstderr: %s", code, out.String(), diag.String())
	}
	raw, err := os.ReadFile(filepath.Join(root, ".verdict", "state", "5201", "verify-receipt.json"))
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	var receipt core.VerifyReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}
