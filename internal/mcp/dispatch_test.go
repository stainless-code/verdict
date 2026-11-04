package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestCallDecodesArgumentsOnce pins the parse budget: one call decodes its
// arguments exactly once, before dispatch. A second decode would be the
// double-parsing regression STA-73 removes.
func TestCallDecodesArgumentsOnce(t *testing.T) {
	fixture(t)
	cfg := testConfig()
	server := New(&cfg)
	report, ok := server.byName["report"]
	if !ok {
		t.Fatal("report is not registered")
	}
	decodes := 0
	decode := report.decode
	report.decode = func(raw json.RawMessage) (any, error) {
		decodes++
		return decode(raw)
	}
	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":{"repo":"acme/app","pr":"5101"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"report","arguments":{"repo":"acme/app","pr":"1","bogus":true}}}`,
	}
	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if decodes != len(lines) {
		t.Errorf("arguments decoded %d times, want %d", decodes, len(lines))
	}
	frames := decodeFrames(t, out.Bytes())
	if frames["2"].Error == nil || frames["2"].Error.Code != codeInvalidParams {
		t.Errorf("bad arguments = %+v, want -32602", frames["2"].Error)
	}
}

// TestEnvelopeContract pins the one-pass envelope decoder: syntax errors stay
// -32700, shape errors stay -32600, and notification-shaped garbage is silent.
//
//nolint:revive // the envelope matrix reads as one assertion block
func TestEnvelopeContract(t *testing.T) {
	t.Parallel()
	cases := []struct { //nolint:govet // the case fields read in name/id/code order
		name string
		line string
		id   string
		code int
		text string
	}{
		{name: "malformed", line: `not json`, id: "null", code: codeParse, text: "parse error"},
		{name: "truncated", line: `{"jsonrpc":`, id: "null", code: codeParse, text: "parse error"},
		{name: "top-level array", line: `[]`, id: "null", code: codeInvalidRequest, text: "invalid request"},
		{name: "top-level string", line: `"x"`, id: "null", code: codeInvalidRequest, text: "invalid request"},
		{name: "method wrong type", line: `{"jsonrpc":"2.0","id":1,"method":5}`, id: "null", code: codeInvalidRequest, text: "invalid request"},
		{name: "missing method", line: `{"jsonrpc":"2.0","id":1}`, id: "1", code: codeInvalidRequest, text: "invalid request"},
		{name: "wrong version", line: `{"jsonrpc":"1.0","id":1,"method":"ping"}`, id: "1", code: codeInvalidRequest, text: "invalid request"},
		{name: "notification-shaped garbage", line: `{}`, id: "", code: 0, text: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := testConfig()
			frames := serve(t, &cfg, tc.line)
			if tc.id == "" {
				if len(frames) != 0 {
					t.Fatalf("frames = %v, want none", frames)
				}
				return
			}
			got, ok := frames[tc.id]
			if !ok {
				t.Fatalf("no frame with id %q: %v", tc.id, frames)
			}
			if got.Error == nil || got.Error.Code != tc.code || got.Error.Message != tc.text {
				t.Fatalf("error = %+v, want %d %q", got.Error, tc.code, tc.text)
			}
		})
	}
}
