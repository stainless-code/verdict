package readgolden_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/testenv"
)

// TestDoctorGolden pins the doctor verb over the network-dead harness stub
// (every gh read fails). Pure read: every leg carries the no-write proof.
func TestDoctorGolden(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("line1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "notes.txt")
	gitRun(t, dir, "commit", "-qm", "seed")
}

// TestCheckTreeGolden pins the check-tree verb: the clean
// tree, the dirty tree, the --expect allowlist (covered and stray), the
// human and machine surfaces, and the non-repo refusal.
func TestCheckTreeGolden(t *testing.T) {
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
