package pack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/testenv"
)

func orgGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = repo
	cmd.Env = testenv.Base(map[string]string{
		"GIT_AUTHOR_NAME":     "pack",
		"GIT_AUTHOR_EMAIL":    "pack@example.invalid",
		"GIT_COMMITTER_NAME":  "pack",
		"GIT_COMMITTER_EMAIL": "pack@example.invalid",
	})
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	_ = strings.TrimSpace(string(out))
}

func TestPackListRefusesUnsyncedOrgPin(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	orgGit(t, root, "init", "-q", "-b", "main")
	cfg := `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"` + strings.Repeat("a", 40) + `","path":"packs/org"}}}`
	if err := os.MkdirAll(filepath.Join(root, ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".verdict", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	orgGit(t, root, "add", "-A")
	orgGit(t, root, "commit", "-qm", "base config")
	var out, diag strings.Builder
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, JSONMode: true, Cmd: "pack", Now: func() time.Time { return time.Unix(0, 0) }}
	code := Run(t.Context(), r, []string{"list"}, Request{
		Runtime:  &core.Runtime{Env: map[string]string{"HOME": t.TempDir()}},
		RepoRoot: root,
	})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d; out=%s", code, cli.ExitRefused, out.String())
	}
	if !strings.Contains(out.String(), "pack-lock-missing") {
		t.Fatalf("envelope = %s, want pack-lock-missing", out.String())
	}
}
