package report

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/overrides"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// renderOutput ports the --output block: the confidence-led markdown
// report. No-op without the flag; a write failure refuses typed.
func renderOutput(st *state) error {
	if st.flags.Output == "" {
		return nil
	}
	doc := buildDocument(st)
	if err := os.WriteFile(st.flags.Output, []byte(doc+"\n"), 0o644); err != nil {
		return &cli.RefusalError{Code: "report-failed",
			Message: fmt.Sprintf("could not write %s: %v", st.flags.Output, err)}
	}
	return nil
}

// buildDocument assembles the report file lines.
func buildDocument(st *state) string {
	lines := []string{
		"# Review report — " + st.req.Repo + "#" + st.req.Pr,
		"",
		"<!-- report-machine:start -->",
		confidenceLead(st),
		basisOutputLine(st.entries),
		"## Counts — open " + strconv.Itoa(st.nAllOpen) + " · head " + readcommon.ShortSHA(st.headSHA),
	}
	lines = append(lines, rankedLines(st)...)
	lines = append(lines, linkDefs(st)...)
	if overview := renderOverview(st); overview != "" {
		lines = append(lines, overviewLines(overview)...)
	}
	lines = append(lines, checkedBlock(st)...)
	if footer := renderFooter(st); footer != "" {
		lines = append(lines, overviewLines(footer)...)
	}
	lines = append(lines, "<!-- report-machine:end -->")
	return strings.Join(lines, "\n")
}

// overviewLines splits a rendered block into lines.
func overviewLines(block string) []string {
	return strings.Split(block, "\n")
}

// confidenceLead ports the confidence lead: review-level confidence and
// safety from the first existing completed-post/machine record; absent or
// unparsable records read neutral, never fail.
func confidenceLead(st *state) string {
	conf, sec := "unrecorded", "not assessed"
	for _, rec := range []string{st.dir + "/post.json", st.dir + "/record.json"} {
		if !isRegular(rec) {
			continue
		}
		if c := jsonRaw(rec, "confidence"); c == "1" || c == "2" || c == "3" || c == "4" || c == "5" {
			conf = c + "/5"
		}
		if s := jsonRaw(rec, "security_summary"); s != "" {
			sec = s
		}
		break
	}
	return "> Confidence: " + conf + " · Safety: " + sec
}

// jsonRaw reads one field with `jq -r ... // empty` semantics: missing or
// null reads "", scalars render raw, containers render pretty.
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

// basisOutputLine ports basis_line over the whole findings store.
func basisOutputLine(entries []any) string {
	return "> Basis: " + basisCounts(entries)
}

// basisCounts ports basis_counts: rigor rung counts in evidence order,
// distinct cited kb rule ids, addressed (verified-fixed) findings. Null
// entries count as pointed (their rigor reads null); cited ids dedupe in
// jq order across mixed types.
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
	parts := []string{
		strconv.Itoa(counts["reproduced"]),
		strconv.Itoa(counts["ran"]),
		strconv.Itoa(counts["walked"]),
		strconv.Itoa(counts["pointed"]),
		strconv.Itoa(counts["said"]),
		strconv.Itoa(len(dedupeValues(cited))),
		strconv.Itoa(verified),
	}
	return parts[0] + " reproduced · " + parts[1] + " ran · " + parts[2] + " walked · " +
		parts[3] + " pointed · " + parts[4] + " said · " + parts[5] + " rules cited · " +
		parts[6] + " verified fixes"
}

// basisRigor normalizes one entry's rigor rung: null/false/missing reads
// pointed; present non-strings match no rung and count nowhere.
func basisRigor(entry any) string {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return "pointed"
	}
	rigor, _ := m.Get("rigor")
	if rigor == nil || rigor == false {
		return "pointed"
	}
	if s, ok := rigor.(string); ok {
		return s
	}
	return ""
}

// collectCitedIDs appends one entry's raw array kb ids.
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

// rankedLines ports the ranked report lines, with permalink reference
// links for linkable GitHub bundles.
func rankedLines(st *state) []string {
	linkable := provenance.EvidenceLinkable(st.dir, st.headSHA) == "true"
	lines := make([]string, 0, len(st.ranked))
	for _, item := range st.ranked {
		lines = append(lines, rankedLine(st, item, linkable))
	}
	return lines
}

// rankedLine renders one ranked line.
func rankedLine(st *state, item readcommon.Ranked, linkable bool) string {
	locator := readcommon.Locator(item.Entry)
	if linkable && fieldOf(item.Entry, "path") != nil && fieldOf(item.Entry, "line") != nil {
		locator = "[" + locator + "][ev-" + strconv.Itoa(item.Rank) + "]"
	}
	return strconv.Itoa(item.Rank) + ". [" + jq.Str(fieldOf(item.Entry, "status")) + "] " + locator +
		" (" + sigOutput(item.Entry) + ", " + rigorOutput(item.Entry) + ") — " + bodyOutput(item.Entry)
}

// sigOutput ports \(.sig[0:8]) for output lines.
func sigOutput(entry any) string {
	if sig, ok := fieldOf(entry, "sig").(string); ok {
		return readcommon.Prefix(sig)
	}
	return jq.Str(fieldOf(entry, "sig"))
}

// rigorOutput ports \(.rigor // "?") for output lines.
func rigorOutput(entry any) string {
	rigor := fieldOf(entry, "rigor")
	if rigor == nil || rigor == false {
		return "?"
	}
	return jq.Str(rigor)
}

// bodyOutput ports (.body // "" | gsub("\n"; " ") | .[0:200]).
func bodyOutput(entry any) string {
	body, _ := fieldOf(entry, "body").(string)
	body = strings.ReplaceAll(body, "\n", " ")
	return readcommon.PrefixN(body, bodyRunes)
}

// bodyRunes caps output body excerpts.
const bodyRunes = 200

// linkDefs ports the permalink definition list for linkable bundles.
func linkDefs(st *state) []string {
	if provenance.EvidenceLinkable(st.dir, st.headSHA) != "true" {
		return nil
	}
	base := provenance.EvidenceBaseURL(st.dir, st.req.Repo)
	var defs []string
	for _, item := range st.ranked {
		path := fieldOf(item.Entry, "path")
		line := fieldOf(item.Entry, "line")
		if path == nil || line == nil {
			continue
		}
		defs = append(defs, "[ev-"+strconv.Itoa(item.Rank)+"]: "+base+"/blob/"+st.headSHA+"/"+jq.Str(path)+"#L"+jq.Str(line))
	}
	return defs
}

// checkedBlock ports the bounded coverage list on clean reports: up to 7
// files from the stored bundle, with the preamble-fragment heading when
// one is pinned.
func checkedBlock(st *state) []string {
	if st.nAllOpen != 0 {
		return nil
	}
	files := checkedFiles(st.dir)
	if files == "" {
		return nil
	}
	rawLines := strings.Split(files, "\n")
	n := 0
	for _, ln := range rawLines {
		if ln != "" {
			n++
		}
	}
	shown := rawLines
	if len(shown) > checkedShown {
		shown = shown[:checkedShown]
	}
	var lines []string
	if frag, ok := preambleHeading(st); ok {
		if frag != "" {
			lines = append(lines, strings.Split(frag, "\n")...)
		}
	} else {
		lines = append(lines, "## What we checked")
	}
	for _, ln := range shown {
		lines = append(lines, "- "+ln)
	}
	if n > checkedShown {
		lines = append(lines, "(+"+strconv.Itoa(n-checkedShown)+" more — see toc.txt for the full coverage manifest)")
	}
	return lines
}

// checkedShown caps the coverage list.
const checkedShown = 7

// checkedFiles ports the stored file list: PR.json files first, else diff
// headers stripped to b-side paths.
func checkedFiles(dir string) string {
	if isRegular(dir + "/PR.json") {
		raw, err := os.ReadFile(dir + "/PR.json")
		if err == nil {
			if lines := prFileLines(raw); lines != nil {
				return strings.Join(lines, "\n")
			}
		}
	}
	raw, err := os.ReadFile(dir + "/full.diff")
	if err != nil {
		return ""
	}
	return diffFileLines(string(raw))
}

// prFileLines renders PR.json files as "path (+a/-d)" lines, or nil when
// the record carries no file list (falling back to the diff).
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

// addStr renders a +/- count (null/false read 0).
func addStr(v any) string {
	if v == nil || v == false {
		return "0"
	}
	return jq.Str(v)
}

// diffFileLines strips diff headers to b-side paths.
func diffFileLines(diff string) string {
	var lines []string
	for _, ln := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(ln, "diff --git ") {
			continue
		}
		lines = append(lines, setCurrent(strings.TrimPrefix(ln, "diff --git ")))
	}
	return strings.Join(lines, "\n")
}

// preambleHeading ports the clean-review preamble heading: the substituted
// fragment when pinned (possibly empty, suppressing the heading but keeping
// the bullets), or the default heading when no fragment exists.
func preambleHeading(st *state) (string, bool) {
	if !isRegular(overrides.Preamble) {
		return "", false
	}
	raw, err := os.ReadFile(overrides.Preamble)
	if err != nil {
		return "", true
	}
	// The shell captures the fragment through $(...) which strips trailing
	// newlines, so trim them here too before the caller splits into lines.
	return substitutePlaceholders(strings.TrimRight(strings.ReplaceAll(string(raw), "\r", ""), "\n"), st), true
}

// substitutePlaceholders ports substitute_override_placeholders for the
// preamble fragment and the attribution text.
func substitutePlaceholders(text string, st *state) string {
	tier, cost := provenance.Spend(st.dir)
	vals := &overrides.Values{
		Version:       provenance.ToolVersion(st.req.Root),
		Tier:          tier,
		ProductURL:    cli.DefaultProductURL,
		Cost:          cost,
		FilesReviewed: provenance.FileCount(st.dir),
		HeadShort:     provenance.HeadShort(st.dir),
		Model:         provenance.OverrideModel(st.dir, st.req.ReviewModel()),
	}
	vals.BaseAttribution = "Reviewed with [**Verdict " + vals.Version + "**](" + cli.DefaultProductURL + ")"
	return overrides.Substitute(text, vals)
}

// renderFooter ports the footer block for the report file.
func renderFooter(st *state) string {
	r := st.runner
	attrSet := st.req.ForcesOrEmpty().PublishAttributionTextSet
	out, err := provenance.RenderFooter(&provenance.FooterOpts{
		Dir:                    st.dir,
		PublishFooter:          r.Effective.PublishFooter,
		PublishAttribution:     r.Effective.PublishAttribution,
		PublishAttributionText: r.Effective.PublishAttributionText,
		PublishCopyright:       r.Effective.PublishCopyright,
		PublishLogo:            r.Effective.PublishLogo,
		PublishLogoURL:         r.Effective.PublishLogoURL,
		Root:                   st.req.Root,
		ReviewModel:            st.req.ReviewModel(),
		AttributionTextSet:     attrSet,
	})
	if err != nil {
		return ""
	}
	return out
}
