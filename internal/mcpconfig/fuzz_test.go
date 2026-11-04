package mcpconfig

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// decodeStd decodes one JSON document with encoding/json (numbers kept
// literal): the oracle for what the operator's file meant before the merge.
func decodeStd(data []byte) (any, bool) {
	if !json.Valid(data) {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	return v, true
}

// refusalCodes are the typed refusals mergeJSON/mergeTOML may return.
var refusalCodes = map[string]bool{CodeUnparseable: true, CodeForeign: true, CodeUnmergeable: true}

// FuzzMergeJSON holds the .mcp.json / mcp.json merge to the promise in the
// package doc — never rewrite the operator's file into something else: a
// merged result is valid JSON that keeps every key and value of the original
// (only mcpServers.verdict is added), a second merge is a no-op, and anything
// unmergeable refuses with a documented code and no bytes.
func FuzzMergeJSON(f *testing.F) {
	for _, s := range []string{
		``, `{}`, `{"mcpServers":{}}`, `{"mcpServers":{"other":{"command":"x","args":[1,2.50,1e999]}},"theme":"dark","n":-0.0}`,
		`{"mcpServers":{"verdict":{"command":"verdict","args":["mcp"],"env":{"A":"b"}}}}`,
		`{"mcpServers":{"verdict":{"command":"evil","args":["mcp"]}}}`, `{"mcpServers":[]}`, `[]`, `null`,
		`{"a":1,"a":2}`, `{"mcpServers":{"verdict":null}}`, `{"mcpServers":{}}}`, "{\"k\":\"\xff\"}", `{"mcpServers":{"verdict":{"command":"verdict","args":["mcp","x"]}}}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, old []byte) {
		out, changed, ref := mergeJSON(old, "cfg.json")
		if ref != nil {
			if out != nil || changed || !refusalCodes[ref.Code] {
				t.Fatalf("refusal %q returned out=%q changed=%v", ref.Code, out, changed)
			}
			return
		}
		if !changed {
			if out != nil {
				t.Fatalf("unchanged merge returned bytes %q", out)
			}
			assertAlreadyConfigured(t, old)
			return
		}
		assertMergedJSON(t, old, out)
	})
}

// assertAlreadyConfigured checks a keep verdict: the original already holds
// the canonical verdict entry.
func assertAlreadyConfigured(t *testing.T, old []byte) {
	t.Helper()
	doc, ok := decodeStd(old)
	root, isObj := doc.(map[string]any)
	if !ok || !isObj {
		t.Fatalf("keep verdict on a non-object document %q", old)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	server, _ := servers["verdict"].(map[string]any)
	if server["command"] != "verdict" || !reflect.DeepEqual(server["args"], []any{"mcp"}) {
		t.Fatalf("keep verdict but %q does not configure `verdict mcp`", old)
	}
}

// assertMergedJSON checks a changed merge against the original.
func assertMergedJSON(t *testing.T, old, out []byte) {
	t.Helper()
	merged, ok := decodeStd(out)
	if !ok {
		t.Fatalf("merge produced invalid JSON %q from %q", out, old)
	}
	mroot, ok := merged.(map[string]any)
	if !ok {
		t.Fatalf("merge produced a non-object %q", out)
	}
	servers, _ := mroot["mcpServers"].(map[string]any)
	want := map[string]any{"command": "verdict", "args": []any{"mcp"}}
	if !reflect.DeepEqual(servers["verdict"], want) {
		t.Fatalf("merged verdict entry = %#v, want %#v", servers["verdict"], want)
	}
	// Everything the operator had is still there, byte-for-meaning.
	delete(servers, "verdict")
	var origRoot map[string]any
	if orig, ok := decodeStd(old); ok {
		origRoot, _ = orig.(map[string]any)
	}
	origServers, hadServers := origRoot["mcpServers"].(map[string]any)
	if !hadServers {
		delete(mroot, "mcpServers")
	} else if !reflect.DeepEqual(servers, origServers) {
		t.Fatalf("other servers changed: %#v -> %#v", origServers, servers)
	}
	if origRoot == nil {
		origRoot = map[string]any{}
	}
	if !reflect.DeepEqual(mroot, origRoot) {
		t.Fatalf("operator keys changed: %#v -> %#v (input %q)", origRoot, mroot, old)
	}
	if _, again, ref := mergeJSON(out, "cfg.json"); ref != nil || again {
		t.Fatalf("second merge not a no-op: changed=%v ref=%v for %q", again, ref, out)
	}
}

// FuzzMergeTOML holds the Codex config.toml merge to what a text-level edit
// can promise without a parser: it only inserts lines (every non-blank
// original line survives in order), the result carries one canonical
// [mcp_servers.verdict] table with the `verdict mcp` command, a second merge
// is a no-op, and anything it cannot place refuses with no bytes.
func FuzzMergeTOML(f *testing.F) {
	for _, s := range []string{
		``, "model = \"x\"\n", "[mcp_servers.other]\ncommand = \"o\"\n", "[mcp_servers.verdict]\n",
		"[mcp_servers.verdict]\ncommand = \"verdict\"\n", "[mcp_servers.verdict]\nargs = ['mcp']\n",
		"[mcp_servers.verdict]\ncommand = \"evil\"\nargs = [\"mcp\"]\n", "mcp_servers.verdict.command = \"x\"\n",
		"[mcp_servers . \"verdict\"] # c\ncommand = 'verdict'\n", "[[mcp_servers.verdict]]\n", "[mcp_servers.verdict.env]\nA=\"b\"\n",
		"note = '''\n[mcp_servers.verdict]\n'''\n", "\r\n[a]\r\nb=1\r\n",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, old []byte) {
		out, changed, ref := mergeTOML(old, "config.toml")
		if ref != nil {
			if out != nil || changed || !refusalCodes[ref.Code] {
				t.Fatalf("refusal %q returned out=%q changed=%v", ref.Code, out, changed)
			}
			return
		}
		if !changed {
			if out != nil {
				t.Fatalf("unchanged merge returned bytes %q", out)
			}
			return
		}
		assertMergedTOML(t, old, out)
	})
}

// assertMergedTOML checks a changed TOML merge.
func assertMergedTOML(t *testing.T, old, out []byte) {
	t.Helper()
	if !isSubsequence(nonBlankLines(string(old)), nonBlankLines(string(out))) {
		t.Fatalf("merge dropped or reordered operator lines:\nold %q\nnew %q", old, out)
	}
	headers := 0
	for _, ln := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(ln) == "[mcp_servers.verdict]" {
			headers++
		}
	}
	if headers == 0 && !bytes.Contains(old, []byte("mcp_servers")) {
		t.Fatalf("merge added no [mcp_servers.verdict] table: %q", out)
	}
	if _, again, ref := mergeTOML(out, "config.toml"); ref != nil || again {
		t.Fatalf("second merge not a no-op: changed=%v ref=%v for %q", again, ref, out)
	}
}

// nonBlankLines returns the trimmed-right non-empty lines of s.
func nonBlankLines(s string) []string {
	var out []string
	for ln := range strings.SplitSeq(s, "\n") {
		if ln = strings.TrimRight(ln, " \t\r"); ln != "" {
			out = append(out, ln)
		}
	}
	return out
}

// isSubsequence reports whether sub appears in full in order.
func isSubsequence(sub, full []string) bool {
	i := 0
	for _, l := range full {
		if i < len(sub) && sub[i] == l {
			i++
		}
	}
	return i == len(sub)
}
