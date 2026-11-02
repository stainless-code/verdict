package trend

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
)

// rollup is the portfolio rollup over bundle rows.
type rollup struct {
	bundles   int
	findings  int
	open      int
	addressed int
	closed    int
	byStatus  map[string]int
	byTier    map[string]int
	tools     []any
	models    []any
	hashes    []any
	periods   []period
}

// period is one time-bucketed rollup point.
type period struct {
	name      string
	hasName   bool
	bundles   int
	findings  int
	open      int
	addressed int
	closed    int
	byTier    map[string]int
}

// rollupRows ports the rollup filter over bundle rows.
func rollupRows(rows []row) rollup {
	out := rollup{
		byStatus: map[string]int{"open": 0, "addressed": 0, "wont-fix": 0, "false-positive": 0, "deferred": 0},
		byTier:   map[string]int{"critical": 0, "important": 0, "informational": 0, "untagged": 0},
	}
	for _, r := range rows {
		out.bundles++
		out.findings += len(r.findings)
		out.open += r.byStatus["open"]
		out.addressed += r.byStatus["addressed"]
		out.closed += r.byStatus["wont-fix"] + r.byStatus["false-positive"] + r.byStatus["deferred"]
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
		return periodCmp(periodName(a), periodName(b))
	})
	var periods []period
	for _, r := range sorted {
		name := periodName(r)
		if len(periods) == 0 || periods[len(periods)-1].name != name || periods[len(periods)-1].hasName != r.hasAt {
			periods = append(periods, period{name: name, hasName: r.hasAt, byTier: map[string]int{
				"critical": 0, "important": 0, "informational": 0, "untagged": 0,
			}})
		}
		last := &periods[len(periods)-1]
		last.bundles++
		last.findings += len(r.findings)
		last.open += r.byStatus["open"]
		last.addressed += r.byStatus["addressed"]
		last.closed += r.byStatus["wont-fix"] + r.byStatus["false-positive"] + r.byStatus["deferred"]
		for k, v := range r.byTier {
			last.byTier[k] += v
		}
	}
	return periods
}

// periodName ports `if .at == null then null else .at[0:10] end`.
func periodName(r row) string {
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
func rollupDoc(out rollup) *jq.Obj {
	periods := make([]any, 0, len(out.periods))
	for _, p := range out.periods {
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
			"open", out.byStatus["open"],
			"addressed", out.byStatus["addressed"],
			"wont-fix", out.byStatus["wont-fix"],
			"false-positive", out.byStatus["false-positive"],
			"deferred", out.byStatus["deferred"],
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
		"critical", counts["critical"],
		"important", counts["important"],
		"informational", counts["informational"],
		"untagged", counts["untagged"],
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
func rowDoc(r row) *jq.Obj {
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
		"open", r.byStatus["open"],
		"addressed", r.byStatus["addressed"],
		"closed", r.byStatus["wont-fix"]+r.byStatus["false-positive"]+r.byStatus["deferred"],
		"by_status", jq.NewObj(
			"open", r.byStatus["open"],
			"addressed", r.byStatus["addressed"],
			"wont-fix", r.byStatus["wont-fix"],
			"false-positive", r.byStatus["false-positive"],
			"deferred", r.byStatus["deferred"],
		),
		"by_tier", tierDoc(r.byTier),
	)
}

// emitHuman ports the human rollup lines.
func emitHuman(r *cli.Runner, req Request, out rollup, overlapVal any) {
	if out.bundles == 0 {
		fmt.Fprintf(r.Human, "trend: %s — no reviewed bundles (no .review/*/findings.json yet)\n", req.Repo)
	} else {
		fmt.Fprintf(r.Human, "trend: %s — %d bundle(s), %d findings (open=%d addressed=%d closed=%d)\n",
			req.Repo, out.bundles, out.findings, out.open, out.addressed, out.closed)
		for _, p := range out.periods {
			name := "undated"
			if p.hasName {
				name = p.name
			}
			fmt.Fprintf(r.Human, "%s  bundles=%d findings=%d open=%d addressed=%d closed=%d tiers critical=%d important=%d informational=%d untagged=%d\n",
				name, p.bundles, p.findings, p.open, p.addressed, p.closed,
				p.byTier["critical"], p.byTier["important"], p.byTier["informational"], p.byTier["untagged"])
		}
		fmt.Fprintf(r.Human, "tiers: critical=%d important=%d informational=%d untagged=%d\n",
			out.byTier["critical"], out.byTier["important"], out.byTier["informational"], out.byTier["untagged"])
		fmt.Fprintf(r.Human, "status: open=%d addressed=%d wont-fix=%d false-positive=%d deferred=%d\n",
			out.byStatus["open"], out.byStatus["addressed"], out.byStatus["wont-fix"], out.byStatus["false-positive"], out.byStatus["deferred"])
		fmt.Fprintf(r.Human, "provenance: %s\n", provenanceLine(out))
	}
	if overlapVal != nil {
		emitOverlapHuman(r, overlapVal.(*overlapRollup))
	}
}

// provenanceLine ports the provenance summary line.
func provenanceLine(out rollup) string {
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
	var mapped []string
	for _, v := range vals {
		if v == nil || v == false {
			mapped = append(mapped, "unmarked")
			continue
		}
		mapped = append(mapped, jq.Str(v))
	}
	slices.Sort(mapped)
	out := mapped[:0]
	for i, s := range mapped {
		if i == 0 || mapped[i-1] != s {
			out = append(out, s)
		}
	}
	return out
}

// emitEnvelope ports the trend --json envelope: generated_at first, then
// the rollup keys, per_bundle, and the optional overlap join.
func emitEnvelope(r *cli.Runner, req Request, now string, out rollup, rows []row, overlapVal any) {
	perBundle := make([]any, 0, len(rows))
	for _, row := range rows {
		perBundle = append(perBundle, rowDoc(row))
	}
	middle := rollupDoc(out)
	data := jq.NewObj("generated_at", now)
	for _, k := range middle.Keys {
		v, _ := middle.Get(k)
		data.Set(k, v)
	}
	data.Set("per_bundle", perBundle)
	if overlapVal != nil {
		data.Set("overlap", overlapDoc(overlapVal.(*overlapRollup)))
	}
	next := "calibrate recent bundles for per-model tier precision"
	if out.bundles == 0 {
		next = "no reviewed bundles yet — fetch + post/record first"
	}
	env := jq.NewObj(
		"schema_version", "1",
		"verb", "trend",
		"repo", req.Repo,
		"pr", nil,
		"at", now,
		"next", next,
		"warnings", cliStrings(r.Warnings),
		"data", data,
		"error", nil,
	)
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
}

// cliStrings converts warnings to JSON string values.
func cliStrings(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}
