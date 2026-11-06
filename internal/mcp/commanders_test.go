package mcp

import (
	"context"
	"fmt"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
)

// TestToolCommanders pins each tool's production runner at construction, so
// the mapping cannot drift back into a name switch in the server. A Runner
// pins its image cap and missing-tool pre-check too: the two documented
// postures differ only in those options.
func TestToolCommanders(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"adopt":       "<nil>",
		"calibrate":   "exec.Runner{cap=4194304 missing=false}",
		"check-tree":  "exec.Runner{cap=4194304 missing=false}",
		"config":      "<nil>",
		"doctor":      "exec.Runner{cap=4194304 missing=false}",
		"fetch":       "exec.Runner{cap=67108864 missing=true}",
		"fetch-local": "exec.Runner{cap=67108864 missing=true}",
		"fix":         "exec.Runner{cap=67108864 missing=true}",
		"init":        "exec.Runner{cap=67108864 missing=true}",
		"learn":       "<nil>",
		"next":        "<nil>",
		"post":        "exec.Runner{cap=67108864 missing=true}",
		"ready":       "exec.Runner{cap=4194304 missing=false}",
		"record":      "<nil>",
		"report":      "<nil>",
		"show":        "<nil>",
		"threads":     "<nil>",
		"trend":       "<nil>",
		"triage":      "<nil>",
		"verify":      "exec.Watchdog",
	}
	tools := allTools(false, true)
	if len(want) != len(tools) {
		t.Fatalf("commander pin has %d entries, tools = %d", len(want), len(tools))
	}
	for _, tool := range tools {
		if got := commanderPose(tool.deps.commander); got != want[tool.name] {
			t.Errorf("%s commander = %s, want %s", tool.name, got, want[tool.name])
		}
	}
}

func commanderPose(c core.Commander) string {
	if r, ok := c.(coreexec.Runner); ok {
		limit := r.OutputCap
		if limit <= 0 {
			limit = coreexec.ReadOutputCap
		}
		return fmt.Sprintf("exec.Runner{cap=%d missing=%t}", limit, r.MissingTool)
	}
	return fmt.Sprintf("%T", c)
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

type stubCommander struct{}

func (*stubCommander) Run(_ context.Context, _ core.Command) (core.Result, error) {
	return core.Result{}, nil
}

var _ core.Commander = (*stubCommander)(nil)
