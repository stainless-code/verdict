package fix_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/testenv"
)

// promotedTestPath is the artifact seedReproBundle retains for sigBatch: the
// literal name every promotion golden pins.
const promotedTestPath = ".verdict/tests/" + sigBatch + "-161139e764cc.py"

// gitIn runs one git command in dir with the fixture identity and returns its
// stdout.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
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
