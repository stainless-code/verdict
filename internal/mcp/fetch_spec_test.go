//go:build !windows

package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func setupMCPSpecRepo(t *testing.T) (root, base, head string) {
	t.Helper()
	root = t.TempDir()
	mcpGit(t, root, "init", "-q")
	mcpGit(t, root, "config", "user.email", "t@example.com")
	mcpGit(t, root, "config", "user.name", "t")
	writeSpecTestFile(t, filepath.Join(root, "app.go"), "package app\n\nfunc v() int { return 1 }\n")
	mcpGit(t, root, "add", "app.go")
	mcpGit(t, root, "commit", "-q", "-m", "base")
	base = mcpGit(t, root, "rev-parse", "HEAD")

	writeSpecTestFile(t, filepath.Join(root, "app.go"), "package app\n\nfunc v() int { return 2 }\n")
	mcpGit(t, root, "add", "app.go")
	mcpGit(t, root, "commit", "-q", "-m", "change")
	head = mcpGit(t, root, "rev-parse", "HEAD")

	writeSpecTestFile(t, filepath.Join(root, "spec.md"), "# Requirements\n- Return 2 from v()\n")
	t.Chdir(root)
	return root, base, head
}

func writeSpecTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestFetchLocalSpecFlagWritesSpecRole proves the MCP fetch-local decoder
// accepts flags.spec and passes it through so the fetched bundle's
// context.json records the source with role "spec".
func TestFetchLocalSpecFlagWritesSpecRole(t *testing.T) {
	root, base, head := setupMCPSpecRepo(t)
	cfg := testConfig()
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"fetch-local","arguments":{"repo":"acme/app","pr":"mcpspec","flags":{"rangeSpec":"` + base + `..` + head + `","spec":["spec.md"]}}}}`
	frames := serve(t, &cfg, call)
	if frames["1"].Error != nil {
		t.Fatalf("tools/call error: %+v", frames["1"].Error)
	}

	raw, err := os.ReadFile(filepath.Join(root, ".verdict", "state", "local-mcpspec", "context.json"))
	if err != nil {
		t.Fatalf("read context.json: %v", err)
	}
	var sources []struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(raw, &sources); err != nil {
		t.Fatalf("unmarshal context.json: %v", err)
	}
	if len(sources) != 1 || sources[0].Role != "spec" {
		t.Fatalf("context.json = %+v, want one source with role %q", sources, "spec")
	}
}
