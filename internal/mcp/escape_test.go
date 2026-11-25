package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The runes encoding/json writes raw but a terminal obeys: two C1 controls
// (U+009B is a CSI) and two bidi controls. They are built from code points so
// no editor or tool rewrites the source.
var controlRunes = []rune{0x85, 0x9B, 0x202E, 0x2066}

// hostileBody is a finding body that carries every control rune.
func hostileBody() string {
	return "Usable body" + string(controlRunes[0]) + " here" + string(controlRunes[1]) +
		"[2J with enough" + string(controlRunes[2]) + " substance" + string(controlRunes[3]) + " to post."
}

// escaped is the lowercase `\uXXXX` spelling the wire must carry for r.
func escaped(r rune) string { return fmt.Sprintf("%cu%04x", '\\', r) }

// assertNoRawControls fails when wire carries a control rune unescaped.
func assertNoRawControls(t *testing.T, wire string) {
	t.Helper()
	for _, r := range controlRunes {
		if strings.ContainsRune(wire, r) {
			t.Errorf("wire carries the raw rune %U", r)
		}
	}
}

// TestResponseEscapesC1AndBidiControls proves one MCP response (the findings
// query, whose text block and structuredContent both carry the body) reaches
// stdout with the controls escaped and decodes to the same string.
func TestResponseEscapesC1AndBidiControls(t *testing.T) {
	queryFixture(t)
	writeFile(t, ".verdict/state/1234/findings.json",
		"["+strings.Replace(queryFinding(querySigOpen, "open", "critical"),
			"Usable body here with enough substance.", hostileBody(), 1)+"]")
	cfg := testConfig()
	var out bytes.Buffer
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` +
		`{"name":"query-findings","arguments":{"bundle":"1234"}}}` + "\n")
	if err := New(&cfg).Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	wire := out.String()
	assertNoRawControls(t, wire)
	for _, r := range controlRunes {
		// The text block holds the envelope as a string, so its escape is
		// itself escaped; the structured copy carries it once.
		if !strings.Contains(wire, escaped(r)) {
			t.Errorf("wire lacks the escape %s", escaped(r))
		}
	}
	frames := decodeFrames(t, out.Bytes())
	var result callToolResult
	if err := json.Unmarshal(frames["1"].Result, &result); err != nil {
		t.Fatal(err)
	}
	var data any
	if err := json.Unmarshal(result.StructuredContent, &data); err != nil {
		t.Fatal(err)
	}
	if !decodedHasBody(t, data) {
		t.Errorf("decoded structuredContent lost the body %q", hostileBody())
	}
}

// TestFrameWriterEscapesControls drives the frame writer with a result this
// package marshals itself (an error message), which never passes through the
// jq renderer: only the frame writer can escape a control rune a caller
// echoed back.
func TestFrameWriterEscapesControls(t *testing.T) {
	cfg := testConfig()
	srv := New(&cfg)
	var out bytes.Buffer
	srv.out = &out
	message := "bad" + string(controlRunes[1]) + "[2J name" + string(controlRunes[2]) + string(controlRunes[3])
	srv.writeFrame(rpcResponse{
		JSONRPC: jsonRPCVersion,
		ID:      json.RawMessage(`1`),
		Error:   &rpcError{Code: -32602, Message: message},
	})
	wire := out.String()
	assertNoRawControls(t, wire)
	want := `"message":"bad` + escaped(controlRunes[1]) + `[2J name` + escaped(controlRunes[2]) + escaped(controlRunes[3]) + `"`
	if !strings.Contains(wire, want) {
		t.Fatalf("frame = %q; want it to contain %s", wire, want)
	}
	var f frame
	if err := json.Unmarshal(out.Bytes(), &f); err != nil {
		t.Fatal(err)
	}
	if f.Error == nil || f.Error.Message != message {
		t.Fatalf("decoded error = %+v; the message must round-trip unchanged", f.Error)
	}
}

// decodedHasBody reports whether the decoded document carries the hostile
// body as one string value (re-encoding leaves the control runes raw, so the
// encoded body is a substring exactly when some string equals it).
func decodedHasBody(t *testing.T, v any) bool {
	t.Helper()
	doc, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(hostileBody())
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Contains(doc, body)
}
