package config

import (
	"os"
	"path/filepath"
	"testing"
)

// groupBudgetFixture is one two-group resolution: group-a owns a.txt and b.txt,
// group-b owns c.txt. Literal expectations keep the arithmetic checkable by
// hand rather than recomputed by the code under test.
func groupBudgetFixture() *Resolved {
	return &Resolved{
		Defaults: ResolvedDefaults{Values: EffectiveDefaults{Effort: "standard"}},
		Plan: Plan{Groups: []PlanGroup{
			{ID: "group-a", Files: []string{"a.txt", "b.txt"}},
			{ID: "group-b", Files: []string{"c.txt"}},
		}},
		Files: []FileResolution{
			{Path: "a.txt", Defaults: ResolvedDefaults{Values: EffectiveDefaults{BudgetUSD: usd(5), Effort: "standard"}}},
			{Path: "b.txt", Defaults: ResolvedDefaults{Values: EffectiveDefaults{BudgetUSD: usd(5), Effort: "standard"}}},
			{Path: "c.txt", Defaults: ResolvedDefaults{Values: EffectiveDefaults{BudgetUSD: usd(5), Effort: "standard"}}},
		},
	}
}

func usd(v float64) *float64 { return new(v) }

// sample is one historic cost record bound to its own capture.
func sample(run, inputs string, costs ...GroupCostAttribution) CostSample {
	return CostSample{Binding: RunBinding{Run: run, InputsSHA256: inputs}, Costs: costs}
}

// cost is one group's attribution inside a record.
func cost(group, effort, est string) GroupCostAttribution {
	return GroupCostAttribution{Group: group, Effort: effort, EstUSD: est}
}

// TestRepeatingMeanUsesExactGuards keeps display rounding out of decisions.
func TestRepeatingMeanUsesExactGuards(t *testing.T) {
	budgets := GroupBudgets(groupBudgetFixture())
	budgets[0].GuardUSD = "0.3333332"
	samples := []CostSample{
		sample("r1", "h1", cost("group-a", "standard", "0"), cost("group-b", "standard", "0")),
		sample("r2", "h2", cost("group-a", "standard", "0"), cost("group-b", "standard", "0")),
		sample("r3", "h3", cost("group-a", "standard", "1"), cost("group-b", "standard", "0")),
	}
	rep := EvaluateGroupSpend(budgets, samples, "0.3333332")
	if !rep.TotalKnown || !rep.OverPersonal || !rep.Groups[0].OverGuard {
		t.Fatalf("one third must exceed 0.3333332 before display rounding: %+v", rep)
	}
}

// TestGroupSpendAggregatesAgainstThePersonalLimit is the accepted example:
// two groups each inside their own guard ($3 and $4 against $5) whose exact
// sum ($7) exceeds the personal whole-run limit ($6).
func TestGroupSpendAggregatesAgainstThePersonalLimit(t *testing.T) {
	budgets := GroupBudgets(groupBudgetFixture())
	samples := []CostSample{
		sample("r0", "h0", cost("group-a", "standard", "3"), cost("group-b", "standard", "4")),
	}
	rep := EvaluateGroupSpend(budgets, samples, "6")
	assertGroupsWithinGuards(t, rep.Groups)
	if !rep.TotalKnown || rep.Total != "7" {
		t.Fatalf("total = %q (known %v), want exact 7", rep.Total, rep.TotalKnown)
	}
	if !rep.OverPersonal {
		t.Fatalf("total 7 against personal limit 6 did not exceed the whole-run limit")
	}
}

// assertGroupsWithinGuards requires two groups guarded at 5, both known and
// inside their own guard.
func assertGroupsWithinGuards(t *testing.T, groups []GroupSpend) {
	t.Helper()
	if len(groups) != 2 || groups[0].GuardUSD != "5" || groups[1].GuardUSD != "5" {
		t.Fatalf("group guards = %+v, want two groups guarded at 5", groups)
	}
	for _, group := range groups {
		if !group.Known || group.OverGuard {
			t.Errorf("group %s = %+v, want known and inside its own guard", group.GroupID, group)
		}
	}
}

// TestGroupSpendRefusesOneGroupOverItsGuard keeps a known group violation
// visible even though the aggregate stays under the personal limit.
func TestGroupSpendRefusesOneGroupOverItsGuard(t *testing.T) {
	budgets := GroupBudgets(groupBudgetFixture())
	samples := []CostSample{
		sample("r0", "h0", cost("group-a", "standard", "5.5"), cost("group-b", "standard", "0.4")),
	}
	rep := EvaluateGroupSpend(budgets, samples, "10")
	if !rep.Groups[0].OverGuard {
		t.Errorf("group-a projection 5.5 against guard 5 is not over: %+v", rep.Groups[0])
	}
	if rep.Groups[1].OverGuard {
		t.Errorf("group-b projection 0.4 against guard 5 is over: %+v", rep.Groups[1])
	}
	if !rep.TotalKnown || rep.Total != "5.9" || rep.OverPersonal {
		t.Fatalf("total = %q over personal %v, want 5.9 under the 10 limit", rep.Total, rep.OverPersonal)
	}
}

// TestGroupSpendUnknownIsNeverZero: the unpriced group leaves the aggregate
// unknown instead of silently contributing zero.
func TestGroupSpendUnknownIsNeverZero(t *testing.T) {
	budgets := GroupBudgets(groupBudgetFixture())
	samples := []CostSample{sample("r0", "h0", cost("group-a", "standard", "3"))}
	rep := EvaluateGroupSpend(budgets, samples, "6")
	if rep.Groups[1].Known || rep.Groups[1].Projection != "" {
		t.Fatalf("group-b = %+v, want unknown with no projection", rep.Groups[1])
	}
	if rep.TotalKnown || rep.Total != "" {
		t.Fatalf("total = %q (known %v), want unknown rather than a partial sum", rep.Total, rep.TotalKnown)
	}
	if rep.OverPersonal {
		t.Fatal("an unknown total claimed a personal-limit verdict")
	}
}

// TestGroupSpendNeedsTheGroupsOwnEffortTier: an explicit per-group tier is
// never priced from another tier's history.
func TestGroupSpendNeedsTheGroupsOwnEffortTier(t *testing.T) {
	res := groupBudgetFixture()
	res.Plan.Groups[0].Files = []string{"a.txt", "b.txt"}
	res.Files[0].Defaults.Values.Effort = "deep"
	res.Files[0].Defaults.Origins = map[string]FieldOrigin{"effort": {Layer: "scopes", Source: "scoped effort"}}
	res.Files[2].Defaults.Values.Effort = "standard"
	res.Files[2].Defaults.Origins = map[string]FieldOrigin{"effort": {Layer: "scopes", Source: "scoped effort"}}
	budgets := GroupBudgets(res)
	if budgets[0].Effort != "deep" || budgets[1].Effort != "standard" {
		t.Fatalf("budget efforts = %q/%q, want deep/standard", budgets[0].Effort, budgets[1].Effort)
	}
	samples := []CostSample{
		sample("r0", "h0", cost("group-a", "standard", "3"), cost("group-b", "deep", "4")),
	}
	rep := EvaluateGroupSpend(budgets, samples, "20")
	if rep.Groups[0].Known {
		t.Fatalf("group-a at deep was priced from a standard-tier record: %+v", rep.Groups[0])
	}
	if rep.Groups[1].Known {
		t.Fatalf("group-b at standard was priced from a deep-tier record: %+v", rep.Groups[1])
	}
}

// TestGroupSpendIgnoresHistoryBoundToAnotherCapture: a record naming another
// run or another captured input can never price the current groups.
func TestGroupSpendIgnoresHistoryBoundToAnotherCapture(t *testing.T) {
	budgets := GroupBudgets(groupBudgetFixture())
	samples := []CostSample{
		sample("", "", cost("group-a", "standard", "3"), cost("group-b", "standard", "4")),
	}
	rep := EvaluateGroupSpend(budgets, samples, "20")
	if rep.Groups[0].Known || rep.Groups[1].Known {
		t.Fatalf("foreign-bound records priced groups: %+v", rep.Groups)
	}
	if rep.TotalKnown {
		t.Fatal("foreign-bound history produced a known total")
	}
}

// TestGroupSpendDuplicateAttributionIsUnknown: two records claiming the same
// group at the same tier are ambiguous, so the group stays unknown.
func TestGroupSpendDuplicateAttributionIsUnknown(t *testing.T) {
	budgets := GroupBudgets(groupBudgetFixture())
	samples := []CostSample{
		sample("r0", "h0", cost("group-a", "standard", "3"), cost("group-a", "standard", "4"),
			cost("group-b", "standard", "1")),
	}
	rep := EvaluateGroupSpend(budgets, samples, "20")
	if rep.Groups[0].Known {
		t.Fatalf("a duplicate attribution priced group-a: %+v", rep.Groups[0])
	}
}

// TestGroupBudgetsCountsAnOverlappingPathOnce: a path claimed by an earlier
// group is not counted again for a later one.
func TestGroupBudgetsCountsAnOverlappingPathOnce(t *testing.T) {
	res := &Resolved{
		Plan: Plan{Groups: []PlanGroup{
			{ID: "group-a", Files: []string{"shared.txt"}},
			{ID: "group-b", Files: []string{"shared.txt", "own.txt"}},
		}},
		Files: []FileResolution{
			{Path: "shared.txt", Defaults: ResolvedDefaults{Values: EffectiveDefaults{BudgetUSD: usd(5)}}},
			{Path: "own.txt", Defaults: ResolvedDefaults{Values: EffectiveDefaults{BudgetUSD: usd(5)}}},
		},
	}
	budgets := GroupBudgets(res)
	if len(budgets[0].Files) != 1 || len(budgets[1].Files) != 1 || budgets[1].Files[0] != "own.txt" {
		t.Fatalf("group files = %+v, want the shared path claimed once by group-a", budgets)
	}
}

// TestGroupSpendSumsExactly pins exact decimal addition: 0.1 + 0.2 is exactly
// 0.3, so the personal-limit comparison is not a float rounding artifact.
func TestGroupSpendSumsExactly(t *testing.T) {
	budgets := GroupBudgets(groupBudgetFixture())
	samples := []CostSample{
		sample("r0", "h0", cost("group-a", "standard", "0.1"), cost("group-b", "standard", "0.2")),
	}
	rep := EvaluateGroupSpend(budgets, samples, "0.3")
	if rep.Total != "0.3" {
		t.Fatalf("total = %q, want the exact decimal 0.3", rep.Total)
	}
	if rep.OverPersonal {
		t.Fatal("0.1+0.2 was compared as above 0.3")
	}
}

// TestGroupSpendMeansSeveralPastCaptures: two valid past records for one group
// legitimately average ($2 and $4 give $3); duplicate history is not ambiguity.
func TestGroupSpendMeansSeveralPastCaptures(t *testing.T) {
	budgets := GroupBudgets(groupBudgetFixture())
	samples := []CostSample{
		sample("r-past-1", "h-past-1", cost("group-a", "standard", "2"), cost("group-b", "standard", "1")),
		sample("r-past-2", "h-past-2", cost("group-a", "standard", "4"), cost("group-b", "standard", "3")),
	}
	rep := EvaluateGroupSpend(budgets, samples, "20")
	if !rep.Groups[0].Known || rep.Groups[0].Projection != "3" {
		t.Fatalf("group-a = %+v, want the mean of 2 and 4", rep.Groups[0])
	}
	if !rep.TotalKnown || rep.Total != "5" {
		t.Fatalf("total = %q, want 3 + 2 = 5", rep.Total)
	}
}

// TestGroupSpendRejectsTheSameGroupTwiceInOneRecord: duplication inside one
// cost record is ambiguous for that group, so it stays unknown.
func TestGroupSpendRejectsTheSameGroupTwiceInOneRecord(t *testing.T) {
	budgets := GroupBudgets(groupBudgetFixture())
	samples := []CostSample{
		sample("r0", "h0", cost("group-a", "standard", "3"), cost("group-a", "standard", "4"),
			cost("group-b", "standard", "1")),
	}
	rep := EvaluateGroupSpend(budgets, samples, "20")
	if rep.Groups[0].Known {
		t.Fatalf("a twice-named group was priced: %+v", rep.Groups[0])
	}
	if rep.TotalKnown {
		t.Fatal("the aggregate claimed a total while one group was ambiguous")
	}
}

// TestGroupSpendRejectsASampleWithoutItsOwnCapture: a sample whose binding is
// incomplete cannot be validated against its own run.json, so it is dropped.
func TestGroupSpendRejectsASampleWithoutItsOwnCapture(t *testing.T) {
	budgets := GroupBudgets(groupBudgetFixture())
	samples := []CostSample{
		sample("r-past", "", cost("group-a", "standard", "2")),
		sample("", "h-past", cost("group-b", "standard", "4")),
	}
	rep := EvaluateGroupSpend(budgets, samples, "20")
	if rep.Groups[0].Known || rep.Groups[1].Known || rep.TotalKnown {
		t.Fatalf("unvalidatable samples priced groups: %+v", rep)
	}
}

// TestGroupSpendUsesTheHistoricalTierWhenEffortIsBuiltIn: with no explicit
// effort layer the estimate reads the tier past records used, not a forced
// standard.
func TestGroupSpendUsesTheHistoricalTierWhenEffortIsBuiltIn(t *testing.T) {
	res := groupBudgetFixture()
	res.Defaults.Values.Effort = "standard"
	budgets := GroupBudgets(res)
	if budgets[0].EffortExplicit {
		t.Fatalf("a builtin effort was marked explicit: %+v", budgets[0])
	}
	samples := []CostSample{
		sample("r1", "h1", cost("group-a", "deep", "8")),
		sample("r2", "h2", cost("group-a", "deep", "10")),
	}
	rep := EvaluateGroupSpend(budgets, samples, "20")
	if !rep.Groups[0].Known || rep.Groups[0].Projection != "9" || rep.Groups[0].Effort != "deep" {
		t.Fatalf("group-a = %+v, want the deep-tier mean 9", rep.Groups[0])
	}
}

// TestGroupSpendComparesExactDecimals: 0.100000000000000001 + 0.2 is above 0.3,
// a difference a float comparison would lose.
func TestGroupSpendComparesExactDecimals(t *testing.T) {
	budgets := GroupBudgets(groupBudgetFixture())
	samples := []CostSample{
		sample("r0", "h0", cost("group-a", "standard", "0.100000000000000001"),
			cost("group-b", "standard", "0.2")),
	}
	rep := EvaluateGroupSpend(budgets, samples, "0.3")
	if !rep.TotalKnown || rep.Total != "0.300000000000000001" {
		t.Fatalf("total = %q, want the exact sum", rep.Total)
	}
	if !rep.OverPersonal {
		t.Fatal("an exact sum above 0.3 was not detected against the personal limit")
	}
}

// TestReadGroupCostsBindsEachRecordToItsOwnCapture: two records bound to two
// different past captures both reach the model, and their group costs average.
func TestReadGroupCostsBindsEachRecordToItsOwnCapture(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cost.json")
	writeRecord(t, path, `[
	  {"schema_version":"2","run":"local-g","inputs_sha256":"aa",
	   "group_costs":[{"group":"group-a","effort":"standard","est_usd":2}]},
	  {"schema_version":"2","run":"local-g","inputs_sha256":"bb",
	   "group_costs":[{"group":"group-a","effort":"standard","est_usd":4}]}
	]`)
	samples, status := ReadGroupCosts(&RunState{Name: "local-g", InputsSHA256: "bb"}, path)
	if status != LensUsageRecorded {
		t.Fatalf("status = %q, want recorded", status)
	}
	if len(samples) != 1 {
		t.Fatalf("samples = %d, want the one record bound to this capture", len(samples))
	}
	rep := EvaluateGroupSpend(GroupBudgets(groupBudgetFixture()), samples, "20")
	if !rep.Groups[0].Known || rep.Groups[0].Projection != "4" {
		t.Fatalf("group-a = %+v, want the bound record's 4", rep.Groups[0])
	}
}

// TestReadGroupCostsRejectsUnsupportedShape: an unknown group-cost field is
// malformed evidence, never a silently accepted claim.
func TestReadGroupCostsRejectsUnsupportedShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cost.json")
	writeRecord(t, path, `{"schema_version":"2","run":"local-g","inputs_sha256":"bb",
	  "group_costs":[{"group":"group-a","effort":"standard","est_usd":2,"bonus":1}]}`)
	if _, status := ReadGroupCosts(&RunState{Name: "local-g", InputsSHA256: "bb"}, path); status != LensUsageMalformed {
		t.Fatalf("status = %q, want malformed for an unknown group-cost field", status)
	}
}

// writeRecord plants one cost document in the fixture directory.
func writeRecord(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestReadGroupCostsIgnoresUnregisteredTopLevelField pins the split by plane:
// aggregation prices the fields it knows and ignores a top-level field a newer
// writer added, so one run's stray key never voids the group history the
// estimate is built from. The nested entry shape stays strict — see
// TestReadGroupCostsRejectsUnsupportedShape.
func TestReadGroupCostsIgnoresUnregisteredTopLevelField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cost.json")
	writeRecord(t, path, `{"schema_version":"2","run":"local-g","inputs_sha256":"aa","notes":"stray",
	  "group_costs":[{"group":"group-a","effort":"standard","est_usd":2}]}`)
	samples, status := ReadGroupCosts(&RunState{Name: "local-g", InputsSHA256: "aa"}, path)
	if status != LensUsageRecorded {
		t.Fatalf("status = %q, want recorded despite the stray top-level field", status)
	}
	if len(samples) != 1 || len(samples[0].Costs) != 1 || samples[0].Costs[0].EstUSD != "2" {
		t.Fatalf("samples = %+v, want the record's own group cost", samples)
	}
}
