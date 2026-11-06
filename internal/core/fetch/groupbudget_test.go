package fetchcore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const groupFixtureBase = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// TestBudgetGateGroupsRefusePerGuardAndAggregate drives the real gate over
// verified historical captures: two groups inside their own guards whose exact
// aggregate exceeds the personal ceiling refuse spend-capped, one group over
// its own guard refuses budget-exceeded, and an unpriced group under the
// strict posture refuses budget-unknown without inventing USD.
func TestBudgetGateGroupsRefusePerGuardAndAggregate(t *testing.T) {
	root := groupedRepo(t)
	r := &session{repoRoot: root, Human: io.Discard, Diag: io.Discard, warnings: new([]string)}
	writeBoundCapture(t, root, "local-g1", "3", "4")
	resolved := groupedPlan(usd(5), usd(5), usd(6), false)
	var st budgetState
	ref := gateRefusal(t, budgetGate(r, &st, resolved, false))
	if ref.Code != "spend-capped" {
		t.Fatalf("aggregate 7 over ceiling 6 = %s (%s), want spend-capped", ref.Code, ref.Message)
	}
	var consented budgetState
	if err := budgetGate(r, &consented, resolved, true); err != nil {
		t.Fatalf("consented ceiling hit = %v, want nil", err)
	}
	if !consented.receiptNeeded {
		t.Fatal("a consented ceiling hit armed no receipt")
	}
	if st.groupSpend == nil || !st.groupSpend.report.TotalKnown || st.groupSpend.report.Total != "7" {
		t.Fatalf("gate recorded no exact total: %+v", st.groupSpend)
	}
}

// TestBudgetGateGroupsRefuseOneGroupOverItsGuard keeps a single group violation
// visible even though the aggregate stays under the personal ceiling.
func TestBudgetGateGroupsRefuseOneGroupOverItsGuard(t *testing.T) {
	root := groupedRepo(t)
	r := &session{repoRoot: root, Human: io.Discard, Diag: io.Discard, warnings: new([]string)}
	writeBoundCapture(t, root, "local-g1", "5.5", "0.4")
	var st budgetState
	ref := gateRefusal(t, budgetGate(r, &st, groupedPlan(usd(5), usd(5), usd(10), false), false))
	if ref.Code != "budget-exceeded" {
		t.Fatalf("group-a 5.5 over guard 5 = %s (%s), want budget-exceeded", ref.Code, ref.Message)
	}
	if len(st.groupSpend.overGuard) != 1 || st.groupSpend.overGuard[0] != "group-a" {
		t.Fatalf("recorded guard overruns = %v, want group-a named", st.groupSpend.overGuard)
	}
}

// TestBudgetGateGroupsRefuseUnknownUnderStrictPosture proves an unpriced
// group refuses budget-unknown under require_known_estimate (unless consented)
// and that no USD is invented for it.
func TestBudgetGateGroupsRefuseUnknownUnderStrictPosture(t *testing.T) {
	root := groupedRepo(t)
	r := &session{repoRoot: root, Human: io.Discard, Diag: io.Discard, warnings: new([]string)}
	writeBoundCapture(t, root, "local-g1", "3")
	resolved := groupedPlan(usd(5), usd(5), usd(20), true)
	var st budgetState
	ref := gateRefusal(t, budgetGate(r, &st, resolved, false))
	if ref.Code != "budget-unknown" {
		t.Fatalf("unpriced group-b = %s (%s), want budget-unknown", ref.Code, ref.Message)
	}
	if st.receiptNeeded {
		t.Fatal("a refused unknown gate armed a receipt before consent")
	}
	if st.groupSpend.report.TotalKnown || st.groupSpend.report.KnownSubtotal != "3" {
		t.Fatalf("unknown aggregate = %+v, want a known subtotal of 3 and no total", st.groupSpend.report)
	}
	var consented budgetState
	if err := budgetGate(r, &consented, resolved, true); err != nil {
		t.Fatalf("consented unknown gate = %v, want nil", err)
	}
	if !consented.receiptNeeded {
		t.Fatal("consented unknown run armed no receipt")
	}
}

// TestBudgetGateGroupsKnownSubtotalOverCeiling proves a known lower bound above
// the personal ceiling refuses even though another group is unknown.
func TestBudgetGateGroupsKnownSubtotalOverCeiling(t *testing.T) {
	root := groupedRepo(t)
	r := &session{repoRoot: root, Human: io.Discard, Diag: io.Discard, warnings: new([]string)}
	writeBoundCapture(t, root, "local-g1", "8")
	var st budgetState
	ref := gateRefusal(t, budgetGate(r, &st, groupedPlan(usd(20), usd(20), usd(6), false), false))
	if ref.Code != "spend-capped" {
		t.Fatalf("known subtotal 8 over ceiling 6 = %s (%s), want spend-capped", ref.Code, ref.Message)
	}
	if st.groupSpend.report.TotalKnown {
		t.Fatal("the gate reported an exact total while a group was unknown")
	}
}

// TestBudgetGateGroupsIgnoreForeignCaptureHistory proves a cost record bound
// to another capture never prices this plan's groups.
func TestBudgetGateGroupsIgnoreForeignCaptureHistory(t *testing.T) {
	root := groupedRepo(t)
	r := &session{repoRoot: root, Human: io.Discard, Diag: io.Discard, warnings: new([]string)}
	writeBoundCaptureThenRebind(t, root, "local-g1", "3", "4")
	var st budgetState
	ref := gateRefusal(t, budgetGate(r, &st, groupedPlan(usd(5), usd(5), usd(6), true), false))
	if ref.Code != "budget-unknown" {
		t.Fatalf("history bound to an old capture = %s (%s), want budget-unknown", ref.Code, ref.Message)
	}
}

// gateRefusal requires the gate to refuse and returns the typed refusal.
func gateRefusal(t *testing.T, err error) *core.RefusalError {
	t.Helper()
	if err == nil {
		t.Fatal("the gate passed, want a typed refusal")
	}
	var ref *core.RefusalError
	if !errors.As(err, &ref) {
		t.Fatalf("gate error = %v, want a typed refusal", err)
	}
	return ref
}

// groupedRepo makes the reviewed root and the bundle home the gate scans.
func groupedRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	return root
}

// groupedPlan is the current plan: two groups over a.txt and c.txt, with their
// own guards, a personal ceiling, and the strict posture when asked.
func groupedPlan(guardA, guardC, personal *float64, strict bool) *config.Resolved {
	return &config.Resolved{
		Defaults: config.ResolvedDefaults{Values: config.EffectiveDefaults{Effort: "standard"}},
		Spend:    config.SpendView{PersonalLimit: personal, RequireKnownEstimate: strict},
		Plan: config.Plan{Groups: []config.PlanGroup{
			{ID: "group-a", Files: []string{"a.txt"}},
			{ID: "group-b", Files: []string{"c.txt"}},
		}},
		Files: []config.FileResolution{
			{Path: "a.txt", Defaults: config.ResolvedDefaults{Values: config.EffectiveDefaults{BudgetUSD: guardA}}},
			{Path: "c.txt", Defaults: config.ResolvedDefaults{Values: config.EffectiveDefaults{BudgetUSD: guardC}}},
		},
	}
}

// writeBoundCapture writes one real verified capture whose cost record is bound
// to that capture's own run name and inputs hash, with the given per-group
// est_usd literals.
func writeBoundCapture(t *testing.T, root, name string, ests ...string) {
	t.Helper()
	dir := filepath.Join(root, ".verdict", "state", name)
	writeFixtureFile(t, filepath.Join(dir, "panel.md"), "panel\n")
	writeFixtureFile(t, filepath.Join(dir, "full.diff"), "diff\n")
	writeFixtureFile(t, filepath.Join(dir, "review.json"), `{"approved":false,"comments":[]}`)
	snap, ref := controls.Resolve(context.Background(), nil,
		&controls.Request{BundleDir: dir, BaseSHA: groupFixtureBase, AllowHead: true, Root: root})
	if ref != nil {
		t.Fatalf("controls snapshot: %v", ref)
	}
	rs := &config.RunState{
		BaseSHA:    groupFixtureBase,
		CapturedAt: "2026-11-02T12:00:00Z",
		Evidence:   config.Evidence{SchemaVersion: "1", BaseSHA: groupFixtureBase},
		Inventory:  config.InventoryHashes(snap),
	}
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("write run state: %v", serr)
	}
	groups := []string{"group-a", "group-b"}
	var entries strings.Builder
	for i, est := range ests {
		if i > 0 {
			entries.WriteString(",")
		}
		entries.WriteString(`{"group":"` + groups[i] + `","effort":"standard","est_usd":` + est + `}`)
	}
	body := `{"schema_version":"2","run":"` + rs.Name + `","inputs_sha256":"` + rs.InputsSHA256 +
		`","group_costs":[` + entries.String() + `]}`
	writeFixtureFile(t, filepath.Join(dir, "cost.json"), body)
}

// writeBoundCaptureThenRebind writes a verified capture whose cost record
// names a different run, so the history cannot price the current plan.
func writeBoundCaptureThenRebind(t *testing.T, root, name string, ests ...string) {
	t.Helper()
	writeBoundCapture(t, root, name, ests...)
	dir := filepath.Join(root, ".verdict", "state", name)
	raw, err := os.ReadFile(filepath.Join(dir, "cost.json"))
	if err != nil {
		t.Fatal(err)
	}
	rebound := stringsReplaceFirst(string(raw), `"run":"`+name+`"`, `"run":"local-old"`)
	writeFixtureFile(t, filepath.Join(dir, "cost.json"), rebound)
}

// writeFixtureFile plants one fixture file, creating its directory.
func writeFixtureFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stringsReplaceFirst replaces the first occurrence, keeping the fixture
// literal visible in the test.
func stringsReplaceFirst(body, old, replacement string) string {
	if i := indexOf(body, old); i >= 0 {
		return body[:i] + replacement + body[i+len(old):]
	}
	return body
}

// indexOf reports the first index of sub in s.
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// usd takes the address of an amount literal.
func usd(v float64) *float64 { return new(v) }

// TestGroupedReceiptBodyStatesGroupsAndOverride proves the consented grouped
// receipt names each group's guard, projection and source, the overridden guard
// ids, and the personal comparison basis — with no USD invented for an unknown
// group.
func TestGroupedReceiptBodyStatesGroupsAndOverride(t *testing.T) {
	root := groupedRepo(t)
	r := &session{repoRoot: root, Human: io.Discard, Diag: io.Discard, warnings: new([]string)}
	writeBoundCapture(t, root, "local-g1", "5.5", "0.4")
	resolved := groupedPlan(usd(5), usd(5), usd(10), false)
	var st budgetState
	if err := budgetGate(r, &st, resolved, true); err != nil {
		t.Fatalf("consented grouped gate = %v, want nil", err)
	}
	if !st.receiptNeeded {
		t.Fatal("consent armed no receipt")
	}
	writeBudgetReceipt(r, &st, root, "2026-11-02T12:00:00Z")
	body := readReceipt(t, root)
	for _, want := range []string{
		`"group": "group-a"`, `"projected_usd": 5.5`, `"guard_usd": 5`, `"over_guard": true`,
		`"group": "group-b"`, `"projected_usd": 0.4`, `"over_guard": false`,
		`"overridden_group_guards":`, `"group-a"`, `"personal_basis": "exact-total"`,
		`"personal_total_usd": 5.9`, `"personal_limit_usd": 10`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("receipt body is missing %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, `"unknown_groups"`) || strings.Contains(body, `"projected_tier"`) {
		t.Errorf("grouped receipt carries whole-run or unknown-group fields it cannot support:\n%s", body)
	}
}

// TestGroupedReceiptBodyStatesKnownLowerBound proves an unknown group's receipt
// states the lower-bound basis and names no projection for it.
func TestGroupedReceiptBodyStatesKnownLowerBound(t *testing.T) {
	root := groupedRepo(t)
	r := &session{repoRoot: root, Human: io.Discard, Diag: io.Discard, warnings: new([]string)}
	writeBoundCapture(t, root, "local-g1", "8")
	resolved := groupedPlan(usd(20), usd(20), usd(6), true)
	var st budgetState
	if err := budgetGate(r, &st, resolved, true); err != nil {
		t.Fatalf("consented grouped gate = %v, want nil", err)
	}
	writeBudgetReceipt(r, &st, root, "2026-11-02T12:00:00Z")
	body := readReceipt(t, root)
	for _, want := range []string{
		`"personal_basis": "known-lower-bound"`, `"personal_known_subtotal_usd": 8`,
		`"unknown_groups":`, `"group-b"`, `"reason": "unknown-estimate"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("receipt body is missing %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, `"personal_total_usd"`) {
		t.Errorf("receipt claims an exact total while a group is unknown:\n%s", body)
	}
}

// readReceipt reads the written budget receipt body.
func readReceipt(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "budget.json"))
	if err != nil {
		t.Fatalf("read the receipt: %v", err)
	}
	return string(raw)
}

// TestGroupedEstimateReachesThePublicWire proves a successful grouped run's
// public data carries the per-group guards, tiers and known projections, the
// exact total, and no budget or zero for the unknown case.
func TestGroupedEstimateReachesThePublicWire(t *testing.T) {
	root := groupedRepo(t)
	r := &session{repoRoot: root, Human: io.Discard, Diag: io.Discard, warnings: new([]string)}
	writeBoundCapture(t, root, "local-g1", "3", "4")
	var known budgetState
	if err := budgetGate(r, &known, groupedPlan(usd(5), usd(5), usd(20), false), false); err != nil {
		t.Fatalf("known grouped gate = %v, want nil", err)
	}
	data := &core.FetchData{Artifacts: jq.NewObj()}
	addFetchExtras(data, &known, &fetchCap{}, nil)
	body := data.Wire().MarshalCompact()
	for _, want := range []string{
		`"groups":[{"group":"group-a","effort":"standard","guard_usd":5,"projected_usd":3,` +
			`"over_guard":false},{"group":"group-b","effort":"standard","guard_usd":5,"projected_usd":4,` +
			`"over_guard":false}]`,
		`"personal":{"basis":"exact-total","limit_usd":20,"total_usd":7}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("public wire is missing %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, `"budget":`) {
		t.Errorf("a grouped run with no ordinary budget published one:\n%s", body)
	}
}

// TestGroupedEstimateWireKeepsUnknownUnknown proves the public wire drops the
// projection for an unpriced group and states the known lower bound instead of
// an exact total.
func TestGroupedEstimateWireKeepsUnknownUnknown(t *testing.T) {
	root := groupedRepo(t)
	r := &session{repoRoot: root, Human: io.Discard, Diag: io.Discard, warnings: new([]string)}
	writeBoundCapture(t, root, "local-g1", "3")
	var st budgetState
	if err := budgetGate(r, &st, groupedPlan(usd(5), usd(5), usd(20), false), false); err != nil {
		t.Fatalf("grouped gate = %v, want nil", err)
	}
	data := &core.FetchData{Artifacts: jq.NewObj()}
	addFetchExtras(data, &st, &fetchCap{}, nil)
	body := data.Wire().MarshalCompact()
	if !strings.Contains(body, `"group":"group-a","effort":"standard","guard_usd":5,"projected_usd":3`) {
		t.Errorf("public wire lost the priced group:\n%s", body)
	}
	if !strings.Contains(body, `{"group":"group-b","effort":"standard","guard_usd":5,"over_guard":false}`) {
		t.Errorf("an unknown group published a projection:\n%s", body)
	}
	if !strings.Contains(body, `"basis":"known-lower-bound","limit_usd":20,"known_subtotal_usd":3`) {
		t.Errorf("public wire states no known lower bound:\n%s", body)
	}
	if strings.Contains(body, `"total_usd"`) || strings.Contains(body, `"budget":`) {
		t.Errorf("public wire fabricated a total or a budget for an unknown group:\n%s", body)
	}
}

// TestSoleGroupPricesFromWholeProposalHistory proves a plan with exactly one
// group has no bound group price but still prices from the whole-run history,
// because that group's work is the whole proposal: the legacy estimate object
// and its measured basis survive, the group's own guard refuses, and the
// personal ceiling is compared exactly.
func TestSoleGroupPricesFromWholeProposalHistory(t *testing.T) {
	root := groupedRepo(t)
	writeWholeRunHistory(t, root, "2.5")
	usage := writeMeasuredUsage(t, root, "2.5")
	r := &session{
		repoRoot: root, Human: io.Discard, Diag: io.Discard,
		warnings: new([]string), env: map[string]string{"VERDICT_USAGE": usage},
	}
	resolved := soleGroupPlan(usd(1), nil, false)
	var st budgetState
	ref := gateRefusal(t, budgetGate(r, &st, resolved, false))
	if ref.Code != "budget-exceeded" {
		t.Fatalf("sole group 2.5 over guard 1 = %s (%s), want budget-exceeded", ref.Code, ref.Message)
	}
	if !strings.Contains(ref.Message, "[measured from 1 harness session(s)]") {
		t.Fatalf("refusal lost the measured basis: %s", ref.Message)
	}
	var consented budgetState
	if err := budgetGate(r, &consented, resolved, true); err != nil {
		t.Fatalf("consented sole group = %v, want nil", err)
	}
	data := &core.FetchData{Artifacts: jq.NewObj(), Estimate: consented.estimateJSON}
	want := `"estimate":{"tier":"standard","projected_usd":2.5,"comparable_runs":1,` +
		`"basis":"measured","harness":["claude-code"],"measured_runs":1}`
	if body := data.Wire().MarshalCompact(); !strings.Contains(body, want) {
		t.Errorf("public wire lost the whole-proposal estimate %s:\n%s", want, body)
	}
}

// TestSoleGroupWithoutHistoryStaysUnknown proves the fallback invents nothing:
// no history keeps the group unpriced (never zero) under the strict posture.
func TestSoleGroupWithoutHistoryStaysUnknown(t *testing.T) {
	root := groupedRepo(t)
	r := &session{repoRoot: root, Human: io.Discard, Diag: io.Discard, warnings: new([]string)}
	resolved := soleGroupPlan(usd(1), nil, true)
	var st budgetState
	ref := gateRefusal(t, budgetGate(r, &st, resolved, false))
	if ref.Code != "budget-unknown" {
		t.Fatalf("unpriced sole group = %s (%s), want budget-unknown", ref.Code, ref.Message)
	}
	estimate, ok := st.estimateJSON.(*jq.Obj)
	if !ok {
		t.Fatalf("unpriced estimate = %#v, want an ordered object", st.estimateJSON)
	}
	if got := estimate.MarshalCompact(); got != `{"tier":null,"projected_usd":null,"comparable_runs":0}` {
		t.Fatalf("unpriced estimate = %s, want the unknown shape", got)
	}
}

// TestMultiGroupStaysUnknownWithoutBoundPrices proves the fallback is
// sole-group only: a multi-group plan with no bound prices refuses instead of
// pricing every group from one whole-run figure.
func TestMultiGroupStaysUnknownWithoutBoundPrices(t *testing.T) {
	root := groupedRepo(t)
	writeWholeRunHistory(t, root, "2.5")
	r := &session{repoRoot: root, Human: io.Discard, Diag: io.Discard, warnings: new([]string)}
	var st budgetState
	ref := gateRefusal(t, budgetGate(r, &st, groupedPlan(usd(5), usd(5), nil, true), false))
	if ref.Code != "budget-unknown" {
		t.Fatalf("multi-group unpriced plan = %s (%s), want budget-unknown", ref.Code, ref.Message)
	}
}

// soleGroupPlan resolves one group over a.txt with its own guard and an
// optional personal ceiling.
func soleGroupPlan(guard, personal *float64, strict bool) *config.Resolved {
	return &config.Resolved{
		Defaults: config.ResolvedDefaults{Values: config.EffectiveDefaults{Effort: "standard"}},
		Spend:    config.SpendView{PersonalLimit: personal, RequireKnownEstimate: strict},
		Plan:     config.Plan{Groups: []config.PlanGroup{{ID: "group-solo", Files: []string{"a.txt"}}}},
		Files: []config.FileResolution{
			{Path: "a.txt", Defaults: config.ResolvedDefaults{Values: config.EffectiveDefaults{
				BudgetUSD: guard, BudgetDisplay: displayOf(guard),
			}}},
		},
	}
}

// displayOf renders the guard the resolution displays.
func displayOf(guard *float64) string {
	if guard == nil {
		return ""
	}
	return strconv.FormatFloat(*guard, 'f', -1, 64)
}

// writeWholeRunHistory plants one unbound legacy cost record with est_usd, the
// history the whole-run estimator prices.
func writeWholeRunHistory(t *testing.T, root, est string) {
	t.Helper()
	writeFixtureFile(t, filepath.Join(root, ".verdict", "state", "1", "cost.json"),
		`{"schema_version":"1","effort":"standard","models":[],"est_usd":`+est+`}`)
}

// writeMeasuredUsage plants a handed usage record priced at usd and returns its
// path.
func writeMeasuredUsage(t *testing.T, root, usd string) string {
	t.Helper()
	path := filepath.Join(root, "usage.json")
	writeFixtureFile(t, path, `{"schema_version":"1","source":"claude-code","model":"sonnet",
		"tokens":{"input":3000,"output":170},"usd":`+usd+`,"started_at":"2000-01-01T00:00:00Z",
		"ended_at":"2099-01-01T00:00:00Z"}`)
	return path
}
