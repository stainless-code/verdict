package dashboard

import (
	"slices"
	"strconv"

	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/jq"
)

// target names the bundle the Detail, Diff, and Compare tabs show.
type target struct {
	root, bundle string
}

// detailData is the Detail tab: one bundle's verdict, gate, and findings.
type detailData struct {
	gatePassed *bool
	bundle     string
	repo       string
	head       string
	dims       []gateDim
	items      []finding
	verdict    [4]int // open, deferred, addressed, terminal
}

type gateDim struct {
	id, status, detail string
}

// finding is one Detail row. quote is the stored quote check (verified,
// unverified, or unchecked): the second evidence axis beside the rigor rung.
type finding struct {
	sev, status, rigor, quote, locator, body, sig string
}

// diffData is the Diff tab: the lanes in risk order and every file's hunks.
type diffData struct {
	lanes []laneRow
	files []diffFile
}

type laneRow struct {
	lane                        string
	files, adds, dels, findings int
}

type diffFile struct {
	path, lane string
	hunks      []diffHunk
	adds, dels int
	binary     bool
}

type diffHunk struct {
	header string
	lines  []diffLine
}

// diffLine is one diff row; sev is the strongest severity anchored on it.
type diffLine struct {
	kind, text, sev string
	oldNo, newNo    int
}

// verdictKeys are a report verdict's four status counts, in wire order.
var verdictKeys = [...]string{statusOpen, "deferred", "addressed", "terminal"}

// compareBuckets are the four buckets a compare publishes, in display order.
var compareBuckets = [...]string{"new", "persisting", "resolved", "not_reviewed"}

// compareData is the Compare tab against one baseline bundle.
type compareData struct {
	baseline string
	buckets  [len(compareBuckets)][]compareRow
	found    int
}

type compareRow struct {
	sev, status, path, claim string
	line                     int
}

// tableData is a Cost or Precision table, already rendered to cells.
type tableData struct {
	by     string
	note   string
	header []string
	rows   [][]string
	totals []string
}

func decodeDetail(v *query.View) *detailData {
	report := asObj(field(v.Data, "report"))
	d := &detailData{bundle: v.Pr, repo: v.Repo, head: strField(asObj(field(report, "head")), "fetched")}
	verdict := asObj(field(report, "verdict"))
	for i, key := range verdictKeys {
		d.verdict[i] = intField(verdict, key)
	}
	if gate := asObj(field(v.Data, "gate")); gate != nil {
		passed := truthy(field(gate, "passed"))
		d.gatePassed = &passed
		for _, e := range jq.Elements(field(gate, "dimensions")) {
			o := asObj(e)
			d.dims = append(d.dims, gateDim{id: strField(o, "id"), status: strField(o, "status"), detail: strField(o, "detail")})
		}
	}
	for _, e := range jq.Elements(field(report, "items")) {
		o := asObj(e)
		quote := strField(asObj(field(o, "verify")), "quote")
		d.items = append(d.items, finding{
			sev: strField(o, "severity"), status: strField(o, "status"), rigor: strField(o, "rigor"),
			quote: quote, locator: strField(o, "locator"), body: strField(o, "body"), sig: strField(o, "sig"),
		})
	}
	return d
}

func decodeDiff(v *query.View) *diffData {
	d := &diffData{}
	for _, e := range jq.Elements(field(v.Data, "lanes")) {
		o := asObj(e)
		d.lanes = append(d.lanes, laneRow{
			lane: strField(o, "lane"), files: intField(o, "file_count"), adds: intField(o, "additions"),
			dels: intField(o, "deletions"), findings: intField(o, "findings"),
		})
	}
	for _, e := range jq.Elements(field(v.Data, "files")) {
		o := asObj(e)
		f := diffFile{
			path: strField(o, "path"), lane: strField(o, "lane"), adds: intField(o, "additions"),
			dels: intField(o, "deletions"), binary: truthy(field(o, "is_binary")),
		}
		for _, h := range jq.Elements(field(o, "hunks")) {
			f.hunks = append(f.hunks, decodeHunk(asObj(h)))
		}
		d.files = append(d.files, f)
	}
	return d
}

func decodeHunk(o *jq.Obj) diffHunk {
	h := diffHunk{header: strField(o, "header")}
	for _, e := range jq.Elements(field(o, "lines")) {
		l := asObj(e)
		line := diffLine{
			kind: strField(l, "kind"), text: strField(l, "text"),
			oldNo: intField(l, "old_no"), newNo: intField(l, "new_no"),
		}
		for _, f := range jq.Elements(field(l, "findings")) {
			line.sev = strongerSeverity(line.sev, strField(asObj(f), "severity"))
		}
		h.lines = append(h.lines, line)
	}
	return h
}

func decodeCompare(v *query.View) *compareData {
	cmp := asObj(field(v.Data, "compare"))
	base := asObj(field(cmp, "baseline"))
	d := &compareData{baseline: strField(base, "name"), found: intField(base, "findings")}
	for i, bucket := range compareBuckets {
		for _, e := range jq.Elements(field(cmp, bucket)) {
			o := asObj(e)
			d.buckets[i] = append(d.buckets[i], compareRow{
				sev: strField(o, "severity"), status: strField(o, "status"), path: strField(o, "path"),
				line: intField(o, "line"), claim: strField(o, "claim"),
			})
		}
	}
	return d
}

// floatBits is the float64 precision a cost renders from.
const floatBits = 64

// costColumns are the Cost table's numeric columns, in wire order.
var costColumns = [...]string{"runs", "model_runs", "input_tokens", "output_tokens", "total_tokens", "est_usd"}

func decodeCost(v *query.View) *tableData {
	cost := asObj(field(v.Data, "cost"))
	t := &tableData{
		by:     strField(cost, "by"),
		header: []string{"RUNS", "MODELS", "IN TOK", "OUT TOK", "TOTAL", "USD"},
		note:   v.Next,
	}
	for _, e := range jq.Elements(field(cost, "rows")) {
		o := asObj(e)
		t.rows = append(t.rows, append([]string{strField(o, "dimension")}, cells(o, costColumns[:])...))
	}
	t.totals = append([]string{"total"}, cells(asObj(field(cost, "totals")), costColumns[:])...)
	return t
}

// precisionColumns are the Precision table's outcome columns, in wire order.
var precisionColumns = [...]string{"open", "resolved_unfixed", "unknown", "fixed"}

func decodePrecision(v *query.View) *tableData {
	p := asObj(field(v.Data, "precision"))
	t := &tableData{
		by:     strField(p, "by"),
		header: []string{"OPEN", "UNFIXED", "UNKNOWN", "FIXED"},
		note:   strField(p, "next"),
	}
	for _, e := range jq.Elements(field(p, "outcomes")) {
		o := asObj(e)
		t.rows = append(t.rows, append([]string{strField(o, "dimension")}, cells(o, precisionColumns[:])...))
	}
	t.totals = append([]string{"total"}, cells(asObj(field(p, "totals")), precisionColumns[:])...)
	if asOf := strField(p, "as_of"); asOf != "" {
		t.note = "as of " + asOf + "; " + t.note
	}
	return t
}

// cells renders numeric fields; a null (a quantity the layer cannot
// measure, such as fixed) stays empty and draws as the unknown glyph, never 0.
func cells(o *jq.Obj, keys []string) []string {
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i] = number(field(o, key))
	}
	return out
}

func number(v any) string {
	if n, ok := intOf(v); ok {
		if f, isFloat := v.(float64); isFloat && f != float64(n) {
			return strconv.FormatFloat(f, 'f', 2, floatBits)
		}
		return strconv.Itoa(n)
	}
	switch f := v.(type) {
	case float64:
		return strconv.FormatFloat(f, 'f', 2, floatBits)
	case nil:
		return ""
	default:
		return jq.Str(f)
	}
}

// copyText is what `y` puts on the clipboard and what to call it: the fix
// prompt when the layer assembled one, else the triage command.
func copyText(v *query.View) (text, what string) {
	if prompt := strField(v.Data, "prompt"); prompt != "" {
		return prompt, "fix prompt"
	}
	return strField(v.Data, "triage"), "triage command"
}

// filterRepo narrows a snapshot to one `owner/repo`. The totals are summed
// over the kept rows, or withheld when one of them refused, the same rule
// the layer applies to the whole portfolio.
func filterRepo(s *snapshot, repo string) *snapshot {
	if repo == "" {
		return s
	}
	out := *s
	out.rows = slices.DeleteFunc(slices.Clone(s.rows), func(r bundleRow) bool { return r.repo != repo })
	out.counts = sumRows(out.rows)
	if s.truncated || refusedUnattributed(s.rows) {
		out.counts = nil // a cut listing, or a refused bundle of unknown repo, may be this repo's
	}
	kept := map[string]bool{}
	for _, r := range out.rows {
		kept[r.bundle] = true
	}
	out.refused = slices.DeleteFunc(slices.Clone(s.refused), func(f refusedRow) bool { return f.bundle != "" && !kept[f.bundle] })
	return &out
}

// sumRows totals the rows, or nil when one of them refused.
func sumRows(rows []bundleRow) *counts {
	sum := &counts{}
	for _, r := range rows {
		if r.refused {
			return nil
		}
		sum.bundles++
		sum.open, sum.deferred = sum.open+r.open, sum.deferred+r.deferred
		sum.addressed, sum.terminal = sum.addressed+r.addressed, sum.terminal+r.terminal
	}
	return sum
}

// refusedUnattributed reports a refused bundle whose repository the layer
// could not read: it might belong to any repo, so no per-repo sum is whole.
func refusedUnattributed(rows []bundleRow) bool {
	return slices.ContainsFunc(rows, func(r bundleRow) bool { return r.refused && r.repo == "" })
}
