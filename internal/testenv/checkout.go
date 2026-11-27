package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/gitcmd"
)

// excludeMode and excludeDirMode are the fixture's .git/info/exclude bits.
const (
	excludeDirMode = 0o755
	excludeMode    = 0o600
)

// gitWaitDelay bounds the wait for a git child's captured pipes, so a
// descendant holding one open cannot hold the helper.
const gitWaitDelay = 10 * time.Second

// Checkout makes root a git checkout with everything already in it
// committed, verdict's own .verdict directory left untracked: a review
// launch snapshots the reviewed tree from git, so a fixture that launches
// one needs a real checkout under its bundle.
func Checkout(tb testing.TB, root string) {
	tb.Helper()
	gitIn(tb, root, "init", "-q")
	exclude := filepath.Join(root, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), excludeDirMode); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(exclude, []byte("/.verdict/\n"), excludeMode); err != nil {
		tb.Fatal(err)
	}
	gitIn(tb, root, "add", "-A")
	gitIn(tb, root, "commit", "-q", "--allow-empty", "-m", "fixture")
}

// gitIn runs one hermetic git command in dir.
func gitIn(tb testing.TB, dir string, args ...string) {
	tb.Helper()
	argv := gitcmd.Argv(dir, args...)
	//nolint:gosec // G204: a fixture's fixed git argv, built by gitcmd
	cmd := exec.CommandContext(tb.Context(), argv[0], argv[1:]...)
	cmd.Env = gitcmd.Env(Base(nil))
	cmd.WaitDelay = gitWaitDelay
	if out, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
