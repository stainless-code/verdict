package config

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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
	assertProposedOrigins(t, data.Values, proposed)
}

// assertProposedOrigins pins the proposed relabeling: the repository value
// names the file that supplied it, and a value another layer supplied keeps
// that layer's own label.
func assertProposedOrigins(t *testing.T, values []core.ConfigValue, proposed string) {
	t.Helper()
	if panel := mustValue(t, values, "panel"); panel.Layer != LayerRepo || panel.Source != "proposed file "+proposed {
		t.Fatalf("panel origin = %+v, want the proposed file source", panel)
	}
	if budget := mustValue(t, values, "budget_usd"); budget.Layer != LayerEnv || budget.Source != "REVIEW_BUDGET_USD" {
		t.Fatalf("budget origin = %+v, want the environment layer unchanged", budget)
	}
}

// TestPreviewNoBundleReportsUnavailableBase pins the honest no-bundle
// preview: without a run bundle nothing read the base configuration, so the
// preview labels the repository layer unavailable with its remedy instead of
// claiming the repository configures no policy — even when the checkout does
// carry a tracked configuration. The personal, environment, and built-in
// layers still resolve, so the preview stays a read that needs no fetch.
func TestPreviewNoBundleReportsUnavailableBase(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll(VerdictDir, 0o755); err != nil {
		t.Fatal(err)
	}
	checkout := `{"schema_version":"1","requirements":{"require_checks":["head-current"]}}`
	if err := os.WriteFile(filepath.Join(VerdictDir, "config.json"), []byte(checkout), 0o644); err != nil {
		t.Fatal(err)
	}
	data, warns, ref := Preview(".", "9", "", "", map[string]string{"REVIEW_BUDGET_USD": "1.5"})
	if ref != nil {
		t.Fatalf("Preview: %v", ref)
	}
	assertUnavailableBase(t, data, warns)
	assertBudget(t, data.Values, 1.5)
	if budget := mustValue(t, data.Values, "budget_usd"); budget.Source != "REVIEW_BUDGET_USD" {
		t.Fatalf("budget origin = %+v", budget)
	}
	if maxBytes := mustValue(t, data.Values, "max_bytes"); maxBytes.Value != int64(512000) {
		t.Fatalf("max_bytes = %+v, want the built-in", maxBytes)
	}
}

// assertUnavailableBase pins the no-bundle report: the label and its warning
// say the base was never read, no requirement is invented, and the claim is
// never the unproven "no repository policy".
func assertUnavailableBase(t *testing.T, data *core.ConfigData, warns []string) {
	t.Helper()
	if data.Trusted || data.Source != noBaseLabel {
		t.Fatalf("data = %+v, want the unavailable-base preview", data)
	}
	if data.Source == "no repository policy" {
		t.Fatal("no-bundle preview claims the base configures no policy")
	}
	if len(data.Requirements) != 0 {
		t.Fatalf("requirements = %+v, want none proven without the base read", data.Requirements)
	}
	if !slices.Contains(warns, noBaseWarning) {
		t.Fatalf("warnings = %v, want the unavailable-base remedy", warns)
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

// TestPreviewHeadInventoryIsUntrusted keeps a recorded worktree opt-out
// distinct from trusted base policy, including a legacy snapshot.
func TestPreviewHeadInventoryIsUntrusted(t *testing.T) {
	serviceFixture(t)
	if err := os.Remove(filepath.Join(".verdict", "state", "7", "run.json")); err != nil {
		t.Fatal(err)
	}
	data, _, ref := Preview(".", "7", "", "", map[string]string{})
	if ref != nil {
		t.Fatalf("Preview: %v", ref)
	}
	if data.Trusted || data.Source != "recorded worktree inventory" {
		t.Fatalf("data = %+v, want the untrusted worktree inventory", data)
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

// previewPolicy is the preview fixture policy: a root clause, a root required
// check, the bind requirement, and a scope that adds its own clause, check,
// and defaults. Every expectation below is a literal, so the assertions check
// the contract instead of a second call to the same helper.
const previewPolicy = `{"schema_version":"1",
	"defaults":{"panel":["base-panel"],"effort":"standard","budget_usd":2.5},
	"requirements":{"block_clauses":[{"name":"root-security","threshold":"important","categories":["security"]}],
		"require_checks":["no-open-above-block"],"require_bind":true},
	"scopes":[{"name":"vendored","include":["vendor/**"],
		"requirements":{"block_clauses":[{"name":"vendored-sql","threshold":"critical","categories":["security"],"paths":["vendor/**"]}],
			"require_checks":["head-current"]},
		"defaults":{"effort":"deep"}}]}`

// previewBundle commits previewPolicy at the base, resolves and captures it
// with the shipped resolver, persists the run state, and snapshots the trusted
// base through controls.Resolve: a preview then reads exactly the verified
// inputs a fetch would have recorded.
func previewBundle(t *testing.T, files []string) string {
	t.Helper()
	base := baseRepo(t, map[string]string{".verdict/config.json": previewPolicy})
	cfg, ref := Parse([]byte(previewPolicy))
	if ref != nil {
		t.Fatalf("Parse: %v", ref)
	}
	in := &Inputs{Repo: cfg, BaseSHA: base}
	for _, path := range files {
		in.Files = append(in.Files, FileInput{Path: path})
	}
	resolved, ref := Resolve(in)
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	dir := filepath.Join(".", VerdictDir, StateDirName, "7")
	rs := &RunState{
		BaseSHA: base, Trusted: true, CapturedAt: "2026-11-06T00:00:00Z",
		Evidence: Capture(in, resolved, []byte(previewPolicy), "2026-11-06T00:00:00Z"),
		Resolved: resolved,
	}
	if err := WriteRunState(dir, rs); err != nil {
		t.Fatalf("WriteRunState: %v", err)
	}
	if _, ref := controls.Resolve(t.Context(), cmdr, &controls.Request{
		BundleDir: dir, Root: ".", BaseSHA: base,
	}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
	list := strings.Join(files, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".difffiles"), []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
	return base
}

// requirementRows renders one requirement list as comparable rows, so a test
// compares whole entries — key, threshold, status, and sources — as literals.
func requirementRows(reqs []core.ConfigRequirement) []string {
	out := make([]string, 0, len(reqs))
	for _, req := range reqs {
		threshold := "-"
		if req.Threshold != nil {
			threshold = *req.Threshold
		}
		out = append(out, req.Key+" "+threshold+" "+req.Status+" via "+strings.Join(req.Sources, ","))
	}
	return out
}

// assertBundleRequirements pins the prospective requirement rows of
// previewPolicy for the trusted-base bundle.
func assertBundleRequirements(t *testing.T, reqs []core.ConfigRequirement, base string) {
	t.Helper()
	want := []string{
		"block_clauses/root-security important pending via repo base " + base,
		"block_clauses/vendored-sql critical pending via scope vendored",
		"require_bind - pending via repo base " + base,
		"require_checks/head-current - pending via scope vendored",
		"require_checks/no-open-above-block - pending via repo base " + base,
	}
	if got := requirementRows(reqs); !slices.Equal(got, want) {
		t.Fatalf("requirements = %v\nwant %v", got, want)
	}
}

// assertVendoredChecks pins the plan checks the requirements add: the required
// root check reaches every group, and the scope's own check reaches only the
// group that carries the vendored path.
func assertVendoredChecks(t *testing.T, data *core.ConfigData) {
	t.Helper()
	vendored := false
	for i := range data.Plan {
		group := &data.Plan[i]
		if !slices.Contains(group.Files, "vendor/lib.go") {
			continue
		}
		vendored = true
		if want := []string{"no-open-above-block", "head-current"}; !slices.Equal(group.Checks, want) {
			t.Fatalf("vendored checks = %v, want %v", group.Checks, want)
		}
	}
	if !vendored {
		t.Fatalf("plan = %+v, want a group for vendor/lib.go", data.Plan)
	}
}

// TestPreviewBundleMatchesProspectivePolicy pins the preview defect closure: a
// captured trusted base carrying a block clause and required checks previews
// those requirements instead of an empty list, from the same resolution the
// capture stores, and the explanation of the same bundle names them
// identically. Reads stay reads: nothing executes and no check is certified.
func TestPreviewBundleMatchesProspectivePolicy(t *testing.T) {
	base := previewBundle(t, []string{"vendor/lib.go", "internal/app.go"})
	before := walkNames(t, ".")
	data := configData(t, core.ConfigFlags{Preview: true})
	if after := walkNames(t, "."); len(after) != len(before) {
		t.Fatalf("preview wrote files: before=%v after=%v", before, after)
	}
	assertTrustedPreview(t, data, base)
	assertRequirementsMatchExplain(t, data)
	assertPreviewDriftSafe(t, data, base)
}

// configData runs one config request through the service and returns its data,
// so each assertion below reads a real invocation instead of a shortcut.
func configData(t *testing.T, flags core.ConfigFlags) *core.ConfigData {
	t.Helper()
	resp, ref := Run(context.Background(), &core.ConfigRequest{
		Repo: "acme/app", Pr: "7", Flags: flags,
	})
	if ref != nil {
		t.Fatalf("config %+v: %v", flags, ref)
	}
	return resp.Data
}

// assertTrustedPreview pins the trusted inventory preview: the prospective
// requirements and the plan checks the run would enforce, with no observation
// invented.
func assertTrustedPreview(t *testing.T, data *core.ConfigData, base string) {
	t.Helper()
	if data == nil || data.Mode != "preview" || !data.Trusted || data.Source != "trusted base inventory" {
		t.Fatalf("data = %+v; want the trusted inventory preview", data)
	}
	if len(data.Checks) != 0 {
		t.Fatalf("checks = %+v; a preview executes nothing", data.Checks)
	}
	assertBundleRequirements(t, data.Requirements, base)
	assertVendoredChecks(t, data)
	assertWireRequirements(t, data)
}

// assertWireRequirements pins the rendered surface the CLI, the MCP tool, and
// the TypeScript client read: the envelope data profile prints the prospective
// requirements instead of an empty array.
func assertWireRequirements(t *testing.T, data *core.ConfigData) {
	t.Helper()
	raw, ok := data.Wire().Get("requirements")
	rows, isList := raw.([]any)
	if !ok || !isList || len(rows) != len(data.Requirements) || len(rows) == 0 {
		t.Fatalf("wire requirements = %v (present=%v); want the prospective rows", raw, ok)
	}
}

// assertRequirementsMatchExplain pins that explaining the same bundle names the
// policy the preview promised: the two surfaces cannot disagree again.
func assertRequirementsMatchExplain(t *testing.T, preview *core.ConfigData) {
	t.Helper()
	explain := configData(t, core.ConfigFlags{Explain: true})
	if got, want := requirementRows(explain.Requirements), requirementRows(preview.Requirements); !slices.Equal(got, want) {
		t.Fatalf("explain requirements = %v, preview requirements = %v", got, want)
	}
}

// assertPreviewDriftSafe pins that the preview reads the recorded snapshot: a
// later working-config edit and a later environment value move neither the
// preview nor the explanation of the existing review.
func assertPreviewDriftSafe(t *testing.T, before *core.ConfigData, base string) {
	t.Helper()
	drift := `{"schema_version":"1","defaults":{"panel":["drifted"]}}`
	if err := os.WriteFile(filepath.Join(VerdictDir, "config.json"), []byte(drift), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REVIEW_EFFORT", "max")
	drifted := configData(t, core.ConfigFlags{Preview: true})
	assertBundleRequirements(t, drifted.Requirements, base)
	assertPanel(t, drifted.Values, "base-panel")
	explain := configData(t, core.ConfigFlags{Explain: true})
	if got, want := requirementRows(explain.Requirements), requirementRows(before.Requirements); !slices.Equal(got, want) {
		t.Fatalf("explain moved with the working config: %v != %v", got, want)
	}
}

// TestPreviewProposedLabelsProvenance pins the proposed labeling: the same
// policy read from an unmerged file previews its requirements, names that file
// as the source of every repository-layer value and requirement, and stays
// untrusted — no value claims the tracked base and no requirement is certified.
func TestPreviewProposedLabelsProvenance(t *testing.T) {
	proposed := writeProposed(t, previewPolicy)
	data, _, ref := Preview(".", "7", proposed, "", nil)
	if ref != nil {
		t.Fatalf("Preview: %v", ref)
	}
	assertProposedPreview(t, data, proposed)
	assertProposedRequirements(t, data, proposed)
}

// writeProposed writes one proposed configuration in a fresh checkout and
// returns its path.
func writeProposed(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	proposed := filepath.Join(root, "proposed.json")
	if err := os.WriteFile(proposed, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return proposed
}

// assertProposedPreview pins the untrusted label and the per-value relabeling.
func assertProposedPreview(t *testing.T, data *core.ConfigData, proposed string) {
	t.Helper()
	source := "proposed file " + proposed
	if data.Mode != "preview" || data.Trusted || data.Source != source {
		t.Fatalf("data = %+v, want an untrusted proposed preview", data)
	}
	if panel := mustValue(t, data.Values, "panel"); panel.Layer != LayerRepo || panel.Source != source {
		t.Fatalf("panel origin = %+v, want the proposed file on the repo layer", panel)
	}
	if built := mustValue(t, data.Values, "max_bytes"); built.Layer != LayerBuiltIn || built.Source != LayerBuiltIn {
		t.Fatalf("max_bytes origin = %+v; only repo-layer values are relabelled", built)
	}
	if len(data.Checks) != 0 {
		t.Fatalf("checks = %+v; a proposed preview certifies nothing", data.Checks)
	}
}

// assertProposedRequirements pins that the prospective requirements name the
// proposed file as their source, never the tracked path or a base commit.
func assertProposedRequirements(t *testing.T, data *core.ConfigData, proposed string) {
	t.Helper()
	source := "proposed file " + proposed
	want := []string{
		"block_clauses/root-security important pending via " + source,
		"require_bind - pending via " + source,
		"require_checks/no-open-above-block - pending via " + source,
	}
	if got := requirementRows(data.Requirements); !slices.Equal(got, want) {
		t.Fatalf("requirements = %v\nwant %v", got, want)
	}
}

// TestPreviewPreservesCapturedRename checks both preview modes retain old-path
// identity and the old scope's clause instead of reconstructing new paths only.
func TestPreviewPreservesCapturedRename(t *testing.T) {
	base := previewRenamedBundle(t)
	data := configData(t, core.ConfigFlags{Preview: true})
	assertRenamedPreview(t, data, "repo base "+base)
	assertRequirementsMatchExplain(t, data)
	proposed := filepath.Join(t.TempDir(), "proposed.json")
	if err := os.WriteFile(proposed, []byte(previewPolicy), 0o600); err != nil {
		t.Fatal(err)
	}
	data = configData(t, core.ConfigFlags{File: proposed})
	assertRenamedPreview(t, data, "proposed file "+proposed)
}

func previewRenamedBundle(t *testing.T) string {
	t.Helper()
	base := previewBundle(t, []string{"internal/moved.go"})
	cfg, ref := Parse([]byte(previewPolicy))
	if ref != nil {
		t.Fatal(ref)
	}
	in := &Inputs{Repo: cfg, BaseSHA: base, Files: []FileInput{{Path: "internal/moved.go", OldPath: "vendor/lib.go"}}}
	resolved, ref := Resolve(in)
	if ref != nil {
		t.Fatal(ref)
	}
	dir := filepath.Join(VerdictDir, StateDirName, "7")
	rs := mustReadRunState(t, dir)
	rs.Resolved = resolved
	rs.Evidence = Capture(in, resolved, []byte(previewPolicy), "2026-11-06T00:00:00Z")
	if err := WriteRunState(dir, rs); err != nil {
		t.Fatal(err)
	}
	return base
}

func assertRenamedPreview(t *testing.T, data *core.ConfigData, rootSource string) {
	t.Helper()
	if len(data.Files) != 1 || data.Files[0].Path != "internal/moved.go" || data.Files[0].OldPath != "vendor/lib.go" {
		t.Fatalf("files = %+v, want the captured new and old paths", data.Files)
	}
	want := []string{
		"block_clauses/root-security important pending via " + rootSource,
		"block_clauses/vendored-sql critical pending via scope vendored (old path vendor/lib.go)",
		"require_bind - pending via " + rootSource,
		"require_checks/head-current - pending via scope vendored (old path vendor/lib.go)",
		"require_checks/no-open-above-block - pending via " + rootSource,
	}
	if got := requirementRows(data.Requirements); !slices.Equal(got, want) {
		t.Fatalf("rename requirements = %v; want %v", got, want)
	}
}

// TestPreviewRefusesEditedCapturedFiles prevents a renamed file's identity
// being changed independently of the verified capture's input digest.
func TestPreviewRefusesEditedCapturedFiles(t *testing.T) {
	previewRenamedBundle(t)
	path := filepath.Join(VerdictDir, StateDirName, "7", runStateFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(raw), "vendor/lib.go", "outside/lib.go")), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, ref := Preview(".", "7", "", "", nil)
	if ref == nil || ref.Code != controls.CodeStale {
		t.Fatalf("preview refusal = %v, want controls-stale", ref)
	}
}
