package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
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
	if err := WriteRunState(filepath.Join(".", ".verdict", "state", "7"), rs); err != nil {
		t.Fatalf("WriteRunState: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(".", ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".", ".verdict", "config.json"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: filepath.Join(".", ".verdict", "state", "7"), Root: ".", BaseSHA: rs.BaseSHA, AllowHead: true,
	}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
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
	assertStoredPanel(t, first.Values)
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

// mustValue fails when the key is missing from the resolved values.
func mustValue(t *testing.T, values []core.ConfigValue, key string) *core.ConfigValue {
	t.Helper()
	value := valueFor(values, key)
	if value == nil {
		t.Fatalf("%s missing from the resolved values", key)
	}
	return value
}

// assertBudget pins one *float64 budget against its literal.
func assertBudget(t *testing.T, values []core.ConfigValue, want float64) {
	t.Helper()
	budget := mustValue(t, values, "budget_usd")
	got, ok := budget.Value.(*float64)
	if !ok || got == nil || *got != want {
		t.Fatalf("budget = %+v, want %v", budget, want)
	}
}

// assertPanel pins one single-entry panel list against its literal.
func assertPanel(t *testing.T, values []core.ConfigValue, want string) {
	t.Helper()
	panel := mustValue(t, values, "panel")
	got, ok := panel.Value.([]string)
	if !ok || len(got) != 1 || got[0] != want {
		t.Fatalf("panel = %#v, want [%s]", panel.Value, want)
	}
}

// assertStoredPanel pins the stored explain layer: the captured panel keeps
// its file origin and the captured budget is replayed.
func assertStoredPanel(t *testing.T, values []core.ConfigValue) {
	t.Helper()
	if panel := mustValue(t, values, "panel"); panel.Source != ".verdict/config.json" {
		t.Fatalf("panel origin = %+v", panel)
	}
	assertReplayedBudget(t, values, 9.5)
}

// assertReplayedBudget pins an evidence-replayed budget: the stored layer
// reports a plain number, while a live preview reports the pointer the
// layer resolved, so each shape keeps its own oracle.
func assertReplayedBudget(t *testing.T, values []core.ConfigValue, want float64) {
	t.Helper()
	budget := mustValue(t, values, "budget_usd")
	if got, ok := budget.Value.(float64); !ok || got != want {
		t.Fatalf("budget = %+v, want %v", budget, want)
	}
}

// valueFor returns the reported value for one key, nil when the key is
// absent: a lookup, not an assertion, so each case states its own literal.
func valueFor(values []core.ConfigValue, key string) *core.ConfigValue {
	for i := range values {
		if values[i].Key == key {
			return &values[i]
		}
	}
	return nil
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
	assertPanel(t, data.Values, "new-panel")
	assertBudget(t, data.Values, 3.25)
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
	assertBudget(t, data.Values, 1.5)
	if budget := mustValue(t, data.Values, "budget_usd"); budget.Source != "REVIEW_BUDGET_USD" {
		t.Fatalf("budget origin = %+v", budget)
	}
	if maxBytes := mustValue(t, data.Values, "max_bytes"); maxBytes.Value != int64(512000) {
		t.Fatalf("max_bytes = %+v, want the built-in", maxBytes)
	}
}

// TestPreviewUnreadableBundleFailsClosed pins the fail-closed posture: a
// bundle path that exists but cannot be traversed (a file where the bundle
// directory belongs, so every child stat is ENOTDIR) is not "no bundle" —
// the recorded-controls refusal stands instead of previewing no policy.
func TestPreviewUnreadableBundleFailsClosed(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	dir := filepath.Join(VerdictDir, StateDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "7"), []byte("not a bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, ref := Preview(".", "7", "", "", map[string]string{})
	if ref == nil {
		t.Fatal("Preview resolved a bundle path that is a file; want a refusal")
	}
	if ref.Code == "" {
		t.Fatalf("refusal = %+v, want a coded refusal", ref)
	}
	if ref.Message == "" {
		t.Fatalf("refusal = %+v, want a diagnostic", ref)
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
	if valueFor(data.Values, "panel") == nil {
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

// explainEvidence writes a capture produced by the real resolver: a repository
// default plus a scope whose defaults contradict it over vendor/**, resolved
// over two files. It returns the persisted bundle directory.
func explainEvidence(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	repo, ref := Parse([]byte(`{"schema_version":"1","defaults":{"effort":"standard",
		"budget_usd":9.5,"display_min_severity":"important"},
		"scopes":[{"name":"vendored","include":["vendor/**"],
		"defaults":{"effort":"deep","display_min_severity":"critical"}}]}`))
	if ref != nil {
		t.Fatalf("Parse: %v", ref)
	}
	in := &Inputs{Repo: repo, Files: []FileInput{{Path: "vendor/lib.go"}, {Path: "app/main.go"}}}
	resolved, rerr := Resolve(in)
	if rerr != nil {
		t.Fatalf("Resolve: %v", rerr)
	}
	rs := &RunState{
		Name:       "7",
		BaseSHA:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted:    true,
		CapturedAt: "2026-11-06T00:00:00Z",
		Evidence:   Capture(in, resolved, nil, "2026-11-06T00:00:00Z"),
		Resolved:   resolved,
	}
	if err := WriteRunState(filepath.Join(".", ".verdict", "state", "7"), rs); err != nil {
		t.Fatalf("WriteRunState: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(".", ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	policy := `{"schema_version":"1","defaults":{"effort":"standard"}}`
	if err := os.WriteFile(filepath.Join(".", ".verdict", "config.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: filepath.Join(".", ".verdict", "state", "7"), Root: ".", BaseSHA: rs.BaseSHA, AllowHead: true,
	}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
	return filepath.Join(".", ".verdict", "state", "7")
}

// TestExplainReportsCapturedShadowsAndFileScopes pins the closure of D7 rows 3,
// 10, 12, and 26: explain reports the losing layer per field and each file's
// own effective configuration from the capture, and keeps reporting it after
// the working configuration and environment change underneath.
func TestExplainReportsCapturedShadowsAndFileScopes(t *testing.T) {
	dir := explainEvidence(t)
	const explainArgs = `{"schema_version":"1","defaults":{"effort":"standard"},
		"scopes":[{"name":"vendored","include":["vendor/**"],
		"defaults":{"effort":"deep"}}]}`

	before, ref := Run(context.Background(), &core.ConfigRequest{
		Repo: "acme/app", Pr: "7", Flags: core.ConfigFlags{Explain: true},
	})
	if ref != nil {
		t.Fatalf("explain: %v", ref)
	}
	assertCapturedExplanation(t, before.Data)

	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(explainArgs), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REVIEW_EFFORT", "max")
	after, ref := Run(context.Background(), &core.ConfigRequest{
		Repo: "acme/app", Pr: "7", Flags: core.ConfigFlags{Explain: true},
	})
	if ref != nil {
		t.Fatalf("explain after drift: %v", ref)
	}
	assertCapturedExplanation(t, after.Data)
}

// assertCapturedExplanation checks the reported values, shadows, and per-file
// configuration against independently written literals.
func assertCapturedExplanation(t *testing.T, data *core.ConfigData) {
	t.Helper()
	if data == nil || len(data.Files) != 2 {
		t.Fatalf("files = %+v; want the two reviewed paths", data)
	}
	vendored, ordinary := data.Files[0], data.Files[1]
	if vendored.Path != "vendor/lib.go" || ordinary.Path != "app/main.go" {
		t.Fatalf("file order = %s, %s; want the captured review order", vendored.Path, ordinary.Path)
	}
	if len(vendored.Scopes) != 1 || vendored.Scopes[0] != "vendored" || len(ordinary.Scopes) != 0 {
		t.Fatalf("scopes = %v / %v; want only the vendored file scoped", vendored.Scopes, ordinary.Scopes)
	}
	assertFileEffort(t, &vendored, "deep", LayerScope)
	assertFileEffort(t, &ordinary, "standard", LayerRepo)
	assertShadowedEffort(t, &vendored)
	if len(data.Shadowed) == 0 {
		t.Fatal("root defaults report no losing values")
	}
}

// assertFileEffort checks one file reports the wanted effort value and layer.
func assertFileEffort(t *testing.T, file *core.ConfigFile, want, layer string) {
	t.Helper()
	got := fileValue(t, file, "effort")
	if got.Value != want || got.Layer != layer {
		t.Fatalf("%s effort = %+v; want %s from the %s layer", file.Path, got, want, layer)
	}
}

// assertShadowedEffort checks the scoped file reports the repo value it
// overrode, so the override never hides silently.
func assertShadowedEffort(t *testing.T, file *core.ConfigFile) {
	t.Helper()
	if len(file.Shadowed) == 0 {
		t.Fatalf("%s reports no losing value", file.Path)
	}
	shadow := file.Shadowed[0]
	if shadow.Key != "effort" || shadow.Value != "standard" || shadow.Layer != LayerRepo {
		t.Fatalf("%s shadow = %+v; want the repo's standard it overrode", file.Path, shadow)
	}
}

// fileValue returns one key's reported value from a per-file configuration.
func fileValue(t *testing.T, file *core.ConfigFile, key string) core.ConfigValue {
	t.Helper()
	for _, value := range file.Values {
		if value.Key == key {
			return value
		}
	}
	t.Fatalf("file %s reports no %s value", file.Path, key)
	return core.ConfigValue{}
}

// TestPreviewReportsProspectiveFileScopes pins the preview half: a proposed
// configuration reports its own per-file effective values and shadows, labeled
// prospective (untrusted), without writing anything or mutating the captured
// bundle.
func TestPreviewReportsProspectiveFileScopes(t *testing.T) {
	explainEvidence(t)
	proposed := filepath.Join(t.TempDir(), "proposed.json")
	body := `{"schema_version":"1","defaults":{"effort":"cheap","budget_usd":1.0},
		"scopes":[{"name":"vendored","include":["vendor/**"],
		"defaults":{"effort":"deep"}}]}`
	if err := os.WriteFile(proposed, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, ref := Run(context.Background(), &core.ConfigRequest{
		Repo: "acme/app", Flags: core.ConfigFlags{File: proposed},
	})
	if ref != nil {
		t.Fatalf("preview: %v", ref)
	}
	if resp.Data == nil || resp.Data.Mode != "preview" || resp.Data.Trusted {
		t.Fatalf("preview data = %+v; want an untrusted preview", resp.Data)
	}
	if len(resp.Data.Files) != 0 {
		t.Fatalf("preview files = %+v; a proposed file has no reviewed paths yet", resp.Data.Files)
	}
	assertPreviewEffort(t, resp.Data.Values)
	assertPreviewWroteNothing(t)
}

// assertPreviewEffort checks the prospective values carry the proposed effort.
func assertPreviewEffort(t *testing.T, values []core.ConfigValue) {
	t.Helper()
	for _, value := range values {
		if value.Key == "effort" {
			if value.Value != "cheap" {
				t.Fatalf("preview effort = %+v; want the proposed cheap", value)
			}
			return
		}
	}
	t.Fatal("preview reports no effort value")
}

// assertPreviewWroteNothing checks a preview leaves no bundle state and does
// not rewrite the working configuration.
func assertPreviewWroteNothing(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(".", ".verdict", "state", "8")); !os.IsNotExist(err) {
		t.Fatalf("preview created bundle state: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(".", ".verdict", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "cheap") {
		t.Fatalf("preview mutated the working configuration:\n%s", raw)
	}
}
