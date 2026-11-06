package reportcore

import (
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
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
	if st.nOpen > 0 {
		return "address open threads"
	}
	return "none open — nothing to address"
}

func buildData(st *state) *core.ReportData {
	data := &core.ReportData{
		GeneratedAt: st.now,
		Dir:         st.dir,
		Head: core.ReportHead{
			Fetched: new(st.headSHA),
			Base:    new(st.baseSHA),
			Live:    nil,
			Stale:   false,
		},
		Verdict: core.ReportVerdict{
			Open:      countStatus(rankedEntries(st), statusOpen),
			Deferred:  countStatus(rankedEntries(st), "deferred"),
			Addressed: countStatus(rankedEntries(st), "addressed"),
			Terminal:  countTerminal(rankedEntries(st)),
		},
		Counts:  st.counts,
		Panel:   panelValue(st),
		Patch:   st.patch,
		Output:  outputVal(st.flags.Output),
		Filters: core.ReportFilters{Status: st.flags.Status, Kind: st.flags.Kind, Category: outputVal(st.flags.Category)},
		Items:   envelopeItems(st.ranked),
	}
	if st.cost != nil {
		data.Cost = costFromObj(st.cost)
		data.CostRaw = st.cost
	}
	if st.comp != nil {
		data.Completeness = st.comp
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

func rankedEntries(st *state) []any {
	entries := make([]any, 0, len(st.ranked))
	for _, item := range st.ranked {
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
	lines := []string{fmt.Sprintf("report: %s (%d shown) open=%d head=%s",
		st.dir, len(st.ranked), st.nOpen, readcommon.ShortSHA(st.headSHA))}
	for _, item := range st.ranked {
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
	if st.flags.Output != "" {
		return append(lines, "wrote "+st.flags.Output)
	}
	if st.nOpen > 0 {
		return append(lines, "next: address open threads")
	}
	return append(lines, "next: none open — nothing to address")
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
	dimFindings = "findings"
	dimHead     = "head"
	dimLive     = "live_threads"
	dimPass     = "pass"
	dimFail     = "fail"
	dimSkip     = "skip"
	dimUnknown  = "unknown"
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

// gateDimensions is the ceiling on one run's gate checks: findings, head, live.
const gateDimensions = 3

// gateChecks evaluates the dimensions this run asked for, in wire order.
func gateChecks(st *state) []gateCheck {
	checks := make([]gateCheck, 0, gateDimensions)
	if st.flags.Check != "" {
		checks = append(checks, gateTierCheck(st))
	}
	if st.flags.Completeness {
		checks = append(checks, gateCompletenessCheck(st))
	}
	return append(checks, gateLiveCheck(st))
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
		return gateCheck{dim: core.GateDimension{ID: dimLive, Status: dimSkip, Detail: "live recheck skipped: " + liveSkipReason(st)}}
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

// liveSkipReason names why the probe could not read live threads at all.
func liveSkipReason(st *state) string {
	if isLocalDir(st.dir) {
		return "local bundle threads unreadable here"
	}
	return providerName(st.dir) + " threads unreadable here"
}

func tierHits(entries []any, tier, category string) int {
	threshold := map[string]int{tierCritical: 0, tierImportant: 1, tierInformational: 2}[tier]
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
	if sevRankOf(e) > threshold {
		return false
	}
	return category == "" || gateEligible(e)
}

// The recorded evidence rungs, spelled once for the gate and the Basis
// display: every published rung counts, said included — the gate sets no
// minimum rung. Missing, non-string, or unknown rigor never gates.
const (
	rungSaid       = "said"
	rungPointed    = "pointed"
	rungWalked     = "walked"
	rungRan        = "ran"
	rungReproduced = "reproduced"
)

// gateRungsList is the eligibility set the category gate checks.
var gateRungsList = []string{rungSaid, rungPointed, rungWalked, rungRan, rungReproduced}

// gateEligible reports whether one category-matched entry carries the anchor
// and rigor the gate still requires: an inline anchor plus a recorded rung.
func gateEligible(e any) bool {
	if !inlineAnchored(e, true) {
		return false
	}
	m, ok := e.(*jq.Obj)
	if !ok || m == nil {
		return false
	}
	rigor, _ := m.Get("rigor")
	s, ok := rigor.(string)
	return ok && slices.Contains(gateRungsList, s)
}

const sevRankUntagged = 3

func sevRankOf(entry any) int {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return sevRankUntagged
	}
	sev, _ := m.Get("severity")
	if s, ok := sev.(string); ok {
		switch s {
		case tierCritical:
			return 0
		case tierImportant:
			return 1
		case tierInformational:
			return 2
		}
	}
	return sevRankUntagged
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
