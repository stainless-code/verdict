//go:build unix

package fix_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	fixcore "github.com/stainless-code/verdict/internal/core/fix"
	"github.com/stainless-code/verdict/internal/testenv"
)

// repoCommander runs one process inside a fixture worktree: the fix service
// sends git its argv alone, so the test's commander supplies the repository and
// the pinned identity.
type repoCommander struct {
	dir string
	coreexec.Runner
}

// Run executes cmd in the fixture worktree.
func (c repoCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	cmd.Dir = c.dir
	cmd.Env = testenv.Base(nil)
	return c.Runner.Run(ctx, cmd)
}

// promoteRequest builds the request the CLI builds for one --test promotion.
func promoteRequest(dir, sig string) *fixcore.Request {
	req := &fixcore.Request{Test: sig, TestSet: true}
	req.ReadRequest = core.ReadRequest{
		Runtime: &core.Runtime{
			Env: map[string]string{},
			Commander: repoCommander{
				dir:    dir,
				Runner: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true},
			},
		},
		Repo: "acme/app", Pr: "local-test", RepoRoot: dir,
	}
	return req
}

// TestPromoteRefusesSymlinkedTestsDir proves promotion is worktree-confined
// exactly as the reproduction writer is: a .verdict/tests symlink pointing
// outside the checkout refuses instead of landing the artifact there.
func TestPromoteRefusesSymlinkedTestsDir(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	setupTestPromote(t, dir, "")
	link := filepath.Join(dir, ".verdict", "tests")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	_, ref := fixcore.Run(t.Context(), promoteRequest(dir, sigBatch))
	const want = "fix --test: .verdict/tests is a symlink — refusing to write outside the worktree"
	if ref == nil || ref.Code != "test-conflict" || ref.Message != want {
		t.Fatalf("refusal = %+v, want test-conflict: %q", ref, want)
	}
	assertNoEntries(t, outside)
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("fixture symlink changed: %v %v", info, err)
	}
}

// assertNoEntries proves a directory the promotion must never reach is empty.
func assertNoEntries(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("promotion wrote %d entr(ies) outside the worktree: %v", len(entries), entries)
	}
}
