package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testEnv builds a hermetic getenv over the given values.
func testEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// TestMCPConfigPreview proves the default prints the host snippet and writes
// nothing.
func TestMCPConfigPreview(t *testing.T) {
	t.Chdir(t.TempDir())
	home := t.TempDir()
	out, errOut := mcpTempFiles(t)
	code := run([]string{"mcp", "config", "--agent", "claude"}, out, errOut, testEnv(map[string]string{"HOME": home}), os.ReadFile)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, readOutput(t, errOut))
	}
	snippet := readOutput(t, out)
	for _, want := range []string{`"mcpServers"`, `"verdict"`, `"command": "verdict"`, `"mcp"`} {
		if !strings.Contains(snippet, want) {
			t.Errorf("snippet misses %q:\n%s", want, snippet)
		}
	}
	if _, err := os.Stat(".mcp.json"); !os.IsNotExist(err) {
		t.Error("preview wrote .mcp.json")
	}
}

// TestMCPConfigWrite proves --write creates the cursor config, a second run
// keeps it, and the human status names the outcome.
//
//nolint:revive // the create/keep matrix reads as one assertion block
func TestMCPConfigWrite(t *testing.T) {
	t.Chdir(t.TempDir())
	env := testEnv(map[string]string{"HOME": t.TempDir()})
	out, errOut := mcpTempFiles(t)
	if code := run([]string{"mcp", "config", "--agent", "cursor", "--write"}, out, errOut, env, os.ReadFile); code != 0 {
		t.Fatalf("write exit = %d, stderr = %s", code, readOutput(t, errOut))
	}
	if got := readOutput(t, out); !strings.Contains(got, "create") || !strings.Contains(got, filepath.Join(".cursor", "mcp.json")) {
		t.Errorf("write status = %q", got)
	}
	path := filepath.Join(".cursor", "mcp.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("cursor config is not JSON: %v\n%s", err, raw)
	}
	servers, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers = %v", doc["mcpServers"])
	}
	entry, ok := servers["verdict"].(map[string]any)
	if !ok || entry["command"] != "verdict" {
		t.Fatalf("verdict entry = %v", servers["verdict"])
	}
	out2, errOut2 := mcpTempFiles(t)
	if code := run([]string{"mcp", "config", "--agent", "cursor", "--write"}, out2, errOut2, env, os.ReadFile); code != 0 {
		t.Fatalf("keep exit = %d", code)
	}
	if got := readOutput(t, out2); !strings.Contains(got, "keep") {
		t.Errorf("second write status = %q", got)
	}
}

// TestMCPConfigJSON proves --json prints one envelope with the snippet, path,
// and preview flags, and never prompts.
//
//nolint:revive // envelope assertions read as one block
func TestMCPConfigJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	codexHome := t.TempDir()
	env := testEnv(map[string]string{"HOME": t.TempDir(), "CODEX_HOME": codexHome})
	out, errOut := mcpTempFiles(t)
	code := run([]string{"mcp", "config", "--agent", "codex", "--json"}, out, errOut, env, os.ReadFile)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, readOutput(t, errOut))
	}
	var envelope struct {
		Data struct {
			Agent   string `json:"agent"`
			Path    string `json:"path"`
			Format  string `json:"format"`
			Snippet string `json:"snippet"`
			Status  string `json:"status"`
			DryRun  bool   `json:"dry_run"`
			Changed bool   `json:"changed"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(readOutput(t, out)), &envelope); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	data := envelope.Data
	if data.Agent != "codex" || data.Format != "toml" || data.Status != "preview" || !data.DryRun || data.Changed {
		t.Errorf("data = %+v", data)
	}
	if data.Path != filepath.Join(codexHome, "config.toml") {
		t.Errorf("path = %s", data.Path)
	}
	if !strings.Contains(data.Snippet, "[mcp_servers.verdict]") || !strings.Contains(data.Snippet, `args = ["mcp"]`) {
		t.Errorf("snippet = %q", data.Snippet)
	}
	if _, err := os.Stat(data.Path); !os.IsNotExist(err) {
		t.Error("--json preview wrote the config")
	}
}

// TestMCPConfigUsage proves a missing agent is a usage error.
func TestMCPConfigUsage(t *testing.T) {
	out, errOut := mcpTempFiles(t)
	if code := run([]string{"mcp", "config", "--write"}, out, errOut, os.Getenv, os.ReadFile); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if got := readOutput(t, out); got != "" {
		t.Errorf("stdout = %q", got)
	}
	if got := readOutput(t, errOut); !strings.Contains(got, "--agent") {
		t.Errorf("stderr = %q", got)
	}
}
