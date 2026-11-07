package fetchcore

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

// TestEstimateRecsWarnsWithSlashPaths pins the user-visible warning to
// forward-slash paths. The sweep globs bundle cost files, and
// [filepath.Glob] returns OS separators, so Windows rendered
// backslashes where the golden and every bundle path uses slashes.
func TestEstimateRecsWarnsWithSlashPaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".verdict", "state", "2", "cost.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var warnings []string
	r := &session{warnings: &warnings}
	if recs := estimateRecs(r, nil); len(recs) != 0 {
		t.Fatalf("recs = %d, want 0", len(recs))
	}
	want := "cost history unreadable — skipped from estimate: .verdict/state/2/cost.json "
	if len(warnings) != 1 || warnings[0] != want {
		t.Fatalf("warnings = %q, want [%q]", warnings, want)
	}
}

// costState writes one bundle's cost.json list under the run's root.
func costState(t *testing.T, root, bundle, body string) {
	t.Helper()
	path := filepath.Join(root, ".verdict", "state", bundle, "cost.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// resolvedEffort builds the resolved view the estimator reads: the layer that
// chose the effort tier is the whole difference between an explicit request
// and the builtin default.
func resolvedEffort(tier, layer string) *config.Resolved {
	return &config.Resolved{Defaults: config.ResolvedDefaults{
		Values:  config.EffectiveDefaults{Effort: tier},
		Origins: map[string]config.FieldOrigin{"effort": {Layer: layer, Source: layer}},
	}}
}

// TestCostEstimateHonorsResolvedEffort proves the projection follows the
// operator's resolved effort tier, not the historical most-used tier, and
// that absent or unusable selections keep their documented behavior.
func TestCostEstimateHonorsResolvedEffort(t *testing.T) {
	t.Parallel()
	history := `[{"effort":"deep","est_usd":9.00},{"effort":"deep","est_usd":11.00},` +
		`{"effort":"deep","est_usd":10.00},{"effort":"cheap","est_usd":1.00},` +
		`{"effort":"standard","est_usd":5.00}]`
	tests := []struct {
		name    string
		res     *config.Resolved
		records string
		wantT   string
		wantUSD string
	}{
		{"explicit cheap beats deep majority", resolvedEffort("cheap", "repo"), history, "cheap", "1"},
		{"explicit standard overrides history", resolvedEffort("standard", "personal"), history, "standard", "5"},
		{"absent effort keeps the historical pick", resolvedEffort("standard", config.LayerBuiltIn), history, "deep", "10"},
		{"no resolved view keeps the historical pick", nil, history, "deep", "10"},
		{
			"selected tier without records reads unknown", resolvedEffort("deep", "repo"),
			`[{"effort":"cheap","est_usd":1.00}]`, "", "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertEstimate(t, tc.records, tc.res, tc.wantT, tc.wantUSD)
		})
	}
}

// assertEstimate measures one history through the estimator and checks the
// tier and projection the resolution asks for.
func assertEstimate(t *testing.T, records string, res *config.Resolved, wantTier, wantUSD string) {
	t.Helper()
	root := t.TempDir()
	costState(t, root, "2", records)
	r := &session{repoRoot: root, Human: io.Discard, warnings: new([]string)}
	var st budgetState
	if err := costEstimate(r, &st, res); err != nil {
		t.Fatalf("costEstimate: %v", err)
	}
	if st.estTier != wantTier || st.estUSD != wantUSD {
		t.Fatalf("estimate = tier %q usd %q; want tier %q usd %q", st.estTier, st.estUSD, wantTier, wantUSD)
	}
}

// requireKnownNoBudget resolves the require-known posture with no ordinary
// budget and no personal ceiling: the strict spend policy alone.
func requireKnownNoBudget(t *testing.T) *config.Resolved {
	t.Helper()
	repo, ref := config.Parse([]byte(`{"schema_version":"1","defaults":{}}`))
	if ref != nil {
		t.Fatalf("repo parse: %v", ref)
	}
	resolved, rerr := config.Resolve(&config.Inputs{
		Repo:     repo,
		Personal: &config.PersonalSettings{Spend: config.PersonalSpend{RequireKnownEstimate: true}},
	})
	if rerr != nil {
		t.Fatalf("resolve: %v", rerr)
	}
	return resolved
}

// explicitEffortHistory seeds one measured history and resolves a repository
// effort tier, so a caller can drive the estimator through a real layer origin.
func explicitEffortHistory(t *testing.T, effort, cost string) (*session, *config.Resolved) {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(filepath.Join(".verdict", "state", "1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".verdict", "state", "1", "cost.json"), []byte(cost), 0o644); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	r := &session{Human: io.Discard, Diag: io.Discard, warnings: &warnings}
	repo, ref := config.Parse([]byte(`{"schema_version":"1","defaults":{"effort":"` + effort + `"}}`))
	if ref != nil {
		t.Fatalf("repo parse: %v", ref)
	}
	resolved, rerr := config.Resolve(&config.Inputs{Repo: repo, Personal: &config.PersonalSettings{}})
	if rerr != nil {
		t.Fatalf("resolve: %v", rerr)
	}
	return r, resolved
}

// TestBudgetGateRequireKnownWithoutBudgetRefuses pins the D7§8 rule that the
// strict posture alone gates the run: with no USD ceiling anywhere and no
// usable projection, the gate refuses budget-unknown instead of returning
// early, and a fresh consent records a receipt that states the reason
// without inventing a ceiling.
func TestBudgetGateRequireKnownWithoutBudgetRefuses(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var warnings []string
	r := &session{Human: io.Discard, Diag: io.Discard, warnings: &warnings}
	resolved := requireKnownNoBudget(t)
	budget := resolveBudget(r, resolved)
	if budget.hasBudget {
		t.Fatalf("fixture armed an ordinary budget: %q from %q", budget.budgetUSD, budget.budgetSrc)
	}
	if err := budgetGate(r, &budget, resolved, false); err == nil {
		t.Fatal("require-known posture with no projection passed without consent")
	} else {
		var ref *core.RefusalError
		if !errors.As(err, &ref) || ref.Code != "budget-unknown" {
			t.Fatalf("strict gate = %v, want budget-unknown", err)
		}
	}
	budget = resolveBudget(r, resolved)
	if err := budgetGate(r, &budget, resolved, true); err != nil {
		t.Fatalf("consented strict gate = %v, want nil", err)
	}
	if !budget.receiptNeeded {
		t.Fatal("a consented blind run with no ceiling armed no receipt")
	}
	assertBlindReceipt(t, r, &budget, dir)
}

// assertBlindReceipt writes the armed receipt and checks it states the reason
// without attributing the run to a ceiling it never had.
func assertBlindReceipt(t *testing.T, r *session, budget *budgetState, dir string) {
	t.Helper()
	writeBudgetReceipt(r, budget, dir, "2026-01-02T03:04:05Z")
	raw, rerr := os.ReadFile(filepath.Join(dir, "budget.json"))
	if rerr != nil {
		t.Fatalf("read the consented receipt: %v", rerr)
	}
	body := string(raw)
	if !strings.Contains(body, `"reason": "unknown-estimate"`) {
		t.Errorf("receipt does not state the unknown reason: %s", body)
	}
	if strings.Contains(body, "budget_usd") || strings.Contains(body, "budget_src") {
		t.Errorf("receipt fabricates a ceiling the run never had: %s", body)
	}
}

// TestBudgetGateExplicitEffortEstimatesWithoutCeiling pins that an explicitly
// resolved effort tier alone asks the estimator, and that a known projection
// with no monetary guard is reported as a projection — never compared against,
// or attributed to, a budget that does not exist.
func TestBudgetGateExplicitEffortEstimatesWithoutCeiling(t *testing.T) {
	r, resolved := explicitEffortHistory(t, "deep",
		`[{"effort":"standard","est_usd":2.00,"models":[]},{"effort":"deep","est_usd":9.00,"models":[]}]`)
	budget := resolveBudget(r, resolved)
	if budget.hasBudget {
		t.Fatal("fixture armed an ordinary budget")
	}
	if err := budgetGate(r, &budget, resolved, false); err != nil {
		t.Fatalf("explicit-effort gate = %v, want nil: a projection is not a guard", err)
	}
	// The recorded history says 9.00; the projection carries the parsed mean.
	if budget.estTier != "deep" || budget.estUSD != "9" {
		t.Errorf("projection = tier %q $%q, want the deep tier's $9", budget.estTier, budget.estUSD)
	}
	if budget.budgetJSON != nil {
		t.Errorf("a run with no budget reported one: %#v", budget.budgetJSON)
	}
	if budget.receiptNeeded {
		t.Error("a within-projection run with no guard armed a receipt")
	}
}

// TestBudgetGateBuiltinEffortKeepsNoBudgetBehaviour pins the compatibility
// half: with no guard, no policy, and no explicit effort tier, the gate
// measures nothing, exactly as before policy could reach the estimator.
func TestBudgetGateBuiltinEffortKeepsNoBudgetBehaviour(t *testing.T) {
	t.Chdir(t.TempDir())
	var warnings []string
	r := &session{Human: io.Discard, Diag: io.Discard, warnings: &warnings}
	repo, ref := config.Parse([]byte(`{"schema_version":"1","defaults":{}}`))
	if ref != nil {
		t.Fatalf("repo parse: %v", ref)
	}
	resolved, rerr := config.Resolve(&config.Inputs{Repo: repo, Personal: &config.PersonalSettings{}})
	if rerr != nil {
		t.Fatalf("resolve: %v", rerr)
	}
	budget := resolveBudget(r, resolved)
	if err := budgetGate(r, &budget, resolved, false); err != nil {
		t.Fatalf("plain no-budget gate = %v, want nil", err)
	}
	if budget.estRuns != 0 || budget.estUSD != "" || budget.estTier != "" {
		t.Errorf("a plain run measured history: runs=%d usd=%q tier=%q", budget.estRuns, budget.estUSD, budget.estTier)
	}
}
