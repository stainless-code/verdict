package mcp

import (
	"encoding/json"
	"testing"

	learncore "github.com/stainless-code/verdict/internal/core/learn"
)

// TestLearningLifecycleArguments pins the public MCP lifecycle options.
func TestLearningLifecycleArguments(t *testing.T) {
	tool := findTool(t, "learn")
	decoded, err := decodeArgs(tool, json.RawMessage(`{"propose":true,"from_bundle":"acme/widget","pr":"42","dry_run":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req, ok := decoded.(*learncore.Request)
	if !ok {
		t.Fatalf("request type: %T", decoded)
	}
	if !req.Propose || !req.DryRun || req.FromBundle != "acme/widget" || req.Pr != "42" {
		t.Fatalf("lifecycle request: %+v", req)
	}
}
