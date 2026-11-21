// Package scaffold_test holds the golden contract for the Go init verb:
// the shared harness runs the Go binary on hermetic fixtures and pins
// exit codes, stdout/stderr, and every written file against checked-in
// goldens,
// including the preset matrix, the overwrite guard, and the check-tree
// validation.
package scaffold_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/golden"
	"github.com/stainless-code/verdict/internal/testenv"
)

var harness *golden.Harness

// TestMain builds the Go binary once and runs the golden suite.
func TestMain(m *testing.M) {
	os.Exit(golden.RunMain(m, &harness))
}

// initBoth runs one init case with dispatch-faithful args.
func initBoth(t *testing.T, name string, setup func(t *testing.T, dir, home string), args ...string) {
	t.Helper()
	cliArgs := append([]string{args[0]}, args[3:]...)
	golden.Pin(t, harness, name, setup, nil, cliArgs...)
}

// mkRepo initializes a clean git repo sandbox.
func mkRepo(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "probe.txt"), []byte("probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-qm", "init")
}

// gitRun runs one git command in the case sandbox with a pinned identity.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = testenv.Base(map[string]string{
		"GIT_AUTHOR_NAME":     "init",
		"GIT_AUTHOR_EMAIL":    "init@eval",
		"GIT_COMMITTER_NAME":  "init",
		"GIT_COMMITTER_EMAIL": "init@eval",
	})
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// emptyRepo is the empty clean-repo fixture.
func emptyRepo(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
}

// staleConfig is a divergent typed control file: valid JSON, wrong preset.
const staleConfig = "{\"schema_version\":\"1\",\"defaults\":{\"budget_usd\":9.99}}\n"

// seedOverwrite plants divergent control files for the overwrite guard.
func seedOverwrite(t *testing.T, dir string) {
	t.Helper()
	seedFiles(t, dir, map[string]string{
		"config.json": staleConfig,
		"kb.jsonl":    "{\"id\":\"old\"}\n",
	})
}

// seedBudget plants one divergent control file.
func seedBudget(t *testing.T, dir string) {
	t.Helper()
	seedFiles(t, dir, map[string]string{"config.json": staleConfig})
}

// seedFiles writes divergent control files under .verdict/.
func seedFiles(t *testing.T, dir string, seed map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	for f, c := range seed {
		if err := os.WriteFile(filepath.Join(dir, ".verdict", f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// seedStaleWorkflow commits a divergent workflow, so the overwrite guard has a
// user-authored file to refuse.
func seedStaleWorkflow(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	path := filepath.Join(dir, ".github", "workflows", "verdict-review.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# stale workflow\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-qm", "seed stale workflow")
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

// TestInitGolden proves byte parity across presets, the overwrite guard,
// idempotency, validation edges, and the generated every-PR workflow.
func TestInitGolden(t *testing.T) {
	testPresetMatrix(t)
	testOverwriteGuard(t)
	testTreeEdges(t)
	testGeneratedWorkflow(t)
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

// testGeneratedWorkflow covers --ci github: create, refuse, force, and both
// --print shapes, each of which must leave the tree untouched when it only
// renders.
func testGeneratedWorkflow(t *testing.T) {
	t.Helper()
	initBoth(t, "ci-github-create", emptyRepo,
		"init", "", "", "--ci", "github")
	initBoth(t, "ci-github-refuse", seedStaleWorkflow,
		"init", "", "", "--ci", "github")
	initBoth(t, "ci-github-force", seedStaleWorkflow,
		"init", "", "", "--ci", "github", "--force")
	initBoth(t, "ci-github-print", emptyRepo,
		"init", "", "", "--ci", "github", "--print")
	initBoth(t, "ci-github-print-json", emptyRepo,
		"init", "", "", "--ci", "github", "--print", "--json")
}
