package fix_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/testenv"
	"github.com/stainless-code/verdict/internal/testkit"
)

// promotedTestPath is the artifact seedReproBundle retains for sigBatch: the
// literal name every promotion golden pins.
const promotedTestPath = ".verdict/tests/" + sigBatch + "-161139e764cc.py"

// gitIn runs one git command in dir with the fixture identity and returns its
// stdout. The subprocess carries a WaitDelay (GO-CONC-06) so a stray child
// holding the pipes cannot hang the test.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = dir
	cmd.Env = testenv.Base(map[string]string{
		"GIT_AUTHOR_NAME":     "fix",
		"GIT_AUTHOR_EMAIL":    "fix@eval",
		"GIT_COMMITTER_NAME":  "fix",
		"GIT_COMMITTER_EMAIL": "fix@eval",
	})
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// writeIn writes one fixture file under dir.
func writeIn(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// testHome returns an existing isolated HOME for a harness run.
func testHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

// TestPromoteCommitLeavesStagedWorkAlone proves --commit is a pathspec commit:
// a file already staged in the index is absent from the new commit and still
// staged afterwards, and an unrelated unstaged edit stays out of both.
func TestPromoteCommitLeavesStagedWorkAlone(t *testing.T) {
	dir := t.TempDir()
	setupTestPromote(t, dir, "")
	writeIn(t, dir, "other.txt", "other1\nstaged edit\nother2\n")
	gitIn(t, dir, "add", "--", "other.txt")
	writeIn(t, dir, "notes.txt", "line1\ndirty edit\nline3\n")

	out := harness.Run(t, dir, testHome(t), nil,
		"fix", "acme/app", "local-test", "--test", sigBatch, "--commit", "--json")
	if out.Status != 0 {
		t.Fatalf("fix --test --commit = exit %d\nstdout:\n%s\nstderr:\n%s",
			out.Status, out.Stdout, out.Stderr)
	}
	if got := gitIn(t, dir, "show", "--name-only", "--format=", "HEAD"); got != promotedTestPath+"\n" {
		t.Errorf("new commit touches %q, want only %q", got, promotedTestPath)
	}
	if got := gitIn(t, dir, "diff", "--cached", "--name-only"); got != "other.txt\n" {
		t.Errorf("staged after commit = %q, want %q", got, "other.txt\n")
	}
	if got := gitIn(t, dir, "status", "--porcelain", "--", "notes.txt"); got == "" {
		t.Errorf("notes.txt is no longer dirty; want its unstaged edit untouched")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "line1\ndirty edit\nline3\n"; string(raw) != want {
		t.Errorf("notes.txt = %q, want %q", raw, want)
	}
}

// TestPromoteCommitFromSubdirectory proves --commit resolves its repo-relative
// destination from the repository root, so an invocation from a checkout
// subdirectory commits the promoted test instead of refusing on a pathspec the
// subdirectory cannot match.
func TestPromoteCommitFromSubdirectory(t *testing.T) {
	dir := t.TempDir()
	setupTestPromote(t, dir, "")
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	out := harness.Run(t, sub, testHome(t), nil,
		"fix", "acme/app", "local-test", "--test", sigBatch, "--commit", "--json")
	if out.Status != 0 {
		t.Fatalf("fix --test --commit from a subdirectory = exit %d\nstdout:\n%s\nstderr:\n%s",
			out.Status, out.Stdout, out.Stderr)
	}
	if got := gitIn(t, dir, "show", "--name-only", "--format=", "HEAD"); got != promotedTestPath+"\n" {
		t.Errorf("new commit touches %q, want only %q", got, promotedTestPath)
	}
}

// TestPromoteCommitRunsNoRepoCode pins that `fix --test --commit` runs no
// program the checkout chose: with core.hooksPath on an in-tree .husky
// holding every commit and index hook, an fsmonitor, a smudge filter, a
// textconv driver, and an external diff armed, the commit lands and none of
// them leaves a marker. A clean or process filter is the one program that
// runs by design: add stores the file as git itself would. The armed driver
// defines clean, so its marker is the one allowed.
func TestPromoteCommitRunsNoRepoCode(t *testing.T) {
	dir := t.TempDir()
	setupTestPromote(t, dir, "")
	code := testkit.ArmRepoCode(t, dir)

	out := harness.Run(t, dir, testHome(t), nil,
		"fix", "acme/app", "local-test", "--test", sigBatch, "--commit", "--json")
	if ran := code.Ran(t, "clean"); len(ran) > 0 {
		t.Errorf("fix --test --commit ran repository-controlled code: %q", ran)
	}
	if out.Status != 0 {
		t.Fatalf("fix --test --commit = exit %d\nstdout:\n%s\nstderr:\n%s",
			out.Status, out.Stdout, out.Stderr)
	}
	if got := gitIn(t, dir, "show", "--name-only", "--format=", "HEAD"); got != promotedTestPath+"\n" {
		t.Errorf("new commit touches %q, want only %q", got, promotedTestPath)
	}
}

// TestPromoteCommitIgnoresInheritedGitEnv pins that variables a parent git
// exports cannot steer `fix --test --commit`: GIT_CONFIG_PARAMETERS turning
// an in-tree hooks path on runs no hook, and GIT_INDEX_FILE naming another
// index leaves it unwritten, so the checkout's own index records the commit.
func TestPromoteCommitIgnoresInheritedGitEnv(t *testing.T) {
	dir := t.TempDir()
	setupTestPromote(t, dir, "")
	markers := t.TempDir()
	hook := filepath.Join(dir, ".lefthook", "pre-commit")
	testkit.WriteFile(t, hook, "#!/bin/sh\ntouch '"+filepath.ToSlash(filepath.Join(markers, "pre-commit"))+"'\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(dir, ".git", "stray-index")

	out := harness.Run(t, dir, testHome(t), map[string]string{
		"GIT_CONFIG_PARAMETERS": "'core.hooksPath'='.lefthook'",
		"GIT_INDEX_FILE":        stray,
	}, "fix", "acme/app", "local-test", "--test", sigBatch, "--commit", "--json")
	if out.Status != 0 {
		t.Fatalf("fix --test --commit = exit %d\nstdout:\n%s\nstderr:\n%s",
			out.Status, out.Stdout, out.Stderr)
	}
	if left, err := os.ReadDir(markers); err != nil || len(left) != 0 {
		t.Errorf("a hook ran through GIT_CONFIG_PARAMETERS: %v (%v)", left, err)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Errorf("the inherited GIT_INDEX_FILE was written (%v)", err)
	}
	if got := gitIn(t, dir, "status", "--porcelain", "--", promotedTestPath); got != "" {
		t.Errorf("the checkout's index does not record the commit: status %q", got)
	}
}
