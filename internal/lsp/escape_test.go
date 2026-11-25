package lsp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestMarshalMessageEscapesControls proves an LSP body carries C1 and bidi
// controls (built from code points so no tool rewrites the source) as
// lowercase \uXXXX, and the message decodes back unchanged.
func TestMarshalMessageEscapesControls(t *testing.T) {
	t.Parallel()
	runes := []rune{0x85, 0x9B, 0x202E, 0x2066}
	message := "m" + string(runes[0]) + string(runes[1]) + string(runes[2]) + string(runes[3])
	body, err := marshalMessage(rpcErrorResponse{
		JSONRPC: jsonRPCVersion, ID: json.RawMessage(`1`), Error: &rpcError{Code: -32602, Message: message},
	})
	if err != nil {
		t.Fatal(err)
	}
	wire := string(body)
	assertEscaped(t, wire, runes)
	var got rpcErrorResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Error == nil || got.Error.Message != message {
		t.Errorf("decoded error = %+v; want the message unchanged", got.Error)
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
