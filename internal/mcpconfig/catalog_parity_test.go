package mcpconfig

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/agentcatalog"
)

func TestCatalogParity(t *testing.T) {
	names := []string{"claude", "cursor", "codex", "gemini", "qwen", "windsurf", "opencode", "goose", "copilot"}
	accepted := []string{}
	for _, row := range agentcatalog.All() {
		if row.MCP != nil {
			accepted = append(accepted, row.Name)
		}
	}
	slices.Sort(accepted)
	sorted := slices.Clone(names)
	slices.Sort(sorted)
	if !slices.Equal(accepted, sorted) {
		t.Fatalf("names=%v want %v", accepted, sorted)
	}
	paths := []string{".mcp.json", ".cursor/mcp.json", ".codex/config.toml", ".gemini/settings.json", ".qwen/settings.json", ".codeium/windsurf/mcp_config.json", "opencode.json", ".config/goose/config.yaml", ".vscode/mcp.json"}
	for i, name := range names {
		checkCatalogParity(t, name, paths[i], catalogSnippets[i])
	}
}

func checkCatalogParity(t *testing.T, name, path, want string) {
	t.Helper()
	r, ref := Run(&Request{Agent: name, Cwd: "root", Env: map[string]string{"HOME": "root"}})
	if ref != nil {
		t.Fatal(ref)
	}
	if got := filepath.ToSlash(r.Path); got != "root/"+path {
		t.Errorf("%s path=%s", name, got)
	}
	if r.Snippet != want {
		t.Errorf("%s snippet=%q want %q", name, r.Snippet, want)
	}
}

var catalogSnippets = []string{
	"{\n  \"mcpServers\": {\n    \"verdict\": {\n      \"command\": \"verdict\",\n      \"args\": [\n        \"mcp\"\n      ]\n    }\n  }\n}\n",
	"{\n  \"mcpServers\": {\n    \"verdict\": {\n      \"command\": \"verdict\",\n      \"args\": [\n        \"mcp\"\n      ]\n    }\n  }\n}\n",
	"[mcp_servers.verdict]\ncommand = \"verdict\"\nargs = [\"mcp\"]\n",
	"{\n  \"mcpServers\": {\n    \"verdict\": {\n      \"command\": \"verdict\",\n      \"args\": [\n        \"mcp\"\n      ]\n    }\n  }\n}\n",
	"{\n  \"mcpServers\": {\n    \"verdict\": {\n      \"command\": \"verdict\",\n      \"args\": [\n        \"mcp\"\n      ]\n    }\n  }\n}\n",
	"{\n  \"mcpServers\": {\n    \"verdict\": {\n      \"command\": \"verdict\",\n      \"args\": [\n        \"mcp\"\n      ]\n    }\n  }\n}\n",
	"{\n  \"mcp\": {\n    \"verdict\": {\n      \"type\": \"local\",\n      \"command\": [\n        \"verdict\",\n        \"mcp\"\n      ]\n    }\n  }\n}\n",
	"extensions:\n  verdict:\n    enabled: true\n    type: stdio\n    cmd: verdict\n    args: [mcp]\n",
	"{\n  \"servers\": {\n    \"verdict\": {\n      \"type\": \"stdio\",\n      \"command\": \"verdict\",\n      \"args\": [\n        \"mcp\"\n      ]\n    }\n  }\n}\n",
}
