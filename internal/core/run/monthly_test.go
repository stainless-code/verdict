package run

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/spend"
)

// setupMonthlyBundle builds a bundle with run.json for execution-A
// (carrying the monthly ceiling in its captured resolved spend view when
// monthlyCap is non-empty), one seeded MTD receipt ledger under the
// state directory, and a priced $2 projection.
func setupMonthlyBundle(t *testing.T, monthlyCap, mtdReceipts string) (root, dir string, proj spend.Projection) {
	t.Helper()
	root = fixtureRoot(t)
	dir = bundleDir(root)
	execID := "execution-A"
	writeRunJSON(t, dir, execID, monthlyCap)
	writeMTDReceipts(t, root, mtdReceipts)
	proj, _ = spend.Project(execID, []spend.Phase{
		{ExecutionID: execID, LaunchID: "finder-1-discovery-1", Finder: "finder-1", Phase: "discovery", USD: new(2.0)},
	})
	return root, dir, proj
}

func writeRunJSON(t *testing.T, dir, execID, monthlyCap string) {
	t.Helper()
	body := `{"schema_version":"1","execution":{"id":"` + execID + `","finders":2}}`
	if monthlyCap != "" {
		body = `{"schema_version":"1","execution":{"id":"` + execID + `","finders":2},"resolved":{"spend":{"monthly_budget_usd":` +
			monthlyCap + `,"monthly_repo_cap":` + monthlyCap + `}}}`
	}
	if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeMTDReceipts seeds one month-to-date receipt ledger in the bundle
// the fixture roots at.
func writeMTDReceipts(t *testing.T, root, receipts string) {
	t.Helper()
	if receipts == "" {
		return
	}
	writeOneMTD(t, filepath.Join(root, ".verdict", "state", "42"), receipts)
}

func writeOneMTD(t *testing.T, bundleDir string, body string) {
	t.Helper()
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "budget.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// monthlyState builds a run state at a fixed clock inside the seeded
// month, carrying only the strictness under test; the ceiling itself
// rides in the bundle's captured resolved spend view or, for the
// direct-gate tests, in the explicit policy argument.
func monthlyState(t *testing.T, root string, cmdr core.Commander, requireKnown bool) *state {
	t.Helper()
	req := request(root, cmdr)
	req.SpendPolicy = &spend.Policy{RequireKnown: requireKnown}
	req.Now = func() time.Time { return time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC) }
	return &state{req: req, warnings: append([]string{}, req.Warnings...)}
}

// monthlyCapState seeds MTD known spend with a cap-bearing bundle for
// the unknown-projection cases.
func monthlyCapState(t *testing.T, monthlyCap, mtdUSD string) *state {
	t.Helper()
	root, _, _ := setupMonthlyBundle(t, monthlyCap, `[{"at":"2026-05-01T12:00:00Z","projected_usd":`+mtdUSD+`,"execution_id":"mtd-1"}]`)
	return monthlyState(t, root, &scripted{}, false)
}

func TestMonthlyCapRefusesBeforeLaunch(t *testing.T) {
	root, dir, proj := setupMonthlyBundle(t, "3", `[{"at":"2026-05-01T12:00:00Z","projected_usd":1.5,"execution_id":"mtd-1"}]`)
	// MTD 1.5 + projected 2 = 3.5 > cap 3 => refuse.
	st := monthlyState(t, root, &scripted{}, false)
	ref := st.authorizeProjection(t.Context(), dir, mustCaptured(t, dir), 1, []spend.Phase{}, proj)
	if ref == nil || ref.Code != "spend-capped" {
		t.Fatalf("monthly over-cap = %v, want spend-capped", ref)
	}
	// Consent is per invocation: with consent the gate passes, and the
	// next unconsented invocation refuses again.
	st.req.ConsentOverBudget = true
	if ref := st.authorizeMonthlyCap(proj, spend.Policy{MonthlyCap: "3"}); ref != nil {
		t.Fatalf("consented monthly = %v, want nil", ref)
	}
	if r := monthlyState(t, root, &scripted{}, false).authorizeMonthlyCap(proj, spend.Policy{MonthlyCap: "3"}); r == nil || r.Code != "spend-capped" {
		t.Fatalf("unconsented monthly = %v, want spend-capped", r)
	}
}

func TestMonthlyCapUnderStays(t *testing.T) {
	root, _, proj := setupMonthlyBundle(t, "", `[{"at":"2026-05-01T12:00:00Z","projected_usd":1,"execution_id":"mtd-1"}]`)
	// MTD 1 + projected 2 = 3 <= 10 => passes.
	st := monthlyState(t, root, &scripted{}, false)
	if ref := st.authorizeMonthlyCap(proj, spend.Policy{MonthlyCap: "10"}); ref != nil {
		t.Fatalf("under cap = %v, want nil", ref)
	}
}

func TestMonthlyUnknownStrict(t *testing.T) {
	root, _, _ := setupMonthlyBundle(t, "", `[{"at":"2026-05-01T12:00:00Z","projected_usd":null,"execution_id":"mtd-1"}]`)
	st := monthlyState(t, root, &scripted{}, true)
	p := spend.Projection{Unknown: []string{"x"}}
	if ref := st.authorizeMonthlyCap(p, spend.Policy{MonthlyCap: "100", RequireKnown: true}); ref == nil || ref.Code != "budget-unknown" {
		t.Fatalf("strict unknown = %v, want budget-unknown", ref)
	}
	// Consent overrides.
	st.req.ConsentOverBudget = true
	if ref := st.authorizeMonthlyCap(p, spend.Policy{MonthlyCap: "100", RequireKnown: true}); ref != nil {
		t.Fatalf("consented strict = %v, want nil", ref)
	}
}

// TestMonthlyUnknownStrictNamesOnlyProjection proves the strict refusal
// names the unknown leg that is actually unknown: a known month-to-date
// with only an unknown pending projection is refused as projection
// unknown, not as month-to-date unknown.
func TestMonthlyUnknownStrictNamesOnlyProjection(t *testing.T) {
	root, _, _ := setupMonthlyBundle(t, "", `[{"at":"2026-05-01T12:00:00Z","projected_usd":1,"execution_id":"mtd-1"}]`)
	st := monthlyState(t, root, &scripted{}, true)
	p := spend.Projection{Unknown: []string{"x"}}
	ref := st.authorizeMonthlyCap(p, spend.Policy{MonthlyCap: "100", RequireKnown: true})
	if ref == nil || ref.Code != "budget-unknown" {
		t.Fatalf("projection-only strict = %v, want budget-unknown", ref)
	}
	if !strings.Contains(ref.Message, "pending projection has unknown phases") {
		t.Fatalf("strict message = %q, want it to name only the projection leg", ref.Message)
	}
}

func TestMonthlyUnknownLenientWarns(t *testing.T) {
	root, _, _ := setupMonthlyBundle(t, "", `[{"at":"2026-05-01T12:00:00Z","projected_usd":null,"execution_id":"mtd-1"}]`)
	st := monthlyState(t, root, &scripted{}, false)
	p := spend.Projection{Unknown: []string{"x"}}
	if ref := st.authorizeMonthlyCap(p, spend.Policy{MonthlyCap: "100"}); ref != nil {
		t.Fatalf("lenient unknown = %v, want nil (warn only)", ref)
	}
	if len(st.warnings) == 0 {
		t.Fatal("lenient unknown produced no warning")
	}
}

// TestMonthlyUnknownProjectionRefusesOnKnownMTD proves a production run
// with an unpriced projection (no SpendPlan, so no known projection
// amount) refuses when month-to-date known spend alone already exceeds
// the monthly ceiling.
func TestMonthlyUnknownProjectionRefusesOnKnownMTD(t *testing.T) {
	st := monthlyCapState(t, "3", "200")
	unpriced := spend.Projection{}
	ref := st.authorizeMonthlyCap(unpriced, spend.Policy{MonthlyCap: "3"})
	if ref == nil || ref.Code != "spend-capped" {
		t.Fatalf("unknown-projection over-cap = %v, want spend-capped", ref)
	}
	if !strings.Contains(ref.Message, "$200") || !strings.Contains(ref.Message, "$3") ||
		!strings.Contains(ref.Message, "pending projection unknown") {
		t.Fatalf("refusal = %q, want MTD $200, cap $3, and unknown projection named", ref.Message)
	}
	// Consent passes the same state.
	st.req.ConsentOverBudget = true
	if ref := st.authorizeMonthlyCap(unpriced, spend.Policy{MonthlyCap: "3"}); ref != nil {
		t.Fatalf("consented unknown-projection = %v, want nil", ref)
	}
}

// TestMonthlyUnknownProjectionWarnsUnderCap proves an unpriced pending
// projection under the ceiling passes with a warning that names the
// unknown pending projection.
func TestMonthlyUnknownProjectionWarnsUnderCap(t *testing.T) {
	st := monthlyCapState(t, "3", "1")
	unpriced := spend.Projection{}
	if ref := st.authorizeMonthlyCap(unpriced, spend.Policy{MonthlyCap: "3"}); ref != nil {
		t.Fatalf("unknown-projection under-cap = %v, want nil (warn only)", ref)
	}
	if len(st.warnings) == 0 {
		t.Fatal("unknown pending projection produced no warning")
	}
	if !strings.Contains(st.warnings[0], "pending projection is unknown") {
		t.Fatalf("warning = %q, want it to name the unknown pending projection", st.warnings[0])
	}
}

// TestAuthorizeSpendSpendPlanNilStillEnforces proves the production path
// with no SpendPlan (an unpriced projection) still refuses when
// month-to-date known spend alone exceeds the ceiling.
func TestAuthorizeSpendSpendPlanNilStillEnforces(t *testing.T) {
	root, dir, _ := setupMonthlyBundle(t, "3", `[{"at":"2026-05-01T12:00:00Z","projected_usd":200,"execution_id":"mtd-1"}]`)
	req := request(root, &scripted{})
	req.Now = func() time.Time { return time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC) }
	req.SpendPolicy = &spend.Policy{MonthlyCap: "3"}
	st := &state{req: req, warnings: append([]string{}, req.Warnings...)}
	ref := st.authorizeSpend(t.Context(), dir, 1)
	if ref == nil || ref.Code != "spend-capped" {
		t.Fatalf("SpendPlan-nil over-cap = %v, want spend-capped", ref)
	}
}

func TestMonthlyUnpricedProjectionStrict(t *testing.T) {
	root, _, _ := setupMonthlyBundle(t, "", "")
	st := monthlyState(t, root, &scripted{}, true)
	// Both knowns empty, projection fully unknown.
	p := spend.Projection{Unknown: []string{"finder-1-discovery-1"}}
	if ref := st.authorizeMonthlyCap(p, spend.Policy{MonthlyCap: "100", RequireKnown: true}); ref == nil || ref.Code != "budget-unknown" {
		t.Fatalf("unpriced projection strict = %v, want budget-unknown", ref)
	}
}

// TestSpendPolicyPreservesCapturedMonthlyCap proves a CLI-shaped
// SpendPolicy (require_known_estimate, a personal budget, or a monthly
// setting) never wipes the captured monthly ceiling: the preserved cap
// still enforces.
func TestSpendPolicyPreservesCapturedMonthlyCap(t *testing.T) {
	root, _, proj := setupMonthlyBundle(t, "", `[{"at":"2026-05-01T12:00:00Z","projected_usd":10,"execution_id":"mtd-1"}]`)
	ceiling := 3.0
	captured := &config.RunState{Resolved: &config.Resolved{
		Spend: config.SpendView{MonthlyBudgetUSD: &ceiling, MonthlyRepoCap: &ceiling},
	}}
	for _, cli := range []*spend.Policy{
		{RequireKnown: true},
		{Personal: "5"},
		{MonthlyCap: "9"},
		{Budget: "7", RequireKnown: true},
	} {
		st := monthlyState(t, root, &scripted{}, false)
		st.req.SpendPolicy = cli
		got := st.spendPolicy(captured)
		if got.MonthlyCap != "3" {
			t.Fatalf("SpendPolicy %+v preserved MonthlyCap = %q, want 3", cli, got.MonthlyCap)
		}
		if ref := st.authorizeMonthlyCap(proj, got); ref == nil || ref.Code != "spend-capped" {
			t.Fatalf("preserved-cap policy %+v = %v, want spend-capped (10 + 2 > 3)", got, ref)
		}
	}
}

// TestMonthlyCapStringUsesEffective proves the enforced literal is the
// post-precedence effective ceiling: a stricter personal cap wins over
// the repository display literal, and the repository literal serves
// when the repository cap is effective.
func TestMonthlyCapStringUsesEffective(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	// Repo 50 + personal 10: effective 10, not the repo display 50.
	strict := &config.Resolved{
		Spend:    config.SpendView{MonthlyBudgetUSD: f(10), MonthlyRepoCap: f(50), MonthlyPersonalCap: f(10)},
		Defaults: config.ResolvedDefaults{Values: config.EffectiveDefaults{MonthlyBudgetUSD: f(50), MonthlyDisplay: "50"}},
	}
	if got := monthlyCapString(strict); got != "10" {
		t.Fatalf("repo 50 + personal 10 = %q, want 10", got)
	}
	// Repo-only 50: the repository display literal serves.
	repoOnly := &config.Resolved{
		Spend:    config.SpendView{MonthlyBudgetUSD: f(50), MonthlyRepoCap: f(50)},
		Defaults: config.ResolvedDefaults{Values: config.EffectiveDefaults{MonthlyBudgetUSD: f(50), MonthlyDisplay: "50"}},
	}
	if got := monthlyCapString(repoOnly); got != "50" {
		t.Fatalf("repo-only 50 = %q, want 50", got)
	}
	// Personal-only 10: the personal value formats as the literal.
	personalOnly := &config.Resolved{
		Spend: config.SpendView{MonthlyBudgetUSD: f(10), MonthlyPersonalCap: f(10)},
	}
	if got := monthlyCapString(personalOnly); got != "10" {
		t.Fatalf("personal-only 10 = %q, want 10", got)
	}
	// No ceiling at all stays "".
	if got := monthlyCapString(&config.Resolved{}); got != "" {
		t.Fatalf("no ceiling = %q, want empty", got)
	}
}

// TestMonthlyCapEffectiveRefusesAtTwelve proves the effective "10"
// literal refuses known spend over it even though the repository layer
// allows 50: MTD 12, cap 10, priced projection $0 refuses spend-capped.
func TestMonthlyCapEffectiveRefusesAtTwelve(t *testing.T) {
	root, _, _ := setupMonthlyBundle(t, "", `[{"at":"2026-05-01T12:00:00Z","projected_usd":12,"execution_id":"mtd-1"}]`)
	st := monthlyState(t, root, &scripted{}, false)
	zero := 0.0
	if ref := st.authorizeMonthlyCap(spend.Projection{Total: &zero}, spend.Policy{MonthlyCap: "10"}); ref == nil || ref.Code != "spend-capped" {
		t.Fatalf("effective-10 over-cap = %v, want spend-capped", ref)
	}
}

func mustCaptured(t *testing.T, dir string) *config.RunState {
	t.Helper()
	rs, _, err := config.ReadRunState(dir)
	if err != nil || rs == nil {
		t.Fatalf("ReadRunState: %v", err)
	}
	// Ensure Execution present for spend path.
	return rs
}

func TestAuthorizeSpendMonthlyRefusal(t *testing.T) {
	// authorizeSpend refuses an over-cap run before any launch: with no
	// CLI policy, the captured resolved ceiling enforces.
	root, dir, _ := setupMonthlyBundle(t, "3", `[{"at":"2026-05-01T12:00:00Z","projected_usd":10,"execution_id":"mtd-1"}]`)
	req := request(root, &scripted{})
	req.Now = func() time.Time { return time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC) }
	req.SpendPolicy = &spend.Policy{}
	req.SpendPlan = []spend.Phase{{ExecutionID: "execution-A", LaunchID: "finder-1-discovery-1", Finder: "finder-1", Phase: "discovery", USD: new(2.0)}}
	st := &state{req: req, warnings: append([]string{}, req.Warnings...)}
	rounds := 1
	ref := st.authorizeSpend(t.Context(), dir, rounds)
	if ref == nil || ref.Code != "spend-capped" {
		t.Fatalf("full Run monthly refusal = %v, want spend-capped", ref)
	}
}

func TestPerInvocationConsentNotPersisted(t *testing.T) {
	root, _, proj := setupMonthlyBundle(t, "", `[{"at":"2026-05-01T12:00:00Z","projected_usd":10,"execution_id":"mtd-1"}]`)
	// First invocation with consent passes monthly gate.
	st1 := monthlyState(t, root, &scripted{}, false)
	st1.req.ConsentOverBudget = true
	if ref := st1.authorizeMonthlyCap(proj, spend.Policy{MonthlyCap: "3"}); ref != nil {
		t.Fatalf("consented = %v, want nil", ref)
	}
	// Next invocation without consent must refuse again — consent is per call.
	st2 := monthlyState(t, root, &scripted{}, false)
	if ref := st2.authorizeMonthlyCap(proj, spend.Policy{MonthlyCap: "3"}); ref == nil || ref.Code != "spend-capped" {
		t.Fatalf("unconsented after consent = %v, want spend-capped (not persisted)", ref)
	}
}

// TestMonthlyConcurrentBundlesSerialize proves the repo-wide monthly lock
// serializes the MTD scan plus receipt append across different bundles:
// with $8 already recorded and a $10 cap, two concurrent $2 projections
// must not both authorize. One succeeds and the second refuses spend-capped.
func seedMonthlyState(t *testing.T) string {
	t.Helper()
	root := fixtureRoot(t)
	seedBundle(t, root, "42", `[{"at":"2026-05-01T12:00:00Z","projected_usd":8,"execution_id":"mtd-seed"}]`)
	return root
}

func seedBundle(t *testing.T, root, pr, receipts string) {
	t.Helper()
	dir := filepath.Join(root, ".verdict", "state", pr)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if receipts != "" {
		if err := os.WriteFile(filepath.Join(dir, "budget.json"), []byte(receipts), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(`{"number":43,"state":"OPEN"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte("diff --git a/a.go b/a.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func concurrentArgs(t *testing.T, root string) (string, string, spend.Projection, spend.Projection) {
	t.Helper()
	dirA := filepath.Join(root, ".verdict", "state", "43")
	dirB := filepath.Join(root, ".verdict", "state", "44")
	seedBundle(t, root, "43", "")
	seedBundle(t, root, "44", "")
	writeRunJSON(t, dirA, "execution-A", "10")
	writeRunJSON(t, dirB, "execution-B", "10")
	projA, _ := spend.Project("execution-A", []spend.Phase{{ExecutionID: "execution-A", LaunchID: "finder-1-discovery-1", Finder: "finder-1", Phase: "discovery", USD: new(2.0)}})
	projB, _ := spend.Project("execution-B", []spend.Phase{{ExecutionID: "execution-B", LaunchID: "finder-1-discovery-1", Finder: "finder-1", Phase: "discovery", USD: new(2.0)}})
	return dirA, dirB, projA, projB
}

func TestMonthlyConcurrentBundlesSerialize(t *testing.T) {
	root := seedMonthlyState(t)
	dirA, dirB, projA, projB := concurrentArgs(t, root)
	stA := monthlyState(t, root, &scripted{}, false)
	stB := monthlyState(t, root, &scripted{}, false)
	capA := mustCaptured(t, dirA)
	capB := mustCaptured(t, dirB)
	results := make([]*core.RefusalError, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0] = stA.authorizeProjection(t.Context(), dirA, capA, 1, []spend.Phase{{ExecutionID: "execution-A", LaunchID: "finder-1-discovery-1", Finder: "finder-1", Phase: "discovery", USD: new(2.0)}}, projA)
	}()
	go func() {
		defer wg.Done()
		results[1] = stB.authorizeProjection(t.Context(), dirB, capB, 1, []spend.Phase{{ExecutionID: "execution-B", LaunchID: "finder-1-discovery-1", Finder: "finder-1", Phase: "discovery", USD: new(2.0)}}, projB)
	}()
	wg.Wait()
	assertConcurrentResult(t, results)
}

func assertConcurrentResult(t *testing.T, results []*core.RefusalError) {
	t.Helper()
	if (results[0] == nil) == (results[1] == nil) {
		t.Fatalf("concurrent authorize results = %v / %v, want exactly one success", results[0], results[1])
	}
	for _, r := range results {
		if r != nil && r.Code != "spend-capped" {
			t.Fatalf("concurrent refusal = %+v, want spend-capped", r)
		}
	}
}
