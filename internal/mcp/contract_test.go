package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

var wantAnnotations = map[string]toolAnnotations{
	"adopt": {Title: "Adopt findings", IdempotentHint: true},
	"calibrate": {
		Title: "Calibrate findings", IdempotentHint: true, OpenWorldHint: true,
	},
	"check-tree": {Title: "Check worktree", ReadOnlyHint: true, IdempotentHint: true},
	"config":     {Title: "Explain policy", ReadOnlyHint: true, IdempotentHint: true},
	"doctor": {
		Title: "Preflight environment", ReadOnlyHint: true, IdempotentHint: true,
	},
	"fetch": {
		Title: "Fetch pull request", IdempotentHint: true, OpenWorldHint: true,
	},
	"fetch-local": {Title: "Fetch local diff", IdempotentHint: true},
	"fix":         {Title: "Prepare a fix"},
	"index":       {Title: "Query code index", ReadOnlyHint: true, IdempotentHint: true},
	"init": {
		Title: "Scaffold controls", DestructiveHint: true, IdempotentHint: true,
	},
	"learn": {Title: "Manage knowledge base", IdempotentHint: true},
	"next":  {Title: "Next finding", ReadOnlyHint: true, IdempotentHint: true},
	"pack":  {Title: "List lens packs", ReadOnlyHint: true, IdempotentHint: true},
	"post": {
		Title: "Post review", DestructiveHint: true, IdempotentHint: true, OpenWorldHint: true,
	},
	"ready": {
		Title: "Merge readiness", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: true,
	},
	"record": {Title: "Record review", IdempotentHint: true},
	"report": {Title: "Render report", IdempotentHint: true},
	"show":   {Title: "Show finding", ReadOnlyHint: true, IdempotentHint: true},
	"threads": {
		Title: "Refresh review threads", IdempotentHint: true, OpenWorldHint: true,
	},
	"trend":  {Title: "Review trend", ReadOnlyHint: true, IdempotentHint: true},
	"triage": {Title: "Triage finding", DestructiveHint: true},
	"verify": {
		Title: "Run verification", DestructiveHint: true, OpenWorldHint: true,
	},
}

// TestToolsListSnapshot pins the agent-facing tool contract: names, titles,
// annotations, and the deliberate read-only/destructive split. The operator
// set is the full snapshot; TestSurfaceSets pins which subset is default.
//
//nolint:revive // the snapshot matrix reads as one block
func TestToolsListSnapshot(t *testing.T) {
	t.Parallel()
	tools := allTools(false, true)
	if len(tools) != len(wantAnnotations) {
		t.Fatalf("tools = %d, want %d", len(tools), len(wantAnnotations))
	}
	for _, tool := range tools {
		want, ok := wantAnnotations[tool.name]
		if !ok {
			t.Errorf("%s has no snapshot entry", tool.name)
			continue
		}
		got := toolAnnotations{
			Title:           tool.title,
			ReadOnlyHint:    tool.readOnly,
			DestructiveHint: tool.destructive,
			IdempotentHint:  tool.idempotent,
			OpenWorldHint:   tool.openWorld,
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s annotations = %+v, want %+v", tool.name, got, want)
		}
		if tool.readOnly && tool.destructive {
			t.Errorf("%s is both read-only and destructive", tool.name)
		}
		if strings.TrimSpace(tool.title) == "" {
			t.Errorf("%s has no title", tool.name)
		}
		if strings.Count(tool.description, ". ") < 2 || len(tool.description) > 320 {
			t.Errorf("%s description is not three short clauses: %q", tool.name, tool.description)
		}
	}
}

// TestSurfaceSets pins the default, read-only, and operator tool sets.
func TestSurfaceSets(t *testing.T) {
	t.Parallel()
	names := func(tools []*tool) []string {
		out := make([]string, 0, len(tools))
		for _, tool := range tools {
			out = append(out, tool.name)
		}
		return out
	}
	defaults := names(allTools(false, false))
	wantDefaults := []string{
		"adopt", "calibrate", "check-tree", "config", "doctor", "fetch", "fetch-local", "fix",
		"index", "init", "learn", "next", "pack", "post", "ready", "record", "report", "show",
		"threads", "trend", "verify",
	}
	if !reflect.DeepEqual(defaults, wantDefaults) {
		t.Errorf("default tools = %v, want %v", defaults, wantDefaults)
	}
	readOnly := names(allTools(true, false))
	wantReadOnly := []string{"check-tree", "config", "doctor", "index", "next", "pack", "ready", "record", "report", "show", "trend"}
	if !reflect.DeepEqual(readOnly, wantReadOnly) {
		t.Errorf("read-only tools = %v, want %v", readOnly, wantReadOnly)
	}
	operator := names(allTools(false, true))
	wantOperator := append(append([]string{}, wantDefaults[:len(wantDefaults)-1]...), "triage", "verify")
	if !reflect.DeepEqual(operator, wantOperator) {
		t.Errorf("operator tools = %v, want %v", operator, wantOperator)
	}
}

// TestInitializeInstructions proves every client receives the short doctrine
// block, and that the operator boundary matches the surface.
//
//nolint:revive,gocognit // the per-surface matrix reads as one block
func TestInitializeInstructions(t *testing.T) {
	t.Parallel()
	//nolint:govet // the case fields read in name/flag order
	cases := []struct {
		name     string
		operator bool
		want     []string
		absent   []string
	}{
		{
			name: "agent",
			want: []string{"fetch", "review.json", "post", "triage[]", "hint", "not on this surface"},
		},
		{
			name:     "operator",
			operator: true,
			want:     []string{"fetch", "post", "triage", "runs with --operator"},
			absent:   []string{"not on this surface"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := testConfig()
			cfg.Operator = tc.operator
			frames := serve(t, &cfg,
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
			var init initializeResult
			if err := json.Unmarshal(frames["1"].Result, &init); err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(init.Instructions, want) {
					t.Errorf("instructions miss %q:\n%s", want, init.Instructions)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(init.Instructions, absent) {
					t.Errorf("instructions unexpectedly contain %q:\n%s", absent, init.Instructions)
				}
			}
			if len(strings.Split(init.Instructions, "\n")) < 5 {
				t.Errorf("instructions are too thin:\n%s", init.Instructions)
			}
		})
	}
}
