package config

import (
	"encoding/json"
	"math/big"
	"strconv"
)

// GroupBudget is one resolved plan group's spend contract: the guard and the
// effort tier the group's own captured files resolve to, and the files that
// selected them. Files are disjoint across budgets, so a path shared by two
// scopes is counted once.
type GroupBudget struct {
	GroupID        string
	Effort         string
	GuardUSD       string
	Source         string
	Files          []string
	EffortExplicit bool
}

// GroupCostAttribution is one historic cost record's claim about a group: the
// stable plan group id, the effort tier it ran at, and the exact decimal
// estimate it reported.
type GroupCostAttribution struct {
	Group  string
	Effort string
	EstUSD string
}

// CostSample is one historic cost record together with the capture it belongs
// to. The reader validates Binding against that record's own saved run.json
// before the sample reaches this model, so no current-run binding is assumed
// or normalized here.
type CostSample struct {
	Binding RunBinding
	Costs   []GroupCostAttribution
}

// GroupSpend is one group's evaluation: its guard, the projection the
// attributable history supports, and whether it is known and over its guard.
type GroupSpend struct {
	GroupID    string
	Effort     string
	GuardUSD   string
	Source     string
	Projection string
	Known      bool
	OverGuard  bool
}

// GroupSpendReport is the whole-run evaluation: every group's verdict, the
// exact aggregate of the known projections, and whether the aggregate exceeds
// the personal limit. Unknown is never zero: TotalKnown stays false and Total
// stays empty unless every group is priced.
type GroupSpendReport struct {
	Total         string
	PersonalLimit string
	// KnownSubtotal is the exact sum of the priced groups when at least one
	// group is unknown: a lower bound, so a known overspend is still reported
	// as a real violation rather than hidden by the unknown group.
	KnownSubtotal string
	// Groups holds one verdict per resolved plan group.
	Groups []GroupSpend
	// KnownSubtotalOverPersonal marks a lower-bound ceiling hit: some group is
	// unknown, but the priced groups alone already exceed the ceiling.
	KnownSubtotalOverPersonal bool
	TotalKnown                bool
	OverPersonal              bool
}

// RunBinding identifies the capture a cost record claims to price: only a
// record bound to this run and these captured inputs may price its groups.
type RunBinding struct {
	Run          string
	InputsSHA256 string
}

// GroupBudgets derives each resolved plan group's guard and effort from the
// file resolutions its captured file list selects. A path already claimed by
// an earlier group is skipped, so overlapping scopes cannot double count.
func GroupBudgets(res *Resolved) []GroupBudget {
	if res == nil {
		return nil
	}
	claimed := make(map[string]bool, len(res.Files))
	out := make([]GroupBudget, 0, len(res.Plan.Groups))
	for i := range res.Plan.Groups {
		out = append(out, res.groupBudget(&res.Plan.Groups[i], claimed))
	}
	return out
}

// groupBudget claims one group's unclaimed files and takes the guard and effort
// its first claimed file resolves to, falling back to the shared default guard.
func (r *Resolved) groupBudget(group *PlanGroup, claimed map[string]bool) GroupBudget {
	budget := GroupBudget{GroupID: group.ID, Effort: r.Defaults.Values.Effort}
	for _, path := range group.Files {
		if claimed[path] {
			continue
		}
		file, ok := r.fileResolution(path)
		if !ok {
			continue
		}
		claimed[path] = true
		budget.Files = append(budget.Files, path)
		budget.inherit(&file)
	}
	if origin, ok := r.Defaults.Origins["budget_usd"]; ok && budget.GuardUSD == "" {
		budget.GuardUSD, budget.Source = r.Defaults.Values.BudgetDisplay, origin.Source
	}
	return budget
}

// inherit takes the file-level guard and the explicitly resolved effort tier,
// leaving a builtin or absent layer at the shared default.
func (b *GroupBudget) inherit(file *FileResolution) {
	if file.Defaults.Values.BudgetUSD != nil && b.GuardUSD == "" {
		b.GuardUSD = decimalLiteral(*file.Defaults.Values.BudgetUSD)
		b.Source = file.Defaults.Origins["budget_usd"].Source
	}
	if layer := file.Defaults.Origins["effort"].Layer; layer != "" && layer != LayerBuiltIn {
		b.Effort, b.EffortExplicit = file.Defaults.Values.Effort, true
	}
}

// fileResolution finds the first resolution for path, which is the one the
// resolver recorded for the group that selected it.
func (r *Resolved) fileResolution(path string) (FileResolution, bool) {
	for i := range r.Files {
		if r.Files[i].Path == path {
			return r.Files[i], true
		}
	}
	return FileResolution{}, false
}

// EvaluateGroupSpend prices every resolved group from historic samples and
// compares the groups and their exact aggregate against the group guards and
// the personal whole-run limit. Each sample was validated against its own
// capture, so several past records for one group legitimately contribute (their
// mean is the estimate). A group duplicated inside one record is ambiguous and
// stays unknown, and an unknown group keeps the aggregate unknown rather than
// zero; known groups still report their own guard verdict.
func EvaluateGroupSpend(groups []GroupBudget, samples []CostSample, personalLimit string) GroupSpendReport {
	rep := GroupSpendReport{Groups: make([]GroupSpend, 0, len(groups))}
	rep.PersonalLimit = personalLimit
	total := new(big.Rat)
	allKnown := len(groups) > 0
	for i := range groups {
		group := &groups[i]
		spend := GroupSpend{
			GroupID: group.GroupID, Effort: group.Effort, GuardUSD: group.GuardUSD, Source: group.Source,
		}
		if tier, est, ok := estimateGroup(group, samples); ok {
			spend.Effort, spend.Projection, spend.Known = tier, formatDecimal(est), true
			spend.OverGuard = exceeds(est, rationalOf(group.GuardUSD))
			total.Add(total, est)
		} else {
			allKnown = false
		}
		rep.Groups = append(rep.Groups, spend)
	}
	if allKnown {
		rep.Total, rep.TotalKnown = formatDecimal(total), true
		rep.OverPersonal = exceeds(total, rationalOf(personalLimit))
		return rep
	}
	if priced := pricedCount(rep.Groups); priced > 0 {
		rep.KnownSubtotal = formatDecimal(total)
		rep.KnownSubtotalOverPersonal = exceeds(total, rationalOf(personalLimit))
	}
	return rep
}

// pricedCount counts the groups the history priced.
func pricedCount(groups []GroupSpend) int {
	count := 0
	for i := range groups {
		if groups[i].Known {
			count++
		}
	}
	return count
}

// estimateGroup is the mean of the valid past projections this group's effort
// tier accepts: the explicit tier when one was resolved, else the most-used
// historical tier with the deeper tier breaking a tie. A record that names the
// group twice, or names it at an unusable tier with no value, is ambiguous.
func estimateGroup(group *GroupBudget, samples []CostSample) (string, *big.Rat, bool) {
	tier, ok := groupTier(group, samples)
	if !ok {
		return "", nil, false
	}
	sum, picked := new(big.Rat), 0
	for i := range samples {
		est, ok := sampleEstimate(&samples[i], group, tier)
		if !ok {
			return "", nil, false
		}
		if est == nil {
			continue
		}
		sum.Add(sum, est)
		picked++
	}
	if picked == 0 {
		return "", nil, false
	}
	return tier, new(big.Rat).Quo(sum, new(big.Rat).SetInt64(int64(picked))), true
}

// groupTier picks the effort tier the estimate reads: the resolved explicit
// tier, else the tier historic records used most, deeper on a tie.
func groupTier(group *GroupBudget, samples []CostSample) (string, bool) {
	if group.EffortExplicit {
		return group.Effort, true
	}
	return mostUsedTier(group.GroupID, samples)
}

// mostUsedTier counts the tiers histories named for one group and returns the
// winner, with the deeper tier breaking a tie.
func mostUsedTier(groupID string, samples []CostSample) (string, bool) {
	counts := tierCounts(groupID, samples)
	best, bestCount := "", 0
	for _, tier := range effortOrder() {
		if counts[tier] >= bestCount && counts[tier] > 0 {
			best, bestCount = tier, counts[tier]
		}
	}
	return best, best != ""
}

// tierCounts tallies the effort tiers the samples named for one group.
func tierCounts(groupID string, samples []CostSample) map[string]int {
	counts := map[string]int{}
	for i := range samples {
		for j := range samples[i].Costs {
			if cost := &samples[i].Costs[j]; cost.Group == groupID {
				counts[cost.Effort]++
			}
		}
	}
	return counts
}

// Effort tiers, cheapest first: a later (deeper) tier wins an equal-count tie.
const (
	effortCheap    = "cheap"
	effortStandard = "standard"
	effortDeep     = "deep"
)

// effortOrder lists the known effort tiers cheapest first.
func effortOrder() []string { return []string{effortCheap, effortStandard, effortDeep} }

// sampleEstimate reads one record's contribution: nil when it never mentions
// the group, the parsed value when it names the group once at the chosen tier,
// and a refusal when the record is ambiguous for this group.
func sampleEstimate(sample *CostSample, group *GroupBudget, tier string) (*big.Rat, bool) {
	if sample.Binding.Run == "" || sample.Binding.InputsSHA256 == "" {
		return nil, false
	}
	named, atTier, est := scanSample(sample, group.GroupID, tier)
	if named > 1 || atTier > 1 || (atTier == 1 && est == nil) {
		return nil, false
	}
	return est, true
}

// scanSample counts how often one record names the group, and at the chosen
// tier, returning that tier's value when it names it once.
func scanSample(sample *CostSample, groupID, tier string) (named, atTier int, est *big.Rat) {
	for j := range sample.Costs {
		cost := &sample.Costs[j]
		if cost.Group != groupID {
			continue
		}
		named++
		if cost.Effort == tier {
			atTier++
			est = rationalOf(cost.EstUSD)
		}
	}
	return named, atTier, est
}

// SpendExceeds reports whether a known projection is strictly above a limit,
// comparing exact decimals so display rounding never flips a guard. Either side
// unknown means no claim.
func SpendExceeds(projected, limit string) bool {
	return exceeds(rationalOf(projected), rationalOf(limit))
}

// rationalOf parses an exact decimal literal; a missing or negative value is
// not a projection at all.
func rationalOf(lit string) *big.Rat {
	rat, ok := parseDecimal(lit)
	if !ok {
		return nil
	}
	return rat
}

// exceeds reports whether a known projection is strictly above a guard; either
// side unknown means no claim. Comparison is exact rationals, never floats.
func exceeds(projected, guard *big.Rat) bool {
	if projected == nil || guard == nil {
		return false
	}
	return projected.Cmp(guard) > 0
}

// parseDecimal parses an exact decimal literal without rounding: the new
// group math never compares floats, so 0.1+0.2 stays at or below 0.3.
func parseDecimal(lit string) (*big.Rat, bool) {
	if lit == "" {
		return nil, false
	}
	rat, ok := new(big.Rat).SetString(lit)
	if !ok || rat.Sign() < 0 {
		return nil, false
	}
	return rat, true
}

// decFloatBits is the float width FormatFloat renders without rounding.
const decFloatBits = 64

// Prime factors an exact finite decimal is built from.
const (
	decBaseTwo  = 2
	decBaseFive = 5
)

// decFracDigits bounds the display precision of a repeating mean; guard and
// aggregate decisions are made on exact rationals before any rendering.
const decFracDigits = 6

// formatDecimal renders an exact rational without a float round trip: an exact
// finite decimal is rendered digit for digit, and only a repeating mean falls
// back to display precision, which never feeds a guard decision.
func formatDecimal(rat *big.Rat) string {
	if rat == nil {
		return ""
	}
	if rat.IsInt() {
		return rat.Num().String()
	}
	if scale, ok := decimalScale(rat.Denom()); ok {
		return rat.FloatString(scale)
	}
	return rat.FloatString(decFracDigits)
}

// decimalScale reports the k of a denominator 2^a·5^b, i.e. the exact number of
// fractional digits the rational needs to render without rounding.
func decimalScale(den *big.Int) (int, bool) {
	rest := new(big.Int).Set(den)
	twos, fives := 0, 0
	two, five := big.NewInt(decBaseTwo), big.NewInt(decBaseFive)
	zero := big.NewInt(0)
	for new(big.Int).Rem(rest, two).Cmp(zero) == 0 {
		rest.Div(rest, two)
		twos++
	}
	for new(big.Int).Rem(rest, five).Cmp(zero) == 0 {
		rest.Div(rest, five)
		fives++
	}
	if rest.Cmp(big.NewInt(1)) != 0 {
		return 0, false
	}
	return max(twos, fives), true
}

// decimalLiteral renders a resolved float default as an exact decimal literal.
func decimalLiteral(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, decFloatBits)
}

// ReadGroupCosts reads the per-group cost claims bound to the current capture:
// each schema-v2 record whose run and inputs hash match the verified bundle
// contributes one sample, and only its own group_costs entries. Top-level
// fields this build does not know are ignored, because history outlives its
// writer: one run's stray field never makes another run's estimate unreadable.
// An unreadable group entry is still malformed evidence, never a priced group:
// the strict nested shape keeps unknown fields out of the estimate.
func ReadGroupCosts(rs *RunState, path string) ([]CostSample, string) {
	if rs == nil {
		return nil, LensUsageAbsent
	}
	raw, missing := readCostFile(path)
	if missing != "" {
		return nil, missing
	}
	doc, ref := decodeStrict(raw)
	if ref != nil {
		return nil, LensUsageMalformed
	}
	records, ok := lensRecords(doc)
	if !ok {
		return nil, LensUsageMalformed
	}
	samples, ok := boundSamples(records, rs)
	if !ok {
		return nil, LensUsageMalformed
	}
	if len(samples) == 0 {
		return nil, LensUsageUnbound
	}
	return samples, LensUsageRecorded
}

// boundSamples keeps the records bound to the verified capture, each as one
// sample carrying that record's own group claims. Only the entries are read
// strictly: a top-level field a newer writer added is not this build's
// business, so it neither voids the sample nor prunes the history.
func boundSamples(records []map[string]any, rs *RunState) ([]CostSample, bool) {
	binding := RunBinding{Run: rs.Name, InputsSHA256: rs.InputsSHA256}
	samples := make([]CostSample, 0, len(records))
	for _, obj := range records {
		if !boundTo(obj, rs) {
			continue
		}
		costs, ok := groupCostsOf(obj)
		if !ok {
			return nil, false
		}
		samples = append(samples, CostSample{Binding: binding, Costs: costs})
	}
	return samples, true
}

// groupCostsOf parses one record's group_costs list; absent or null means the
// record claims no group costs, which leaves every group unknown.
func groupCostsOf(record map[string]any) ([]GroupCostAttribution, bool) {
	raw, present := record["group_costs"]
	if !present || raw == nil {
		return nil, true
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	out := make([]GroupCostAttribution, 0, len(list))
	for _, item := range list {
		cost, ok := groupCostEntry(item)
		if !ok {
			return nil, false
		}
		out = append(out, cost)
	}
	return out, true
}

// groupCostEntry parses one group-cost object: group and effort named, est_usd
// a JSON number when present, and no unsupported field.
func groupCostEntry(item any) (GroupCostAttribution, bool) {
	entry, ok := item.(map[string]any)
	if !ok || !knownKeys(entry, groupCostKeys) {
		return GroupCostAttribution{}, false
	}
	group, okG := entry["group"].(string)
	effort, okE := entry["effort"].(string)
	if !okG || !okE || group == "" || effort == "" {
		return GroupCostAttribution{}, false
	}
	cost := GroupCostAttribution{Group: group, Effort: effort}
	est, present := entry["est_usd"]
	if !present || est == nil {
		return cost, true
	}
	num, ok := est.(json.Number)
	if !ok {
		return GroupCostAttribution{}, false
	}
	cost.EstUSD = num.String()
	return cost, true
}
