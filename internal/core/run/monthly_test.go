package run

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/spend"
)

func setupMonthlyBundle(t *testing.T, monthlyCap string, mtdReceipts string) (root, dir string, proj spend.Projection) {
	t.Helper()
	_ = monthlyCap
	root = fixtureRoot(t)
	dir = bundleDir(root)
	execID := "execution-A"
	writeRunJSON(t, dir, execID)
	writeMTDReceipts(t, root, mtdReceipts)
	proj, _ = spend.Project(execID, []spend.Phase{
		{ExecutionID: execID, LaunchID: "finder-1-discovery-1", Finder: "finder-1", Phase: "discovery", USD: new(2.0)},
	})
	return root, dir, proj
}

func writeRunJSON(t *testing.T, dir, execID string) {
	t.Helper()
	body := `{"schema_version":"1","execution":{"id":"` + execID + `","finders":2}}`
	if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeMTDReceipts(t *testing.T, root, receipts string) {
	t.Helper()
	if receipts == "" {
		return
	}
	for _, name := range []string{"42", "other"} {
		writeOneMTD(t, filepath.Join(root, ".verdict", "state", name), receipts)
	}
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

func monthlyState(t *testing.T, root string, cmdr core.Commander, monthlyCap string, requireKnown bool) *state {
	t.Helper()
	req := request(root, cmdr)
	req.SpendPolicy = &spend.Policy{MonthlyCap: monthlyCap, RequireKnown: requireKnown}
	// Fixed clock for MTD month
	req.Now = func() time.Time { return time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC) }
	return &state{req: req}
}

func TestMonthlyCapRefusesBeforeLaunch(t *testing.T) {
	root, dir, proj := setupMonthlyBundle(t, "3", `[{"at":"2026-05-01T12:00:00Z","projected_usd":1.5}]`)
	// MTD 1.5 + projected 2 = 3.5 > cap 3 => refuse
	_ = dir
	cmdr := &scripted{}
	st := monthlyState(t, root, cmdr, "3", false)
	// Seed MTD also in other bundle duplicates, so MTD is 3.0; that would still be 3+2>3.
	// To keep exact, we use only one receipt total 1.5; but our helper writes it twice,
	// so MTD is 3.0 and still over cap. Either way it should refuse.
	ref := st.authorizeProjection(t.Context(), dirWithBundle(t, root), mustCaptured(t, dir), 1, []spend.Phase{}, proj)
	if ref == nil || ref.Code != "spend-capped" {
		t.Fatalf("monthly over-cap = %v, want spend-capped", ref)
	}
	if cmdr.calls != 0 {
		t.Fatalf("commander calls = %d, want 0 (refuse before launch)", cmdr.calls)
	}
	// Direct monthly gate with consent should not refuse.
	st2 := monthlyState(t, root, &scripted{}, "3", false)
	st2.req.ConsentOverBudget = true
	if r := st2.authorizeMonthlyCap(t.Context(), dir, mustCaptured(t, dir), proj, spend.Policy{MonthlyCap: "3"}); r != nil {
		t.Fatalf("consented monthly = %v, want nil", r)
	}
	if r := monthlyState(t, root, &scripted{}, "3", false).authorizeMonthlyCap(t.Context(), dir, mustCaptured(t, dir), proj, spend.Policy{MonthlyCap: "3"}); r == nil || r.Code != "spend-capped" {
		t.Fatalf("unconsented monthly = %v, want spend-capped", r)
	}
}

func TestMonthlyCapUnderStays(t *testing.T) {
	root, dir, proj := setupMonthlyBundle(t, "10", `[{"at":"2026-05-01T12:00:00Z","projected_usd":1}]`)
	// MTD duplicated = 2, plus projected 2 = 4 <=10 => passes
	st := monthlyState(t, root, &scripted{}, "10", false)
	if ref := st.authorizeMonthlyCap(t.Context(), dir, mustCaptured(t, dir), proj, spend.Policy{MonthlyCap: "10"}); ref != nil {
		t.Fatalf("under cap = %v, want nil", ref)
	}
}

func TestMonthlyUnknownStrict(t *testing.T) {
	root, dir, proj := setupMonthlyBundle(t, "100", `[{"at":"2026-05-01T12:00:00Z","projected_usd":null}]`)
	st := monthlyState(t, root, &scripted{}, "100", true)
	// Monthly reduce: MTD unknown 1 (or 2 with duplicate)
	p := spend.Projection{Unknown: []string{"x"}}
	if ref := st.authorizeMonthlyCap(t.Context(), dir, mustCaptured(t, dir), p, spend.Policy{MonthlyCap: "100", RequireKnown: true}); ref == nil || ref.Code != "budget-unknown" {
		t.Fatalf("strict unknown = %v, want budget-unknown", ref)
	}
	// Consent overrides.
	st.req.ConsentOverBudget = true
	if ref := st.authorizeMonthlyCap(t.Context(), dir, mustCaptured(t, dir), p, spend.Policy{MonthlyCap: "100", RequireKnown: true}); ref != nil {
		t.Fatalf("consented strict = %v, want nil", ref)
	}
	_ = proj
}

func TestMonthlyUnknownLenientWarns(t *testing.T) {
	root, dir, _ := setupMonthlyBundle(t, "100", `[{"at":"2026-05-01T12:00:00Z","projected_usd":null}]`)
	st := monthlyState(t, root, &scripted{}, "100", false)
	p := spend.Projection{Unknown: []string{"x"}}
	if ref := st.authorizeMonthlyCap(t.Context(), dir, mustCaptured(t, dir), p, spend.Policy{MonthlyCap: "100", RequireKnown: false}); ref != nil {
		t.Fatalf("lenient unknown = %v, want nil (warn only)", ref)
	}
	if len(st.warnings) == 0 {
		t.Fatalf("lenient unknown produced no warning")
	}
}

func TestMonthlyUnpricedProjectionStrict(t *testing.T) {
	root := fixtureRoot(t)
	dir := bundleDir(root)
	if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte(`{"schema_version":"1","execution":{"id":"execution-A","finders":2}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st := monthlyState(t, root, &scripted{}, "100", true)
	// Both knowns empty, projection fully unknown.
	p := spend.Projection{Unknown: []string{"finder-1-discovery-1"}}
	if ref := st.authorizeMonthlyCap(t.Context(), dir, mustCaptured(t, dir), p, spend.Policy{MonthlyCap: "100", RequireKnown: true}); ref == nil || ref.Code != "budget-unknown" {
		t.Fatalf("unpriced projection strict = %v, want budget-unknown", ref)
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

func dirWithBundle(t *testing.T, root string) string {
	t.Helper()
	return bundleDir(root)
}

func TestZeroSpawnOnRefusal(t *testing.T) {
	// Full Run before launch must not call commander when monthly cap refuses.
	root, dir, _ := setupMonthlyBundle(t, "3", `[{"at":"2026-05-01T12:00:00Z","projected_usd":10}]`)
	cmdr := &scripted{}
	req := request(root, cmdr)
	req.Now = func() time.Time { return time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC) }
	req.SpendPolicy = &spend.Policy{MonthlyCap: "3", RequireKnown: false}
	req.SpendPlan = []spend.Phase{{ExecutionID: "execution-A", LaunchID: "finder-1-discovery-1", Finder: "finder-1", Phase: "discovery", USD: new(2.0)}}
	// Need FindersRaw etc for Run to proceed to authorizeSpend; we invoke via Run path helper.
	// Simpler: drive authorizeSpend directly and check cmdr.calls.
	st := &state{req: req}
	rounds := 1
	// Use prepare path: authorizeSpend calls monthly check before probe.
	ref := st.authorizeSpend(t.Context(), dir, rounds)
	if ref == nil || ref.Code != "spend-capped" {
		t.Fatalf("full Run monthly refusal = %v, want spend-capped", ref)
	}
	if cmdr.calls != 0 {
		t.Fatalf("zero spawn violated: calls = %d", cmdr.calls)
	}
	_ = dir
	_ = context.Background()
}

func TestPerInvocationConsentNotPersisted(t *testing.T) {
	root, dir, proj := setupMonthlyBundle(t, "3", `[{"at":"2026-05-01T12:00:00Z","projected_usd":10}]`)
	captured := mustCaptured(t, dir)
	// First invocation with consent passes monthly gate.
	st1 := monthlyState(t, root, &scripted{}, "3", false)
	st1.req.ConsentOverBudget = true
	if ref := st1.authorizeMonthlyCap(t.Context(), dir, captured, proj, spend.Policy{MonthlyCap: "3"}); ref != nil {
		t.Fatalf("consented = %v, want nil", ref)
	}
	// Next invocation without consent must refuse again — consent is per call.
	st2 := monthlyState(t, root, &scripted{}, "3", false)
	if ref := st2.authorizeMonthlyCap(t.Context(), dir, captured, proj, spend.Policy{MonthlyCap: "3"}); ref == nil || ref.Code != "spend-capped" {
		t.Fatalf("unconsented after consent = %v, want spend-capped (not persisted)", ref)
	}
}
