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
	writeCapture(t, dir, body)
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
	t.Parallel()
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
	t.Parallel()
	root, _, proj := setupMonthlyBundle(t, "", `[{"at":"2026-05-01T12:00:00Z","projected_usd":1,"execution_id":"mtd-1"}]`)
	// MTD 1 + projected 2 = 3 <= 10 => passes.
	st := monthlyState(t, root, &scripted{}, false)
	if ref := st.authorizeMonthlyCap(proj, spend.Policy{MonthlyCap: "10"}); ref != nil {
		t.Fatalf("under cap = %v, want nil", ref)
	}
}

func TestMonthlyUnknownStrict(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// resumedClock is the clock of a resumed authorization: after the bundle's
// 12:00 recorded cost, so its new projection is strictly later.
func resumedClock() time.Time { return time.Date(2026, 5, 15, 12, 5, 0, 0, time.UTC) }

// monthlyProjection is the $2 discovery projection one bundle authorizes.
func monthlyProjection(t *testing.T, execID string) ([]spend.Phase, spend.Projection) {
	t.Helper()
	phases := []spend.Phase{{ExecutionID: execID, LaunchID: "finder-1-discovery-1", Finder: "finder-1", Phase: "discovery", USD: new(2.0)}}
	proj, code := spend.Project(execID, phases)
	if code != "" {
		t.Fatalf("project %s: %s", execID, code)
	}
	return phases, proj
}

// resumedMonthlyFixture builds the resumed-bundle fixture: $6 already spent on
// the month in bundle 42, bundle 43 already recording $1 under execution-A at
// 12:00 (the part the interrupted run measured), and bundle 44 as the second
// bundle authorizing at the same time. Both bundles carry the $10 ceiling.
func resumedMonthlyFixture(t *testing.T) (root, dirA, dirB string) {
	t.Helper()
	root = fixtureRoot(t)
	seedBundle(t, root, "42", `[{"at":"2026-05-01T12:00:00Z","projected_usd":6,"execution_id":"mtd-seed"}]`)
	dirA = filepath.Join(root, ".verdict", "state", "43")
	dirB = filepath.Join(root, ".verdict", "state", "44")
	seedBundle(t, root, "43", "")
	seedBundle(t, root, "44", "")
	writeRunJSON(t, dirA, "execution-A", "10")
	writeRunJSON(t, dirB, "execution-B", "10")
	writeUnder(t, dirA, "cost.json", `[{"at":"2026-05-15T12:00:00Z","est_usd":1,"execution_id":"execution-A"}]`)
	return root, dirA, dirB
}

// TestMonthlyResumedProjectionVisibleToSecondBundle proves a resumed
// bundle's new projection — appended at 12:05, after the $1 it recorded under
// the same execution_id at 12:00 — is counted by a second bundle's scan:
// $6 seeded + $1 recorded + $2 resumed = $9, so the second bundle's $2
// projection is over the $10 ceiling. Suppressing the resumed row would hide
// pending spend from every scan and let the second bundle authorize at $9.
func TestMonthlyResumedProjectionVisibleToSecondBundle(t *testing.T) {
	t.Parallel()
	root, dirA, dirB := resumedMonthlyFixture(t)
	phasesA, projA := monthlyProjection(t, "execution-A")
	stA := monthlyState(t, root, &scripted{}, false)
	stA.req.Now = resumedClock
	if ref := stA.authorizeProjection(t.Context(), dirA, mustCaptured(t, dirA), 1, phasesA, projA); ref != nil {
		t.Fatalf("resumed authorize = %v, want nil ($6 seeded + $1 recorded + $2 pending = $9 <= $10)", ref)
	}
	raw, err := os.ReadFile(filepath.Join(dirA, "budget.json"))
	if err != nil {
		t.Fatalf("resumed bundle recorded no projection: %v", err)
	}
	if !strings.Contains(string(raw), `"at": "2026-05-15T12:05:00Z"`) {
		t.Fatalf("budget.json = %s, want the resumed projection recorded at 12:05", raw)
	}
	phasesB, projB := monthlyProjection(t, "execution-B")
	stB := monthlyState(t, root, &scripted{}, false)
	stB.req.Now = resumedClock
	if ref := stB.authorizeProjection(t.Context(), dirB, mustCaptured(t, dirB), 1, phasesB, projB); ref == nil || ref.Code != "spend-capped" {
		t.Fatalf("second bundle = %v, want spend-capped (the resumed $2 receipt makes $9 + $2 > $10)", ref)
	}
}

// TestMonthlyResumedConcurrentBundlesSerialize proves the repo-wide monthly
// lock makes the resumed bundle's appended projection visible to the other
// bundle's scan whichever authorizes first: both bundles spend $2 against $6
// seeded plus the resumed bundle's $1 recorded, so the first authorizes at
// $9 and the second must see that receipt and refuse at $11. One success and
// one spend-capped refusal is the only outcome the lock allows.
func TestMonthlyResumedConcurrentBundlesSerialize(t *testing.T) {
	t.Parallel()
	root, dirA, dirB := resumedMonthlyFixture(t)
	phasesA, projA := monthlyProjection(t, "execution-A")
	phasesB, projB := monthlyProjection(t, "execution-B")
	stA := monthlyState(t, root, &scripted{}, false)
	stA.req.Now = resumedClock
	stB := monthlyState(t, root, &scripted{}, false)
	stB.req.Now = resumedClock
	capA := mustCaptured(t, dirA)
	capB := mustCaptured(t, dirB)
	results := make([]*core.RefusalError, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0] = stA.authorizeProjection(t.Context(), dirA, capA, 1, phasesA, projA)
	}()
	go func() {
		defer wg.Done()
		results[1] = stB.authorizeProjection(t.Context(), dirB, capB, 1, phasesB, projB)
	}()
	wg.Wait()
	assertConcurrentResult(t, results)
}

// partialMonthlyProjection is a $2 discovery phase plus an unpriced second
// phase: Project keeps $2 as the projection's known subtotal and leaves the
// total unknown, which is what the run layer writes to budget.json as
// projected_usd null plus projection.known_subtotal_usd.
func partialMonthlyProjection(t *testing.T, execID string) ([]spend.Phase, spend.Projection) {
	t.Helper()
	phases := []spend.Phase{
		{ExecutionID: execID, LaunchID: "finder-1-discovery-1", Finder: "finder-1", Phase: "discovery", USD: new(2.0)},
		{ExecutionID: execID, LaunchID: "finder-2-discovery-1", Finder: "finder-2", Phase: "discovery"},
	}
	proj, code := spend.Project(execID, phases)
	if code != "" {
		t.Fatalf("project %s: %s", execID, code)
	}
	if proj.Total != nil || proj.KnownSubtotal == nil || *proj.KnownSubtotal != 2 {
		t.Fatalf("projection %+v, want unknown total with a $2 known subtotal", proj)
	}
	return phases, proj
}

// partialMonthlyFixture builds the partial-receipt fixture: $8 seeded on the
// month, bundle 43 as execution-A, bundle 44 as execution-B, and the $10
// ceiling in both captured run states.
func partialMonthlyFixture(t *testing.T) (root, dirA, dirB string) {
	t.Helper()
	root = seedMonthlyState(t)
	dirA = filepath.Join(root, ".verdict", "state", "43")
	dirB = filepath.Join(root, ".verdict", "state", "44")
	seedBundle(t, root, "43", "")
	seedBundle(t, root, "44", "")
	writeRunJSON(t, dirA, "execution-A", "10")
	writeRunJSON(t, dirB, "execution-B", "10")
	return root, dirA, dirB
}

// assertPartialReceipt proves the authorized bundle wrote the two literals the
// month-to-date scan reads. The recorded projection is compact inside the
// pretty ledger, so both are asserted in their written shape.
func assertPartialReceipt(t *testing.T, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "budget.json"))
	if err != nil {
		t.Fatalf("partial bundle recorded no projection: %v", err)
	}
	for _, want := range []string{`"projected_usd": null`, `"known_subtotal_usd":2`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("budget.json = %s, want it to carry %s", raw, want)
		}
	}
}

// TestMonthlyPartialProjectionVisibleToSecondBundle proves a partially priced
// receipt's known subtotal reaches a second bundle's month-to-date scan: with
// $8 seeded and a $10 ceiling, the first bundle authorizes its $2 known
// subtotal plus an unpriced phase at $10, and the second bundle's $2
// projection is then over the ceiling at $12. Dropping the subtotal left the
// second scan at $8, so both bundles authorized $2 against the same month.
func TestMonthlyPartialProjectionVisibleToSecondBundle(t *testing.T) {
	t.Parallel()
	root, dirA, dirB := partialMonthlyFixture(t)
	phasesA, projA := partialMonthlyProjection(t, "execution-A")
	stA := monthlyState(t, root, &scripted{}, false)
	if ref := stA.authorizeProjection(t.Context(), dirA, mustCaptured(t, dirA), 1, phasesA, projA); ref != nil {
		t.Fatalf("partial authorize = %v, want nil ($8 seeded + $2 known subtotal = $10 <= $10)", ref)
	}
	assertPartialReceipt(t, dirA)
	phasesB, projB := monthlyProjection(t, "execution-B")
	stB := monthlyState(t, root, &scripted{}, false)
	ref := stB.authorizeProjection(t.Context(), dirB, mustCaptured(t, dirB), 1, phasesB, projB)
	if ref == nil || ref.Code != "spend-capped" {
		t.Fatalf("second bundle = %v, want spend-capped ($8 + $2 subtotal + $2 = $12 > $10)", ref)
	}
	if !strings.Contains(ref.Message, "month-to-date $10") {
		t.Fatalf("refusal = %q, want month-to-date $10 (the recorded $2 known subtotal counted)", ref.Message)
	}
}

// TestMonthlyCapRefusalRoundsAndNamesKeys proves the ceiling refusal prints
// measured spend without the agent CLI's float noise and names the settings
// keys that set the monthly ceiling.
func TestMonthlyCapRefusalRoundsAndNamesKeys(t *testing.T) {
	t.Parallel()
	root, _, proj := setupMonthlyBundle(t, "", `[{"at":"2026-05-01T12:00:00Z","execution_id":"mtd-1",`+
		`"scope":"invocation","invocation_id":"mtd-1@1","projected_usd":1.30604299999999994,"priced_by":"measured"}]`)
	st := monthlyState(t, root, &scripted{}, false)
	ref := st.authorizeMonthlyCap(proj, spend.Policy{MonthlyCap: "1"})
	if ref == nil || ref.Code != "spend-capped" {
		t.Fatalf("monthly over-cap = %v, want spend-capped", ref)
	}
	want := "monthly spend ceiling $1 exceeded — month-to-date $1.306 plus projected $2 = $3.306 — " +
		"pass run --consent-over-budget for this invocation or raise the monthly ceiling: " +
		"spend.monthly_budget_usd in the operator settings or defaults.monthly_budget_usd in " +
		".verdict/config.json, whichever is lower"
	if ref.Message != want {
		t.Fatalf("message = %q\nwant      %q", ref.Message, want)
	}
}
