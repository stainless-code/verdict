package readgolden_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/golden"
	"github.com/stainless-code/verdict/internal/testenv"
)

// backtestBase is the canonical golden fixture's window start. The fixture
// pins identity and dates through testenv, so the commit ids are literals.
const backtestBase = "84b31730499aa237889660981350fda2b3edc91d"

// TestBacktestGolden pins `backtest mine` on the fixed merged-PR fixture: the
// envelope, the human rendering, and the deterministic mine.json. run.json is
// omitted because its timestamps and git version are machine facts, not the
// artifact contract; TestMineArtifacts pins its shape.
func TestBacktestGolden(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T, dir, home string) {
		t.Helper()
		runGitEnv(t, dir, home, "init", "-q", "-b", "main")
		writeFiles(t, dir, map[string]string{
			"main.go": "package main\n\nimport \"os\"\n\nfunc main() {\n\tos.Remove(\"out.txt\")\n}\n",
		})
		runGitDated(t, dir, home, "2024-01-01T00:00:00Z", "add", "-A")
		runGitDated(t, dir, home, "2024-01-01T00:00:00Z", "commit", "-qm", "feat: base")
		runGitEnv(t, dir, home, "switch", "-qc", "feature")
		writeFiles(t, dir, map[string]string{
			"main.go": "package main\n\nimport \"os\"\n\nfunc main() {\n\tos.Remove(\"missing\")\n}\n",
		})
		runGitDated(t, dir, home, "2024-01-02T00:00:00Z", "add", "-A")
		runGitDated(t, dir, home, "2024-01-02T00:00:00Z", "commit", "-qm", "feat: remove the output")
		runGitEnv(t, dir, home, "switch", "-q", "main")
		runGitDated(t, dir, home, "2024-01-03T00:00:00Z", "merge", "--no-ff", "feature", "-m", "Merge pull request #2 from acme/feature")
		writeFiles(t, dir, map[string]string{
			"main.go": "package main\n\nimport \"os\"\n\nfunc main() {\n\tif err := os.Remove(\"missing\"); err != nil {\n\t\tpanic(err)\n\t}\n}\n",
		})
		runGitDated(t, dir, home, "2024-01-04T00:00:00Z", "add", "-A")
		runGitDated(t, dir, home, "2024-01-04T00:00:00Z", "commit", "-qm", "fix: guard the removal")
	}
	omit := func(key string) bool { return strings.HasSuffix(key, "/run.json") }
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"backtest/mine-json", []string{"backtest", "mine", "--since", backtestBase, "--json"}},
		{"backtest/mine-human", []string{"backtest", "mine", "--since", backtestBase}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := &golden.Case{Name: tc.name, Setup: setup, Args: tc.args, Omit: omit}
			c.Run(t, harness)
		})
	}
}

// runGitEnv runs one fixture git command with the pinned identity and an
// isolated HOME.
func runGitEnv(t *testing.T, dir, home string, args ...string) {
	t.Helper()
	runGit(t, dir, home, "", args...)
}

// runGitDated runs one fixture git command with a pinned commit date.
func runGitDated(t *testing.T, dir, home, date string, args ...string) {
	t.Helper()
	runGit(t, dir, home, date, args...)
}

// runGit runs one fixture git command; an empty date keeps testenv's identity
// pinning but sets no clock.
func runGit(t *testing.T, dir, home, date string, args ...string) {
	t.Helper()
	env := map[string]string{"HOME": home}
	if date != "" {
		env["GIT_AUTHOR_DATE"] = date
		env["GIT_COMMITTER_DATE"] = date
	}
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = testenv.Base(env)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
