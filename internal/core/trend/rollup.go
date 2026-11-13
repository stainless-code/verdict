package trendcore

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

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

type rollup struct {
	withheld   map[string]int
	byStatus   map[string]int
	byTier     map[string]int
	byCategory map[string]int
	tools      []any
	models     []any
	hashes     []any
	periods    []period
	clean      int
	bundles    int
	findings   int
	open       int
	addressed  int
	closed     int
}

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

func rollupRows(rows []row) rollup {
	out := rollup{
		withheld: readcommon.WithheldCounts(nil),
		byStatus: map[string]int{
			statusOpen: 0, statusAddressed: 0, statusWontFix: 0, statusFalsePositive: 0, statusDeferred: 0,
		},
		byTier: map[string]int{
			tierCritical: 0, tierImportant: 0, tierInformational: 0, tierUntagged: 0,
		},
		byCategory: map[string]int{},
	}
	for i := range rows {
		r := &rows[i]
		addWithheld(out.withheld, r.findings)
		out.bundles++
		out.clean += cleanCount(r)
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
		for k, v := range r.byCategory {
			out.byCategory[k] += v
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

func provField(prov *jq.Obj, key string) any {
	if prov == nil {
		return nil
	}
	v, _ := prov.Get(key)
	return v
}

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

func periodName(r *row) string {
	if !r.hasAt {
		return ""
	}
	return string(prefixRunes(r.at, periodRunes))
}

const periodRunes = 10

func prefixRunes(s string, n int) []rune {
	runes := []rune(s)
	if len(runes) > n {
		return runes[:n]
	}
	return runes
}

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
		"reviews", out.bundles,
		"reviews_with_findings", out.bundles-out.clean,
		"reviews_clean", out.clean,
		"findings_per_review", rateOrNil(out.findings, out.bundles),
		"clean_rate", rateOrNil(out.clean, out.bundles),
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
		"by_category", categoryDoc(out.byCategory),
		"withheld", out.withheld,
		"provenance", jq.NewObj(
			"tools", anyList(out.tools),
			"models", anyList(out.models),
			"prompts_hashes", anyList(out.hashes),
		),
		"periods", periods,
	)
}

func tierDoc(counts map[string]int) *jq.Obj {
	return jq.NewObj(
		tierCritical, counts[tierCritical],
		tierImportant, counts[tierImportant],
		tierInformational, counts[tierInformational],
		tierUntagged, counts[tierUntagged],
	)
}

// categoryDoc renders one sparse category tally in sorted key order: only
// observed categories appear, with untagged for null entries.
func categoryDoc(counts map[string]int) *jq.Obj {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	o := jq.NewObj()
	for _, k := range keys {
		o.Set(k, counts[k])
	}
	return o
}

func anyList(vals []any) []any {
	if vals == nil {
		return []any{}
	}
	return vals
}

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
		"clean", len(r.findings) == 0,
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
		"by_category", categoryDoc(r.byCategory),
		"withheld", readcommon.WithheldCounts(r.findings),
	)
}

func (st *state) emitHuman(out *rollup, overlap *overlapRollup, corpus *corpusRollup) {
	st.emitRollupHuman(out)
	if overlap != nil {
		st.emitOverlapHuman(overlap)
	}
	if corpus != nil {
		st.emitCorpusHuman(corpus)
	}
}

func (st *state) emitRollupHuman(out *rollup) {
	if withheldTotal(out.withheld) > 0 {
		st.human(fmt.Sprintf("withheld: exclusion=%d agent=%d below-severity=%d validation=%d",
			out.withheld["exclusion"], out.withheld["agent"], out.withheld["below-severity"], out.withheld["validation"]))
	}
	if out.bundles == 0 {
		st.human("trend: " + st.req.Repo + " — no reviewed bundles (no .verdict/state/*/findings.json yet)")
	} else {
		st.human(fmt.Sprintf("trend: %s — %d bundle(s), %d findings (open=%d addressed=%d closed=%d)",
			st.req.Repo, out.bundles, out.findings, out.open, out.addressed, out.closed))
		st.emitReviewHuman(out)
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
}

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

func unmarkedList(vals []any) []string {
	mapped := make([]string, 0, len(vals))
	for _, v := range vals {
		mapped = append(mapped, unmarkedValue(v))
	}
	slices.Sort(mapped)
	return uniqueStrings(mapped)
}

func unmarkedValue(v any) string {
	if v == nil || v == false {
		return "unmarked"
	}
	return jq.Str(v)
}

func uniqueStrings(sorted []string) []string {
	var out []string
	for i, s := range sorted {
		if i == 0 || sorted[i-1] != s {
			out = append(out, s)
		}
	}
	return out
}

func rateOrNil(n, d int) any {
	if d == 0 {
		return nil
	}
	return math.Round(float64(n)/float64(d)*rateScale) / rateScale
}

const (
	rateScale    = 10000
	percentScale = 100
)

func cleanCount(r *row) int {
	if len(r.findings) == 0 {
		return 1
	}
	return 0
}

func (st *state) emitReviewHuman(out *rollup) {
	if out.bundles > 0 {
		st.human(fmt.Sprintf("reviews: %d (%d clean) — findings/review %.4f, clean rate %.1f%% (n=%d)", out.bundles, out.clean,
			rateOrNil(out.findings, out.bundles), percentScale*float64(out.clean)/float64(out.bundles), out.bundles))
	}
}

func withheldTotal(counts map[string]int) int {
	n := 0
	for _, v := range counts {
		n += v
	}
	return n
}

func addWithheld(counts map[string]int, entries []any) {
	for source, n := range readcommon.WithheldCounts(entries) {
		counts[source] += n
	}
}
