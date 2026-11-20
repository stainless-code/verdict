package skill

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

func TestCatalogParity(t *testing.T) {
	names := []string{"agents", "claude", "codex", "cursor", "opencode", "copilot", "gemini", "goose", "qwen", "windsurf"}
	if !slices.Equal(supportedAgents(), names) {
		t.Fatalf("names=%v want %v", supportedAgents(), names)
	}
	dirs := []string{".agents/skills", ".claude/skills", ".codex/skills", ".cursor/skills", ".config/opencode/skills", ".copilot/skills", ".gemini/skills", ".config/goose/skills", ".qwen/skills", ".codeium/windsurf/skills"}
	req := Request{Runtime: &core.Runtime{Env: map[string]string{"HOME": "root"}}}
	for i, n := range names {
		got, ok := req.agentDir(n)
		if !ok || filepath.ToSlash(got) != "root/"+dirs[i] {
			t.Errorf("%s dir=%q known=%v", n, got, ok)
		}
	}
}
