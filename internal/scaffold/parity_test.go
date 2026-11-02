// Package scaffold_test holds the shell-parity proof for the Go init verb:
// the shared harness runs BOTH scripts/review.sh init and the Go binary on
// identical fixtures and compares exit codes, stdout/stderr (timestamps
// scrubbed only), and every written file byte for byte, including the
// preset matrix, the overwrite guard, and the check-tree validation.
package scaffold_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/parity"
)

var harness *parity.Harness

// TestMain builds the Go binary once and runs the parity suite.
func TestMain(m *testing.M) {
	h, err := parity.Build()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	harness = h
	code := m.Run()
	harness.Teardown()
	os.Exit(code)
}

// initBoth runs one init case with dispatch-faithful args.
func initBoth(t *testing.T, name string, setup func(t *testing.T, dir, home string), args ...string) {
	t.Helper()
	bashArgs := append([]string{args[0]}, args[3:]...)
	harness.RunBothSplit(t, name, setup, nil, false, bashArgs, args)
}

// mkRepo initializes a clean git repo sandbox.
func mkRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(ctx context.Context, args ...string) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=init", "GIT_AUTHOR_EMAIL=init@eval",
			"GIT_COMMITTER_NAME=init", "GIT_COMMITTER_EMAIL=init@eval")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	ctx := t.Context()
	run(ctx, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "probe.txt"), []byte("probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(ctx, "add", "-A")
	run(ctx, "commit", "-qm", "init")
}

// emptyRepo is the empty clean-repo fixture.
func emptyRepo(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
}

// seedOverwrite plants divergent control files for the overwrite guard.
func seedOverwrite(t *testing.T, dir string) {
	t.Helper()
	seed := map[string]string{
		"budget":    "9.99\n",
		"panel":     "# old\n",
		"kb-policy": "MIN_JUDGED=9\nMAX_FP_RATE=0.9\n",
		"kb.jsonl":  "{\"id\":\"x\"}\n",
	}
	if err := os.MkdirAll(filepath.Join(dir, ".review"), 0o755); err != nil {
		t.Fatal(err)
	}
	for f, c := range seed {
		if err := os.WriteFile(filepath.Join(dir, ".review", f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// seedBudget plants a single divergent budget file.
func seedBudget(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".review"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".review", "budget"), []byte("9.99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// overwriteRefuse seeds divergent files for the refuse case.
func overwriteRefuse(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	seedOverwrite(t, dir)
}

// overwriteBudgetRefuse seeds a divergent budget for the JSON refuse case.
func overwriteBudgetRefuse(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	seedBudget(t, dir)
}

// overwriteBudgetYes seeds a divergent budget for the overwrite-yes case.
func overwriteBudgetYes(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	seedBudget(t, dir)
}

// dirtyTree seeds a dirty worktree fixture.
func dirtyTree(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// notARepo seeds a non-repo fixture.
func notARepo(t *testing.T, dir, _ string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "probe.txt"), []byte("probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestInitParity proves byte parity across presets, the overwrite guard,
// idempotency, and validation edges.
func TestInitParity(t *testing.T) {
	testPresetMatrix(t)
	testOverwriteGuard(t)
	testTreeEdges(t)
}

// testPresetMatrix covers presets, aliases, and bad/JSON flags.
func testPresetMatrix(t *testing.T) {
	t.Helper()
	initBoth(t, "standard", emptyRepo,
		"init", "", "", "--preset", "standard", "--yes")
	initBoth(t, "chill", emptyRepo,
		"init", "", "", "--preset", "chill", "--yes")
	initBoth(t, "strict", emptyRepo,
		"init", "", "", "--preset", "strict", "--yes")
	initBoth(t, "preset-eq", emptyRepo,
		"init", "", "", "--preset=chill", "--yes")
	initBoth(t, "bad-preset", emptyRepo,
		"init", "", "", "--preset", "bogus", "--yes", "--json")
	initBoth(t, "json", emptyRepo,
		"init", "", "", "--preset", "standard", "--yes", "--json")
}

// testOverwriteGuard covers the overwrite confirm paths.
func testOverwriteGuard(t *testing.T) {
	t.Helper()
	initBoth(t, "overwrite-refuse", overwriteRefuse,
		"init", "", "", "--preset", "chill")
	initBoth(t, "overwrite-json-refuse", overwriteBudgetRefuse,
		"init", "", "", "--preset", "chill", "--json")
	initBoth(t, "overwrite-yes", overwriteBudgetYes,
		"init", "", "", "--preset", "chill", "--yes")
}

// testTreeEdges covers dirty-tree, non-repo, and unknown flags.
func testTreeEdges(t *testing.T) {
	t.Helper()
	initBoth(t, "dirty-tree", dirtyTree,
		"init", "", "", "--preset", "standard", "--yes")
	initBoth(t, "not-a-repo", notARepo,
		"init", "", "", "--preset", "standard", "--yes")
	initBoth(t, "unknown-flag", emptyRepo,
		"init", "", "", "--bogus")
}
