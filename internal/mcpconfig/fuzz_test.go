package mcpconfig

import (
	"bytes"
	"encoding/json"
	"maps"
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
		`{"mcpServers":{"verdict":{"command":"verdict","args":["mcp","--read-only"]}}}`,
		`{"mcpServers":{"verdict":{"command":"verdict","args":["mcp","--read-only"],"env":{"A":"b"},"note":"n"}}}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, old []byte) {
		out, changed, ref := mergeJSON(old, "cfg.json", []string{"mcp"})
		if ref != nil {
			assertRefusalIsClean(t, ref.Code, out, changed)
			return
		}
		if changed {
			assertMergedJSON(t, old, out)
			return
		}
		if out != nil {
			t.Fatalf("unchanged merge returned bytes %q", out)
		}
		assertAlreadyConfigured(t, old)
	})
}

// assertRefusalIsClean checks a refusal carries a documented code and no
// bytes.
func assertRefusalIsClean(t *testing.T, code string, out []byte, changed bool) {
	t.Helper()
	if out != nil || changed || !refusalCodes[code] {
		t.Fatalf("refusal %q returned out=%q changed=%v", code, out, changed)
	}
}

// assertAlreadyConfigured checks a keep verdict: the original already holds
// the canonical verdict entry.
func assertAlreadyConfigured(t *testing.T, old []byte) {
	t.Helper()
	doc, ok := decodeStd(old)
	root := asObject(doc)
	if !ok || root == nil {
		t.Fatalf("keep verdict on a non-object document %q", old)
	}
	server := asObject(asObject(root["mcpServers"])["verdict"])
	if server["command"] != "verdict" || !reflect.DeepEqual(server["args"], []any{"mcp"}) {
		t.Fatalf("keep verdict but %q does not configure `verdict mcp`", old)
	}
}

// asObject reads v as a JSON object, nil for anything else.
func asObject(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m
}

// stdRoot decodes a JSON object document with encoding/json, nil when it is
// not one.
func stdRoot(data []byte) map[string]any {
	doc, ok := decodeStd(data)
	if !ok {
		return nil
	}
	return asObject(doc)
}

// assertMergedJSON checks a changed merge against the original.
func assertMergedJSON(t *testing.T, old, out []byte) {
	t.Helper()
	merged := stdRoot(out)
	if merged == nil {
		t.Fatalf("merge produced invalid JSON or a non-object: %q (from %q)", out, old)
	}
	servers := asObject(merged["mcpServers"])
	entry := asObject(servers["verdict"])
	if entry == nil || entry["command"] != "verdict" || !reflect.DeepEqual(entry["args"], []any{"mcp"}) {
		t.Fatalf("merged verdict entry = %#v, want command verdict and argv [mcp]", servers["verdict"])
	}
	orig := stdRoot(old)
	origServers := asObject(orig["mcpServers"])
	if before := asObject(origServers["verdict"]); before != nil {
		assertOwnedEntryKept(t, before, entry)
	} else if want := map[string]any{"command": "verdict", "args": []any{"mcp"}}; !reflect.DeepEqual(entry, want) {
		t.Fatalf("added verdict entry = %#v, want %#v", entry, want)
	}
	delete(servers, "verdict")
	delete(origServers, "verdict")
	assertOperatorKeysKept(t, orig, merged, old)
	if _, again, ref := mergeJSON(out, "cfg.json", []string{"mcp"}); ref != nil || again {
		t.Fatalf("second merge not a no-op: changed=%v ref=%v for %q", again, ref, out)
	}
}

// assertOwnedEntryKept checks the in-place update: command and every key but
// args survive, and only args move to the requested argv.
func assertOwnedEntryKept(t *testing.T, before, after map[string]any) {
	t.Helper()
	was := maps.Clone(before)
	now := maps.Clone(after)
	delete(was, "args")
	delete(now, "args")
	if !reflect.DeepEqual(now, was) {
		t.Fatalf("in-place update changed the owned entry: %#v -> %#v", before, after)
	}
}

// assertOperatorKeysKept checks that, once the verdict entry is removed, the
// merged document means what the original did.
func assertOperatorKeysKept(t *testing.T, orig, merged map[string]any, old []byte) {
	t.Helper()
	if origServers := asObject(orig["mcpServers"]); origServers == nil {
		delete(merged, "mcpServers")
	} else if !reflect.DeepEqual(asObject(merged["mcpServers"]), origServers) {
		t.Fatalf("other servers changed: %#v -> %#v", origServers, merged["mcpServers"])
	}
	if len(orig) == 0 && len(merged) == 0 {
		return
	}
	if !reflect.DeepEqual(merged, orig) {
		t.Fatalf("operator keys changed: %#v -> %#v (input %q)", orig, merged, old)
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
		"note = \"\"\"\nhello\n", "note = '''\n[mcp_servers.verdict]\ncommand = \"verdict\"\nargs = [\"mcp\"]\n'''\n",
		"note = \"see [mcp_servers.verdict] in the docs\"\n", "# '''\n[mcp_servers.verdict]\n", "model = \"gpt\"",
		"\u2004", "model = \"x\"\n\u2003", " \t\n",
		"[mcp_servers.verdict]\"\ncommand = \"verdict\"\nargs = [\"mcp\"]\n\"\n", "[mcp_servers.verdict]'''\ntext\n'''\n",
		"model = \"x\"\r\n\r\n[b]\nc=1\n",
		"[mcp_servers.verdict]\ncommand = \"verdict\"\nargs = [\"mcp\", \"--read-only\"]\n",
		"[mcp_servers.verdict]\ncommand = \"verdict\"\nargs = [\"mcp\", \"--operator\"]\nstartup_timeout_sec = 20\n",
		"[mcp_servers . \"verdict\"]\ncommand = 'verdict'\nargs = ['mcp', '--read-only']\n",
	} {
		f.Add([]byte(s))
	}
	// Shapes that must gain the table: a merge that reports no change here
	// leaves the operator without the config the command exists to add, so
	// these rows keep the fuzz body from passing on a do-nothing merge.
	for _, s := range []string{
		"", "model = \"x\"\n", "[mcp_servers.other]\ncommand = \"o\"\n",
		"note = \"see [mcp_servers.verdict] in the docs\"\n",
	} {
		mustMergeTOML(f, s)
	}
	f.Fuzz(func(t *testing.T, old []byte) {
		out, changed, ref := mergeTOML(old, "config.toml", []string{"mcp"})
		if ref != nil {
			assertRefusalIsClean(t, ref.Code, out, changed)
			return
		}
		if changed {
			assertMergedTOML(t, old, out)
		} else if out != nil {
			t.Fatalf("unchanged merge returned bytes %q", out)
		}
	})
}

// mustMergeTOML fails when a config with no verdict table comes back
// unchanged or without the table.
func mustMergeTOML(f *testing.F, s string) {
	f.Helper()
	out, changed, ref := mergeTOML([]byte(s), "config.toml", []string{"mcp"})
	if ref != nil || !changed || !bytes.Contains(out, []byte("[mcp_servers.verdict]")) {
		f.Fatalf("mergeTOML(%q) = changed=%v out=%q ref=%v, want the verdict table", s, changed, out, ref)
	}
}

// assertMergedTOML checks a changed TOML merge.
func assertMergedTOML(t *testing.T, old, out []byte) {
	t.Helper()
	if !isSubsequence(operatorLines(string(old)), nonBlankLines(string(out))) {
		t.Fatalf("merge dropped or reordered operator lines:\nold %q\nnew %q", old, out)
	}
	if !bytes.Contains(out, []byte("[mcp_servers.verdict]")) && !bytes.Contains(old, []byte("mcp_servers")) {
		t.Fatalf("merge added no [mcp_servers.verdict] table: %q", out)
	}
	if _, again, ref := mergeTOML(out, "config.toml", []string{"mcp"}); ref != nil || again {
		t.Fatalf("second merge not a no-op: changed=%v ref=%v for %q", again, ref, out)
	}
}

// operatorLines returns the non-blank lines the merge must keep, dropping the
// verdict table's args lines: that is the one line an in-place posture update
// rewrites. The scan is the merge's own line model, because only it can say
// which of TOML's many spellings a line is; everything it does not read as an
// args line must survive byte-for-byte, in order.
func operatorLines(s string) []string {
	doc := scanTOML(s)
	start, end := doc.verdictTable()
	var out []string
	for i, line := range doc.lines {
		if start >= 0 && i > start && i < end && doc.scan[i].hasPair && doc.scan[i].key == keyArgs {
			continue
		}
		if line = strings.TrimRight(line, " \t\r"); line != "" {
			out = append(out, line)
		}
	}
	return out
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
