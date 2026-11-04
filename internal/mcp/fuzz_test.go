package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// inertCommander refuses every spawn, so a fuzzed tools/call can reach the
// request decoders and the service layer but never start a process.
type inertCommander struct{}

// Run implements core.Commander.
func (inertCommander) Run(context.Context, core.Command) (core.Result, error) {
	return core.Result{}, context.Canceled
}

// FuzzServeFrames drives the whole stdio loop with hostile frames and holds it
// to the transport contract, using encoding/json and the input itself as the
// oracle: stdout carries only JSON-RPC 2.0 response objects; each carries
// exactly one of result and error; a response id is null or an id some input
// frame carried; and no input line yields more than one response. Tool calls
// run in a scratch cwd with an empty PATH and inert commanders, so nothing
// the fuzzer builds can reach the host.
func FuzzServeFrames(f *testing.F) {
	for _, s := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}` + "\n" + `{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"report","arguments":{"repo":"o/r","pr":"1"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"report","arguments":[1]}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"nope"}}`,
		`{"jsonrpc":"2.0","id":"s","method":"tools/call","params":null}`,
		`{"jsonrpc":"2.0","id":{"a":[1]},"method":"ping"}`,
		`{"jsonrpc":"1.0","id":7,"method":"ping"}`, `{"jsonrpc":"2.0","id":8}`, `[]`, `null`, `{`, `"x"`,
		`{"jsonrpc":"2.0","id":9,"method":"notifications/cancelled","params":{"requestId":9}}`,
		`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n" + `{"jsonrpc":"2.0","id":1,"method":"ping"}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<16 {
			return
		}
		t.Chdir(t.TempDir())
		t.Setenv("PATH", "")
		cfg := testConfig()
		cfg.Commanders = map[string]core.Commander{}
		for _, tl := range allTools(true, true) {
			cfg.Commanders[tl.name] = inertCommander{}
		}
		cfg.Operator = true
		var out bytes.Buffer
		if err := New(&cfg).Serve(context.Background(), bytes.NewReader(append(input, '\n')), &out); err != nil {
			t.Fatalf("Serve: %v", err)
		}
		assertTransportContract(t, input, out.Bytes())
	})
}

// assertTransportContract checks the response stream against the input.
func assertTransportContract(t *testing.T, input, output []byte) {
	t.Helper()
	ids, lines := inputIDs(input)
	frames := 0
	for resp := range strings.SplitSeq(strings.TrimSuffix(string(output), "\n"), "\n") {
		if resp == "" {
			continue
		}
		frames++
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(resp), &raw); err != nil {
			t.Fatalf("stdout line is not a JSON object: %q (%v)\ninput %q", resp, err, input)
		}
		if string(raw["jsonrpc"]) != `"2.0"` {
			t.Fatalf("response without jsonrpc 2.0: %q", resp)
		}
		_, hasResult := raw["result"]
		_, hasError := raw["error"]
		if hasResult == hasError {
			t.Fatalf("response must hold exactly one of result/error: %q", resp)
		}
		if id := string(raw["id"]); id != "null" && !ids[id] {
			t.Fatalf("response id %s matches no input id: %q\ninput %q", id, resp, input)
		}
	}
	if frames > lines {
		t.Fatalf("%d responses for %d input lines\ninput %q\noutput %q", frames, lines, input, output)
	}
}

// inputIDs collects the compacted id of every input line that decodes as a
// JSON object, and counts the non-blank lines.
func inputIDs(input []byte) (map[string]bool, int) {
	ids := map[string]bool{}
	lines := 0
	for line := range bytes.SplitSeq(input, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		lines++
		var obj struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(line, &obj) != nil || len(obj.ID) == 0 {
			continue
		}
		var buf bytes.Buffer
		if json.Compact(&buf, obj.ID) == nil {
			ids[buf.String()] = true
		}
	}
	return ids, lines
}
