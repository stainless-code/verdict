package readparity_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/testenv"
)

// TestDoctorParity proves the doctor verb matches bash with the
// network-dead harness stub (every gh read fails identically on both
// sides). Pure read: every leg carries the no-write proof.
func TestDoctorParity(t *testing.T) {
	t.Parallel()

	empty := func(t *testing.T, _, _ string) {
		t.Helper()
	}
	goArgs := splitBare("doctor")
	goldenRead(t, "doctor/human", empty, nil, nil, goArgs...)

	goArgs = splitBare("doctor", "--json")
	goldenRead(t, "doctor/json", empty, nil, nil, goArgs...)
}

// gitRun runs git in dir with the harness's pinned configuration.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = testenv.Base(nil)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitRepo crafts a git checkout: initialized with one committed file so
// the tree reads clean, then per-leg dirt applies on top.
func gitRepo(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "user.email", "parity@test")
	gitRun(t, dir, "config", "user.name", "parity")
	// Background auto-maintenance writes .git/objects/maintenance.lock
	// mid-snapshot, which flaked the file-presence comparison.
	gitRun(t, dir, "config", "maintenance.auto", "false")
	gitRun(t, dir, "config", "gc.auto", "0")
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("line1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "notes.txt")
	gitRun(t, dir, "commit", "-qm", "seed")
}

// TestCheckTreeParity proves the check-tree verb matches bash: the clean
// tree, the dirty tree, the --expect allowlist (covered and stray), the
// human and machine surfaces, and the non-repo refusal.
func TestCheckTreeParity(t *testing.T) {
	t.Parallel()

	clean := func(t *testing.T, dir, _ string) {
		t.Helper()
		gitRepo(t, dir)
	}
	goArgs := splitBare("check-tree")
	goldenRead(t, "check-tree/clean-human", clean, nil, nil, goArgs...)

	goArgs = splitBare("check-tree", "--json")
	goldenRead(t, "check-tree/clean-json", clean, nil, nil, goArgs...)

	dirty := func(t *testing.T, dir, _ string) {
		t.Helper()
		gitRepo(t, dir)
		if err := os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("scratch\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	goArgs = splitBare("check-tree")
	goldenRead(t, "check-tree/dirty-human", dirty, nil, nil, goArgs...)

	goArgs = splitBare("check-tree", "--json")
	goldenRead(t, "check-tree/dirty-json", dirty, nil, nil, goArgs...)

	goArgs = splitBare("check-tree", "--expect", "scratch.txt", "--json")
	goldenRead(t, "check-tree/expect-covered", dirty, nil, nil, goArgs...)

	goArgs = splitBare("check-tree", "--expect", "other.txt")
	goldenRead(t, "check-tree/expect-uncovered", dirty, nil, nil, goArgs...)

	// The .review/ scratch lines read ignored, never failing.
	scratch := func(t *testing.T, dir, _ string) {
		t.Helper()
		gitRepo(t, dir)
		writeFiles(t, dir, map[string]string{
			".review/5101/findings.json": `[]`,
		})
	}
	goArgs = splitBare("check-tree")
	goldenRead(t, "check-tree/review-scratch", scratch, nil, nil, goArgs...)

	norepo := func(t *testing.T, dir, _ string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("line1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
			t.Fatal("fixture leaked a .git directory")
		}
	}
	goArgs = splitBare("check-tree", "--json")
	goldenRead(t, "check-tree/norepo-json", norepo, nil, nil, goArgs...)
}
