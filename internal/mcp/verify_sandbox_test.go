package mcp

import (
	"encoding/json"
	"testing"

	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

// TestVerifySandboxArgumentDecodes proves the MCP `sandbox` argument reaches
// the core request instead of being left as an unnoticed schema property.
func TestVerifySandboxArgumentDecodes(t *testing.T) {
	a, err := newArgs(json.RawMessage(`{"sandbox":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req := &verifycore.Request{}
	decodeVerify(req, a)
	if err := a.done(); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !req.Sandbox {
		t.Error("req.Sandbox = false, want true")
	}
}
