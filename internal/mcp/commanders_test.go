package mcp

import (
	"context"
	"fmt"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestToolCommanders pins each tool's production runner at construction, so
// the mapping cannot drift back into a name switch in the server.
func TestToolCommanders(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"adopt":       "<nil>",
		"calibrate":   "exec.SpawnCommander",
		"check-tree":  "exec.SpawnCommander",
		"doctor":      "exec.SpawnCommander",
		"fetch":       "exec.SplitCommander",
		"fetch-local": "exec.SplitCommander",
		"fix":         "exec.SplitCommander",
		"init":        "exec.SplitCommander",
		"learn":       "<nil>",
		"next":        "<nil>",
		"post":        "exec.SplitCommander",
		"ready":       "exec.SpawnCommander",
		"record":      "<nil>",
		"report":      "<nil>",
		"show":        "<nil>",
		"threads":     "<nil>",
		"trend":       "<nil>",
		"triage":      "<nil>",
		"verify":      "exec.OSCommander",
	}
	tools := allTools(false, true)
	if len(want) != len(tools) {
		t.Fatalf("commander pin has %d entries, tools = %d", len(want), len(tools))
	}
	for _, tool := range tools {
		if got := fmt.Sprintf("%T", tool.deps.commander); got != want[tool.name] {
			t.Errorf("%s commander = %s, want %s", tool.name, got, want[tool.name])
		}
	}
}

// TestCommanderOverride proves a test can replace one tool's runner through
// Config and that the session then injects it without a name switch.
func TestCommanderOverride(t *testing.T) {
	t.Parallel()
	fake := &stubCommander{}
	cfg := testConfig()
	cfg.Commanders = map[string]core.Commander{"post": fake}
	server := New(&cfg)
	post, ok := server.byName["post"]
	if !ok {
		t.Fatal("post is not registered")
	}
	if post.deps.commander != fake {
		t.Fatalf("post commander = %T, want the injected fake", post.deps.commander)
	}
	if got := server.bindings(post).commander; got != fake {
		t.Fatalf("bindings commander = %T, want the injected fake", got)
	}
	if other := server.byName["report"]; other.deps.commander != nil {
		t.Fatalf("report commander = %T, want its production nil", other.deps.commander)
	}
}

// stubCommander is a no-op runner for injection tests. It intentionally
// satisfies only the base Commander interface.
type stubCommander struct{}

// Run returns an empty result.
func (*stubCommander) Run(_ context.Context, _ core.Command) (core.Result, error) {
	return core.Result{}, nil
}

// The compile-time check keeps the stub usable as a core.Commander.
var _ core.Commander = (*stubCommander)(nil)
