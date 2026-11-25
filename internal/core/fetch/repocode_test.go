package fetchcore

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/testkit"
)

// TestRunLocalRunsNoRepoCode pins that fetch-local reads a checkout without
// running any program the checkout chose. The checkout points core.hooksPath
// at an in-tree .husky holding every index and checkout hook, and arms an
// fsmonitor, a smudge filter, a textconv driver, and an external diff, each
// writing a marker. A working-tree bundle (the live diff, the content
// naming), an isolated --worktree bundle (the stash snapshot and the
// checkout, with every worktree armed as a snapshot carrying its own .husky
// and .gitattributes would be), and an unborn-branch scan (the status read)
// all build and leave no marker. The clean filter is the one program that
// runs by design: a working-tree read must hash a file as git itself does.
func TestRunLocalRunsNoRepoCode(t *testing.T) {
	for _, tc := range []struct {
		name      string
		flags     LocalFlags
		born      bool
		worktrees bool
	}{
		{name: "worktree", flags: LocalFlags{HeadControls: true}, born: true},
		{name: "isolated", flags: LocalFlags{HeadControls: true, Worktree: true}, born: true, worktrees: true},
		{name: "unborn-scan", flags: LocalFlags{HeadControls: true, Scan: []string{"app.txt"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			code := armedCheckout(t, root, tc.born, tc.worktrees)
			runLocalReal(t, &tc.flags)
			if ran := code.Ran(t, "clean"); len(ran) > 0 {
				t.Errorf("fetch-local ran repository-controlled code: %q", ran)
			}
		})
	}
}

// runLocalReal runs fetch-local with flags on the working directory's
// checkout through the real git runner.
func runLocalReal(t *testing.T, flags *LocalFlags) {
	t.Helper()
	var human, diag bytes.Buffer
	var streamed []string
	req := worktreeReq(nil, &human, &diag, &streamed)
	req.Commander = coreexec.Runner{}
	req.Flags = *flags
	if _, err := RunLocal(context.Background(), req); err != nil {
		t.Fatalf("RunLocal: %v\n%s", err, diag.String())
	}
}

// armedCheckout makes root a checkout (repoCodeFixture) and arms it, in
// every worktree when worktrees is set.
func armedCheckout(t *testing.T, root string, born, worktrees bool) testkit.RepoCode {
	t.Helper()
	repoCodeFixture(t, root, born)
	code := testkit.ArmRepoCode(t, root)
	if worktrees {
		code.Worktrees(t, root)
	}
	return code
}

// repoCodeFixture makes root a checkout with a dirty tracked file: committed
// when born, staged on an unborn branch otherwise.
func repoCodeFixture(t *testing.T, root string, born bool) {
	t.Helper()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.WaitDelay = 10 * time.Second
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	app := filepath.Join(root, "app.txt")
	mustWrite(t, app, "base\n")
	git("add", "app.txt")
	if born {
		git("commit", "-q", "-m", "base")
		mustWrite(t, app, strings.Repeat("reviewed edit\n", 2))
	}
}
