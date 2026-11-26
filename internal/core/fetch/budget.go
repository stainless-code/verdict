package fetchcore

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/noblock"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/spend"
)

// floatBitSize is the bit size of the float64 the budget values parse into.
const floatBitSize = 64

// codeSpendCapped is the refusal code for a personal-ceiling hit.
const codeSpendCapped = "spend-capped"

type budgetState struct {
	estimateJSON any
	budgetJSON   any
	groupSpend   *groupSpendState
	budgetUSD    string
	budgetSrc    string
	estTier      string
	estUSD       string
	// estExact is the projection guards compare: the exact mean, never the
	// four-decimal estUSD a person reads.
	estExact      string
	estLowerBound string
	estBasis      string
	estHarness    []string
	estRuns       int
	measuredRuns  int
	tierRuns      int
	hasBudget     bool
	receiptNeeded bool
}

// resolveBudget reads the ordinary spend guard from the effective
// resolution: flags beat environment, which beats repository scopes and
// shared defaults, which beat personal review. Unset disables the
// ordinary plane; the personal ceiling resolves separately and no
// repository or environment layer can raise it. Malformed values refuse
// at parse time, never here.
func resolveBudget(_ *session, res *config.Resolved) budgetState {
	var st budgetState
	if res == nil || res.Defaults.Values.BudgetUSD == nil {
		return st
	}
	display := res.Defaults.Values.BudgetDisplay
	if display == "" {
		display = strconv.FormatFloat(*res.Defaults.Values.BudgetUSD, 'f', -1, floatBitSize)
	}
	st.budgetUSD, st.budgetSrc, st.hasBudget = display,
		res.Defaults.Origins["budget_usd"].Source, true
	return st
}

// budgetGate gates a run on the spend policy: the ordinary estimate
// guard against the resolved budget, then the personal ceiling. Both
// ceilings are consentable — a spent-limit hit refuses only until the
// operator consents, and the consenting run records a receipt that names
// the ceiling and where it came from. An unknown projection under a
// strict posture refuses with budget-unknown unless the operator consents,
// and a consented blind run records its receipt like any other.
//
// `enforce_cap` is a hard cap model this build does not implement, so a
// request that sets it refuses with unsupported-cap before any budget,
// force, or consent can authorize the run.
func budgetGate(r *session, st *budgetState, res *config.Resolved, consent bool) error {
	if ref := refuseUnsupportedCap(res); ref != nil {
		return ref
	}
	if hasPlanGroups(res) {
		return gateGroupedSpend(r, st, res, consent)
	}
	return gateWholeRunBudget(r, st, res, consent)
}

// gateWholeRunBudget is the whole-run plane for a resolution without plan
// groups: the historical projection, its guard, and the personal ceiling.
func gateWholeRunBudget(r *session, st *budgetState, res *config.Resolved, consent bool) error {
	if !estimationRequired(st, res) {
		return nil
	}
	if err := costEstimate(r, st, res); err != nil {
		return err
	}
	recordEstimate(st, consent)
	if err := gateHistoricalLowerBound(st, res, consent); err != nil {
		return err
	}
	if st.estUSD == "" {
		return gateUnknownEstimate(r, st, res, consent)
	}
	if ref := gatePersonalCeiling(st, res); ref != nil {
		if !consent {
			return ref
		}
		st.receiptNeeded = true
	}
	return gateOrdinaryBudget(st, consent)
}

func gateHistoricalLowerBound(st *budgetState, res *config.Resolved, consent bool) error {
	if st.estLowerBound == "" {
		return nil
	}
	policy := spend.Policy{Budget: st.budgetUSD}
	if res != nil && res.Spend.PersonalLimit != nil {
		policy.Personal = strconv.FormatFloat(*res.Spend.PersonalLimit, 'f', -1, floatBitSize)
	}
	code := spend.Authorize(spendProjection(st.estLowerBound), policy, false).Code
	if code == "" {
		return nil
	}
	if consent {
		st.receiptNeeded = true
		return nil
	}
	return &core.RefusalError{Code: code, Message: core.Msgf(
		"known subtotal of comparable %s runs averages at least $%s and exceeds a spend guard — consent required",
		st.estTier, st.estLowerBound)}
}

// hasPlanGroups reports whether the resolution carries resolved plan groups,
// which select the grouped spend plane instead of the whole-run estimate.
func hasPlanGroups(res *config.Resolved) bool {
	return res != nil && len(res.Plan.Groups) > 0
}

// recordEstimate puts the measured projection on the run's budget record: the
// ordinary guard only when the run has one, and the estimate object always.
func recordEstimate(st *budgetState, consent bool) {
	if st.hasBudget {
		st.budgetJSON = jq.NewObj("usd", jsonNumber(st.budgetUSD), keySource, st.budgetSrc, "consented", consent)
	}
	tierVal := any(nil)
	if st.estTier != "" {
		tierVal = st.estTier
	}
	st.estimateJSON = estimateObject(st, tierVal)
}

// gateOrdinaryBudget applies the ordinary spend guard to a known projection: a
// run with no budget has nothing to exceed, and a consented hit arms a receipt
// instead of refusing.
func gateOrdinaryBudget(st *budgetState, consent bool) error {
	if !st.hasBudget || !spend.Exceeds(st.estExact, st.budgetUSD) {
		return nil
	}
	if consent {
		st.receiptNeeded = true
		return nil
	}
	return budgetExceeded(st)
}

// estimationRequired reports whether this run needs a cost projection: a
// monetary guard, the require-known posture, or an explicitly resolved effort
// tier asks the estimator. Absent all three — the builtin-effort default with
// no budget and no ceiling — nothing is measured, so a plain run keeps its
// previous behavior.
func estimationRequired(st *budgetState, res *config.Resolved) bool {
	if st.hasBudget || (res != nil && res.Spend.PersonalLimit != nil) {
		return true
	}
	if res != nil && res.Spend.RequireKnownEstimate {
		return true
	}
	_, explicit := requestedEstimateTier(res)
	return explicit
}

// refuseUnsupportedCap refuses a request for a hard-cap model this build does
// not implement, before any budget, force, or consent can authorize it.
func refuseUnsupportedCap(res *config.Resolved) *core.RefusalError {
	if res == nil || !res.Spend.EnforceCap {
		return nil
	}
	return &core.RefusalError{Code: "unsupported-cap", Message: "spend.enforce_cap requests a hard-cap " +
		"model this build does not implement — remove it from the operator settings; budget, force, " +
		"and consent cannot authorize a cap that is unsupported"}
}

// budgetExceeded reports a known projection over the resolved budget, naming
// the source and the comparable runs the projection rests on.
func budgetExceeded(st *budgetState) error {
	return &core.RefusalError{Code: "budget-exceeded", Message: core.Msgf(
		"projected %s run ~$%s%s exceeds budget $%s (%s) from %d comparable run(s) — consent required before this run starts",
		st.estTier, spend.DisplayBreach(st.estExact, st.budgetUSD), st.basisNote(), st.budgetUSD, st.budgetSrc, st.estRuns)}
}

// gatePersonalCeiling reports the operator's authorization limit: a
// known projection above it refuses until the operator consents, naming
// the personal source no repository or environment value can override.
// The caller turns the hit into a receipt once consent is given.
func gatePersonalCeiling(st *budgetState, res *config.Resolved) error {
	if res == nil || res.Spend.PersonalLimit == nil || st.estUSD == "" {
		return nil
	}
	limit := strconv.FormatFloat(*res.Spend.PersonalLimit, 'f', -1, floatBitSize)
	if spend.Exceeds(st.estExact, limit) {
		return personalCeilingError(st, limit)
	}
	return nil
}

// personalCeilingError reports a projection the operator's own ceiling does not
// authorize, comparing exact decimals rather than display strings.
func personalCeilingError(st *budgetState, limit string) error {
	return &core.RefusalError{Code: codeSpendCapped, Message: core.Msgf(
		"projected %s run ~$%s%s exceeds the personal spend ceiling $%s (spend.budget_usd in the operator settings) from %d "+
			"comparable run(s) — consent before this run starts, or raise the ceiling in settings and "+
			"narrow the run",
		st.estTier, spend.DisplayBreach(st.estExact, limit), st.basisNote(), limit, st.estRuns)}
}

// gateUnknownEstimate handles a set budget with no usable projection:
// a strict posture refuses until the operator consents (budget-unknown,
// strict only), and consent — required or not — records a blind-consent
// receipt that states the reason even when no USD ceiling is set; a
// lenient posture proceeds unenforced.
func gateUnknownEstimate(r *session, st *budgetState, res *config.Resolved, consent bool) error {
	if res != nil && spend.Authorize(spend.Projection{}, spend.Policy{RequireKnown: res.Spend.RequireKnownEstimate}, consent).Code != "" {
		return &core.RefusalError{Code: "budget-unknown", Message: core.Msgf("%s", unknownEstimateMessage(st))}
	}
	if consent {
		st.receiptNeeded = true
		fmt.Fprintln(r.Human, consentedBlindReceipt(st))
	}
	return nil
}

// unknownEstimateMessage names the require-known refusal, quoting the ordinary
// budget only when the run actually carries one.
func unknownEstimateMessage(st *budgetState) string {
	msg := "no usable cost projection, and the operator settings require a known estimate — " +
		"consent before this blind run starts"
	if st.hasBudget {
		return msg + fmt.Sprintf(" (budget $%s from %s)", st.budgetUSD, st.budgetSrc)
	}
	return msg
}

// consentedBlindReceipt names what the consented blind run is recorded
// against, so a run with no USD ceiling says so instead of implying one.
func consentedBlindReceipt(st *budgetState) string {
	if st.hasBudget {
		return "» receipt: consented blind run (no usable cost projection) — recorded alongside the budget ledger"
	}
	return "» receipt: consented blind run (no usable cost projection) — recorded against the " +
		"require-known spend policy; no USD ceiling is set"
}

func estimateObject(st *budgetState, tierVal any) *jq.Obj {
	o := jq.NewObj("tier", tierVal, "projected_usd", jsonFloat(st.estUSD), "comparable_runs", st.estRuns)
	if st.measuredRuns == 0 {
		return o
	}
	o.Set("basis", st.estBasis)
	if len(st.estHarness) > 0 {
		o.Set("harness", st.estHarness)
	}
	o.Set("measured_runs", st.measuredRuns)
	return o
}

// basisNote renders the measured-basis clause appended to human estimates
// and refusals; empty when the projection is purely self-reported.
func (st *budgetState) basisNote() string {
	switch {
	case st.measuredRuns == 0:
		return ""
	case st.estBasis == "measured":
		return fmt.Sprintf(" [measured from %d harness session(s)]", st.measuredRuns)
	default:
		return fmt.Sprintf(" [partly measured: %d harness session(s)]", st.measuredRuns)
	}
}

// costEstimate averages est_usd over the most-run effort
// tier across every .verdict/state/*/cost.json record (ties break toward the
// deeper tier). Unknown history reads unknown, never blocking. Handed usage
// records price the runs they bracket; a broken handoff refuses typed.
func costEstimate(r *session, st *budgetState, res *config.Resolved) error {
	st.estUSD, st.estExact, st.estRuns, st.estTier, st.tierRuns = "", "", 0, "", 0
	usage, err := measuredUsage(r)
	if err != nil {
		return err
	}
	recs := estimateRecs(r, usage)
	if len(recs) == 0 {
		reportNoHistory(r)
		return nil
	}
	want, explicit := requestedEstimateTier(res)
	tier, tierRuns, stat, ok := pickEstimateTier(recs, want, explicit)
	st.estRuns = len(recs)
	if !ok {
		st.estRuns = 0
		reportNoHistory(r)
		return nil
	}
	st.estTier, st.tierRuns = tier, tierRuns
	st.recordBasis(&stat)
	st.estLowerBound = stat.lowerBound
	if stat.mean == "" {
		reportUnknownProjection(r, st, tier, tierRuns)
		return nil
	}
	st.estUSD, st.estExact = stat.mean, stat.exact.RatString()
	fmt.Fprintln(r.Human, core.Msgf("%s%s", estimateLine(st, tier), st.basisNote()))
	return nil
}

// reportNoHistory explains an empty sample: unknown history reads unknown, and
// a run with no guard is not measured against anything.
func reportNoHistory(r *session) {
	fmt.Fprintln(r.Human,
		"» estimate: no comparable past runs — cost unknown (no usable .verdict/state/*/cost.json records yet)")
}

// reportUnknownProjection explains a picked tier whose records carry no
// est_usd, quoting the ordinary budget only when the run actually has one.
func reportUnknownProjection(r *session, st *budgetState, tier string, tierRuns int) {
	line := core.Msgf("» estimate: tier %s from %d comparable run(s) — projected cost unknown (none reported est_usd)",
		tier, tierRuns)
	if st.hasBudget {
		line += core.Msgf("; budget $%s (%s) unenforced on unknown", st.budgetUSD, st.budgetSrc)
	}
	fmt.Fprintln(r.Human, line)
}

// estimateLine renders a known projection, comparing it against the ordinary
// guard only when the run carries one.
func estimateLine(st *budgetState, tier string) string {
	shown := st.estUSD
	if st.hasBudget {
		shown = spend.DisplayBreach(st.estExact, st.budgetUSD)
	}
	line := core.Msgf("» estimate: tier %s ~$%s/run from %d comparable run(s)", tier, shown, st.estRuns)
	if !st.hasBudget {
		return line
	}
	if overBudget(st.estExact, st.budgetUSD) {
		return line + core.Msgf(" — OVER budget $%s (%s)", st.budgetUSD, st.budgetSrc)
	}
	return line + core.Msgf(" — within budget $%s (%s)", st.budgetUSD, st.budgetSrc)
}

// recordBasis carries the measured split of the picked tier into the gate's
// estimate provenance. A picked tier with no measured sample leaves the
// legacy output untouched.
func (st *budgetState) recordBasis(stat *tierStat) {
	if stat.measured == 0 {
		return
	}
	st.measuredRuns = stat.measured
	st.estHarness = stat.harness
	if stat.priced > 0 && stat.measured == stat.priced {
		st.estBasis = "measured"
		return
	}
	st.estBasis = "mixed"
}

type costRec struct {
	measured    *big.Rat
	subtotal    *float64
	tier        string
	usageSource string
	usd         string
	has         bool
}

// readCostRecs reads one cost file's records and, index for index, the
// identity each carries.
func readCostRecs(path string) ([]costRec, []costIdent, bool) {
	raw, err := noblock.ReadFile(path)
	if err != nil {
		return nil, nil, false
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil, nil, false
	}
	var items []any
	if arr, ok := doc.([]any); ok {
		items = arr
	} else {
		items = []any{doc}
	}
	out := make([]costRec, 0, len(items))
	idents := make([]costIdent, 0, len(items))
	for _, it := range items {
		out = append(out, costRecord(it))
		m, _ := jq.AsMap(it)
		idents = append(idents, costIdent{at: m["at"], execution: jq.StrOf(m["execution_id"])})
	}
	return out, idents, true
}

func costRecord(it any) costRec {
	m, _ := jq.AsMap(it)
	tier := tierStandard
	var effort any
	if m != nil {
		effort = m["effort"]
	}
	if s, ok := effort.(string); ok {
		switch strings.ToLower(s) {
		case "cheap", tierStandard, "deep":
			tier = strings.ToLower(s)
		}
	}
	return priceCostRecord(costRec{tier: tier}, m)
}

func priceCostRecord(rec costRec, m map[string]any) costRec {
	if projection, present, valid := spend.RecordProjection(m); present {
		if !valid {
			return rec
		}
		rec.subtotal = projection.KnownSubtotal
		if projection.Total != nil {
			rec.usd = strconv.FormatFloat(*projection.Total, 'f', -1, floatBitSize)
			rec.has = true
		}
		return rec
	}
	rec.usd, rec.has = estNumber(m["est_usd"])
	return rec
}

// estNumber renders est_usd as a jq number literal when it is a number,
// and null otherwise.
func estNumber(v any) (string, bool) {
	switch t := v.(type) {
	case jq.Number:
		return t.String(), true
	case json.Number:
		return t.String(), true
	case float64:
		return jq.Compact(t), true
	case int:
		return strconv.Itoa(t), true
	default:
		return "", false
	}
}

// tierStat accumulates one tier's run count and priced mean plus the
// measured-sample split behind it.
type tierStat struct {
	exact      *big.Rat
	mean       string
	lowerBound string
	harness    []string
	meanVal    float64
	subtotal   float64
	runs       int
	priced     int
	measured   int
}

// no-pick, like max_by over an empty array.
// requestedEstimateTier reports the operator's resolved effort tier when a
// reviewed layer chose it; the builtin default is not an explicit request and
// leaves the historical most-used pick in place.
func requestedEstimateTier(res *config.Resolved) (string, bool) {
	if res == nil {
		return "", false
	}
	origin, ok := res.Defaults.Origins["effort"]
	if !ok || origin.Layer == config.LayerBuiltIn {
		return "", false
	}
	return res.Defaults.Values.Effort, true
}

// pickEstimateTier selects the tier to project from: an explicitly resolved
// effort tier projects from that tier's records alone, and unknown when that
// tier has none. With no explicit request it keeps the historical pick — the
// most-run tier, ties breaking toward the deeper one.
func pickEstimateTier(recs []costRec, want string, explicit bool) (tier string, tierRuns int, stat tierStat, ok bool) {
	if len(recs) == 0 {
		return "", 0, tierStat{}, false
	}
	if explicit {
		stat = tierStats(recs, want)
		return want, stat.runs, stat, stat.runs > 0
	}
	best := [2]float64{-1, -1}
	for _, t := range []string{"deep", tierStandard, "cheap"} {
		s := tierStats(recs, t)
		if float64(s.runs) > best[0] || (float64(s.runs) == best[0] && s.meanVal > best[1]) {
			best = [2]float64{float64(s.runs), s.meanVal}
			tier, tierRuns, stat, ok = t, s.runs, s, true
		}
	}
	return tier, tierRuns, stat, ok
}

// tierStats handles one tier's accumulation: the run count plus the priced
// mean (-1 with no priced runs, so unpriced tiers still compete on runs).
// A handed usage record prices that run and names its source.
func tierStats(recs []costRec, t string) tierStat {
	st := tierStat{exact: new(big.Rat)}
	var sum float64
	for _, r := range recs {
		if r.tier == t {
			sum = addTierSample(r, sum, &st)
		}
	}
	st.meanVal = -1.0
	if st.priced > 0 {
		st.meanVal, st.mean = spend.MeanUSD(sum, st.priced)
		st.exact.Quo(st.exact, big.NewRat(int64(st.priced), 1))
	}
	if st.subtotal > 0 {
		st.lowerBound = strconv.FormatFloat((sum+st.subtotal)/float64(st.runs), 'f', -1, floatBitSize)
	}
	slices.Sort(st.harness)
	st.harness = slices.Compact(st.harness)
	return st
}

// estimateRecs owns the cost-history sweep: every readable record under
// the reviewed repository's bundle home accumulates, while unreadable
// files warn once naming every one.
func estimateRecs(r *session, usage []readcommon.ContractUsage) []costRec {
	var recs []costRec
	var bad []string
	files, gerr := filepath.Glob(filepath.Join(r.repoRoot, ".verdict", "state", "*", readcommon.CostFile))
	if gerr != nil {
		files = nil
	}
	slices.Sort(files)
	for _, f := range files {
		fileRecs, idents, ok := readCostRecs(f)
		if !ok {
			// The warning is user-visible: glob returns OS separators,
			// so Windows would print `.verdict\state\2\cost.json` where every
			// bundle path uses slashes.
			bad = append(bad, filepath.ToSlash(f))
			continue
		}
		joinMeasured(fileRecs, usage, f)
		recs = append(recs, unownedRecs(fileRecs, idents, filepath.Dir(f))...)
	}
	if len(bad) > 0 {
		r.Warn(fmt.Sprintf("cost history unreadable — skipped from estimate: %s ", strings.Join(bad, " ")))
	}
	return append(recs, measuredRunRecs(r.repoRoot)...)
}

// measuredRunRecs reads every bundle's two-finder invocation receipts as
// comparable runs, one per execution however many invocations a resume took.
// A fully settled execution is priced by what its spawns measured; one with
// an unsettled invocation is a run whose measured part is only a known lower
// bound. A receipt names no effort tier, so it reads standard, as a cost
// record without one does. Loop spawn receipts are single launches, not runs,
// and stay out.
func measuredRunRecs(repoRoot string) []costRec {
	dirs, err := filepath.Glob(filepath.Join(repoRoot, ".verdict", "state", "*"))
	if err != nil {
		return nil
	}
	slices.Sort(dirs)
	var recs []costRec
	for _, dir := range dirs {
		for _, run := range spend.InvocationRuns(dir) {
			if !run.Complete {
				lower, _ := run.USD.Float64()
				recs = append(recs, costRec{tier: tierStandard, subtotal: &lower})
				continue
			}
			recs = append(recs, costRec{tier: tierStandard, measured: run.USD})
		}
	}
	return recs
}

// costIdent is the identity a cost record carries for ownership: the
// execution it names and when it was written.
type costIdent struct {
	at        any
	execution string
}

// unownedRecs drops the self-reported records a measured invocation receipt
// already counts, by the month-to-date scan's ownership rule: no self-report
// outranks measured spend, and one run is never sampled twice.
func unownedRecs(recs []costRec, idents []costIdent, dir string) []costRec {
	owned := spend.InvocationOwner(dir)
	kept := make([]costRec, 0, len(recs))
	for i := range recs {
		if !owned(idents[i].execution, idents[i].at) {
			kept = append(kept, recs[i])
		}
	}
	return kept
}

// measuredUsage reads the usage records the operator handed over via
// VERDICT_USAGE. A broken handoff refuses typed at the gate; no handoff
// simply leaves the self-reported record in charge.
func measuredUsage(r *session) ([]readcommon.ContractUsage, error) {
	//nolint:wrapcheck // typed refusal passes through untouched so the edge renders its code/message
	return readcommon.ReadContractUsage(r.env)
}

// joinMeasured attaches the handed usage record whose window brackets the
// cost file's mtime to the run that wrote it (the last record of a
// re-review array). No matching record leaves every record untouched.
func joinMeasured(fileRecs []costRec, usage []readcommon.ContractUsage, path string) {
	info, err := os.Stat(path)
	if err != nil || len(fileRecs) == 0 {
		return
	}
	match := readcommon.MatchContractUsage(usage, info.ModTime())
	if match == nil || match.USD == nil {
		return
	}
	usd, ok := spend.ParseAmount(strconv.FormatFloat(*match.USD, 'f', -1, floatBitSize))
	if !ok {
		return
	}
	last := len(fileRecs) - 1
	fileRecs[last].measured = usd
	fileRecs[last].usageSource = match.Source
}

// addTierSample folds one same-tier record into the accumulator.
func addTierSample(r costRec, sum float64, st *tierStat) float64 {
	st.runs++
	sum, st.priced = tierPriced(r, sum, st.exact, st.priced)
	if r.measured == nil {
		if !r.has && r.subtotal != nil {
			st.subtotal += *r.subtotal
		}
		return sum
	}
	st.measured++
	if r.usageSource != "" {
		st.harness = append(st.harness, r.usageSource)
	}
	return sum
}

// tierPriced handles one priced record: the handed usage cost wins,
// else the self-reported usd parses into the sum. exact accumulates the same
// samples without float rounding, for the guards to compare.
func tierPriced(r costRec, sum float64, exact *big.Rat, priced int) (float64, int) {
	if r.measured != nil {
		exact.Add(exact, r.measured)
		f, _ := r.measured.Float64()
		return sum + f, priced + 1
	}
	if r.has {
		f, err := strconv.ParseFloat(r.usd, numBitSize)
		rat, ok := new(big.Rat).SetString(r.usd)
		if err == nil && ok {
			exact.Add(exact, rat)
			return sum + f, priced + 1
		}
	}
	return sum, priced
}

func overBudget(projected, budget string) bool {
	return spend.Exceeds(projected, budget)
}

func jsonNumber(lit string) any {
	if lit == "" {
		return nil
	}
	return jq.Number(lit)
}

// jsonFloat renders a computed mean/est literal ("" reads null). Computed
// values print in canonical jq form.
func jsonFloat(lit string) any {
	return jsonNumber(lit)
}

// writeBudgetReceipt records a consented over-budget
// run appends one receipt to the bundle's budget.json ledger.
func writeBudgetReceipt(r *session, st *budgetState, dir, now string) {
	if !st.receiptNeeded {
		return
	}
	rc := jq.NewObj("at", now)
	receiptProjectionBody(st, rc)
	rc.Set("consent", "--consent-over-budget")
	merged := budgetLedger(r, dir, rc)
	if err := atomic.WriteFile(dir+"/budget.json", []byte(merged+newline)); err != nil {
		r.Warn(fmt.Sprintf("could not write %s/budget.json: %v", dir, err))
		return
	}
	fmt.Fprint(r.Human, core.Msgf("» receipt: %s recorded in %s/budget.json (%s)\n",
		receiptKind(st), dir, receiptSummary(st)))
}

func budgetLedger(r *session, dir string, rc *jq.Obj) string {
	merged := "[" + rc.MarshalPretty() + "]"
	raw, err := noblock.ReadFile(dir + "/budget.json")
	if err != nil {
		return merged
	}
	// An appended ledger renders jq-array-pretty (elements indented).
	doc, derr := jq.DecodeOrdered(raw)
	if derr != nil {
		r.Warn("budget.json unreadable — starting a fresh receipt ledger")
		return merged
	}
	arr, ok := doc.([]any)
	if !ok {
		return merged
	}
	parts := make([]string, 0, len(arr)+1)
	for _, e := range arr {
		if o, ok := e.(*jq.Obj); ok {
			parts = append(parts, indentPretty(o.MarshalPretty()))
		} else {
			parts = append(parts, "  "+jq.Compact(e))
		}
	}
	return "[\n" + strings.Join(append(parts, indentPretty(rc.MarshalPretty())), ",\n") + "\n]"
}

// indentPretty indents every line of a pretty object by two spaces,
// matching jq's array-element layout.
func indentPretty(pretty string) string {
	lines := strings.Split(pretty, newline)
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	return strings.Join(lines, newline)
}

func spendProjection(literal string) spend.Projection {
	value, err := strconv.ParseFloat(literal, floatBitSize)
	if err != nil {
		return spend.Projection{}
	}
	return spend.Projection{Total: &value}
}
