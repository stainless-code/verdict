//go:build !windows

package mcp

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

func mcpGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestVerifyToolGroupEntrypoint proves the MCP verify tool exposes group and
// command in its published schema and that a call executes the explicit
// command with no patch.json, recording a receipt bound to the real head.
// setupMCPGroupRepo builds the committed repository and verified bundle the
// verify tool runs against.
func setupMCPGroupRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mcpGit(t, root, "init", "-q")
	mcpGit(t, root, "config", "user.email", "t@example.com")
	mcpGit(t, root, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(root, "marker.txt"), []byte("root\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mcpGit(t, root, "add", "marker.txt")
	mcpGit(t, root, "commit", "-q", "-m", "c")
	head := mcpGit(t, root, "rev-parse", "HEAD")
	dir := filepath.Join(root, ".verdict", "state", "5201")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"controls.json": `{"schema_version":"1","policy":"head","base_sha":"` + head + `","sha":"","files":[]}`,
		"PR.json":       `{"headRefOid":"` + head + `","state":"OPEN"}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	maxLenses := 1
	rs := &config.RunState{BaseSHA: head, Trusted: true, Resolved: &config.Resolved{Plan: config.Plan{
		Status: config.PendingStatus,
		Groups: []config.PlanGroup{{ID: "group-a1", Label: "deep", Profile: "deep", Depth: "deep", MaxLenses: &maxLenses}},
	}}}
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	t.Chdir(root)
	return head
}

// TestVerifyToolGroupEntrypoint proves the MCP verify tool exposes group and
// command in its published schema and that a call executes the explicit
// command with no patch.json, recording a receipt bound to the real head.
func TestVerifyToolGroupEntrypoint(t *testing.T) {
	head := setupMCPGroupRepo(t)
	cfg := testConfig()
	frames := serve(t, &cfg,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"verify","arguments":{"repo":"acme/app","pr":"5201","timeoutRaw":"30","group":"group-a1","command":"cat marker.txt"}}}`)
	requireVerifySchemaFields(t, frames["1"])
	if frames["2"].Error != nil {
		t.Fatalf("tools/call error: %+v", frames["2"].Error)
	}
	requireMCPReceipt(t, head)
}

// requireVerifySchemaFields asserts the published verify schema names both
// deep-validation arguments.
func requireVerifySchemaFields(t *testing.T, frame frame) {
	t.Helper()
	var list toolsListResult
	if err := json.Unmarshal(frame.Result, &list); err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "verify" {
			requireSchemaProps(t, tool)
			return
		}
	}
	t.Fatalf("verify tool is not registered: %+v", list.Tools)
}

// requireSchemaProps asserts one tool's schema names group and command.
func requireSchemaProps(t *testing.T, tool toolDescriptor) {
	t.Helper()
	props, ok := tool.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("verify schema properties = %T, want an object", tool.InputSchema["properties"])
	}
	for _, name := range []string{"group", "command"} {
		if _, ok := props[name]; !ok {
			t.Fatalf("verify schema lacks %s: %+v", name, props)
		}
	}
}

// requireMCPReceipt asserts no patch was fabricated and the tool recorded a
// passing receipt bound to the real head and group.
func requireMCPReceipt(t *testing.T, head string) {
	t.Helper()
	dir := filepath.Join(".verdict", "state", "5201")
	if _, err := os.Stat(filepath.Join(dir, "patch.json")); !os.IsNotExist(err) {
		t.Fatalf("MCP group mode fabricated patch.json")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "validation-group-a1.json"))
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	var receipt core.ValidationReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.Passed || receipt.Status != "passed" || receipt.ExecutionHead != head || receipt.Group != "group-a1" {
		t.Fatalf("receipt = %+v", receipt)
	}
}
