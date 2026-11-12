package backtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestComposedRefusesMissingAgentBeforeMining proves the composed form refuses
// an absent --agent before the miner runs. A guard moved after mining still
// refuses, so only the absent run tree shows that nothing was spent.
func TestComposedRefusesMissingAgentBeforeMining(t *testing.T) {
	root := gitCheckout(t)
	var diag strings.Builder
	r := &cli.Runner{
		Cmd: "backtest", Out: &diag, Human: &diag, Diag: &diag,
		Now: time.Now, Effective: cli.Defaults(), EnvNoColor: true,
	}
	code := Run(t.Context(), r, []string{"--since", "90d"},
		Request{Env: map[string]string{"HOME": t.TempDir()}, Root: root, RepoRoot: root})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d; output:\n%s", code, cli.ExitRefused, diag.String())
	}
	if !strings.Contains(diag.String(), "--agent <name>") {
		t.Errorf("refusal does not name --agent:\n%s", diag.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".verdict")); !os.IsNotExist(err) {
		t.Errorf("the refused composed run left a .verdict tree: %v", err)
	}
}

// gitCheckout is one hermetic repository with a single commit, so a miner run
// against it would write a run tree.
func gitCheckout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		cmd.Env = testenv.Base(map[string]string{
			"GIT_AUTHOR_DATE":    "2024-01-01T00:00:00Z",
			"GIT_COMMITTER_DATE": "2024-01-01T00:00:00Z",
		})
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("add", "a.txt")
	git("commit", "-qm", "feat: base")
	return dir
}
