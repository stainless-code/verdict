package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core/pack"
	"github.com/stainless-code/verdict/internal/testenv"
)

func mcpOrgGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = testenv.Base(map[string]string{"GIT_AUTHOR_NAME": "pack", "GIT_AUTHOR_EMAIL": "pack@example.invalid", "GIT_COMMITTER_NAME": "pack", "GIT_COMMITTER_EMAIL": "pack@example.invalid"})
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func TestWirePackWithUnsyncedOrgPin(t *testing.T) {
	root := t.TempDir()
	mcpOrgGit(t, root, "init", "-q", "-b", "main")
	cfg := `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"` + strings.Repeat("a", 40) + `","path":"packs/org"}}}`
	if err := os.MkdirAll(filepath.Join(root, ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".verdict", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	mcpOrgGit(t, root, "add", "-A")
	mcpOrgGit(t, root, "commit", "-qm", "base")
	// Change dir to root for RepoRoot
	t.Chdir(root)
	b := &bindings{
		env:  map[string]string{"HOME": t.TempDir()},
		root: root,
	}
	ctx := context.Background()
	req := &pack.Request{List: true}
	if err := wirePack(ctx, b, req); err != nil {
		t.Fatalf("wirePack: %v", err)
	}
	if req.OrgPin == nil {
		t.Fatalf("expected non-nil OrgPin")
	}
	_, ref := invokePack(ctx, req)
	if ref == nil || ref.Code != pack.CodeLockMissing {
		t.Fatalf("invokePack = %v, want pack-lock-missing", ref)
	}
}
