package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// bundleNameRefusalText is the one line every bundle verb renders for a
// positional that is not a single path element.
const bundleNameRefusalText = "bundle name must be one path element (a PR number or fetch-local name), not a path"

// spawnlessCommander fails any spawn or look-path: a tool call must refuse the
// bundle name before it reaches a host.
type spawnlessCommander struct{}

// Run answers any spawn with a loud error.
func (spawnlessCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	return core.Result{}, fmt.Errorf("unexpected spawn: %q", cmd.Argv)
}

// LookPath answers any tool probe with a loud error.
func (spawnlessCommander) LookPath(tool string) error {
	return fmt.Errorf("unexpected look-path: %s", tool)
}

// TestToolPrGuard proves every MCP tool that takes a pr refuses a name that is
// not one path element, with the shared code and message, before it reads a
// bundle or reaches a host.
func TestToolPrGuard(t *testing.T) {
	cases := []prToolCase{
		{name: "adopt", args: `"repo":"acme/app","pr":"../escaped","from":"twin"`},
		{name: "calibrate", args: `"repo":"acme/app","pr":"../escaped"`},
		{name: "config", args: `"repo":"acme/app","pr":"../escaped","flags":{"explain":true}`},
		{name: "fetch", args: `"repo":"acme/app","pr":"../escaped"`},
		{name: "fix", args: `"repo":"acme/app","pr":"../escaped"`},
		{name: "index", args: `"pr":"../escaped","query":"blast-radius"`},
		{name: "next", args: `"repo":"acme/app","pr":"../escaped"`},
		{name: "post", args: `"repo":"acme/app","pr":"../escaped","flags":{"approve":"true"}`},
		{name: "ready", args: `"repo":"acme/app","pr":"../escaped"`},
		{name: "record", args: `"repo":"acme/app","pr":"../escaped"`},
		{name: "report", args: `"repo":"acme/app","pr":"../escaped"`},
		{name: "show", args: `"repo":"acme/app","pr":"../escaped","sig":"abc"`},
		{name: "threads", args: `"repo":"acme/app","pr":"../escaped"`},
		{name: "triage", args: `"repo":"acme/app","pr":"../escaped","sig":"abc","status":"open"`},
		{name: "verify", args: `"repo":"acme/app","pr":"../escaped"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture(t)
			cfg := testConfig()
			cfg.Operator = true
			cfg.Commanders = map[string]core.Commander{tc.name: spawnlessCommander{}}
			line := fmt.Sprintf(
				`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":{%s}}}`,
				tc.name, tc.args)
			frames := serve(t, &cfg, line)
			refusal := toolRefusal(t, frames["1"])
			if refusal.Code != "bad-flags" || refusal.Message != bundleNameRefusalText {
				t.Fatalf("%s refusal = %s: %q; want bad-flags: %q",
					tc.name, refusal.Code, refusal.Message, bundleNameRefusalText)
			}
		})
	}
}

// prToolCase is one pr-taking tool and the arguments its schema requires.
type prToolCase struct {
	name string
	args string
}

// toolRefusal decodes one tool call's refusal result.
func toolRefusal(t *testing.T, frame frame) refusalContent {
	t.Helper()
	if frame.Error != nil {
		t.Fatalf("tool call failed at the protocol layer: %+v", frame.Error)
	}
	var result callToolResult
	if err := json.Unmarshal(frame.Result, &result); err != nil {
		t.Fatalf("tool result: %v", err)
	}
	if !result.IsError {
		t.Fatalf("tool result = %+v; want an isError refusal", result)
	}
	var refusal refusalContent
	if err := json.Unmarshal(result.StructuredContent, &refusal); err != nil {
		t.Fatalf("refusal content: %v", err)
	}
	return refusal
}
