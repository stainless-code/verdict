package mcp

import (
	"fmt"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestToolFixCombos proves the fix tool refuses the same flag combinations the
// CLI refuses: the check lives in fixcore.Run, so one MCP call cannot drop a
// field the CLI would reject.
func TestToolFixCombos(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{
			name: "all-and-sig",
			args: `"repo":"acme/app","pr":"1","all":true,"sig":"deadbeef"`,
			want: "fix takes --sig or --all, not both (--all selects every open inline finding)",
		},
		{
			name: "min-severity-without-all",
			args: `"repo":"acme/app","pr":"1","minSeverity":"critical"`,
			want: "--min-severity narrows a batch: pass it with --all",
		},
		{
			name: "verify-cmd-without-all",
			args: `"repo":"acme/app","pr":"1","verifyCmd":"go test ./..."`,
			want: "--verify-cmd proves a batch: pass it with --all",
		},
		{
			name: "paths-without-all",
			args: `"repo":"acme/app","pr":"1","paths":["internal/**"]`,
			want: "--paths scopes a batch: pass it with --all",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture(t)
			cfg := testConfig()
			cfg.Operator = true
			cfg.Commanders = map[string]core.Commander{"fix": spawnlessCommander{}}
			line := fmt.Sprintf(
				`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"fix","arguments":{%s}}}`,
				tc.args)
			refusal := toolRefusal(t, serve(t, &cfg, line)["1"])
			if refusal.Code != "bad-flags" || refusal.Message != tc.want {
				t.Fatalf("fix refusal = %s: %q; want bad-flags: %q",
					refusal.Code, refusal.Message, tc.want)
			}
		})
	}
}
