package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// serviceFixture writes a run bundle with hand-authored policy: the
// trusted base carries panel base-panel and a max-bytes override, the
// personal layer carries nothing, and the environment supplies the
// budget. The oracle is the resolution contract, not the service.
func serviceFixture(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	base := `{"schema_version":"1","defaults":{"panel":["base-panel"],"max_bytes":100}}`
	rs := &RunState{
		BaseSHA:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted:    true,
		CapturedAt: "2026-11-02T12:00:00Z",
		Evidence: Evidence{
			SchemaVersion: "1",
			BaseSHA:       "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Plan:          Plan{Status: PendingStatus},
			Values: []EvidenceValue{
				{Key: "panel", Value: []string{"base-panel"}, Origin: FieldOrigin{Layer: LayerRepo, Source: ".verdict/config.json"}},
				{Key: "budget_usd", Value: 9.5, Origin: FieldOrigin{Layer: LayerEnv, Source: "REVIEW_BUDGET_USD"}},
			},
		},
		Resolved: &Resolved{
			Plan: Plan{Status: PendingStatus},
			Defaults: ResolvedDefaults{
				Values: EffectiveDefaults{BudgetUSD: new(9.5)},
			},
		},
	}
	if err := WriteRunState(".", "7", rs); err != nil {
		t.Fatalf("WriteRunState: %v", err)
	}
	inv := filepath.Join(".verdict", "state", "7", "inventory")
	if err := os.MkdirAll(inv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inv, "config.json"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
}

//go:fix inline

// TestExplainRequiresBundle pins the required-bundle rule: explaining a
// never-fetched run refuses config-no-bundle naming the path.
func TestExplainRequiresBundle(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	_, _, ref := Explain(".", "99")
	if ref == nil || ref.Code != CodeNoBundle {
		t.Fatalf("Explain absent = %v, want config-no-bundle", ref)
	}
}

// TestExplainRendersStoredEvidence pins the read contract: explain
// replays the stored evidence byte-for-byte, names the trusted base,
// and carries the outcomes — with no environment input at all, so
// later environment changes cannot move it.
func TestExplainRendersStoredEvidence(t *testing.T) {
	serviceFixture(t)
	first, _, ref := Explain(".", "7")
	if ref != nil {
		t.Fatalf("Explain: %v", ref)
	}
	if first.Mode != "explain" || !first.Trusted {
		t.Fatalf("data = %+v, want explain/trusted", first)
	}
	var panel, budget any
	for _, value := range first.Values {
		switch value.Key {
		case "panel":
			panel = value.Value
			if value.Source != ".verdict/config.json" {
				t.Fatalf("panel origin = %+v", value)
			}
		case "budget_usd":
			budget = value.Value
		}
	}
	if panel == nil || budget != 9.5 {
		t.Fatalf("values = %+v, want stored panel and budget", first.Values)
	}
	if first.Source != "run bundle 7 (base bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb)" {
		t.Fatalf("source = %q", first.Source)
	}
	second, _, ref := Explain(".", "7")
	if ref != nil {
		t.Fatalf("second Explain: %v", ref)
	}
	if len(first.Values) != len(second.Values) || first.Source != second.Source || first.Trusted != second.Trusted {
		t.Fatal("explanation moved between reads with no input change")
	}
}

// TestPreviewProposedFile pins the proposed path: a hand-authored file
// with an unknown-to-the-bundle panel resolves untrusted, names the
// file, and merges the current environment budget over it.
func TestPreviewProposedFile(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	proposed := filepath.Join(root, "proposed.json")
	if err := os.WriteFile(proposed, []byte(`{"schema_version":"1","defaults":{"panel":["new-panel"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	data, _, ref := Preview(".", "7", proposed, "", map[string]string{"REVIEW_BUDGET_USD": "3.25"})
	if ref != nil {
		t.Fatalf("Preview: %v", ref)
	}
	if data.Mode != "preview" || data.Trusted {
		t.Fatalf("data = %+v, want untrusted preview", data)
	}
	if data.Source != "proposed file "+proposed {
		t.Fatalf("source = %q", data.Source)
	}
	got := map[string]any{}
	for _, value := range data.Values {
		got[value.Key] = value.Value
	}
	if panel, ok := got["panel"].([]string); !ok || len(panel) != 1 || panel[0] != "new-panel" {
		t.Fatalf("panel = %#v, want the proposed panel", got["panel"])
	}
	if budget, ok := got["budget_usd"].(*float64); !ok || budget == nil || *budget != 3.25 {
		t.Fatalf("budget_usd = %#v, want the environment budget", got["budget_usd"])
	}
}

// TestPreviewNoBundleResolvesNoPolicy pins the no-init preview: with
// no bundle and no file, the personal and environment layers resolve
// over the built-ins instead of refusing.
func TestPreviewNoBundleResolvesNoPolicy(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	data, _, ref := Preview(".", "9", "", "", map[string]string{"REVIEW_BUDGET_USD": "1.5"})
	if ref != nil {
		t.Fatalf("Preview: %v", ref)
	}
	if data.Trusted || data.Source != "no repository policy" {
		t.Fatalf("data = %+v, want untrusted no-policy preview", data)
	}
	found := false
	for _, value := range data.Values {
		if value.Key == "budget_usd" {
			found = true
			budget, ok := value.Value.(*float64)
			if !ok || budget == nil || *budget != 1.5 || value.Source != "REVIEW_BUDGET_USD" {
				t.Fatalf("budget = %+v", value)
			}
		}
		if value.Key == "max_bytes" && value.Value != int64(512000) {
			t.Fatalf("max_bytes = %+v, want the built-in", value)
		}
	}
	if !found {
		t.Fatal("budget missing from no-policy preview")
	}
}

// TestPreviewTrustedInventory pins the trusted path: the inventory
// snapshot resolves trusted even with no run bundle present.
func TestPreviewTrustedInventory(t *testing.T) {
	serviceFixture(t)
	if err := os.Remove(filepath.Join(".verdict", "state", "7", "run.json")); err != nil {
		t.Fatal(err)
	}
	data, _, ref := Preview(".", "7", "", "", map[string]string{})
	if ref != nil {
		t.Fatalf("Preview: %v", ref)
	}
	if !data.Trusted || data.Source != "trusted base inventory" {
		t.Fatalf("data = %+v, want the trusted inventory", data)
	}
	found := false
	for _, value := range data.Values {
		if value.Key == "panel" {
			found = true
		}
	}
	if !found {
		t.Fatal("inventory panel missing from preview")
	}
}

// TestPreviewProposedGarbage pins the strict path: a malformed proposed
// file refuses with the parser diagnostic, never a resolved preview.
func TestPreviewProposedGarbage(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	proposed := filepath.Join(root, "proposed.json")
	if err := os.WriteFile(proposed, []byte(`{"schema_version":"1","defaults":{"panel":"nope"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, ref := Preview(".", "7", proposed, "", map[string]string{})
	if ref == nil || ref.Code != CodeBadConfig {
		t.Fatalf("Preview garbage = %v, want bad-config", ref)
	}
}

// TestWireRoundTrip pins the envelope shape: wire output carries every
// data key the schema pins, with origins beside values.
func TestWireRoundTrip(t *testing.T) {
	serviceFixture(t)
	data, _, ref := Explain(".", "7")
	if ref != nil {
		t.Fatalf("Explain: %v", ref)
	}
	wire := data.Wire()
	for _, key := range []string{"mode", "trusted", "source", "values", "requirements", "plan", "plan_status", "spend", "privacy"} {
		if !wire.Has(key) {
			t.Fatalf("wire misses %q", key)
		}
	}
	if mode, _ := wire.Get("mode"); mode != "explain" {
		t.Fatalf("mode = %v", mode)
	}
	_ = core.ConfigData{}
}

// TestRunResolvesLocalForm pins the service mapping: the positional
// resolves to the fetch-local bundle name, and the envelope carries it.
func TestRunResolvesLocalForm(t *testing.T) {
	serviceFixture(t)
	if err := os.Rename(
		filepath.Join(".verdict", "state", "7"),
		filepath.Join(".verdict", "state", "local-demo")); err != nil {
		t.Fatal(err)
	}
	resp, ref := Run(context.Background(), &core.ConfigRequest{
		Repo: "acme/app", Pr: "demo",
		Flags: core.ConfigFlags{Explain: true},
	})
	if ref != nil {
		t.Fatalf("Run: %v", ref)
	}
	if resp.Pr != "local-demo" || resp.Data == nil || resp.Data.Mode != "explain" {
		t.Fatalf("resp = %+v", resp)
	}
}

// TestRunPreviewWritesNothing pins the read-only contract: a preview
// over a proposed file creates no bundle, state, or inventory files.
func TestRunPreviewWritesNothing(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	proposed := filepath.Join(root, "proposed.json")
	if err := os.WriteFile(proposed, []byte(`{"schema_version":"1","defaults":{"panel":["new-panel"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	before := walkNames(t, root)
	resp, ref := Run(context.Background(), &core.ConfigRequest{
		Repo: "acme/app", Pr: "9",
		Flags: core.ConfigFlags{Preview: true, File: proposed},
	})
	if ref != nil {
		t.Fatalf("Run: %v", ref)
	}
	if resp.Data == nil || resp.Data.Trusted {
		t.Fatalf("resp = %+v, want untrusted preview", resp)
	}
	if after := walkNames(t, root); len(after) != len(before) {
		t.Fatalf("preview wrote files: before=%v after=%v", before, after)
	}
}

// walkNames lists every file under root for the read-only assertion.
func walkNames(t *testing.T, root string) []string {
	t.Helper()
	var names []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			names = append(names, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}
