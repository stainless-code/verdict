package fetchcore

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

// TestDumpPlanPrintsStableGroupID proves the agent-facing plan surface and
// the captured run.json plan agree: every rendered line carries the exact
// PlanGroup.ID observations and lens attestations must name, and the
// display name stays a separate field. The ids here come from a real
// Resolve over two distinct profiles, not from literals the dump echoes.
func TestDumpPlanPrintsStableGroupID(t *testing.T) {
	resolved, ref := config.Resolve(&config.Inputs{
		Repo:  twoProfileConfig(),
		Files: []config.FileInput{{Path: "app/main.go"}, {Path: "docs/readme.md"}},
	})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	ctx := &fetchRunCtx{runCfg: &runConfig{resolved: resolved}}
	var sb strings.Builder
	dumpPlan(ctx, &sb, "NONCE")
	if len(resolved.Plan.Groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(resolved.Plan.Groups))
	}
	assertPlanIdentities(t, sb.String(), resolved.Plan.Groups)
}

// twoProfileConfig is the fixture: two distinct profiles reached through two
// scopes, so the ids under test come from the resolver, never from the fixture.
func twoProfileConfig() *core.Config {
	deep, zero, docs := "deep", 0, "docs"
	deepProfile, docsProfile := "deep-review", "docs-review"
	return &core.Config{
		SchemaVersion: "1",
		Profiles: map[string]core.ConfigProfile{
			"deep-review": {Depth: &deep, MaxLenses: &zero},
			"docs-review": {Depth: &docs},
		},
		Scopes: []core.ConfigScope{
			{Name: "code", Include: []string{"app/**"}, Profile: &deepProfile},
			{Name: "docs", Include: []string{"docs/**"}, Profile: &docsProfile},
		},
	}
}

// assertPlanIdentities checks the rendered plan surface keeps each group's
// stable id and its display label as separate fields.
func assertPlanIdentities(t *testing.T, out string, groups []config.PlanGroup) {
	t.Helper()
	for i := range groups {
		group := &groups[i]
		if !strings.Contains(out, " id="+group.ID+" ") {
			t.Errorf("plan surface omits stable id %s:\n%s", group.ID, out)
		}
		if !strings.Contains(out, group.Label+":") {
			t.Errorf("plan surface omits display label %s:\n%s", group.Label, out)
		}
		if strings.Contains(out, "id="+group.Label) {
			t.Errorf("display label %q was used as an identity", group.Label)
		}
	}
}
