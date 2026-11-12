package mcp

import "testing"

// TestNextToolRefusesUnreadableStore proves the next tool answers a findings
// store it cannot decode with the report's typed refusal, not the empty item a
// caller reads as "nothing to act on".
func TestNextToolRefusesUnreadableStore(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFile(t, ".verdict/state/5101/findings.json", "[42]")
	cfg := testConfig()
	frames := serve(t, &cfg,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"next",`+
			`"arguments":{"repo":"acme/app","pr":"5101"}}}`)
	refusal := toolRefusal(t, frames["1"])
	if refusal.Code != "corrupt-store" {
		t.Fatalf("next refusal = %s: %q; want corrupt-store", refusal.Code, refusal.Message)
	}
}
