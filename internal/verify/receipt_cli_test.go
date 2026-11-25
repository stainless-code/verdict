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
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/verify"
)

// TestVerifyPatchReceiptNamesContent pins what the receipt's tree covers:
// the index paths, as the bundle's diff does. verdict's own state (bundle,
// loop receipts, the generated .gitignore) leaves it at HEAD's tree, and so
// does an untracked file — a regression test or the KB under .verdict/ —
// until `git add -N` makes it content.
func TestVerifyPatchReceiptNamesContent(t *testing.T) {
	for _, tc := range []struct {
		name, extra string // extra is a .verdict-relative file written untracked
		intent      bool   // git add -N the extra file
		isHeadTree  bool
	}{
		{name: "state-and-generated-gitignore", extra: ".gitignore", isHeadTree: true},
		{name: "loop-round-receipts", extra: "loop/20261125T000000Z/round-1.json", isHeadTree: true},
		{name: "untracked-regression-test", extra: "tests/regress_test.go", isHeadTree: true},
		{name: "intent-to-add-regression-test", extra: "tests/regress_test.go", intent: true},
		{name: "intent-to-add-kb", extra: "kb.jsonl", intent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := committedRepo(t)
			writeTreeFile(t, root, ".verdict/"+tc.extra, "x\n")
			if tc.intent {
				gitOrFatal(t, root, "add", "-N", ".verdict/"+tc.extra)
			}
			receipt, _ := runPatchVerify(t, root)
			requireReceiptTree(t, root, receipt.Tree, tc.isHeadTree)
		})
	}
}

// requireReceiptTree checks the receipt names the checkout's content tree,
// and whether that is HEAD's.
func requireReceiptTree(t *testing.T, root, tree string, isHeadTree bool) {
	t.Helper()
	headTree := gitOrFatal(t, root, "rev-parse", "HEAD^{tree}")
	if want := contentTree(t, root); tree == "" || (tree == headTree) != isHeadTree || tree != want {
		t.Fatalf("receipt tree = %q, HEAD tree %s, content tree %s; want it to be HEAD's: %t", tree, headTree, want, isHeadTree)
	}
}

// TestVerifyPatchReceiptLeavesOutUntracked pins that a VERIFIED claim is
// never backed by content no reviewer saw: an untracked source file — outside
// the bundle's diff — is neither in the receipt's tree nor in the snapshot
// the command runs in, and verify names it.
func TestVerifyPatchReceiptLeavesOutUntracked(t *testing.T) {
	root := committedRepo(t)
	writeTreeFile(t, root, "helper.go", "package main\n")
	writeTreeFile(t, root, ".verdict/state/5201/patch.json", `{"verifyCmd":"test ! -e helper.go"}`)
	receipt, diag := runPatchVerify(t, root)
	if receipt.Tree != gitOrFatal(t, root, "rev-parse", "HEAD^{tree}") || receipt.Exit != 0 {
		t.Fatalf("receipt tree = %q exit %d, want HEAD's tree and the command not to see helper.go", receipt.Tree, receipt.Exit)
	}
	if !strings.Contains(diag, "untracked file(s) the bundle does not cover: helper.go") ||
		!strings.Contains(diag, "git add -N") {
		t.Errorf("diagnostics never name the untracked file:\n%s", diag)
	}
}

// TestVerifyRunsNoRepositoryHooks pins that making the snapshot runs no code
// the checkout controls: with core.hooksPath at a committed .husky directory
// (PR content) and a post-checkout and post-index-change hook there and in
// .git/hooks, each writing a marker outside the repository, verify leaves no
// marker behind.
func TestVerifyRunsNoRepositoryHooks(t *testing.T) {
	root := committedRepo(t)
	markers := t.TempDir()
	for _, dir := range []string{".husky", ".git/hooks"} {
		writeHooks(t, root, dir, markers)
	}
	gitOrFatal(t, root, "add", ".husky")
	gitOrFatal(t, root, "commit", "-q", "--no-verify", "-m", "hooks")
	gitOrFatal(t, root, "config", "core.hooksPath", ".husky")
	clearDir(t, markers)
	writeTreeFile(t, root, "marker.txt", "edited\n")
	if receipt, _ := runPatchVerify(t, root); receipt.Tree == "" {
		t.Fatal("verify recorded no tree")
	}
	if left, err := os.ReadDir(markers); err != nil || len(left) != 0 {
		t.Errorf("repository hooks ran during verify: %v (%v)", left, err)
	}
}

// writeHooks writes, under root's dir, a post-checkout, post-index-change,
// and reference-transaction hook that each touch a marker in markers.
func writeHooks(t *testing.T, root, dir, markers string) {
	t.Helper()
	for _, hook := range []string{"post-checkout", "post-index-change", "reference-transaction"} {
		writeTreeFile(t, root, dir+"/"+hook, "#!/bin/sh\ntouch "+filepath.Join(markers, hook)+"\n")
		if err := os.Chmod(filepath.Join(root, filepath.FromSlash(dir), hook), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// clearDir empties dir.
func clearDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			t.Fatal(err)
		}
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
	receipt, _ := runPatchVerify(t, root)
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

// contentTree computes the checkout's content tree with plain git — the
// user's index with every tracked entry's working-tree bytes (`add -u`),
// verdict's state restored to HEAD — through a throwaway copy of the index,
// independently of the code under test. The fixtures carry no conversions,
// so git's clean form is the bytes on disk.
func contentTree(t *testing.T, root string) string {
	t.Helper()
	index := filepath.Join(t.TempDir(), "index")
	raw, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+index)
		// A descendant holding the output pipe cannot hang the read (GO-CONC-06).
		cmd.WaitDelay = 10 * time.Second
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("add", "-u", "--", ".")
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
// wrote and the human and diagnostic output.
func runPatchVerify(t *testing.T, root string) (core.VerifyReceipt, string) {
	t.Helper()
	t.Chdir(root)
	var out, diag strings.Builder
	r := &cli.Runner{
		Cmd: "verify", Repo: "acme/app", Pr: "5201",
		Out: &out, Diag: &diag, Human: &out,
		Now: time.Now, Effective: cli.Defaults(), EnvNoColor: true,
	}
	code := verify.Run(context.Background(), r, nil, readcommon.Request{
		Runtime: &core.Runtime{Env: map[string]string{"HOME": t.TempDir(), "PATH": os.Getenv("PATH")}},
		Repo:    "acme/app", Pr: "5201", Root: &root, RepoRoot: root,
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
	return receipt, out.String() + diag.String()
}
