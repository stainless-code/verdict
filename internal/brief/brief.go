// Package brief validates and decodes the per-finding repair brief: the
// optional review.json comment object stored on a finding entry so fix and
// show can hand an agent the affected sites, the required outcome, an
// optional approach, and open questions without parsing the body prose.
// Validation drops bad parts with warnings; it never refuses a review.
package brief

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	// minOutcome/maxOutcome bound the one required sentence.
	minOutcome = 10
	maxOutcome = 500
	// minApproach/maxApproach bound the optional suggested approach.
	minApproach = 10
	maxApproach = 1000
	// minQuestion/maxQuestion bound one open question.
	minQuestion = 10
	maxQuestion = 300
	// maxSites caps named sites; past it a site drops with a warning.
	maxSites = 10
	// maxQuestions caps open questions; past it a question drops with a warning.
	maxQuestions = 10
)

// key names on the brief and site objects.
const (
	keySites     = "sites"
	keyOutcome   = "outcome"
	keyApproach  = "approach"
	keyQuestions = "questions"
	keyPath      = "path"
	keyLine      = "line"
)

// Validate normalizes one comment's raw brief against the anchor hunk rows,
// returning the storable brief (nil when the outcome is unusable) plus one
// warning per dropped part. Sites outside every changed hunk are dropped, never
// snapped to a nearby range.
func Validate(raw any, rows []hunks.Hunk) (*core.Brief, []string) {
	m, keys, ok := objectOf(raw)
	if !ok {
		return nil, []string{"brief is not an object — dropped"}
	}
	outcome, warns := outcomeOf(m)
	if outcome == "" {
		return nil, warns
	}
	sites, siteWarns := sitesOf(m[keySites], rows)
	approach, approachWarns := approachOf(m[keyApproach])
	questions, questionWarns := questionsOf(m[keyQuestions])
	warns = append(warns, siteWarns...)
	warns = append(warns, approachWarns...)
	warns = append(warns, questionWarns...)
	warns = append(warns, unknownKeys(keys)...)
	return &core.Brief{
		Sites:     sites,
		Outcome:   outcome,
		Approach:  approach,
		Questions: questions,
	}, warns
}

// FromRecord decodes a finding entry's stored brief without re-validating the
// hunks: the store is the truth a bundle may have moved under. Absent or
// non-object reads as nil.
func FromRecord(rec *jq.Obj) *core.Brief {
	if rec == nil {
		return nil
	}
	raw, ok := rec.Get("brief")
	if !ok || jq.Falsey(raw) {
		return nil
	}
	m, _, ok := objectOf(raw)
	if !ok {
		return nil
	}
	sites, _ := sitesOf(m[keySites], nil)
	approach, _ := approachOf(m[keyApproach])
	questions, _ := questionsOf(m[keyQuestions])
	outcome, ok := m[keyOutcome].(string)
	if !ok {
		return nil
	}
	return &core.Brief{
		Sites:     sites,
		Outcome:   outcome,
		Approach:  approach,
		Questions: questions,
	}
}

// objectOf reads v as a string map plus its key order (*jq.Obj keeps file
// order; a plain map reads sorted so warnings stay deterministic).
func objectOf(v any) (map[string]any, []string, bool) {
	switch t := v.(type) {
	case *jq.Obj:
		if t == nil {
			return nil, nil, false
		}
		return t.Fields, t.Keys, true
	case map[string]any:
		return t, slices.Sorted(maps.Keys(t)), true
	default:
		return nil, nil, false
	}
}

// outcomeOf validates the required one-sentence outcome: a missing or
// out-of-range value drops the whole brief.
func outcomeOf(m map[string]any) (string, []string) {
	text, ok := m[keyOutcome].(string)
	if !ok {
		return "", []string{"brief outcome is missing — dropped the brief"}
	}
	if n := utf8.RuneCountInString(text); n < minOutcome || n > maxOutcome {
		return "", []string{fmt.Sprintf(
			"brief outcome is %d chars (want %d-%d) — dropped the brief",
			n, minOutcome, maxOutcome)}
	}
	return text, nil
}

// approachOf validates the optional suggested approach (an empty value reads
// as absent, so an obvious fix needs no filler sentence).
func approachOf(v any) (string, []string) {
	if jq.Falsey(v) {
		return "", nil
	}
	text, ok := v.(string)
	if !ok {
		return "", []string{"brief approach is not a string — dropped"}
	}
	if text == "" {
		return "", nil
	}
	if n := utf8.RuneCountInString(text); n < minApproach || n > maxApproach {
		return "", []string{fmt.Sprintf(
			"brief approach is %d chars (want %d-%d) — dropped",
			n, minApproach, maxApproach)}
	}
	return text, nil
}

// questionsOf validates the optional open-question list, dropping the items
// that fail and keeping the rest in order.
func questionsOf(v any) ([]string, []string) {
	if jq.Falsey(v) {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, []string{"brief questions is not an array — dropped"}
	}
	out := make([]string, 0, len(items))
	var warns []string
	for i, item := range items {
		text, warn, keep := questionAt(i, item)
		if !keep {
			warns = append(warns, warn)
			continue
		}
		out = append(out, text)
	}
	return out, warns
}

// questionAt validates one question: the first maxQuestions keep, and a
// non-string or out-of-range item drops with a warning.
func questionAt(i int, item any) (string, string, bool) {
	if i >= maxQuestions {
		return "", fmt.Sprintf("brief question %d is past the %d-question cap — dropped", i+1, maxQuestions), false
	}
	text, ok := item.(string)
	if !ok {
		return "", fmt.Sprintf("brief question %d is not a string — dropped", i+1), false
	}
	if n := utf8.RuneCountInString(text); n < minQuestion || n > maxQuestion {
		return "", fmt.Sprintf("brief question %d is %d chars (want %d-%d) — dropped",
			i+1, n, minQuestion, maxQuestion), false
	}
	return text, "", true
}

// sitesOf validates the optional site list against the hunk rows: a path with
// no hunk, or a line outside every OLD/NEW hunk, drops that site. Duplicates
// dedupe in order, and nil rows (a stored brief read back) validate nothing.
func sitesOf(v any, rows []hunks.Hunk) ([]core.BriefSite, []string) {
	if jq.Falsey(v) {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, []string{"brief sites is not an array — dropped"}
	}
	out := make([]core.BriefSite, 0, len(items))
	var warns []string
	seen := map[string]bool{}
	for i, item := range items {
		site, warn, keep := siteAt(i, item, rows)
		if !keep {
			warns = append(warns, warn)
			continue
		}
		if key := siteLocator(site); !seen[key] {
			seen[key] = true
			out = append(out, site)
		}
	}
	return out, warns
}

// siteAt validates one site: the first maxSites keep, and a malformed or
// unanchored item drops with a warning naming it.
func siteAt(i int, item any, rows []hunks.Hunk) (core.BriefSite, string, bool) {
	if i >= maxSites {
		return core.BriefSite{}, fmt.Sprintf("brief site %d is past the %d-site cap — dropped", i+1, maxSites), false
	}
	m, _, ok := objectOf(item)
	if !ok {
		return core.BriefSite{}, "brief site is not an object — dropped", false
	}
	path, ok := m[keyPath].(string)
	if !ok || path == "" {
		return core.BriefSite{}, "brief site has no path — dropped", false
	}
	line, warn, ok := lineOf(m, path)
	if !ok {
		return core.BriefSite{}, warn, false
	}
	if rows != nil && !inHunks(rows, path, line) {
		return core.BriefSite{}, fmt.Sprintf("brief site %s is not a changed file with hunks — dropped",
			siteLocator(core.BriefSite{Path: path, Line: line})), false
	}
	return core.BriefSite{Path: path, Line: line}, "", true
}

// lineOf reads a site's optional line: absent reads nil, a non-integer fails.
func lineOf(m map[string]any, path string) (*int, string, bool) {
	raw, ok := m[keyLine]
	if !ok || jq.Falsey(raw) {
		return nil, "", true
	}
	n, ok := jq.IntOf(raw)
	if !ok {
		return nil, fmt.Sprintf("brief site %s has a non-integer line — dropped", path), false
	}
	return &n, "", true
}

// siteLocator renders path[:line] for warnings and assertions.
func siteLocator(site core.BriefSite) string {
	if site.Line == nil {
		return site.Path
	}
	return fmt.Sprintf("%s:%d", site.Path, *site.Line)
}

// inHunks reports whether the path has a hunk and, when the site names a
// line, whether that line sits inside one on either side.
func inHunks(rows []hunks.Hunk, path string, line *int) bool {
	key := stripDiffPrefix(path)
	if !hunks.Indexed(rows, key) {
		return false
	}
	if line == nil {
		return true
	}
	return hunks.Contains(rows, key, "OLD", *line, *line) ||
		hunks.Contains(rows, key, "NEW", *line, *line)
}

// unknownKeys warns about every key the brief does not define, in order.
func unknownKeys(keys []string) []string {
	var warns []string
	for _, key := range keys {
		switch key {
		case keySites, keyOutcome, keyApproach, keyQuestions:
		default:
			warns = append(warns, fmt.Sprintf("brief.%s is not a known brief key — dropped", key))
		}
	}
	return warns
}

// stripDiffPrefix drops a diff's a/ or b/ path prefix so a site matches the
// stripped paths in the hunk rows.
func stripDiffPrefix(path string) string {
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		return path[2:]
	}
	return path
}
