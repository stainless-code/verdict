package mcp

import (
	"testing"
)

// TestInvalidParamsContract pins the exact JSON-RPC error code and message for
// every usage-shaped failure on the argument surface: bad envelope params,
// unknown tools, missing required values, wrong JSON types, hidden adapter
// fields, and unknown keys. These strings are part of the agent contract.
//
//nolint:revive // the table reads as one assertion block
func TestInvalidParamsContract(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		line string
		want string
	}{
		{
			name: "arguments array",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":[]}}`,
			want: "arguments must be an object: json: cannot unmarshal array into Go value of type map[string]jsontext.Value",
		},
		{
			name: "arguments string",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":"x"}}`,
			want: "arguments must be an object: json: cannot unmarshal string into Go value of type map[string]jsontext.Value",
		},
		{
			name: "arguments number",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":7}}`,
			want: "arguments must be an object: json: cannot unmarshal number into Go value of type map[string]jsontext.Value",
		},
		{
			name: "missing params",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call"}`,
			want: "missing tools/call params",
		},
		{
			name: "null params",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":null}`,
			want: "missing tools/call params",
		},
		{
			name: "params not an object",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":"x"}`,
			want: "invalid tools/call params: json: cannot unmarshal string into Go value of type mcp.callParams",
		},
		{
			name: "missing tool name",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`,
			want: "missing tool name",
		},
		{
			name: "unknown tool",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
			want: "unknown tool: nope",
		},
		{
			name: "missing repo",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":{}}}`,
			want: `missing required argument "repo"`,
		},
		{
			name: "empty repo",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":{"repo":"","pr":"1"}}}`,
			want: `missing required argument "repo"`,
		},
		{
			name: "missing pr",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":{"repo":"a/b"}}}`,
			want: `missing required argument "pr"`,
		},
		{
			name: "null pr",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":{"repo":"a/b","pr":null}}}`,
			want: `missing required argument "pr"`,
		},
		{
			name: "repo wrong type",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":{"repo":7,"pr":"1"}}}`,
			want: `argument "repo": json: cannot unmarshal number into Go value of type string`,
		},
		{
			name: "pr wrong type",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":{"repo":"a/b","pr":7}}}`,
			want: `argument "pr": json: cannot unmarshal number into Go value of type string`,
		},
		{
			name: "unknown top-level argument",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":{"repo":"a/b","pr":"1","bogus":true}}}`,
			want: `unknown argument "bogus"`,
		},
		{
			name: "unknown argument is lexicographic",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":{"repo":"a/b","pr":"1","zeta":1,"alpha":2}}}`,
			want: `unknown argument "alpha"`,
		},
		{
			name: "flags not an object",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":{"repo":"a/b","pr":"1","flags":[]}}}`,
			want: `argument "flags" must be an object`,
		},
		{
			name: "nested argument wrong type is unqualified",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":{"repo":"a/b","pr":"1","flags":{"status":7}}}}`,
			want: `argument "status": json: cannot unmarshal number into Go value of type string`,
		},
		{
			name: "nested argument wrong bool",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":{"repo":"a/b","pr":"1","flags":{"cost":"x"}}}}`,
			want: `argument "cost": json: cannot unmarshal string into Go value of type bool`,
		},
		{
			name: "unknown nested argument",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":{"repo":"a/b","pr":"1","flags":{"bogus":true}}}}`,
			want: `unknown argument "flags.bogus"`,
		},
		{
			name: "hidden presence flag is not an argument",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"fetch","arguments":{"repo":"a/b","pr":"1","flags":{"hasBase":true}}}}`,
			want: `unknown argument "flags.hasBase"`,
		},
		{
			name: "omitted repo on check-tree",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"check-tree","arguments":{"repo":"a/b"}}}`,
			want: `unknown argument "repo"`,
		},
		{
			name: "omitted pr on trend",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"trend","arguments":{"repo":"a/b","pr":"1"}}}`,
			want: `unknown argument "pr"`,
		},
		{
			name: "record has no flags object",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"record","arguments":{"repo":"a/b","pr":"1","flags":{}}}}`,
			want: `unknown argument "flags"`,
		},
		{
			name: "omitted repo on learn",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"learn","arguments":{"repo":"a/b"}}}`,
			want: `unknown argument "repo"`,
		},
		{
			name: "omitted repo on init",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"init","arguments":{"repo":"a/b"}}}`,
			want: `unknown argument "repo"`,
		},
		{
			name: "missing show sig",
			line: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"show","arguments":{"repo":"a/b","pr":"1"}}}`,
			want: `missing required argument "sig"`,
		},
		{
			name: "initialize params not an object",
			line: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":[]}`,
			want: "invalid initialize params",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := testConfig()
			frames := serve(t, &cfg, tc.line)
			got, ok := frames["1"]
			if !ok {
				t.Fatalf("no response frame for id 1")
			}
			if got.Error == nil {
				t.Fatalf("error = nil, want -32602 %q", tc.want)
			}
			if got.Error.Code != codeInvalidParams {
				t.Errorf("code = %d, want %d", got.Error.Code, codeInvalidParams)
			}
			if got.Error.Message != tc.want {
				t.Errorf("message = %q, want %q", got.Error.Message, tc.want)
			}
		})
	}
}
