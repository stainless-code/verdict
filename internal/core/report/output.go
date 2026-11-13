package reportcore

import (
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/overrides"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func outputArtifact(st *state) *core.FileArtifact {
	if st.flags.Output == "" {
		return nil
	}
	return &core.FileArtifact{Path: st.flags.Output, Content: []byte(buildDocument(st) + newline)}
}

func buildDocument(st *state) string {
	lines := []string{
		"# Review report — " + st.req.Repo + "#" + st.req.Pr,
		"",
		"<!-- report-machine:start -->",
		confidenceLead(st),
		basisOutputLine(st.entries),
		"## Counts — open " + strconv.Itoa(st.nAllOpen) + " · head " + readcommon.ShortSHA(st.headSHA),
	}
	if sources := readcommon.ContextSources(st.dir); len(sources) > 0 {
		lines = append(lines, "Context used: "+strings.Join(sources, ", "))
	}
	for _, item := range viewRanked(st) {
		if line := readcommon.SourcesLine(item.Entry); line != "" {
			lines = append(lines, line)
		}
	}
	lines = append(lines, rankedLines(st)...)
	lines = append(lines, quietLines(st)...)
	lines = append(lines, linkDefs(st)...)
	if overview := renderOverview(st); overview != "" {
		lines = append(lines, overviewLines(overview)...)
	}
	lines = append(lines, checkedBlock(st)...)
	if footer := renderFooter(st); footer != "" {
		lines = append(lines, overviewLines(footer)...)
	}
	lines = append(lines, "<!-- report-machine:end -->")
	return strings.Join(lines, newline)
}

func overviewLines(block string) []string {
	return strings.Split(block, newline)
}

func confidenceLead(st *state) string {
	rec, ok := firstLeadRecord(st.dir)
	if !ok {
		return "> Confidence: unrecorded · Safety: not assessed"
	}
	conf, sec := leadValues(rec)
	return "> Confidence: " + conf + " · Safety: " + sec
}

func firstLeadRecord(dir string) (string, bool) {
	for _, rec := range []string{dir + "/post.json", dir + "/record.json"} {
		if isRegular(rec) {
			return rec, true
		}
	}
	return "", false
}

func leadValues(rec string) (string, string) {
	conf, sec := "unrecorded", "not assessed"
	if c := jsonRaw(rec, "confidence"); c == "1" || c == "2" || c == "3" || c == "4" || c == "5" {
		conf = c + "/5"
	}
	if s := jsonRaw(rec, "security_summary"); s != "" {
		sec = s
	}
	return conf, sec
}

func jsonRaw(path, field string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	v, ok := m[field]
	if !ok || v == nil {
		return ""
	}
	return jq.R(v)
}

func basisOutputLine(entries []any) string {
	return "> Basis: " + basisCounts(entries)
}

func basisCounts(entries []any) string {
	counts := map[string]int{}
	var cited []any
	verified := 0
	for _, e := range entries {
		counts[basisRigor(e)]++
		cited = collectCitedIDs(cited, e)
		if readcommon.StatusOf(e) == "addressed" {
			verified++
		}
	}
	counted := make([]string, 0, len(core.RungValues))
	for _, rung := range core.RungValues {
		counted = append(counted, strconv.Itoa(counts[rung])+" "+rung)
	}
	rules := strconv.Itoa(len(dedupeValues(cited)))
	fixes := strconv.Itoa(verified)
	return strings.Join(counted, " · ") + " · " + rules + " rules cited · " +
		fixes + " verified fixes"
}

func basisRigor(entry any) string {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return core.RungPointed
	}
	rigor, _ := m.Get("rigor")
	if rigor == nil || rigor == false {
		return core.RungPointed
	}
	if s, ok := rigor.(string); ok {
		return s
	}
	return ""
}

func collectCitedIDs(cited []any, entry any) []any {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return cited
	}
	kb, _ := m.Get("kb")
	list, ok := kb.([]any)
	if !ok {
		return cited
	}
	return append(cited, list...)
}

// dedupeValues dedupes values in jq order (unique).
func dedupeValues(vals []any) []any {
	sorted := append([]any{}, vals...)
	slices.SortStableFunc(sorted, readcommon.Cmp)
	out := sorted[:0]
	for i, v := range sorted {
		if i == 0 || readcommon.Cmp(sorted[i-1], v) != 0 {
			out = append(out, v)
		}
	}
	return out
}

func rankedLines(st *state) []string {
	linkable := provenance.EvidenceLinkable(st.dir, st.headSHA) == strTrue
	view := viewRanked(st)
	lines := make([]string, 0, len(view))
	for _, item := range view {
		lines = append(lines, rankedLine(st, item, linkable))
	}
	return lines
}

func rankedLine(_ *state, item readcommon.Ranked, linkable bool) string {
	locator := readcommon.Locator(item.Entry)
	if linkable && readcommon.FieldOf(item.Entry, "path") != nil && readcommon.FieldOf(item.Entry, "line") != nil {
		locator = "[" + locator + "][ev-" + strconv.Itoa(item.Rank) + "]"
	}
	return strconv.Itoa(item.Rank) + ". [" + jq.Str(readcommon.FieldOf(item.Entry, "status")) + "] " + locator +
		" (" + sigOutput(item.Entry) + ", " + rigorOutput(item.Entry) + ") — " + bodyOutput(item.Entry)
}

func sigOutput(entry any) string {
	if sig, ok := readcommon.FieldOf(entry, "sig").(string); ok {
		return readcommon.Prefix(sig)
	}
	return jq.Str(readcommon.FieldOf(entry, "sig"))
}

func rigorOutput(entry any) string {
	rigor := readcommon.FieldOf(entry, "rigor")
	if rigor == nil || rigor == false {
		return "?"
	}
	return jq.Str(rigor)
}

func bodyOutput(entry any) string {
	body, ok := readcommon.FieldOf(entry, "body").(string)
	if !ok {
		body = ""
	}
	body = strings.ReplaceAll(body, newline, " ")
	return readcommon.PrefixN(body, bodyRunes)
}

const bodyRunes = 200

func linkDefs(st *state) []string {
	if provenance.EvidenceLinkable(st.dir, st.headSHA) != strTrue {
		return nil
	}
	base := provenance.EvidenceBaseURL(st.dir, st.req.Repo)
	var defs []string
	for _, item := range viewRanked(st) {
		path := readcommon.FieldOf(item.Entry, "path")
		line := readcommon.FieldOf(item.Entry, "line")
		if path == nil || line == nil {
			continue
		}
		defs = append(defs, "[ev-"+strconv.Itoa(item.Rank)+"]: "+base+"/blob/"+st.headSHA+"/"+jq.Str(path)+"#L"+jq.Str(line))
	}
	return defs
}

func checkedBlock(st *state) []string {
	if st.nAllOpen != 0 {
		return nil
	}
	files := checkedFiles(st.dir)
	if files == "" {
		return nil
	}
	rawLines := strings.Split(files, newline)
	n := countNonEmpty(rawLines)
	shown := truncateLines(rawLines, checkedShown)
	lines := checkedHeading(st)
	for _, ln := range shown {
		lines = append(lines, "- "+ln)
	}
	if n > checkedShown {
		lines = append(lines, "(+"+strconv.Itoa(n-checkedShown)+" more — see toc.txt for the full coverage manifest)")
	}
	return lines
}

func countNonEmpty(lines []string) int {
	n := 0
	for _, ln := range lines {
		if ln != "" {
			n++
		}
	}
	return n
}

func truncateLines(lines []string, maxShown int) []string {
	if len(lines) > maxShown {
		return lines[:maxShown]
	}
	return lines
}

func checkedHeading(st *state) []string {
	if frag, ok := preambleHeading(st); ok {
		if frag == "" {
			return nil
		}
		return strings.Split(frag, newline)
	}
	return []string{"## What we checked"}
}

const checkedShown = 7

func checkedFiles(dir string) string {
	if isRegular(dir + prJSONSuffix) {
		raw, err := os.ReadFile(dir + prJSONSuffix)
		if err == nil {
			if lines := prFileLines(raw); lines != nil {
				return strings.Join(lines, newline)
			}
		}
	}
	raw, err := os.ReadFile(dir + fullDiffSuffix)
	if err != nil {
		return ""
	}
	return diffFileLines(string(raw))
}

func prFileLines(raw []byte) []string {
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil
	}
	files, ok := m["files"].([]any)
	if !ok || len(files) == 0 {
		return nil
	}
	lines := make([]string, 0, len(files))
	for _, f := range files {
		fm, ok := f.(map[string]any)
		if !ok {
			lines = append(lines, "null (+0/-0)")
			continue
		}
		lines = append(lines, jq.Str(fm["path"])+" (+"+addStr(fm["additions"])+"/-"+addStr(fm["deletions"])+")")
	}
	return lines
}

func addStr(v any) string {
	if v == nil || v == false {
		return "0"
	}
	return jq.Str(v)
}

func diffFileLines(diff string) string {
	sections := hunks.Files(diff)
	lines := make([]string, 0, len(sections))
	for i := range sections {
		lines = append(lines, sections[i].Path)
	}
	return strings.Join(lines, newline)
}

func preambleHeading(st *state) (string, bool) {
	if !isRegular(overrides.Preamble) {
		return "", false
	}
	raw, err := os.ReadFile(overrides.Preamble)
	if err != nil {
		return "", true
	}
	return substitutePlaceholders(strings.TrimRight(strings.ReplaceAll(string(raw), "\r", ""), newline), st), true
}

func substitutePlaceholders(text string, st *state) string {
	tier, cost := provenance.Spend(st.dir)
	vals := &overrides.Values{
		Version:       provenance.ToolVersion(st.req.RootPath()),
		Tier:          tier,
		ProductURL:    core.DefaultProductURL,
		Cost:          cost,
		FilesReviewed: provenance.FileCount(st.dir),
		HeadShort:     provenance.HeadShort(st.dir),
		Model:         provenance.OverrideModel(st.dir, st.req.ReviewModel()),
	}
	vals.BaseAttribution = "Reviewed with [**Verdict " + vals.Version + "**](" + core.DefaultProductURL + ")"
	return overrides.Substitute(text, vals)
}

func renderFooter(st *state) string {
	eff := st.req.Effective
	attrSet := st.req.ForcesOrEmpty().PublishAttributionTextSet
	out, err := provenance.RenderFooter(&provenance.FooterOpts{
		Dir:                    st.dir,
		PublishFooter:          eff.PublishFooter,
		PublishAttribution:     eff.PublishAttribution,
		PublishAttributionText: eff.PublishAttributionText,
		PublishCopyright:       eff.PublishCopyright,
		PublishLogo:            eff.PublishLogo,
		PublishLogoURL:         eff.PublishLogoURL,
		Root:                   st.req.RootPath(),
		ReviewModel:            st.req.ReviewModel(),
		AttributionTextSet:     attrSet,
	})
	if err != nil {
		return ""
	}
	return out
}
