package fetchcore

import (
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

// TestDumpPlanExportsResolvedGroupDispatch proves the captured plan surface
// carries each group's actually resolved panel roster and accounting effort,
// not just the shared global defaults. The values come from a real Resolve
// over two scopes with different panels and efforts, so the dump cannot echo
// fixture literals it never saw. This is an offline contract check: it
// proves the exported surface and the shipped instructions agree, not that a
// model obeyed them.
func TestDumpPlanExportsResolvedGroupDispatch(t *testing.T) {
	resolved := resolveDispatchFixture(t, &config.Partial{})
	out := dumpPlanText(resolved)
	assertGroupDispatch(t, out, resolved, "code", "panel=[panel-a] panel_from=scope code effort=cheap")
	assertGroupDispatch(t, out, resolved, "docs", "panel=[panel-b] panel_from=scope docs effort=deep")
	if strings.Contains(out, "effort=standard") {
		t.Fatalf("plan surface leaked an unresolved effort tier:\n%s", out)
	}
}

// TestDumpPlanOperatorOverrideOutranksScopes proves an explicit invocation
// layer wins over both scope panels and efforts: the dump exports the
// operator's values and neither scope's roster.
func TestDumpPlanOperatorOverrideOutranksScopes(t *testing.T) {
	effort := "standard"
	resolved := resolveDispatchFixture(t, &config.Partial{
		Panel: []string{"panel-override"}, PanelSet: true, Effort: &effort,
	})
	out := dumpPlanText(resolved)
	if len(resolved.Plan.Groups) != 1 {
		t.Fatalf("groups = %d, want one after a uniform override", len(resolved.Plan.Groups))
	}
	assertGroupDispatch(t, out, resolved, "", "panel=[panel-override] panel_from=command line effort=standard")
	for _, leaked := range []string{"panel-a", "panel-b", "effort=cheap", "effort=deep"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("override dump leaked scope value %q:\n%s", leaked, out)
		}
	}
}

// resolveDispatchFixture runs the genuine resolver over two scopes whose
// panels and efforts differ, plus whatever invocation layer the caller sets.
func resolveDispatchFixture(t *testing.T, flags *config.Partial) *config.Resolved {
	t.Helper()
	cheap, deep := "cheap", "deep"
	resolved, ref := config.Resolve(&config.Inputs{
		Repo: &core.Config{
			SchemaVersion: "1",
			Scopes: []core.ConfigScope{
				{Name: "code", Include: []string{"app/**"}, Defaults: &core.ConfigDefaults{
					Panel: []string{"panel-a"}, Effort: &cheap,
				}},
				{Name: "docs", Include: []string{"docs/**"}, Defaults: &core.ConfigDefaults{
					Panel: []string{"panel-b"}, Effort: &deep,
				}},
			},
		},
		Files: []config.FileInput{{Path: "app/main.go"}, {Path: "docs/readme.md"}},
		Flags: *flags,
	})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	return resolved
}

// dumpPlanText renders the plan section through the real producer path.
func dumpPlanText(resolved *config.Resolved) string {
	var sb strings.Builder
	dumpPlan(&fetchRunCtx{runCfg: &runConfig{resolved: resolved}}, &sb, "NONCE")
	return sb.String()
}

// requireGroupLine finds the single line naming the group id and requires the
// literal dispatch surface on it.
func requireGroupLine(t *testing.T, out, groupID, want string) {
	t.Helper()
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.Contains(line, groupID) {
			continue
		}
		if !strings.Contains(line, want) {
			t.Fatalf("group line = %q, want %q", line, want)
		}
		return
	}
	t.Fatalf("plan surface omits %q:\n%s", groupID, out)
}

// assertGroupDispatch requires the literal dispatch surface on the rendered
// line of the group carrying the named scope (empty names the only group).
func assertGroupDispatch(t *testing.T, out string, resolved *config.Resolved, scope, want string) {
	t.Helper()
	for i := range resolved.Plan.Groups {
		group := &resolved.Plan.Groups[i]
		if group.ID == "" || (scope != "" && !slices.Contains(group.Scopes, scope)) {
			continue
		}
		requireGroupLine(t, out, "id="+group.ID+" ", want)
		return
	}
	t.Fatalf("plan surface omits a group line for %q:\n%s", want, out)
}
