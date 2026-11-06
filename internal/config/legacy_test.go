package config

import (
	"testing"
)

// TestLegacyLayersOrder pins the legacy file layer between the typed
// repository layer and the personal layer: the repository wins, legacy
// wins over personal, and each effective value names the file behind it.
func TestLegacyLayersOrder(t *testing.T) {
	repo, ref := Parse([]byte(`{"schema_version":"1","defaults":{"panel":["repo-panel"],"max_bytes":100}}`))
	if ref != nil {
		t.Fatalf("Parse: %v", ref)
	}
	got, rerr := Resolve(&Inputs{
		Repo:     repo,
		Personal: &PersonalSettings{Review: Partial{Panel: []string{"my-panel"}, PanelSet: true}},
		Legacy: LegacyInputs{
			Panel:    []byte("# staff picks\nlegacy-panel\n"),
			Budget:   []byte("$2.50\n"),
			MaxBytes: []byte("200\n"),
		},
	})
	if rerr != nil {
		t.Fatalf("Resolve: %v", rerr)
	}
	values, origins := got.Defaults.Values, got.Defaults.Origins
	if len(values.Panel) != 1 || values.Panel[0] != "repo-panel" {
		t.Errorf("panel = %v, want the repository panel winning over legacy and personal", values.Panel)
	}
	if origins["panel"].Source != ".verdict/config.json" {
		t.Errorf("panel origin = %q, want the repository file", origins["panel"].Source)
	}
	if values.MaxBytes != 100 || origins["max_bytes"].Source != ".verdict/config.json" {
		t.Errorf("cap = %d from %q, want 100 from the repository file winning over legacy", values.MaxBytes, origins["max_bytes"].Source)
	}
	if values.BudgetUSD == nil || *values.BudgetUSD != 2.5 || origins["budget_usd"].Source != ".review/budget" {
		t.Errorf("budget = %v from %q, want 2.5 from the legacy file with no repository value", values.BudgetUSD, origins["budget_usd"].Source)
	}
	if values.BudgetDisplay != "2.50" {
		t.Errorf("budget display = %q, want the file literal for human lines", values.BudgetDisplay)
	}
}

// TestLegacyMalformed pins the shipped file-named refusals: a malformed
// budget or byte-cap knob file refuses instead of silently falling back.
func TestLegacyMalformed(t *testing.T) {
	assertLegacyRefusal(t, "budget garbage", LegacyInputs{Budget: []byte("free\n")}, "bad-budget")
	assertLegacyRefusal(t, "budget non-positive", LegacyInputs{Budget: []byte("$0\n")}, "bad-budget")
	assertLegacyRefusal(t, "cap garbage", LegacyInputs{MaxBytes: []byte("lots\n")}, "bad-max-bytes")
	assertLegacyRefusal(t, "cap non-positive", LegacyInputs{MaxBytes: []byte("0\n")}, "bad-max-bytes")
}

func assertLegacyRefusal(t *testing.T, name string, files LegacyInputs, code string) {
	t.Helper()
	_, ref := Resolve(&Inputs{Legacy: files})
	if ref == nil || ref.Code != code {
		t.Fatalf("%s: Resolve = %v, want %s", name, ref, code)
	}
}

// TestLegacyCommentLines pins the knob-file scan: comments and blanks
// never become values, and usable lines keep file order.
func TestLegacyCommentLines(t *testing.T) {
	got, ref := Resolve(&Inputs{Legacy: LegacyInputs{
		Panel: []byte("# staff\n\nbase-panel\n# retired\n"),
	}})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	if len(got.Defaults.Values.Panel) != 1 || got.Defaults.Values.Panel[0] != "base-panel" {
		t.Errorf("panel = %v, want the single usable line", got.Defaults.Values.Panel)
	}
}
