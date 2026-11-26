package reportcore

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/approval"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	"github.com/stainless-code/verdict/internal/core/reviewdoc"
	"github.com/stainless-code/verdict/internal/core/risk"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/spend"
	"github.com/stainless-code/verdict/internal/untrusted"
)

// strZero renders a zero count so every cost line spells it identically.
const strZero = "0"

func buildResponse(st *state) *Response {
	var gateFailed bool
	var gate *core.ReportGate
	if st.gate {
		gateFailed, gate = gateTail(st)
	}
	resp := &Response{
		Repo:     st.req.Repo,
		Pr:       st.req.Pr,
		At:       st.now,
		Data:     buildData(st),
		Warnings: st.warnings,
		Next:     reportNext(st),
		Human:    buildHuman(st),
		Output:   outputArtifact(st),
		Sarif:    sarifArtifact(st),
	}
	resp.Data.Receipt = reportReceipt(st)
	if st.flags.Provenance {
		resp.Data.ProvenanceTruncated = provenance.MergeTruncated(st.provenance, readcommon.BundleTruncation(st.dir))
	}
	resp.GateFailed, resp.Gate = gateFailed, gate
	return resp
}

// orgPackHuman renders the governing central pack version, empty when no
// org pin governed the run.
func orgPackHuman(org *core.OrgPackInfo) string {
	if org == nil {
		return ""
	}
	line := fmt.Sprintf("org pack: %s@%s path=%s", org.Repo, org.Ref, org.Path)
	if org.Name != "" {
		line += fmt.Sprintf(" pack=%s@%s", org.Name, org.Version)
	}
	if org.Hash != "" {
		line += " sha256:" + org.Hash
	}
	if org.SHA != "" {
		line += " commit:" + org.SHA
	}
	return line
}

func nextStep(open int) string {
	if open > 0 {
		return "address open threads"
	}
	return "none open — nothing to address"
}

func reportNext(st *state) string {
	if st.flags.Output != "" {
		return "wrote " + st.flags.Output
	}
	return nextStep(viewOpen(viewRanked(st)))
}

func buildData(st *state) *core.ReportData {
	data := &core.ReportData{
		Verification: st.execution.Attach(panelcore.Summarize(st.entries)),
		Validation:   st.validation,
		Approval:     st.approval,
		GeneratedAt:  st.now,
		Dir:          st.dir,
		Head: core.ReportHead{
			Fetched: new(st.headSHA),
			Base:    new(st.baseSHA),
			Live:    nil,
			Stale:   false,
		},
		Verdict: core.ReportVerdict{
			Open:      countStatus(viewEntries(st), statusOpen),
			Deferred:  countStatus(viewEntries(st), "deferred"),
			Addressed: countStatus(viewEntries(st), "addressed"),
			Terminal:  countTerminal(viewEntries(st)),
		},
		Counts: st.counts,
		Panel:  panelValue(st),
		Patch:  st.patch,
		Output: outputVal(st.flags.Output),
		Filters: core.ReportFilters{
			Status: st.flags.Status, Kind: st.flags.Kind, Category: outputVal(st.flags.Category), IncludeWithheld: st.flags.IncludeWithheld,
		},
		QuietByPolicy: quietEntries(st),
		Items:         envelopeItems(viewRanked(st)),
		OrgPack:       st.orgPack,
		Compare:       st.compare,
	}
	applyCostToData(st, data)
	if st.risk != nil {
		data.Risk = st.risk
	}
	if st.compliance != nil && st.compliance.HasPolicy {
		data.Compliance = &core.ReportCompliance{
			Policy:     true,
			Verified:   !st.compliance.Unverified,
			Blockers:   len(st.compliance.Blockers),
			Conflicted: len(st.compliance.Conflicts),
			Pending:    len(st.compliance.Pending),
			Gaps:       len(st.requiredGaps),
			Satisfied:  st.compliance.Satisfied() && len(st.requiredGaps) == 0,
		}
	}
	if st.flags.Provenance {
		data.ProvenanceEnabled = true
		data.ProvenanceReceipt = st.provenance
	}
	return data
}

func applyCostToData(st *state, data *core.ReportData) {
	if st.cost == nil {
		return
	}
	data.Cost = costFromObj(st.cost)
	if st.mtd != nil {
		data.Cost.Mtd = st.mtd
	}
	data.CostRaw = st.cost
}

func outputVal(output string) *string {
	if output == "" {
		return nil
	}
	return &output
}

func panelValue(st *state) core.ReportPanel {
	var src *string
	if st.panelSrc != "" {
		s := st.panelSrc
		src = &s
	}
	members := st.panel
	if members == nil {
		members = []any{}
	}
	return core.ReportPanel{Source: src, Members: members}
}

func envelopeItems(ranked []readcommon.Ranked) []core.FindingItem {
	items := make([]core.FindingItem, 0, len(ranked))
	for _, item := range ranked {
		items = append(items, envelopeItem(item))
	}
	return items
}

func envelopeItem(item readcommon.Ranked) core.FindingItem {
	f := core.FindingItem{
		Locator: readcommon.Locator(item.Entry),
		Rank:    item.Rank,
	}
	if m, ok := item.Entry.(*jq.Obj); ok && m != nil {
		f.Raw = m
		if s, ok := readcommon.FieldOf(item.Entry, "sig").(string); ok {
			f.Sig = s
		}
		if s, ok := readcommon.FieldOf(item.Entry, "status").(string); ok {
			f.Status = s
		}
		f.Anchored = readcommon.FieldOf(item.Entry, "anchored")
	}
	return f
}

// viewRanked returns the display-filtered ranked set the views render,
// falling back to the full set for states built without the floor pass.
func viewRanked(st *state) []readcommon.Ranked {
	if st.view != nil {
		return st.view
	}
	return st.ranked
}

// viewOpen counts the open findings in the display-filtered view for
// the human summary line; gates keep their own full-set tallies.
func viewOpen(view []readcommon.Ranked) int {
	n := 0

	for _, item := range view {
		if readcommon.StatusOf(item.Entry) == statusOpen {
			n++
		}
	}
	return n
}

// viewEntries flattens the display-filtered view for the payload counts
// and items: the floor withholds below-floor findings from the view
// while every gate keeps reading the full ranked set.
func viewEntries(st *state) []any {
	view := viewRanked(st)
	entries := make([]any, 0, len(view))
	for _, item := range view {
		entries = append(entries, item.Entry)
	}
	return entries
}

func countStatus(entries []any, status string) int {
	n := 0
	for _, e := range entries {
		if readcommon.StatusOf(e) == status {
			n++
		}
	}
	return n
}

func countTerminal(entries []any) int {
	n := 0
	for _, e := range entries {
		if s := readcommon.StatusOf(e); s == "wont-fix" || s == "false-positive" {
			n++
		}
	}
	return n
}

func costFromObj(cost *jq.Obj) *core.ReportCost {
	c := &core.ReportCost{
		Runs:         jqInt(cost, keyRuns),
		RunsWithCost: jqInt(cost, "runs_with_cost"),
		InputTokens:  nullableCostInt(cost, "input_tokens"),
		OutputTokens: nullableCostInt(cost, "output_tokens"),
		TotalTokens:  nullableCostInt(cost, "total_tokens"),
		ByTier:       costTiers(cost),
	}
	c.CachedInputTokens = nullableCostInt(cost, "cached_input_tokens")
	if est, ok := readcommon.NumVal(jqVal(cost, "est_usd")); ok {
		c.EstUSD = &est
	}
	if receipts, ok := jqVal(cost, "receipts").([]any); ok {
		c.Receipts = receipts
	}
	if usd, ok := readcommon.NumVal(jqVal(cost, readcommon.KeyMeasuredUSD)); ok {
		c.MeasuredUSD = &usd
		c.MeasuredReceipts = jqInt(cost, readcommon.KeyMeasuredReceipts)
	}
	return c
}

func costTiers(cost *jq.Obj) core.CostTiers {
	byTier, _ := cost.Get("by_tier")
	obj, ok := byTier.(*jq.Obj)
	if !ok || obj == nil {
		return core.CostTiers{}
	}
	return core.CostTiers{
		Cheap:    costTier(obj, "cheap"),
		Standard: costTier(obj, "standard"),
		Deep:     costTier(obj, "deep"),
	}
}

func costTier(byTier *jq.Obj, tier string) core.CostTier {
	raw, _ := byTier.Get(tier)
	obj, ok := raw.(*jq.Obj)
	if !ok || obj == nil {
		return core.CostTier{}
	}
	return core.CostTier{
		Runs:         jqInt(obj, keyRuns),
		InputTokens:  nullableCostInt(obj, "input_tokens"),
		OutputTokens: nullableCostInt(obj, "output_tokens"),
		TotalTokens:  nullableCostInt(obj, "total_tokens"),
	}
}

// keyRuns names the rollup's runs field (frozen wire key).
const (
	keyRuns        = "runs"
	costNumberBase = 10
)

func jqInt(obj *jq.Obj, key string) int {
	f, _ := readcommon.NumVal(jqVal(obj, key))
	return int(f)
}

func jqVal(obj *jq.Obj, key string) any {
	v, _ := obj.Get(key)
	return v
}

func buildHuman(st *state) []string {
	view := viewRanked(st)
	lines := []string{fmt.Sprintf("report: %s (%d shown) open=%d head=%s",
		st.dir, len(view), viewOpen(view), readcommon.ShortSHA(st.headSHA))}
	if line := orgPackHuman(st.orgPack); line != "" {
		lines = append(lines, line)
	}
	lines = append(lines, readcommon.ContextLine(st.dir)...)
	lines = append(lines, cleanHeadline(st)...)
	lines = append(lines, verificationHuman(st.entries)...)
	lines = append(lines, compareHuman(st)...)
	if line := reviewdoc.FinderLine(st.execution.Attach(nil)); line != "" {
		lines = append(lines, strings.TrimPrefix(line, "> "))
	}
	for _, item := range view {
		lines = append(lines, readcommon.CitationLines(item.Entry)...)
		lines = append(lines, fmt.Sprintf("%d. [%s] %s (%s)",
			item.Rank, jq.Str(readcommon.FieldOf(item.Entry, "status")),
			untrusted.Line(readcommon.Locator(item.Entry)), sigHuman(item.Entry))+reproductionOutput(item.Entry))
	}
	lines = appendHumanTail(st, lines)
	if st.flags.Provenance {
		lines = append(lines, provenance.FormatTable(st.provenance, provenance.MergeTruncated(st.provenance, reportReceipt(st)))...)
	}
	return finalizeHuman(st, view, lines)
}

func finalizeHuman(st *state, view []readcommon.Ranked, lines []string) []string {
	if st.flags.Output != "" {
		return append(lines, "wrote "+st.flags.Output)
	}
	// The footer follows the view the header reports: with a display floor the
	// rendered list can be empty while the store still holds open findings, and
	// "address open threads" beside "(0 shown) open=0" read as a contradiction.
	return append(lines, "next: "+nextStep(viewOpen(view)))
}

func appendHumanTail(st *state, lines []string) []string {
	if st.flags.Category == "" {
		if line := categoryHuman(st.counts.ByCategory); line != "" {
			lines = append(lines, line)
		}
	}
	if line := sourceHuman(st.counts.BySource); line != "" {
		lines = append(lines, line)
	}
	if st.flags.Cost {
		lines = append(lines, costHuman(st)...)
	}
	if st.risk != nil {
		lines = append(lines, riskHuman(st.risk)...)
	}
	lines = append(lines, validationHuman(st)...)
	for _, line := range quietLines(st) {
		lines = append(lines, untrusted.Line(line))
	}
	return append(lines, extraHuman(st)...)
}

// complianceHuman names the captured compliance verdict before any display
// filter: blockers fail, pending or unverified work stays unknown, and only a
// fully satisfied capture passes.
// extraHuman appends the captured compliance verdict, empty when the bundle
// carries no typed policy.
func extraHuman(st *state) []string {
	if line := complianceHuman(st); line != "" {
		return []string{line}
	}
	return nil
}

func complianceHuman(st *state) string {
	if st.compliance == nil || !st.compliance.HasPolicy {
		return ""
	}
	switch {
	case len(st.compliance.Blockers) > 0:
		return fmt.Sprintf("compliance: unmet — %d blocking finding(s)", len(st.compliance.Blockers))
	case len(st.compliance.Conflicts) > 0:
		first := st.compliance.Conflicts[0]
		return fmt.Sprintf("compliance: unmet — required review withholds %s (rule %s)",
			untrusted.Line(first.Path), untrusted.Line(first.Rule))
	case st.compliance.Unverified:
		return "compliance: unknown — captured provenance unavailable"
	case len(st.compliance.Pending) > 0:
		return fmt.Sprintf("compliance: unknown — %d required check(s) pending", len(st.compliance.Pending))
	case len(st.requiredGaps) > 0:
		return "compliance: unknown — " + st.requiredGaps[0]
	default:
		return "compliance: satisfied"
	}
}

func riskHuman(r *core.ReportRisk) []string {
	lines := []string{
		fmt.Sprintf("risk: %s (claimed=%s floor=%s)", r.Reported, r.Claimed, r.Floor),
		riskInputsHuman(r.Inputs),
	}
	if r.Score != nil {
		lines = append(lines, riskScoreHuman(r.Score))
	}
	if r.AssessedHead != nil {
		headLine := "risk assessed: " + readcommon.ShortSHA(*r.AssessedHead)
		if r.Rationale != nil && *r.Rationale != "" {
			headLine += " — " + *r.Rationale
		}
		lines = append(lines, headLine)
	}
	if len(r.ReceiptReasons) > 0 {
		lines = append(lines, "risk receipt: "+strings.Join(r.ReceiptReasons, ", "))
	}
	return lines
}

func riskInputsHuman(in core.RiskInputs) string {
	unrev := "missing"
	if in.UnreviewedFiles != nil {
		unrev = strconv.Itoa(*in.UnreviewedFiles)
	}
	return fmt.Sprintf(
		"risk floor inputs: open_critical=%d open_important=%d deferred=%d "+
			"unanchored=%d unreviewed_files=%s stale_head=%t missing_coverage=%t",
		in.OpenCritical, in.OpenImportant, in.Deferred, in.Unanchored, unrev, in.StaleHead, in.MissingCoverage,
	)
}

func riskScoreHuman(s *core.RiskScore) string {
	return fmt.Sprintf("risk score: %d/100 (reach %dw%d churn %dw%d category %dw%d rung %dw%d, measured %d/4)",
		s.Value, s.Reach.Points, s.Reach.Weight, s.Churn.Points, s.Churn.Weight,
		s.Category.Points, s.Category.Weight, s.Rung.Points, s.Rung.Weight, s.Measured)
}

func sigHuman(entry any) string {
	if sig, ok := readcommon.FieldOf(entry, "sig").(string); ok {
		return readcommon.Prefix(sig)
	}
	return jq.Str(readcommon.FieldOf(entry, "sig"))
}

// categoryHuman renders the whole-store category group line: every non-zero
// category in wire order, untagged last when non-zero. Empty when the store
// holds no findings, so empty stores print exactly today's lines.
func categoryHuman(counts map[string]int) string {
	parts := make([]string, 0, len(core.CategoryValues)+1)
	for _, c := range core.CategoryValues {
		if counts[c] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", c, counts[c]))
		}
	}
	if counts[core.CategoryUntagged] > 0 {
		parts = append(parts, fmt.Sprintf("%s %d", core.CategoryUntagged, counts[core.CategoryUntagged]))
	}
	if len(parts) == 0 {
		return ""
	}
	return "by category: " + strings.Join(parts, ", ")
}

func sourceHuman(counts map[string]int) string {
	if counts["tool"] == 0 {
		return ""
	}
	return fmt.Sprintf("by source: model %d, tool %d", counts["model"], counts["tool"])
}

func costHuman(st *state) []string {
	cost := st.cost
	if cost == nil {
		return nil
	}
	runs := jqStr(cost, keyRuns)
	if runs == strZero {
		lines := []string{fmt.Sprintf("cost: unrecorded (no usable %s/%s — the agent writes one per prompts/review.md)",
			st.dir, readcommon.CostFile)}
		lines = appendMeasured(lines, cost)
		if st.mtd != nil {
			lines = append(lines, mtdHuman(st.mtd))
		}
		return lines
	}
	lines := []string{fmt.Sprintf("cost: %s run(s), %s with records — in=%s out=%s total=%s est_usd=%s cached=%s",
		runs, jqStr(cost, "runs_with_cost"), orNA(jqVal(cost, "input_tokens")),
		orNA(jqVal(cost, "output_tokens")), orNA(jqVal(cost, "total_tokens")),
		orNA(jqVal(cost, "est_usd")), orNA(jqVal(cost, "cached_input_tokens")))}
	byTier, _ := cost.Get("by_tier")
	tierObj, ok := byTier.(*jq.Obj)
	if !ok {
		tierObj = nil
	}
	lines = append(lines, fmt.Sprintf("tiers: cheap=%s standard=%s deep=%s",
		tierRuns(tierObj, "cheap"), tierRuns(tierObj, "standard"), tierRuns(tierObj, "deep")))
	if consented := consentedReceipts(st.receipts); len(consented) > 0 {
		latest := consented[len(consented)-1]
		lines = append(lines, fmt.Sprintf("receipts: %d consented over-budget run(s) (latest projected $%s %s vs budget $%s @ %s)",
			len(consented), spend.DisplayUSD(receiptField(latest, "projected_usd")),
			receiptField(latest, "projected_tier"), receiptField(latest, "budget_usd"), receiptField(latest, "at")))
	}
	lines = appendMeasured(lines, cost)
	if st.mtd != nil {
		lines = append(lines, mtdHuman(st.mtd))
	}
	return lines
}

// appendMeasured adds the bundle's measured spend line when any receipt the
// agent CLIs priced exists.
func appendMeasured(lines []string, cost *jq.Obj) []string {
	usd, ok := jqVal(cost, readcommon.KeyMeasuredUSD).(jq.Number)
	if !ok {
		return lines
	}
	return append(lines, fmt.Sprintf("measured: $%s from %s spend receipt(s) the agent CLIs priced",
		spend.DisplayUSD(usd.String()), jqStr(cost, readcommon.KeyMeasuredReceipts)))
}

func mtdHuman(mtd *core.ReportMTD) string {
	counts := fmt.Sprintf("(%d priced, %d unknown)", mtd.KnownCount, mtd.UnknownCount)
	if mtd.MonthlyCapUSD == nil {
		return fmt.Sprintf("mtd: %s %s %s — no ceiling", mtd.Month, mtdSpend(mtd, ""), counts)
	}
	ceiling := strconv.FormatFloat(*mtd.MonthlyCapUSD, 'f', -1, mtdFloatBits)
	known := mtdSpend(mtd, ceiling)
	if mtd.RemainingUSD != nil {
		rem := visibleUSD(strconv.FormatFloat(*mtd.RemainingUSD, 'f', -1, mtdFloatBits), "")
		return fmt.Sprintf(
			"mtd: %s %s of $%s ceiling, remaining $%s %s",
			mtd.Month, known, ceiling, rem, counts)
	}
	return fmt.Sprintf("mtd: %s %s of $%s ceiling %s", mtd.Month, known, ceiling, counts)
}

// mtdSpend renders the month's known leg: a dollar amount when spend is
// known, "unknown" when unpriced receipts exist, and $0 when nothing was
// recorded.
func mtdSpend(mtd *core.ReportMTD, ceiling string) string {
	if mtd.KnownUSD != nil {
		return "known $" + visibleUSD(strconv.FormatFloat(*mtd.KnownUSD, 'f', -1, mtdFloatBits), ceiling)
	}
	if mtd.UnknownCount > 0 {
		return "known unknown"
	}
	return "known $" + strZero
}

// visibleUSD rounds an amount for a person without letting the rounding hide
// what matters: a breach of ceiling (when one is set) or a nonzero amount
// both round up rather than read as within the ceiling or as zero.
func visibleUSD(amount, ceiling string) string {
	if ceiling != "" {
		if shown := spend.DisplayBreach(amount, ceiling); shown != strZero {
			return shown
		}
	}
	return spend.DisplayBreach(amount, strZero)
}

func jqStr(obj *jq.Obj, key string) string {
	return jq.Str(jqVal(obj, key))
}

func orNA(v any) string {
	if v == nil {
		return "n/a"
	}
	return jq.Str(v)
}

func tierRuns(byTier *jq.Obj, tier string) string {
	if byTier == nil {
		return strZero
	}
	raw, _ := byTier.Get(tier)
	obj, ok := raw.(*jq.Obj)
	if !ok {
		obj = nil
	}
	if obj == nil {
		return strZero
	}
	return jqStr(obj, keyRuns)
}

// consentedReceipts keeps the ledger rows an operator's
// --consent-over-budget wrote. run settles a spend receipt into the same
// ledger for every two-finder execution, consented or not, and those are
// spend, not consent.
func consentedReceipts(receipts []any) []any {
	var out []any
	for _, row := range receipts {
		if consent := receiptField(row, "consent"); consent != fieldAbsent && consent != "" {
			out = append(out, row)
		}
	}
	return out
}

// fieldAbsent stands in for a receipt field the row does not carry.
const fieldAbsent = "?"

func receiptField(latest any, key string) string {
	if m, ok := latest.(map[string]any); ok {
		return receiptFieldFromMap(m, key)
	}
	return receiptFieldFromObj(latest, key)
}

func receiptFieldFromMap(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil || v == false {
		return fieldAbsent
	}
	return jq.Str(v)
}

func receiptFieldFromObj(latest any, key string) string {
	om, ok := latest.(*jq.Obj)
	if !ok {
		return fieldAbsent
	}
	v, _ := om.Get(key)
	if v == nil || v == false {
		return fieldAbsent
	}
	return jq.Str(v)
}

// Dimension ids and statuses of the gate verb's envelope.
const (
	dimFindings   = "findings"
	dimHead       = "head"
	dimCoverage   = "coverage"
	dimLive       = "live_threads"
	dimPass       = "pass"
	dimFail       = "fail"
	dimSkip       = "skip"
	dimUnknown    = "unknown"
	dimCompliance = "compliance"
	dimRisk       = "risk"
	dimSpec       = "spec"
	dimEvidence   = "evidence"
)

// gateTail composes the requested dimensions once into the typed result the
// gate verb publishes. A skip stays neutral; fail and unknown fail.
func gateTail(st *state) (bool, *core.ReportGate) {
	dims := gateChecks(st)
	failed := false
	for _, dim := range dims {
		if dim.Status == dimFail || dim.Status == dimUnknown {
			failed = true
		}
	}
	return failed, stoppedGate(st, &core.ReportGate{Passed: !failed, Dimensions: dims})
}

// stoppedGate marks a gate over a two-finder execution that stopped short:
// resumable, or not, with the gap a fresh run has to replace.
func stoppedGate(st *state, gate *core.ReportGate) *core.ReportGate {
	switch {
	case st.execution.Incomplete():
		gate.Resume = true
	case st.execution.Partial():
		gate.Restart = st.execution.Gap
	}
	return gate
}

// gateDimensions is the ceiling on one run's gate checks: findings, head,
// coverage, live, compliance, risk, spec, evidence.
const gateDimensions = 8

// gateChecks evaluates the dimensions this run asked for, in wire order. The
// spec and evidence dimensions are always on: the derived approval withholds on
// a violated spec check, so a gate that passed over one would certify a review
// the decision refuses, and a gate that passed without a receipt for the gated
// head would certify a review nothing checked.
func gateChecks(st *state) []core.GateDimension {
	specs := []struct {
		build   func(*state) core.GateDimension
		enabled bool
	}{
		{gateTierCheck, st.flags.Check != ""},
		{gateCompletenessCheck, st.flags.Completeness},
		{gateCoverageCheck, st.flags.Coverage != ""},
		{gateLiveCheck, st.liveGate},
		{gateComplianceCheck, st.compliance != nil && st.compliance.HasPolicy},
		{gateRiskCheck, st.flags.MaxRisk != "" && st.risk != nil},
		{gateSpecCheck, true},
		{gateEvidenceCheck, true},
	}
	checks := make([]core.GateDimension, 0, gateDimensions)
	for _, spec := range specs {
		if spec.enabled {
			checks = append(checks, spec.build(st))
		}
	}
	return checks
}

// gateSpecCheck grades the spec checks the stored record judged: a review the
// decision withholds over cannot pass a gate, so the first failed item names
// itself and the count tallies them all. A failed check is one the code
// violates or one nothing implements; the detail says which, because the two
// are different work for the author.
func gateSpecCheck(st *state) core.GateDimension {
	failures := st.specFailures
	if len(failures) == 0 {
		const detail = "no violated spec check"
		return core.GateDimension{ID: dimSpec, Status: dimPass, Count: new(0), Detail: detail}
	}
	detail := specFailureDetail(failures[0])
	if len(failures) > 1 {
		detail = fmt.Sprintf("%d failed spec checks: %s", len(failures), detail)
		if allViolatedSpecFailures(failures) {
			detail = fmt.Sprintf("%d violated spec checks: %s", len(failures), failures[0].Item)
		}
	}
	return core.GateDimension{ID: dimSpec, Status: dimFail, Count: new(len(failures)), Detail: detail}
}

// specFailureDetail words one failing spec check for the gate line: a violated
// row keeps the sentence it has always carried, and an unmet row says nothing
// implements the requirement.
func specFailureDetail(failure core.SpecFailure) string {
	if failure.Status == "unmet" {
		return "unmet spec check: " + failure.Item
	}
	return "violated spec check: " + failure.Item
}

// allViolatedSpecFailures reports whether every failure is a violated row, so
// the tally keeps the wording a violations-only gate has always printed.
func allViolatedSpecFailures(failures []core.SpecFailure) bool {
	for _, failure := range failures {
		if failure.Status != "violated" {
			return false
		}
	}
	return true
}

func gateRiskCheck(st *state) core.GateDimension {
	r := st.risk
	if !r.Exceeded {
		detail := fmt.Sprintf("reported risk %s <= max-risk %s", r.Reported, *r.MaxRisk)
		return core.GateDimension{ID: dimRisk, Status: dimPass, Detail: detail}
	}
	if r.Reported == risk.LevelUnknown {
		detail := fmt.Sprintf("reported risk unknown > max-risk %s (%s)", *r.MaxRisk, risk.GateReasonUnknown)
		return core.GateDimension{ID: dimRisk, Status: dimUnknown, Detail: detail}
	}
	detail := fmt.Sprintf("reported risk %s exceeds max-risk %s (%s)", r.Reported, *r.MaxRisk, risk.GateReasonExceeded)
	return core.GateDimension{ID: dimRisk, Status: dimFail, Detail: detail}
}

// gateComplianceCheck evaluates captured requirements: a blocking finding
// fails, unverified provenance or a pending required check is unknown, and
// only a fully satisfied capture passes. An absent policy adds no dimension,
// so legacy bundles keep their exact gate.
func gateComplianceCheck(st *state) core.GateDimension {
	comp := st.compliance
	if comp == nil {
		return core.GateDimension{}
	}
	status, detail, count := complianceVerdict(comp, st.requiredGaps, st.headSHA)
	return core.GateDimension{ID: dimCompliance, Status: status, Count: new(count), Detail: detail}
}

// complianceVerdict maps the captured compliance state to one gate verdict:
// blockers and privacy conflicts fail, while unverified provenance, required
// profile work, and pending checks stay unknown — never a pass.
func complianceVerdict(comp *config.ReadCompliance, gaps []string, head string) (string, string, int) {
	switch {
	case len(comp.Blockers) > 0:
		first := comp.Blockers[0]
		detail := fmt.Sprintf("%d open finding(s) violate %q", len(comp.Blockers), first.Clause)
		if first.GroupKnown {
			detail += " for group " + first.Group
		}
		return dimFail, detail, len(comp.Blockers)
	case len(comp.Conflicts) > 0:
		first := comp.Conflicts[0]
		detail := fmt.Sprintf("required review withholds %s (rule %s)", first.Path, first.Rule)
		if first.Group != "" {
			detail += " in group " + first.Group
		}
		return dimFail, detail + " — unmet, no disclosure", len(comp.Conflicts)
	case comp.Unverified:
		return dimUnknown, "captured provenance unavailable", 0
	case len(gaps) > 0:
		return dimUnknown, gaps[0] + " — unknown, never a pass", len(gaps)
	case len(comp.Pending) > 0:
		return dimUnknown, pendingDetail(comp, head), len(comp.Pending)
	default:
		return dimPass, "captured requirements satisfied", 0
	}
}

// pendingDetail names the first required check still pending.
func pendingDetail(comp *config.ReadCompliance, head string) string {
	first := comp.Pending[0]
	detail := fmt.Sprintf("%d required check(s) pending for head %s", len(comp.Pending), readcommon.ShortSHA(head))
	if first.Group != "" {
		detail += " (first: " + first.Check + " in " + first.Group + ")"
	}
	return detail + " — unknown, never a pass"
}

// gateTierCheck counts the open findings that gate. An incomplete execution
// leaves every finding it cross-checks unsettled, so none gates and none can
// be said to be absent: those findings read unknown, never a pass.
func gateTierCheck(st *state) core.GateDimension {
	n := tierHits(readcommon.VisibleFindings(st.entries), st.flags.Check, st.flags.Category)
	if n > 0 {
		return core.GateDimension{
			ID: dimFindings, Status: dimFail, Count: new(n),
			Detail: fmt.Sprintf("%d open finding(s) at-or-above %s%s", n, st.flags.Check, inCategory(st)),
		}
	}
	if st.execution.Partial() {
		return incompleteFindings(st)
	}
	return core.GateDimension{
		ID: dimFindings, Status: dimPass, Count: new(n),
		Detail: "no open findings at-or-above " + st.flags.Check + inCategory(st),
	}
}

// incompleteFindings is the findings dimension of an execution that stopped
// before it settled its cross checks, resumable or not: none of its findings
// can gate yet, so none can be said to be absent either, whatever their tier.
func incompleteFindings(st *state) core.GateDimension {
	pending := gatePendingCross(st)
	detail := fmt.Sprintf("%d open finding(s) at-or-above %s%s await the cross checks of an incomplete "+
		"execution (%s) — unknown, never a pass", pending, st.flags.Check, inCategory(st), st.execution.Gap)
	if pending == 0 {
		detail = "the two-finder execution is incomplete (" + st.execution.Gap + "), so no finding is settled " +
			"— unknown, never a pass"
	}
	return core.GateDimension{ID: dimFindings, Status: dimUnknown, Count: new(pending), Detail: detail}
}

// inCategory names the gated category in a dimension detail, empty when the
// gate spans every category.
func inCategory(st *state) string {
	if st.flags.Category == "" {
		return ""
	}
	return " in " + st.flags.Category
}

func gateCompletenessCheck(st *state) core.GateDimension {
	short := readcommon.ShortSHA(st.headSHA)
	if st.compOK {
		return core.GateDimension{ID: dimHead, Status: dimPass, Detail: "HEAD " + short + " reviewed (post matches fetch)"}
	}
	if st.compSamePatch != nil && *st.compSamePatch {
		posted := readcommon.ShortSHA(st.compPosted)
		return core.GateDimension{ID: dimHead, Status: dimPass, Detail: "same patch reviewed at " + short + " (posted " + posted + ")"}
	}
	return core.GateDimension{ID: dimHead, Status: completenessStatus(st), Detail: completenessDetail(st, short)}
}

// completenessStatus separates a stale post (a definite fail) from a missing
// or headless post record (unknown: nothing recorded the head).
func completenessStatus(st *state) string {
	if isRegular(st.dir+"/post.json") && st.compPosted != "" {
		return dimFail
	}
	return dimUnknown
}

// completenessDetail words the head dimension for the gate's line: the typed
// cause and the re-fetch command that advances it, so the recovery survives
// without the report check line that used to carry it.
func completenessDetail(st *state, short string) string {
	refetch := refetchCommand(st.dir, st.req.ReadRequest, st.compPosted)
	switch {
	case !isRegular(st.dir + "/post.json"):
		return fmt.Sprintf("no completed post record for HEAD %s (missing-post: run %s, then review + post)", short, refetch)
	case st.compPosted == "":
		return fmt.Sprintf("post record carries no head SHA (fetched HEAD %s; corrupt-post: re-run %s, then review + post)", short, refetch)
	default:
		return fmt.Sprintf("posted HEAD %s != fetched HEAD %s (stale-head: re-run %s, then review + post)",
			readcommon.ShortSHA(st.compPosted), short, refetch)
	}
}

func gateLiveCheck(st *state) core.GateDimension {
	switch st.live.status {
	case liveFail:
		return liveDimension(st, dimFail)
	case livePass:
		return liveDimension(st, dimPass)
	case liveUnknownStrict:
		return core.GateDimension{
			ID: dimLive, Status: dimUnknown,
			Detail: "live thread recheck unreachable (" + liveErrDetail(st) + ") — offline posture is strict",
		}
	case liveUnknown:
		return core.GateDimension{
			ID: dimLive, Status: dimSkip,
			Detail: "live thread recheck unreachable — gating on local dimensions only",
		}
	case liveSkipExplicit:
		return core.GateDimension{ID: dimLive, Status: dimSkip, Detail: "live recheck skipped (--live skip)"}
	default:
		return core.GateDimension{
			ID: dimLive, Status: dimSkip,
			Detail: "live recheck skipped: local bundle threads unreadable here",
		}
	}
}

func liveDimension(st *state, status string) core.GateDimension {
	return core.GateDimension{
		ID: dimLive, Status: status, Count: new(st.live.open),
		Detail: fmt.Sprintf("%d live open thread(s) vs %d local open finding(s)", st.live.open, st.nAllOpen),
	}
}

func liveErrDetail(st *state) string {
	if st.live.err == "" {
		return "lookup failed"
	}
	return st.live.err
}

func tierHits(entries []any, tier, category string) int {
	threshold := core.TierRank(tier)
	n := 0
	for _, e := range entries {
		if tierHit(e, threshold, category) {
			n++
		}
	}
	return n
}

// tierHit reports whether one open finding meets the gate tier inside the
// gated category: an empty category gates every class. A category gate
// narrows the class without substituting for anchor or rigor, so it also
// requires an anchored finding with a recorded evidence rung; the baseline
// gate without a category keeps its severity-only semantics.
func tierHit(e any, threshold int, category string) bool {
	if readcommon.StatusOf(e) != statusOpen {
		return false
	}
	if category != "" && core.CategoryOf(e) != category {
		return false
	}
	if core.TierRank(readcommon.FieldOf(e, "severity")) > threshold {
		return false
	}
	if approval.QuoteStatus(e) == "unverified" || !approval.CrossEligible(e) {
		return false
	}
	return category == "" || gateEligible(e)
}

// gateEligible reports whether one category-matched entry carries the anchor
// and rigor the gate still requires: an inline anchor plus a recorded rung.
// Every published rung counts, said included — the category gate sets no
// minimum rung — and an absent, null, or unknown rigor never gates.
func gateEligible(e any) bool {
	if !inlineAnchored(e, true) {
		return false
	}
	rigor, ok := readcommon.FieldOf(e, "rigor").(string)
	return ok && slices.Contains(core.RungValues, rigor)
}

func cleanHeadline(st *state) []string {
	if len(st.entries) == 0 {
		return []string{fmt.Sprintf("no findings — checked %d files", st.counts.CheckedFiles)}
	}
	if readcommon.CountOpen(st.entries) > 0 {
		return nil
	}
	addressed := 0
	for _, e := range st.entries {
		if readcommon.StatusOf(e) == statusAddressed {
			addressed++
		}
	}
	return []string{fmt.Sprintf("no open findings — %d addressed, %d terminal", addressed, len(st.entries)-addressed)}
}

func verificationHuman(entries []any) []string {
	if v := panelcore.Summarize(entries); v != nil {
		return []string{fmt.Sprintf("verification: %d confirmed · %d contested · %d single", v.Confirmed, v.Contested, v.Single)}
	}
	return nil
}

func reportReceipt(st *state) *core.Truncation {
	if st.truncation != nil {
		return st.truncation.ForResult(len(viewRanked(st)))
	}
	if st.flags.Output != "" && st.nAllOpen == 0 {
		total := countNonEmpty(strings.Split(checkedFiles(st.dir), newline))
		if total > checkedShown {
			return core.ResultCut("response_cap", checkedShown, new(total), nil)
		}
	}
	return readcommon.BundleTruncation(st.dir).ForResult(len(viewRanked(st)))
}

// validationHuman renders the pass: the tally and the exact-file hash on top,
// then one row per judged finding with the verdict, the severity move, and the
// validator's reason flattened to one line.
func validationHuman(st *state) []string {
	data := st.validation
	if data == nil {
		return nil
	}
	hash := "none"
	if data.Hash != nil {
		hash = *data.Hash
	}
	lines := []string{fmt.Sprintf("validation: mode=%s hash=%s confirmed=%d rejected=%d downgraded=%d unvalidated=%d",
		data.Mode, hash, data.Counts.Confirmed, data.Counts.Rejected, data.Counts.Downgraded, data.Counts.Unvalidated)}
	if len(st.judgements) == 0 {
		return lines
	}
	return append(lines, validationRows(st.judgements)...)
}

const (
	validationSigHeader    = "sig"
	validationColumns      = 4
	validationSpacing      = "  "
	validationReasonColumn = validationColumns - 1
)

func validationRows(judgements []core.ValidationResult) []string {
	rows := make([][validationColumns]string, 0, len(judgements)+1)
	rows = append(rows, [validationColumns]string{validationSigHeader, "verdict", "move", "reason"})
	for i := range judgements {
		r := &judgements[i]
		move := "-"
		if r.Verdict == core.ValidationDowngraded {
			move = r.FromSeverity + " → " + r.ToSeverity
		}
		rows = append(rows, [validationColumns]string{readcommon.Prefix(r.Sig), r.Verdict, move, flattenReason(r.Reason)})
	}
	return formatValidationRows(rows)
}

func formatValidationRows(rows [][validationColumns]string) []string {
	var widths [validationReasonColumn]int
	for _, row := range rows {
		for c := range widths {
			widths[c] = max(widths[c], utf8.RuneCountInString(row[c]))
		}
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, validationSpacing+padRune(row[0], widths[0])+validationSpacing+
			padRune(row[1], widths[1])+validationSpacing+padRune(row[2], widths[2])+validationSpacing+row[validationReasonColumn])
	}
	return lines
}

func padRune(s string, width int) string {
	return s + strings.Repeat(" ", width-utf8.RuneCountInString(s))
}

func flattenReason(reason string) string {
	plain := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, reason)
	return strings.Join(strings.Fields(plain), " ")
}

func nullableCostInt(obj *jq.Obj, key string) *int {
	n, ok := jq.IntOf(jqVal(obj, key))
	if raw, yes := jqVal(obj, key).(int64); yes {
		n, ok = jq.IntOf(jq.Number(strconv.FormatInt(raw, costNumberBase)))
	}
	if !ok {
		return nil
	}
	value := n
	return &value
}
