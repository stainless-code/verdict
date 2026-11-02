package trendcore

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

// Status and tier names, matching the shell's fixed-key rollup objects.
const (
	statusOpen          = "open"
	statusAddressed     = "addressed"
	statusWontFix       = "wont-fix"
	statusFalsePositive = "false-positive"
	statusDeferred      = "deferred"
	tierCritical        = "critical"
	tierImportant       = "important"
	tierInformational   = "informational"
	tierUntagged        = "untagged"
)

// rollup is the portfolio rollup over bundle rows.
type rollup struct {
	byStatus  map[string]int
	byTier    map[string]int
	tools     []any
	models    []any
	hashes    []any
	periods   []period
	bundles   int
	findings  int
	open      int
	addressed int
	closed    int
}

// period is one time-bucketed rollup point.
type period struct {
	byTier    map[string]int
	name      string
	bundles   int
	findings  int
	open      int
	addressed int
	closed    int
	hasName   bool
}

// rollupRows ports the rollup filter over bundle rows.
func rollupRows(rows []row) rollup {
	out := rollup{
		byStatus: map[string]int{
			statusOpen: 0, statusAddressed: 0, statusWontFix: 0, statusFalsePositive: 0, statusDeferred: 0,
		},
		byTier: map[string]int{
			tierCritical: 0, tierImportant: 0, tierInformational: 0, tierUntagged: 0,
		},
	}
	for i := range rows {
		r := &rows[i]
		out.bundles++
		out.findings += len(r.findings)
		out.open += r.byStatus[statusOpen]
		out.addressed += r.byStatus[statusAddressed]
		out.closed += r.byStatus[statusWontFix] + r.byStatus[statusFalsePositive] + r.byStatus[statusDeferred]
		for k, v := range r.byStatus {
			out.byStatus[k] += v
		}
		for k, v := range r.byTier {
			out.byTier[k] += v
		}
		out.tools = append(out.tools, provField(r.prov, "tool_version"))
		out.models = append(out.models, provField(r.prov, "model"))
		out.hashes = append(out.hashes, provField(r.prov, "prompts_hash"))
	}
	out.tools = dedupeSorted(out.tools)
	out.models = dedupeSorted(out.models)
	out.hashes = dedupeSorted(out.hashes)
	out.periods = periodize(rows)
	return out
}

// provField reads one provenance field (nil when absent).
func provField(prov *jq.Obj, key string) any {
	if prov == nil {
		return nil
	}
	v, _ := prov.Get(key)
	return v
}

// periodize ports the periods grouping: rows bucketed by at[0:10] (null
// stays null), nulls sorting last.
func periodize(rows []row) []period {
	sorted := append([]row{}, rows...)
	slices.SortStableFunc(sorted, func(a, b row) int {
		return periodCmp(periodName(&a), periodName(&b))
	})
	var periods []period
	for i := range sorted {
		r := &sorted[i]
		name := periodName(r)
		if len(periods) == 0 || periods[len(periods)-1].name != name || periods[len(periods)-1].hasName != r.hasAt {
			periods = append(periods, period{name: name, hasName: r.hasAt, byTier: map[string]int{
				tierCritical: 0, tierImportant: 0, tierInformational: 0, tierUntagged: 0,
			}})
		}
		last := &periods[len(periods)-1]
		last.bundles++
		last.findings += len(r.findings)
		last.open += r.byStatus[statusOpen]
		last.addressed += r.byStatus[statusAddressed]
		last.closed += r.byStatus[statusWontFix] + r.byStatus[statusFalsePositive] + r.byStatus[statusDeferred]
		for k, v := range r.byTier {
			last.byTier[k] += v
		}
	}
	return periods
}

// periodName ports `if .at == null then null else .at[0:10] end`.
func periodName(r *row) string {
	if !r.hasAt {
		return ""
	}
	return string(prefixRunes(r.at, periodRunes))
}

// periodRunes caps period names.
const periodRunes = 10

// prefixRunes takes the first n runes.
func prefixRunes(s string, n int) []rune {
	runes := []rune(s)
	if len(runes) > n {
		return runes[:n]
	}
	return runes
}

// periodCmp orders periods with nulls last (jq's sort_by with "~").
func periodCmp(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	return strings.Compare(a, b)
}

// rollupDoc renders the rollup as the envelope fragment.
func rollupDoc(out *rollup) *jq.Obj {
	periods := make([]any, 0, len(out.periods))
	for i := range out.periods {
		p := &out.periods[i]
		var name any
		if p.hasName {
			name = p.name
		}
		periods = append(periods, jq.NewObj(
			"period", name,
			"bundles", p.bundles,
			"findings", p.findings,
			"open", p.open,
			"addressed", p.addressed,
			"closed", p.closed,
			"by_tier", tierDoc(p.byTier),
		))
	}
	return jq.NewObj(
		"bundles", out.bundles,
		"findings", out.findings,
		"open", out.open,
		"addressed", out.addressed,
		"closed", out.closed,
		"by_status", jq.NewObj(
			statusOpen, out.byStatus[statusOpen],
			statusAddressed, out.byStatus[statusAddressed],
			statusWontFix, out.byStatus[statusWontFix],
			statusFalsePositive, out.byStatus[statusFalsePositive],
			statusDeferred, out.byStatus[statusDeferred],
		),
		"by_tier", tierDoc(out.byTier),
		"provenance", jq.NewObj(
			"tools", anyList(out.tools),
			"models", anyList(out.models),
			"prompts_hashes", anyList(out.hashes),
		),
		"periods", periods,
	)
}

// tierDoc renders a by_tier fragment in shell key order.
func tierDoc(counts map[string]int) *jq.Obj {
	return jq.NewObj(
		tierCritical, counts[tierCritical],
		tierImportant, counts[tierImportant],
		tierInformational, counts[tierInformational],
		tierUntagged, counts[tierUntagged],
	)
}

// anyList converts a value slice to the envelope list.
func anyList(vals []any) []any {
	if vals == nil {
		return []any{}
	}
	return vals
}

// rowDoc renders one per-bundle row in shell key order.
func rowDoc(r *row) *jq.Obj {
	var at any
	if r.hasAt {
		at = r.at
	}
	return jq.NewObj(
		"bundle", r.bundle,
		"at", at,
		"models", anyList(r.models),
		"efforts", anyList(r.efforts),
		"provenance", r.prov,
		"findings", len(r.findings),
		"open", r.byStatus[statusOpen],
		"addressed", r.byStatus[statusAddressed],
		"closed", r.byStatus[statusWontFix]+r.byStatus[statusFalsePositive]+r.byStatus[statusDeferred],
		"by_status", jq.NewObj(
			statusOpen, r.byStatus[statusOpen],
			statusAddressed, r.byStatus[statusAddressed],
			statusWontFix, r.byStatus[statusWontFix],
			statusFalsePositive, r.byStatus[statusFalsePositive],
			statusDeferred, r.byStatus[statusDeferred],
		),
		"by_tier", tierDoc(r.byTier),
	)
}

// emitHuman ports the human rollup lines.
func (st *state) emitHuman(out *rollup, overlap *overlapRollup) {
	if out.bundles == 0 {
		st.human("trend: " + st.req.Repo + " — no reviewed bundles (no .review/*/findings.json yet)")
	} else {
		st.human(fmt.Sprintf("trend: %s — %d bundle(s), %d findings (open=%d addressed=%d closed=%d)",
			st.req.Repo, out.bundles, out.findings, out.open, out.addressed, out.closed))
		for i := range out.periods {
			p := &out.periods[i]
			name := "undated"
			if p.hasName {
				name = p.name
			}
			st.human(fmt.Sprintf("%s  bundles=%d findings=%d open=%d addressed=%d closed=%d "+
				"tiers critical=%d important=%d informational=%d untagged=%d",
				name, p.bundles, p.findings, p.open, p.addressed, p.closed,
				p.byTier[tierCritical], p.byTier[tierImportant], p.byTier[tierInformational], p.byTier[tierUntagged]))
		}
		st.human(fmt.Sprintf("tiers: critical=%d important=%d informational=%d untagged=%d",
			out.byTier[tierCritical], out.byTier[tierImportant], out.byTier[tierInformational], out.byTier[tierUntagged]))
		st.human(fmt.Sprintf("status: open=%d addressed=%d wont-fix=%d false-positive=%d deferred=%d",
			out.byStatus[statusOpen], out.byStatus[statusAddressed], out.byStatus[statusWontFix],
			out.byStatus[statusFalsePositive], out.byStatus[statusDeferred]))
		st.human("provenance: " + provenanceLine(out))
	}
	if overlap != nil {
		st.emitOverlapHuman(overlap)
	}
}

// provenanceLine ports the provenance summary line.
func provenanceLine(out *rollup) string {
	tools := unmarkedList(out.tools)
	models := unmarkedList(out.models)
	hashes := unmarkedList(out.hashes)
	unique := 0
	unmarked := false
	seen := map[string]bool{}
	for _, h := range out.hashes {
		if h == nil {
			unmarked = true
			continue
		}
		key := jq.Compact(h)
		if !seen[key] {
			seen[key] = true
			unique++
		}
	}
	suffix := ""
	if unmarked {
		suffix = " (+unmarked)"
	}
	_ = hashes
	return "tools=" + strings.Join(tools, ",") + " models=" + strings.Join(models, ",") +
		" prompts=" + strconv.Itoa(unique) + " unique" + suffix
}

// unmarkedList ports `[(.[] | (. // "unmarked"))] | unique | join(",")`.
func unmarkedList(vals []any) []string {
	mapped := make([]string, 0, len(vals))
	for _, v := range vals {
		mapped = append(mapped, unmarkedValue(v))
	}
	slices.Sort(mapped)
	return uniqueStrings(mapped)
}

// unmarkedValue ports `(. // "unmarked")`.
func unmarkedValue(v any) string {
	if v == nil || v == false {
		return "unmarked"
	}
	return jq.Str(v)
}

// uniqueStrings ports `unique` over a sorted list.
func uniqueStrings(sorted []string) []string {
	var out []string
	for i, s := range sorted {
		if i == 0 || sorted[i-1] != s {
			out = append(out, s)
		}
	}
	return out
}
