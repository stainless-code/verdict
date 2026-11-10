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
		{"foreign-args", `{"mcpServers":{"verdict":{"command":"verdict","args":["mcp","--bogus"]}}}`, CodeForeign},
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
		{"header-opens-basic", "[mcp_servers.verdict]\"\ncommand = \"verdict\"\nargs = [\"mcp\"]\n\"\n", CodeUnmergeable},
		{"header-opens-literal", "[mcp_servers.verdict]'''\ntext\n'''\n", CodeUnmergeable},
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

// seedCodex writes body into a fresh CODEX_HOME config.toml, the path a Codex
// merge reads and rewrites.
func seedCodex(t *testing.T, body string) (*Request, string) {
	t.Helper()
	req := testRequest(t, AgentCodex, true)
	req.Env["CODEX_HOME"] = req.Cwd
	path := filepath.Join(req.Cwd, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return req, path
}

// TOML keeps a triple-quoted block open across newlines, so a file that ends
// inside one has no line outside a string for the table to land on.
//
//nolint:revive // the refusal matrix reads as one assertion block
func TestTOMLUnterminatedRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		body string
	}{
		{"basic at EOF", "note = \"\"\"\nhello\n"},
		{"literal at EOF", "note = '''\nhello\n"},
		{"literal inside the table", "[mcp_servers.verdict]\nnote = '''\ntext\n"},
		{"after a complete table", "[mcp_servers.verdict]\ncommand = \"verdict\"\nargs = [\"mcp\"]\nnote = \"\"\"\n"},
		{"basic string never closed", "note = \"hello\n"},
		{"CRLF", "note = \"\"\"\r\nhello\r\n"},
	} {
		req, path := seedCodex(t, tc.body)
		_, ref := Run(req)
		if ref == nil || ref.Code != CodeUnmergeable {
			t.Fatalf("%s: ref = %v, want %s", tc.name, ref, CodeUnmergeable)
		}
		if !strings.Contains(ref.Message, "string") {
			t.Errorf("%s: message = %q; want it to name the unterminated string", tc.name, ref.Message)
		}
		if raw, err := os.ReadFile(path); err != nil || string(raw) != tc.body {
			t.Errorf("%s: file changed to %q", tc.name, raw)
		}
		if _, err := os.Stat(path + BackupSuffix); !os.IsNotExist(err) {
			t.Errorf("%s: refusal wrote a backup", tc.name)
		}
	}
}

// TOML reads a header only at the start of a line outside a string, so one
// spelled inside a string or a comment names no table.
//
//nolint:revive // the header matrix reads as one assertion block
func TestTOMLHeaderInsideString(t *testing.T) {
	t.Parallel()
	table := "[mcp_servers.verdict]\ncommand = \"verdict\"\nargs = [\"mcp\"]\n"
	for _, tc := range []struct {
		name string
		body string
	}{
		{"complete table in a literal", "note = '''\n" + table + "'''\n"},
		{"header in a multiline basic", "note = \"\"\"\n[mcp_servers.verdict]\ntext\n\"\"\"\n"},
		{"header in a basic string", "note = \"see [mcp_servers.verdict]\"\n"},
		{"header in a comment", "# [mcp_servers.verdict]\nmodel = \"gpt\"\n"},
	} {
		req, path := seedCodex(t, tc.body)
		res, ref := Run(req)
		if ref != nil {
			t.Fatalf("%s: %v", tc.name, ref)
		}
		if res.Status != StatusUpdate {
			t.Fatalf("%s: status = %q, want %s", tc.name, res.Status, StatusUpdate)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(string(raw), "\n\n"+table) {
			t.Errorf("%s: table not appended after the string:\n%s", tc.name, raw)
		}
		again, ref := Run(req)
		if ref != nil || again.Status != StatusKeep {
			t.Errorf("%s: second run = %+v %v, want keep", tc.name, again, ref)
		}
	}
}

// A literal's own lines are not code, so a fake header before the real table
// neither starts the table's body nor receives the missing keys.
func TestTOMLFillAfterAFakeHeader(t *testing.T) {
	t.Parallel()
	body := "note = '''\n[mcp_servers.verdict]\n'''\n\n[mcp_servers.verdict]\nstartup_timeout_sec = 20\n"
	req, path := seedCodex(t, body)
	if _, ref := Run(req); ref != nil {
		t.Fatalf("fill: %v", ref)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "note = '''\n[mcp_servers.verdict]\n'''\n\n[mcp_servers.verdict]\ncommand = \"verdict\"\nargs = [\"mcp\"]\nstartup_timeout_sec = 20\n"
	if string(raw) != want {
		t.Errorf("filled TOML =\n%s\nwant\n%s", raw, want)
	}
	if again, ref := Run(req); ref != nil || again.Status != StatusKeep {
		t.Errorf("second run = %+v %v, want keep", again, ref)
	}
}

// A string value that spells the server's name is prose, not a definition, so
// it neither refuses the merge nor counts as the entry.
//
//nolint:revive // the mention matrix reads as one assertion block
func TestTOMLMentionsOutsideCode(t *testing.T) {
	t.Parallel()
	table := "[mcp_servers.verdict]\ncommand = \"verdict\"\nargs = [\"mcp\"]\n"
	for _, tc := range []struct {
		name string
		body string
	}{
		{"basic string", "note = \"mcp_servers.verdict = { command = \\\"verdict\\\" }\"\n"},
		{"literal string", "note = 'mcp_servers.verdict = { command = \"verdict\" }'\n"},
		{"multiline basic", "note = \"\"\"\nmcp_servers.verdict.command = \"x\"\n\"\"\"\n"},
		{"comment", "# mcp_servers.verdict = { command = \"verdict\" }\nmodel = \"gpt\"\n"},
		{"comment with a triple quote", "# note = \"\"\"\nmodel = \"gpt\"\n"},
	} {
		req, path := seedCodex(t, tc.body)
		res, ref := Run(req)
		if ref != nil {
			t.Fatalf("%s: %v", tc.name, ref)
		}
		if res.Status != StatusUpdate {
			t.Fatalf("%s: status = %q, want %s", tc.name, res.Status, StatusUpdate)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), table) {
			t.Errorf("%s: no table appended:\n%s", tc.name, raw)
		}
		if again, ref := Run(req); ref != nil || again.Status != StatusKeep {
			t.Errorf("%s: second run = %+v %v, want keep", tc.name, again, ref)
		}
	}
}

// A CRLF file keeps its line endings through the append, the fill and the keep
// that follows, so the host never reads a half-converted file.
//
//nolint:revive // the append/fill matrix reads as one assertion block
func TestTOMLCRLF(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			"append",
			"model = \"gpt\"\r\n\r\n[other]\r\nkey = 1\r\n",
			"[mcp_servers.verdict]\r\ncommand = \"verdict\"\r\nargs = [\"mcp\"]\r\n",
		},
		{
			"fill",
			"[mcp_servers.verdict]\r\nstartup_timeout_sec = 20\r\n",
			"command = \"verdict\"\r\nargs = [\"mcp\"]\r\nstartup_timeout_sec = 20\r\n",
		},
	} {
		req, path := seedCodex(t, tc.body)
		if _, ref := Run(req); ref != nil {
			t.Fatalf("%s: %v", tc.name, ref)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(string(raw), tc.want) {
			t.Errorf("%s: TOML misses %q:\n%q", tc.name, tc.want, raw)
		}
		if strings.Contains(strings.ReplaceAll(string(raw), "\r\n", ""), "\n") {
			t.Errorf("%s: mixed line endings:\n%q", tc.name, raw)
		}
		again, ref := Run(req)
		if ref != nil || again.Status != StatusKeep {
			t.Fatalf("%s: second run = %+v %v, want keep", tc.name, again, ref)
		}
		if kept, err := os.ReadFile(path); err != nil || !bytes.Equal(kept, raw) {
			t.Errorf("%s: keep rewrote the file", tc.name)
		}
	}
}

// TOML lets the last line end at EOF, so a file without a final newline still
// has a whole last line and the table appends after it.
func TestTOMLAppendWithoutFinalNewline(t *testing.T) {
	t.Parallel()
	req, path := seedCodex(t, "model = \"gpt\"")
	if _, ref := Run(req); ref != nil {
		t.Fatalf("append: %v", ref)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "\n[mcp_servers.verdict]\ncommand = \"verdict\"\nargs = [\"mcp\"]\n") {
		t.Errorf("appended TOML = %q", raw)
	}
	if again, ref := Run(req); ref != nil || again.Status != StatusKeep {
		t.Errorf("second run = %+v %v, want keep", again, ref)
	}
}

// A file that already carries the entry keeps byte-identical, final newline or
// not.
func TestTOMLKeepWithoutFinalNewline(t *testing.T) {
	t.Parallel()
	body := "[mcp_servers.verdict]\ncommand = \"verdict\"\nargs = [\"mcp\"]"
	req, path := seedCodex(t, body)
	res, ref := Run(req)
	if ref != nil || res.Status != StatusKeep {
		t.Fatalf("keep = %+v %v", res, ref)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != body {
		t.Errorf("keep rewrote the file to %q", raw)
	}
}

// A fill leaves the file with a final newline, so the next line the operator
// adds starts where TOML expects it.
func TestTOMLFillWithoutFinalNewline(t *testing.T) {
	t.Parallel()
	req, path := seedCodex(t, "[mcp_servers.verdict]\nstartup_timeout_sec = 20")
	if _, ref := Run(req); ref != nil {
		t.Fatalf("fill: %v", ref)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "[mcp_servers.verdict]\ncommand = \"verdict\"\nargs = [\"mcp\"]\nstartup_timeout_sec = 20\n" {
		t.Errorf("filled TOML = %q", raw)
	}
}

// A line holding only Unicode whitespace is operator content TOML would reject,
// not a blank line to collapse: the append keeps every byte of it.
func TestTOMLUnicodeSpaceKept(t *testing.T) {
	t.Parallel()
	body := "\u2004"
	req, path := seedCodex(t, body)
	if _, ref := Run(req); ref != nil {
		t.Fatalf("append: %v", ref)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := body + "\n\n[mcp_servers.verdict]\ncommand = \"verdict\"\nargs = [\"mcp\"]\n"
	if string(raw) != want {
		t.Errorf("appended TOML = %q, want %q", raw, want)
	}
	if again, ref := Run(req); ref != nil || again.Status != StatusKeep {
		t.Errorf("second run = %+v %v, want keep", again, ref)
	}
}

// A missing Codex config is created with exactly the documented entry.
func TestTOMLCreate(t *testing.T) {
	t.Parallel()
	req := testRequest(t, AgentCodex, true)
	req.Env["CODEX_HOME"] = req.Cwd
	res, ref := Run(req)
	if ref != nil {
		t.Fatalf("create: %v", ref)
	}
	if res.Status != StatusCreate || !res.Changed || res.Backup != "" {
		t.Fatalf("create result = %+v", res)
	}
	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != res.Snippet {
		t.Errorf("created config = %q, want the snippet %q", raw, res.Snippet)
	}
}

// A table that already carries the entry keeps its own keys and its bytes.
func TestTOMLKeepExtraKeys(t *testing.T) {
	t.Parallel()
	body := "[mcp_servers.verdict]\nstartup_timeout_sec = 20\ncommand = \"verdict\"\nargs = [\"mcp\"]\n"
	req, path := seedCodex(t, body)
	res, ref := Run(req)
	if ref != nil || res.Status != StatusKeep || res.Changed {
		t.Fatalf("keep = %+v %v", res, ref)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != body {
		t.Errorf("keep rewrote the file to %q", raw)
	}
	if _, err := os.Stat(path + BackupSuffix); !os.IsNotExist(err) {
		t.Error("keep wrote a backup")
	}
}

// A closed literal is string content wherever it sits, so it neither hides a
// header nor blocks the append that follows it.
//
//nolint:revive // the append/keep sequence reads as one assertion block
func TestTOMLMultiLineStringElsewhere(t *testing.T) {
	t.Parallel()
	body := "developer_instructions = \"\"\"\nbe careful\n\"\"\"\n\n[other]\nkey = 1\n"
	req, path := seedCodex(t, body)
	if res, ref := Run(req); ref != nil {
		t.Fatalf("append beside a literal: %v", ref)
	} else if res.Status == StatusKeep {
		t.Errorf("status = %q; want the table appended", res.Status)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"developer_instructions = \"\"\"", "[mcp_servers.verdict]", `args = ["mcp"]`} {
		if !strings.Contains(string(stored), want) {
			t.Errorf("appended TOML misses %q:\n%s", want, stored)
		}
	}
	again, ref := Run(req)
	if ref != nil || again.Status != StatusKeep {
		t.Fatalf("keep beside a literal: %+v %v", again, ref)
	}
	if raw, err := os.ReadFile(path); err != nil || !bytes.Equal(raw, stored) {
		t.Errorf("keep rewrote the file to %q", raw)
	}
}

// A literal in a later table is content, so the missing keys still land in this
// table and the literal keeps its own lines.
func TestTOMLMultiLineStringAfterTheTable(t *testing.T) {
	t.Parallel()
	seed := "[mcp_servers.verdict]\nstartup_timeout_sec = 20\n\n[other]\nnote = '''\ntext\n'''\n"
	req, path := seedCodex(t, seed)
	if _, ref := Run(req); ref != nil {
		t.Fatalf("fill beside a later literal: %v", ref)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`args = ["mcp"]`, "note = '''\n"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("filled TOML misses %q:\n%s", want, raw)
		}
	}
	if strings.Count(string(raw), "[mcp_servers.verdict]") != 1 {
		t.Errorf("duplicate table:\n%s", raw)
	}
}

// TestFlagSnippets proves each posture combination lands in the JSON snippet.
func TestFlagSnippets(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		readOnly bool
		operator bool
	}{
		{"plain", false, false},
		{"read-only", true, false},
		{"operator", false, true},
	} {
		checkFlagSnippet(t, tc.name, tc.readOnly, tc.operator)
	}
}

//nolint:revive // the presence/absence checks read as one assertion block
func checkFlagSnippet(t *testing.T, name string, readOnly, operator bool) {
	t.Helper()
	req := testRequest(t, AgentClaude, false)
	req.ReadOnly = readOnly
	req.Operator = operator
	res, ref := Run(req)
	if ref != nil {
		t.Fatalf("%s: %v", name, ref)
	}
	mcp := strings.Index(res.Snippet, `"mcp"`)
	ro := strings.Index(res.Snippet, `"--read-only"`)
	op := strings.Index(res.Snippet, `"--operator"`)
	if mcp < 0 {
		t.Errorf("%s snippet misses \"mcp\":\n%s", name, res.Snippet)
	}
	if readOnly && ro < 0 {
		t.Errorf("%s snippet misses --read-only:\n%s", name, res.Snippet)
	}
	if operator && op < 0 {
		t.Errorf("%s snippet misses --operator:\n%s", name, res.Snippet)
	}
	if !readOnly && ro >= 0 {
		t.Errorf("%s snippet carries --read-only:\n%s", name, res.Snippet)
	}
	if !operator && op >= 0 {
		t.Errorf("%s snippet carries --operator:\n%s", name, res.Snippet)
	}
}

// TestFlagSnippetsTOML proves each posture combination lands in the TOML table.
func TestFlagSnippetsTOML(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		want     string
		readOnly bool
		operator bool
	}{
		{"plain", `args = ["mcp"]`, false, false},
		{"read-only", `args = ["mcp", "--read-only"]`, true, false},
		{"operator", `args = ["mcp", "--operator"]`, false, true},
	} {
		req := testRequest(t, AgentCodex, false)
		req.ReadOnly = tc.readOnly
		req.Operator = tc.operator
		res, ref := Run(req)
		if ref != nil {
			t.Fatalf("%s: %v", tc.name, ref)
		}
		if !strings.Contains(res.Snippet, tc.want) {
			t.Errorf("%s snippet misses %q:\n%s", tc.name, tc.want, res.Snippet)
		}
	}
}

// TestJSONOwnershipUpdate proves --write upgrades an owned entry in place.
//
//nolint:revive,cyclop // the seed/update/keep sequence reads as one assertion block
func TestJSONOwnershipUpdate(t *testing.T) {
	t.Parallel()
	req := testRequest(t, AgentClaude, true)
	req.Cwd = t.TempDir()
	path := filepath.Join(req.Cwd, ".mcp.json")
	seed := `{"zeta":1,"mcpServers":{"verdict":{"command":"verdict","args":["mcp"]}}}` + "\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	req.ReadOnly = true
	res, ref := Run(req)
	if ref != nil {
		t.Fatalf("update: %v", ref)
	}
	if res.Status != StatusUpdate || !res.Changed || res.Backup == "" {
		t.Fatalf("update result = %+v", res)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"--read-only"`) || !strings.Contains(string(raw), `"zeta"`) {
		t.Errorf("updated JSON misses argv or operator keys:\n%s", raw)
	}
	backup, err := os.ReadFile(res.Backup)
	if err != nil || string(backup) != seed {
		t.Errorf("backup = %q %v", backup, err)
	}
	again, ref := Run(req)
	if ref != nil || again.Status != StatusKeep {
		t.Fatalf("keep = %+v %v", again, ref)
	}
}

// TestJSONOwnershipDowngrade proves --write drops a flag the request omits.
func TestJSONOwnershipDowngrade(t *testing.T) {
	t.Parallel()
	req := testRequest(t, AgentClaude, true)
	req.Cwd = t.TempDir()
	path := filepath.Join(req.Cwd, ".mcp.json")
	seed := `{"mcpServers":{"verdict":{"command":"verdict","args":["mcp","--read-only"]}}}` + "\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	res, ref := Run(req)
	if ref != nil {
		t.Fatalf("downgrade: %v", ref)
	}
	if res.Status != StatusUpdate || !res.Changed {
		t.Fatalf("downgrade result = %+v", res)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "--read-only") {
		t.Errorf("downgrade kept the flag:\n%s", raw)
	}
}

// TestTOMLOwnershipUpdate proves --write rewrites an owned TOML argv in place.
//
//nolint:revive // the seed/update/keep sequence reads as one assertion block
func TestTOMLOwnershipUpdate(t *testing.T) {
	t.Parallel()
	req := testRequest(t, AgentCodex, true)
	req.Cwd = t.TempDir()
	req.Env["CODEX_HOME"] = req.Cwd
	req.ReadOnly = true
	path := filepath.Join(req.Cwd, "config.toml")
	seed := "[mcp_servers.verdict]\nstartup_timeout_sec = 20\ncommand = \"verdict\"\nargs = [\"mcp\"]\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	res, ref := Run(req)
	if ref != nil {
		t.Fatalf("update: %v", ref)
	}
	if res.Status != StatusUpdate || !res.Changed {
		t.Fatalf("update result = %+v", res)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `args = ["mcp", "--read-only"]`) || !strings.Contains(string(raw), "startup_timeout_sec = 20") {
		t.Errorf("updated TOML misses argv or operator keys:\n%s", raw)
	}
	if again, ref := Run(req); ref != nil || again.Status != StatusKeep {
		t.Errorf("second run = %+v %v, want keep", again, ref)
	}
}

// TestNewHostPaths proves the documented config location per new host.
func TestNewHostPaths(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cwd := t.TempDir()
	for _, tc := range []struct {
		agent string
		want  string
	}{
		{AgentGemini, filepath.Join(home, ".gemini", "settings.json")},
		{AgentQwen, filepath.Join(home, ".qwen", "settings.json")},
		{AgentWindsurf, filepath.Join(home, ".codeium", "windsurf", "mcp_config.json")},
		{AgentOpencode, filepath.Join(cwd, "opencode.json")},
		{AgentGoose, filepath.Join(home, ".config", "goose", "config.yaml")},
		{AgentCopilot, filepath.Join(cwd, ".vscode", "mcp.json")},
	} {
		req := &Request{Agent: tc.agent, Cwd: cwd, Env: map[string]string{"HOME": home}}
		res, ref := Run(req)
		if ref != nil {
			t.Fatalf("%s: %v", tc.agent, ref)
		}
		if res.Path != tc.want {
			t.Errorf("%s path = %s, want %s", tc.agent, res.Path, tc.want)
		}
	}
}

// TestNewHostSnippetShapes proves each new host gets its own entry shape.
//
//nolint:revive // the per-host matrix reads as one assertion block
func TestNewHostSnippetShapes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		agent string
		want  []string
	}{
		{AgentGemini, []string{`"mcpServers"`, `"verdict"`, `"command": "verdict"`}},
		{AgentQwen, []string{`"mcpServers"`, `"verdict"`, `"command": "verdict"`}},
		{AgentWindsurf, []string{`"mcpServers"`, `"verdict"`, `"command": "verdict"`}},
		{AgentOpencode, []string{`"mcp"`, `"verdict"`, `"type": "local"`, `"verdict"`, `"mcp"`}},
		{AgentGoose, []string{"extensions:", "verdict:", "cmd: verdict"}},
		{AgentCopilot, []string{`"servers"`, `"verdict"`, `"command": "verdict"`}},
	} {
		req := &Request{Agent: tc.agent, Cwd: t.TempDir(), Env: map[string]string{"HOME": t.TempDir()}}
		res, ref := Run(req)
		if ref != nil {
			t.Fatalf("%s: %v", tc.agent, ref)
		}
		for _, w := range tc.want {
			if !strings.Contains(res.Snippet, w) {
				t.Errorf("%s snippet misses %q:\n%s", tc.agent, w, res.Snippet)
			}
		}
	}
}

// TestPrintOnlyWriteRefuses proves print-only hosts refuse --write typed.
func TestPrintOnlyWriteRefuses(t *testing.T) {
	t.Parallel()
	for _, agent := range []string{AgentOpencode, AgentGoose, AgentCopilot} {
		req := &Request{Agent: agent, Write: true, Cwd: t.TempDir(), Env: map[string]string{"HOME": t.TempDir()}}
		_, ref := Run(req)
		if ref == nil || ref.Code != CodeUnmergeable {
			t.Errorf("%s --write ref = %v, want %s", agent, ref, CodeUnmergeable)
		}
		if ref != nil && !strings.Contains(ref.Message, agent) {
			t.Errorf("%s refusal misses the host name: %q", agent, ref.Message)
		}
	}
}

// TestGeminiMergePreservesKeys proves --write keeps the host's own keys.
//
//nolint:revive // the seed/merge sequence reads as one assertion block
func TestGeminiMergePreservesKeys(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	req := &Request{Agent: AgentGemini, Write: true, Cwd: t.TempDir(), Env: map[string]string{"HOME": home}}
	path := filepath.Join(home, ".gemini", "settings.json")
	seed := `{"theme":"dark","mcpServers":{"other":{"command":"x"}}}` + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	res, ref := Run(req)
	if ref != nil {
		t.Fatalf("merge: %v", ref)
	}
	if res.Status != StatusUpdate || res.Backup == "" {
		t.Fatalf("merge result = %+v", res)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"theme"`, `"other"`, `"verdict"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("merged settings miss %q:\n%s", want, raw)
		}
	}
}
