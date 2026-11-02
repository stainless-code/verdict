package mcpconfig

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testRequest builds a preview/write request for one agent.
func testRequest(t *testing.T, agent string, write bool) *Request {
	t.Helper()
	return &Request{Agent: agent, Write: write, Cwd: t.TempDir(), Env: map[string]string{"HOME": t.TempDir()}}
}

// TestSnippets proves each host gets its exact documented entry.
//
//nolint:revive // the per-host matrix reads as one assertion block
func TestSnippets(t *testing.T) {
	t.Parallel()
	cases := []struct {
		agent  string
		format string
		want   []string
	}{
		{AgentClaude, FormatJSON, []string{`"mcpServers"`, `"verdict"`, `"command": "verdict"`, `"mcp"`}},
		{AgentCursor, FormatJSON, []string{`"mcpServers"`, `"verdict"`, `"command": "verdict"`}},
		{AgentCodex, FormatTOML, []string{"[mcp_servers.verdict]", `command = "verdict"`, `args = ["mcp"]`}},
	}
	for _, tc := range cases {
		req := testRequest(t, tc.agent, false)
		res, ref := Run(req)
		if ref != nil {
			t.Fatalf("%s: %v", tc.agent, ref)
		}
		if res.Format != tc.format || res.Status != StatusPreview {
			t.Errorf("%s: format=%q status=%q", tc.agent, res.Format, res.Status)
		}
		for _, want := range tc.want {
			if !strings.Contains(res.Snippet, want) {
				t.Errorf("%s snippet misses %q:\n%s", tc.agent, want, res.Snippet)
			}
		}
		if tc.format == FormatJSON {
			checkJSONSnippet(t, tc.agent, res.Snippet)
		}
		if _, err := os.Stat(res.Path); !os.IsNotExist(err) {
			t.Errorf("%s preview wrote %s", tc.agent, res.Path)
		}
	}
}

// checkJSONSnippet proves the snippet nests mcpServers.verdict, not just the
// command string.
func checkJSONSnippet(t *testing.T, agent, snippet string) {
	t.Helper()
	var doc struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(snippet), &doc); err != nil {
		t.Fatalf("%s snippet is not JSON: %v\n%s", agent, err, snippet)
	}
	server, ok := doc.Servers["verdict"]
	if !ok || server.Command != "verdict" || len(server.Args) != 1 || server.Args[0] != "mcp" {
		t.Fatalf("%s snippet entry = %+v\n%s", agent, doc.Servers, snippet)
	}
}

// runPath resolves one host's path, failing on a refusal.
func runPath(t *testing.T, req *Request) string {
	t.Helper()
	res, ref := Run(req)
	if ref != nil {
		t.Fatalf("%s: %v", req.Agent, ref)
	}
	return res.Path
}

// TestPaths proves the documented config location per host.
//
//nolint:revive // the per-host matrix reads as one assertion block
func TestPaths(t *testing.T) {
	t.Parallel()
	claude := testRequest(t, AgentClaude, false)
	if got := runPath(t, claude); got != filepath.Join(claude.Cwd, ".mcp.json") {
		t.Errorf("claude path = %s", got)
	}
	cursor := testRequest(t, AgentCursor, false)
	if got := runPath(t, cursor); got != filepath.Join(cursor.Cwd, ".cursor", "mcp.json") {
		t.Errorf("cursor path = %s", got)
	}
	codex := testRequest(t, AgentCodex, false)
	codex.Env["CODEX_HOME"] = filepath.Join(codex.Cwd, "codex-home")
	if got := runPath(t, codex); got != filepath.Join(codex.Env["CODEX_HOME"], "config.toml") {
		t.Errorf("codex path = %s", got)
	}
	codex.Env["CODEX_HOME"] = ""
	codex.Env["HOME"] = filepath.Join(codex.Cwd, "home")
	if got := runPath(t, codex); got != filepath.Join(codex.Env["HOME"], ".codex", "config.toml") {
		t.Errorf("codex HOME path = %s", got)
	}
	codex.Env["HOME"] = ""
	if _, ref := Run(codex); ref == nil || ref.Code != CodeNoHome {
		t.Errorf("codex without HOME = %v", ref)
	}
	if _, ref := Run(&Request{Agent: "sublime"}); ref == nil || ref.Code != CodeUnknownAgent {
		t.Errorf("unknown agent = %v", ref)
	}
}

// TestJSONMerge proves create, idempotent keep, unknown-key preservation,
// and the backup-before-update contract.
//
//nolint:revive,cyclop // create/keep reads as one assertion block
func TestJSONMerge(t *testing.T) {
	t.Parallel()
	req := testRequest(t, AgentClaude, true)
	req.Cwd = t.TempDir()
	path := filepath.Join(req.Cwd, ".mcp.json")
	seed := `{"zeta":1,"mcpServers":{"other":{"command":"x"}}}` + "\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	res, ref := Run(req)
	if ref != nil {
		t.Fatalf("create: %v", ref)
	}
	if res.Status != StatusUpdate || !res.Changed || res.Backup != path+BackupSuffix {
		t.Fatalf("create result = %+v", res)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"zeta"`, `"other"`, `"verdict"`, `"command": "verdict"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("merged JSON misses %q:\n%s", want, raw)
		}
	}
	backup, err := os.ReadFile(res.Backup)
	if err != nil || string(backup) != seed {
		t.Errorf("backup = %q %v", backup, err)
	}
	again, ref := Run(req)
	if ref != nil {
		t.Fatalf("keep: %v", ref)
	}
	if again.Status != StatusKeep || again.Changed {
		t.Errorf("second write = %+v, want keep", again)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, raw) {
		t.Errorf("keep rewrote the file")
	}
}

// TestJSONCreate proves a missing file is created with the entry.
func TestJSONCreate(t *testing.T) {
	t.Parallel()
	req := testRequest(t, AgentCursor, true)
	res, ref := Run(req)
	if ref != nil {
		t.Fatalf("create: %v", ref)
	}
	if res.Status != StatusCreate || !res.Changed || res.Backup != "" {
		t.Errorf("create result = %+v", res)
	}
	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"mcpServers"`) {
		t.Errorf("created config = %s", raw)
	}
}

// TestJSONRefusals proves unparseable and foreign configs stay untouched.
//
//nolint:revive // the refusal matrix reads as one assertion block
func TestJSONRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		body string
		code string
	}{
		{"unparseable", `{"mcpServers":`, CodeUnparseable},
		{"array", `[]`, CodeUnparseable},
		{"servers-not-object", `{"mcpServers":[]}`, CodeUnparseable},
		{"foreign-entry", `{"mcpServers":{"verdict":{"command":"other","args":["mcp"]}}}`, CodeForeign},
		{"foreign-args", `{"mcpServers":{"verdict":{"command":"verdict","args":["mcp","--read-only"]}}}`, CodeForeign},
	} {
		req := testRequest(t, AgentClaude, true)
		req.Cwd = t.TempDir()
		path := filepath.Join(req.Cwd, ".mcp.json")
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, ref := Run(req)
		if ref == nil || ref.Code != tc.code {
			t.Errorf("%s: ref = %v, want %s", tc.name, ref, tc.code)
		}
		raw, err := os.ReadFile(path)
		if err != nil || string(raw) != tc.body {
			t.Errorf("%s: file changed to %q", tc.name, raw)
		}
		if _, err := os.Stat(path + BackupSuffix); !os.IsNotExist(err) {
			t.Errorf("%s: refusal wrote a backup", tc.name)
		}
	}
}

// TestTOMLMerge proves append, idempotent keep, missing-key fill, and the
// key-preserving merge.
//
//nolint:revive // append/keep reads as one assertion block
func TestTOMLMerge(t *testing.T) {
	t.Parallel()
	req := testRequest(t, AgentCodex, true)
	req.Cwd = t.TempDir()
	req.Env["CODEX_HOME"] = filepath.Join(req.Cwd, "codex")
	path := filepath.Join(req.Env["CODEX_HOME"], "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "model = \"gpt\"\n\n[other]\nkey = 1\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	res, ref := Run(req)
	if ref != nil {
		t.Fatalf("append: %v", ref)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`model = "gpt"`, "[other]", "[mcp_servers.verdict]", `command = "verdict"`, `args = ["mcp"]`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("merged TOML misses %q:\n%s", want, raw)
		}
	}
	if res.Backup == "" {
		t.Error("append did not back up the previous file")
	}
	again, ref := Run(req)
	if ref != nil || again.Status != StatusKeep {
		t.Fatalf("keep: %+v %v", again, ref)
	}
}

// TestTOMLFillMissing proves an existing canonical table keeps its other
// keys while the missing command/args pair is inserted.
func TestTOMLFillMissing(t *testing.T) {
	t.Parallel()
	req := testRequest(t, AgentCodex, true)
	req.Cwd = t.TempDir()
	req.Env["CODEX_HOME"] = req.Cwd
	path := filepath.Join(req.Cwd, "config.toml")
	seed := "[mcp_servers.verdict]\nstartup_timeout_sec = 20\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ref := Run(req); ref != nil {
		t.Fatalf("fill: %v", ref)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"startup_timeout_sec = 20", `command = "verdict"`, `args = ["mcp"]`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("filled TOML misses %q:\n%s", want, raw)
		}
	}
	if strings.Count(string(raw), "[mcp_servers.verdict]") != 1 {
		t.Errorf("duplicate table:\n%s", raw)
	}
}

// TestTOMLRefusals proves a foreign table and a non-table spelling are
// refused instead of guessed at.
//
//nolint:revive // the refusal matrix reads as one assertion block
func TestTOMLRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		body string
		code string
	}{
		{"foreign-command", "[mcp_servers.verdict]\ncommand = \"other\"\nargs = [\"mcp\"]\n", CodeForeign},
		{"foreign-args", "[mcp_servers.verdict]\ncommand = \"verdict\"\nargs = [\"nope\"]\n", CodeForeign},
		{"inline", "mcp_servers.verdict = { command = \"verdict\" }\n", CodeUnmergeable},
	} {
		req := testRequest(t, AgentCodex, true)
		req.Cwd = t.TempDir()
		req.Env["CODEX_HOME"] = req.Cwd
		path := filepath.Join(req.Cwd, "config.toml")
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, ref := Run(req)
		if ref == nil || ref.Code != tc.code {
			t.Errorf("%s: ref = %v, want %s", tc.name, ref, tc.code)
		}
		raw, err := os.ReadFile(path)
		if err != nil || string(raw) != tc.body {
			t.Errorf("%s: file changed to %q", tc.name, raw)
		}
	}
}
