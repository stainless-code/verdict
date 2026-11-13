package reportcore

import (
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func buildResponse(st *state) *core.ReportResponse {
	resp := &core.ReportResponse{
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
	if st.gate {
		resp.GateFailed, resp.CheckLines, resp.Data.Gate = gateTail(st)
	}
	return resp
}

func reportNext(st *state) string {
	if st.flags.Output != "" {
		return "wrote " + st.flags.Output
	}
	if viewOpen(viewRanked(st)) > 0 {
		return "address open threads"
	}
	return "none open — nothing to address"
}

func buildData(st *state) *core.ReportData {
	data := &core.ReportData{
		Verification: panelcore.Summarize(st.entries),
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
		Counts:  st.counts,
		Panel:   panelValue(st),
		Patch:   st.patch,
		Output:  outputVal(st.flags.Output),
		Filters: core.ReportFilters{Status: st.flags.Status, Kind: st.flags.Kind, Category: outputVal(st.flags.Category)},
		Items:   envelopeItems(viewRanked(st)),
	}
	if st.cost != nil {
		data.Cost = costFromObj(st.cost)
		data.CostRaw = st.cost
	}
	if st.comp != nil {
		data.Completeness = st.comp
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
	return data
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
		InputTokens:  jqInt(cost, "input_tokens"),
		OutputTokens: jqInt(cost, "output_tokens"),
		TotalTokens:  jqInt(cost, "total_tokens"),
		ByTier:       costTiers(cost),
	}
	c.CachedInputTokens = jqInt(cost, "cached_input_tokens")
	if est, ok := readcommon.NumVal(jqVal(cost, "est_usd")); ok {
		c.EstUSD = &est
	}
	if receipts, ok := jqVal(cost, "receipts").([]any); ok {
		c.Receipts = receipts
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
		InputTokens:  jqInt(obj, "input_tokens"),
		OutputTokens: jqInt(obj, "output_tokens"),
		TotalTokens:  jqInt(obj, "total_tokens"),
	}
}

// keyRuns names the rollup's runs field (frozen wire key).
const keyRuns = "runs"

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
	lines = append(lines, readcommon.ContextLine(st.dir)...)
	lines = append(lines, cleanHeadline(st)...)
	lines = append(lines, verificationHuman(st.entries)...)
	for _, item := range view {
		lines = append(lines, readcommon.CitationLines(item.Entry)...)
		lines = append(lines, fmt.Sprintf("%d. [%s] %s (%s)",
			item.Rank, jq.Str(readcommon.FieldOf(item.Entry, "status")),
			readcommon.Locator(item.Entry), sigHuman(item.Entry)))
	}
	if st.flags.Category == "" {
		if line := categoryHuman(st.counts.ByCategory); line != "" {
			lines = append(lines, line)
		}
	}
	if st.flags.Cost {
		lines = append(lines, costHuman(st)...)
	}
	lines = append(lines, extraHuman(st)...)
	if st.flags.Output != "" {
		return append(lines, "wrote "+st.flags.Output)
	}
	// The footer follows the view the header reports: with a display floor the
	// rendered list can be empty while the store still holds open findings, and
	// "address open threads" beside "(0 shown) open=0" read as a contradiction.
	if viewOpen(view) > 0 {
		return append(lines, "next: address open threads")
	}
	return append(lines, "next: none open — nothing to address")
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
		return fmt.Sprintf("compliance: unmet — required review withholds %s (rule %s)", first.Path, first.Rule)
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

func costHuman(st *state) []string {
	cost := st.cost
	if cost == nil {
		return nil
	}
	runs := jqStr(cost, keyRuns)
	if runs == "0" {
		return []string{fmt.Sprintf("cost: unrecorded (no usable %s/%s — the agent writes one per prompts/review.md)",
			st.dir, readcommon.CostFile)}
	}
	lines := []string{fmt.Sprintf("cost: %s run(s), %s with records — in=%s out=%s total=%s est_usd=%s cached=%s",
		runs, jqStr(cost, "runs_with_cost"), jqStr(cost, "input_tokens"),
		jqStr(cost, "output_tokens"), jqStr(cost, "total_tokens"),
		orNA(jqVal(cost, "est_usd")), jqStr(cost, "cached_input_tokens"))}
	byTier, _ := cost.Get("by_tier")
	tierObj, ok := byTier.(*jq.Obj)
	if !ok {
		tierObj = nil
	}
	lines = append(lines, fmt.Sprintf("tiers: cheap=%s standard=%s deep=%s",
		tierRuns(tierObj, "cheap"), tierRuns(tierObj, "standard"), tierRuns(tierObj, "deep")))
	if st.receipts != nil {
		latest := st.receipts[len(st.receipts)-1]
		lines = append(lines, fmt.Sprintf("receipts: %d consented over-budget run(s) (latest projected $%s %s vs budget $%s @ %s)",
			len(st.receipts), receiptField(latest, "projected_usd"),
			receiptField(latest, "projected_tier"), receiptField(latest, "budget_usd"), receiptField(latest, "at")))
	}
	return lines
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
		return "0"
	}
	raw, _ := byTier.Get(tier)
	obj, ok := raw.(*jq.Obj)
	if !ok {
		obj = nil
	}
	if obj == nil {
		return "0"
	}
	return jqStr(obj, keyRuns)
}

func receiptField(latest any, key string) string {
	if m, ok := latest.(map[string]any); ok {
		return receiptFieldFromMap(m, key)
	}
	return receiptFieldFromObj(latest, key)
}

func receiptFieldFromMap(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil || v == false {
		return "?"
	}
	return jq.Str(v)
}

func receiptFieldFromObj(latest any, key string) string {
	om, ok := latest.(*jq.Obj)
	if !ok {
		return "?"
	}
	v, _ := om.Get(key)
	if v == nil || v == false {
		return "?"
	}
	return jq.Str(v)
}

// Dimension ids, statuses, and the gate vocabulary shared by the report check
// lines and the gate verb's envelope.
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
	checkPrefix   = "check: "
)

// gateCheck is one dimension with the report check line it prints; line is
// empty for a dimension the report surface stays quiet about.
type gateCheck struct {
	dim  core.GateDimension
	line string
}

// gateTail composes the requested dimensions once and derives both surfaces
// from them: the report's check lines and the typed result the gate verb and
// report --json publish. A skip stays neutral; fail and unknown fail.
func gateTail(st *state) (bool, []string, *core.ReportGate) {
	checks := gateChecks(st)
	lines := make([]string, 0, len(checks))
	dims := make([]core.GateDimension, 0, len(checks))
	failed := false
	for _, c := range checks {
		if c.line != "" {
			lines = append(lines, c.line)
		}
		dims = append(dims, c.dim)
		if c.dim.Status == dimFail || c.dim.Status == dimUnknown {
			failed = true
		}
	}
	return failed, lines, &core.ReportGate{Passed: !failed, Dimensions: dims}
}

// gateDimensions is the ceiling on one run's gate checks: findings, head,
// coverage, live, compliance.
const gateDimensions = 5

// gateChecks evaluates the dimensions this run asked for, in wire order.
func gateChecks(st *state) []gateCheck {
	checks := make([]gateCheck, 0, gateDimensions)
	if st.flags.Check != "" {
		checks = append(checks, gateTierCheck(st))
	}
	if st.flags.Completeness {
		checks = append(checks, gateCompletenessCheck(st))
	}
	if st.flags.Coverage != "" {
		checks = append(checks, gateCoverageCheck(st))
	}
	checks = append(checks, gateLiveCheck(st))
	if st.compliance != nil && st.compliance.HasPolicy {
		checks = append(checks, gateComplianceCheck(st))
	}
	return checks
}

// gateComplianceCheck evaluates captured requirements: a blocking finding
// fails, unverified provenance or a pending required check is unknown, and
// only a fully satisfied capture passes. An absent policy adds no dimension,
// so legacy bundles keep their exact gate.
func gateComplianceCheck(st *state) gateCheck {
	comp := st.compliance
	if comp == nil {
		return gateCheck{}
	}
	status, detail, count := complianceVerdict(comp, st.requiredGaps, st.headSHA)
	return gateCheck{
		dim:  core.GateDimension{ID: dimCompliance, Status: status, Count: new(count), Detail: detail},
		line: checkPrefix + detail + complianceLineSuffix(status),
	}
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

// complianceLineSuffix names the verdict outcome in the check line.
func complianceLineSuffix(status string) string {
	if status == dimPass {
		return " — gate passes"
	}
	return " — gate fails"
}

func gateTierCheck(st *state) gateCheck {
	n := tierHits(st.entries, st.flags.Check, st.flags.Category)
	if n > 0 {
		return gateCheck{
			dim: core.GateDimension{
				ID: dimFindings, Status: dimFail, Count: new(n),
				Detail: fmt.Sprintf("%d open finding(s) at-or-above %s%s", n, st.flags.Check, inCategory(st)),
			},
			line: fmt.Sprintf("check: %d open finding(s) at-or-above %s%s — gate fails", n, st.flags.Check, inCategory(st)),
		}
	}
	return gateCheck{
		dim: core.GateDimension{
			ID: dimFindings, Status: dimPass, Count: new(n),
			Detail: "no open findings at-or-above " + st.flags.Check + inCategory(st),
		},
		line: "check: no open findings at-or-above " + st.flags.Check + inCategory(st) + " — gate passes",
	}
}

// inCategory names the gated category for check lines, empty when the gate
// spans every category.
func inCategory(st *state) string {
	if st.flags.Category == "" {
		return ""
	}
	return " in " + st.flags.Category
}

func gateCompletenessCheck(st *state) gateCheck {
	short := readcommon.ShortSHA(st.headSHA)
	if st.compOK {
		return gateCheck{
			dim:  core.GateDimension{ID: dimHead, Status: dimPass, Detail: "HEAD " + short + " reviewed (post matches fetch)"},
			line: fmt.Sprintf("check: HEAD %s reviewed (post matches fetch) — completeness passes", short),
		}
	}
	if st.comp != nil && st.comp.SamePatch != nil && *st.comp.SamePatch {
		posted := readcommon.ShortSHA(st.compPosted)
		return gateCheck{
			dim:  core.GateDimension{ID: dimHead, Status: dimPass, Detail: "same patch reviewed at " + short + " (posted " + posted + ")"},
			line: fmt.Sprintf("check: same patch reviewed at %s (posted %s) — completeness passes (rebase/amend)", short, posted),
		}
	}
	return gateCheck{
		dim:  core.GateDimension{ID: dimHead, Status: completenessStatus(st), Detail: completenessDetail(st, short)},
		line: completenessFailLines(st)[0],
	}
}

// completenessStatus separates a stale post (a definite fail) from a missing
// or headless post record (unknown: nothing recorded the head).
func completenessStatus(st *state) string {
	if isRegular(st.dir+"/post.json") && st.compPosted != "" {
		return dimFail
	}
	return dimUnknown
}

func completenessDetail(st *state, short string) string {
	switch {
	case !isRegular(st.dir + "/post.json"):
		return "no completed post record for HEAD " + short
	case st.compPosted == "":
		return "post record carries no head SHA (fetched HEAD " + short + ")"
	default:
		return "posted HEAD " + readcommon.ShortSHA(st.compPosted) + " != fetched HEAD " + short
	}
}

func gateLiveCheck(st *state) gateCheck {
	switch st.live.status {
	case liveFail:
		return gateCheck{
			dim: liveDimension(st, dimFail),
			line: fmt.Sprintf("check: %d live open thread(s) vs %d local open finding(s) — gate fails "+
				"(fetch + triage them, then re-check)", st.live.open, st.nAllOpen),
		}
	case livePass:
		return gateCheck{
			dim: liveDimension(st, dimPass),
			line: fmt.Sprintf("check: %d live open thread(s) vs %d local open finding(s) — live recheck passes",
				st.live.open, st.nAllOpen),
		}
	case liveUnknownStrict:
		return gateCheck{
			dim: core.GateDimension{
				ID: dimLive, Status: dimUnknown,
				Detail: "live thread recheck unreachable (" + liveErrDetail(st) + ") — offline posture is strict",
			},
			line: fmt.Sprintf("check: live thread recheck unreachable (%s — gate fails: offline posture is strict; "+
				"restore the network or re-run with --live skip, then re-check)", liveErrDetail(st)),
		}
	case liveUnknown:
		return gateCheck{
			dim: core.GateDimension{
				ID: dimLive, Status: dimSkip,
				Detail: "live thread recheck unreachable — gating on local dimensions only",
			},
			line: "",
		}
	case liveSkipExplicit:
		return gateCheck{
			dim:  core.GateDimension{ID: dimLive, Status: dimSkip, Detail: "live recheck skipped (--live skip)"},
			line: "check: live recheck skipped (--live skip) — gating on local dimensions only",
		}
	default:
		return gateCheck{dim: core.GateDimension{
			ID: dimLive, Status: dimSkip,
			Detail: "live recheck skipped: local bundle threads unreadable here",
		}}
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

func completenessFailLines(st *state) []string {
	refetch := refetchCommand(st.dir, st.req.ReadRequest, st.compPosted)
	short := readcommon.ShortSHA(st.headSHA)
	switch {
	case !isRegular(st.dir + "/post.json"):
		return []string{fmt.Sprintf("check: incomplete — no completed post for HEAD %s "+
			"(missing-post: run %s, then review + post, then re-check)", short, refetch)}
	case st.compPosted == "":
		return []string{fmt.Sprintf("check: incomplete — post record carries no head SHA "+
			"(corrupt-post: re-run %s, then review + post, then re-check)", refetch)}
	default:
		return []string{fmt.Sprintf("check: incomplete — HEAD %s != posted %s "+
			"(stale-head: re-run %s, then review + post, then re-check)",
			short, readcommon.ShortSHA(st.compPosted), refetch)}
	}
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
