package mcp

import (
	"encoding/json"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

func TestPostFloorDecoder(t *testing.T) {
	a, err := newArgs(json.RawMessage(`{"flags":{"minSeverity":"important"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var req core.PostRequest
	decodePost(&req, a)
	if err := a.done(); err != nil {
		t.Fatal(err)
	}
	if req.Flags.MinSeverity != "important" {
		t.Fatalf("floor = %q; want important", req.Flags.MinSeverity)
	}
}
