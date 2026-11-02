package readcommon

import (
	"slices"

	"github.com/stainless-code/verdict/internal/jq"
)

// Status and kind filter values.
const (
	StatusAll      = "all"
	StatusOpen     = "open"
	StatusDeferred = "deferred"
	KindAll        = "all"
)

// statusRank ports the sk rank: open first, deferred second, rest last.
func statusRank(status string) int {
	switch status {
	case StatusOpen:
		return 0
	case StatusDeferred:
		return 1
	default:
		return 2
	}
}

// sevRank ports the sev rank: untagged severities read 3 and never meet a
// tier gate.
func sevRank(sev any) int {
	if s, ok := sev.(string); ok {
		switch s {
		case "critical":
			return 0
		case "important":
			return 1
		case "informational":
			return 2
		}
	}
	return 3
}

// rigorKey ports (4 - rw[.rigor // "said"]): known rungs descend from
// reproduced to said, unknown rungs read null and sort first.
func rigorKey(rigor any) any {
	r, _ := rigor.(string)
	if rigor == nil || rigor == false {
		r = "said"
	}
	switch r {
	case "reproduced":
		return 0
	case "ran":
		return 1
	case "walked":
		return 2
	case "pointed":
		return 3
	case "said":
		return 4
	default:
		return nil
	}
}

// boolRank ports (if .flag then 0 else 1 end): true first, everything else
// (false, null, missing, exotic) last.
func boolRank(v any) int {
	if v == true {
		return 0
	}
	return 1
}

// fieldOf reads one entry field (nil when absent or non-object).
func fieldOf(entry any, key string) any {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return nil
	}
	v, _ := m.Get(key)
	return v
}

// StatusOf reads an entry's status for filters and counts.
func StatusOf(entry any) string {
	if s, ok := fieldOf(entry, "status").(string); ok {
		return s
	}
	return ""
}

// KindOf reads an entry's kind for filters.
func KindOf(entry any) string {
	if s, ok := fieldOf(entry, "kind").(string); ok {
		return s
	}
	return ""
}

// Ranked is one ranked finding: the entry plus its 1-based rank.
type Ranked struct {
	Entry any
	Rank  int
}

// Rank ports the ranked filter: status/kind display filters, then the
// open-first severity-rigor-anchor-path-line-sig sort (stable, like jq's
// sort_by), then 1-based ranks. Null entries flow through like the shell's
// (their filters read null and their keys sort first).
func Rank(entries []any, statusF, kindF string) []Ranked {
	filtered := make([]any, 0, len(entries))
	for _, e := range entries {
		if statusF != StatusAll && StatusOf(e) != statusF {
			continue
		}
		if kindF != KindAll && KindOf(e) != kindF {
			continue
		}
		filtered = append(filtered, e)
	}
	slices.SortStableFunc(filtered, compareEntries)
	out := make([]Ranked, 0, len(filtered))
	for i, e := range filtered {
		out = append(out, Ranked{Entry: e, Rank: i + 1})
	}
	return out
}

// compareEntries orders two entries by the ranked sort keys.
func compareEntries(a, b any) int {
	ka, kb := sortKeys(a), sortKeys(b)
	for i := range ka {
		if c := Cmp(ka[i], kb[i]); c != 0 {
			return c
		}
	}
	return 0
}

// sortKeys builds the eight ranked sort keys for one entry.
func sortKeys(e any) []any {
	path := fieldOf(e, "path")
	if path == nil || path == false {
		path = ""
	}
	line := fieldOf(e, "line")
	if line == nil || line == false {
		line = 0
	}
	return []any{
		statusRank(StatusOf(e)),
		sevRank(fieldOf(e, "severity")),
		rigorKey(fieldOf(e, "rigor")),
		boolRank(fieldOf(e, "anchored")),
		boolRank(fieldOf(e, "inBounds")),
		path,
		line,
		fieldOf(e, "sig"),
	}
}

// Locator ports the ranked locator: path[:line] when path is truthy
// (null/false read falsy; 0 and "" read truthy, like jq), else the heading
// fallback. Rendering follows \(...) interpolation (null reads "").
func Locator(entry any) string {
	path := fieldOf(entry, "path")
	if !Truthy(path) {
		return jq.Interp(entryHeading(entry))
	}
	locator := jq.Interp(path)
	if line := fieldOf(entry, "line"); Truthy(line) {
		locator += ":" + jq.Interp(line)
	}
	return locator
}

// entryHeading reads the heading fallback (nil or false read "?").
func entryHeading(entry any) any {
	heading := fieldOf(entry, "heading")
	if heading == nil || heading == false {
		return "?"
	}
	return heading
}

// SigPrefix renders the 8-codepoint sig prefix for human tables.
func SigPrefix(entry any) string {
	return Prefix(sigOf(entry))
}

// sigOf reads an entry's sig string ("" unless it is one).
func sigOf(entry any) string {
	sig, _ := fieldOf(entry, "sig").(string)
	return sig
}

// Prefix takes the first 8 runes, mirroring jq's [0:8] codepoint slice
// (sigs are hex, so bytes and runes agree).
func Prefix(s string) string {
	return PrefixN(s, sigPrefixLen)
}

// PrefixN takes the first n runes.
func PrefixN(s string, n int) string {
	return prefixRunes(s, n)
}

// sigPrefixLen is the human-table sig prefix width.
const sigPrefixLen = 8

// prefixRunes takes the first n runes, mirroring jq's [0:8] codepoint
// slice (sigs are hex, so bytes and runes agree).
func prefixRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n])
	}
	return s
}

// CountStatus counts ranked entries with the given status.
func CountStatus(ranked []Ranked, status string) int {
	n := 0
	for _, r := range ranked {
		if StatusOf(r.Entry) == status {
			n++
		}
	}
	return n
}

// CountOpen counts whole-store open findings (the gate's whole-store key:
// display filters never narrow a gate).
func CountOpen(entries []any) int {
	n := 0
	for _, e := range entries {
		if StatusOf(e) == StatusOpen {
			n++
		}
	}
	return n
}
