package acp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestFrameWriterEscapesControls proves an ACP frame carries C1 and bidi
// controls (built from code points so no tool rewrites the source) as
// lowercase \uXXXX, and the message decodes back unchanged.
func TestFrameWriterEscapesControls(t *testing.T) {
	srv := New(&Config{})
	var out bytes.Buffer
	srv.out = &out
	runes := []rune{0x85, 0x9B, 0x202E, 0x2066}
	message := "m" + string(runes[0]) + string(runes[1]) + string(runes[2]) + string(runes[3])
	srv.writeFrame(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage(`1`), Error: &rpcError{Code: -32602, Message: message}})
	wire := out.String()
	assertEscaped(t, wire, runes)
	var f rpcResponse
	if err := json.Unmarshal(out.Bytes(), &f); err != nil {
		t.Fatal(err)
	}
	if f.Error == nil || f.Error.Message != message {
		t.Errorf("decoded error = %+v; want the message unchanged", f.Error)
	}
}

// assertEscaped fails unless every rune left wire as lowercase \uXXXX.
func assertEscaped(t *testing.T, wire string, runes []rune) {
	t.Helper()
	for _, r := range runes {
		if strings.ContainsRune(wire, r) {
			t.Errorf("wire carries the raw rune %U", r)
		}
		if esc := fmt.Sprintf("%cu%04x", '\\', r); !strings.Contains(wire, esc) {
			t.Errorf("wire lacks %s: %q", esc, wire)
		}
	}
}
