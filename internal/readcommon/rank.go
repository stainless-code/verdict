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
	KindInline     = "inline"
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

// sevOrder is the severity tier rank order; unknown tiers sort last.
var sevOrder = []string{"critical", "important", "informational"}

// sevRank ports the sev rank: untagged severities read 3 and never meet a
// tier gate.
func sevRank(sev any) int {
	if s, ok := sev.(string); ok {
		if i := slices.Index(sevOrder, s); i >= 0 {
			return i
		}
	}
	return len(sevOrder)
}

// rigorOrder is the rigor rung rank order; unknown rungs sort first (null).
var rigorOrder = []string{"reproduced", "ran", "walked", "pointed", "said"}

// rigorRank ports (4 - rw[.rigor // "said"]): known rungs descend from
// reproduced to said. Unknown rungs read null in the shell and sort first,
// which the -1 sentinel preserves without a per-comparison value allocation.
func rigorRank(rigor any) int {
	r, ok := rigor.(string)
	if !ok {
		r = ""
	}
	if rigor == nil || rigor == false {
		r = "said"
	}
	if i := slices.Index(rigorOrder, r); i >= 0 {
		return i
	}
	return -1
}

// boolRank ports (if .flag then 0 else 1 end): true first, everything else
// (false, null, missing, exotic) last.
func boolRank(v any) int {
	if v == true {
		return 0
	}
	return 1
}

// FieldOf reads one entry field (nil when absent or non-object).
func FieldOf(entry any, key string) any {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return nil
	}
	v, _ := m.Get(key)
	return v
}

// StatusOf reads an entry's status for filters and counts.
func StatusOf(entry any) string {
	if s, ok := FieldOf(entry, "status").(string); ok {
		return s
	}
	return ""
}

// KindOf reads an entry's kind for filters.
func KindOf(entry any) string {
	if s, ok := FieldOf(entry, "kind").(string); ok {
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
//
// The comparator is shared with [TopOpenInline] and decorates the entries
// once, so the sort no longer rebuilds its eight keys on every comparison.
func Rank(entries []any, statusF, kindF string) []Ranked {
	decorated := decorateEntries(entries, statusF, kindF)
	slices.SortStableFunc(decorated, compareDecorated)
	out := make([]Ranked, 0, len(decorated))
	for i := range decorated {
		out = append(out, Ranked{Entry: decorated[i].entry, Rank: i + 1})
	}
	return out
}

// TopOpenInline selects the highest-ranked open inline finding with the
// same eight-key order the report and next rankings use. It returns the
// entry's full sig ("" when the sig is missing or non-string, which callers
// keep refusing on) and the entry, or ("", nil) when no open inline finding
// exists. This is the one default-pick rule the fix verb shares with next.
func TopOpenInline(entries []any) (string, any) {
	decorated := decorateEntries(entries, StatusOpen, KindInline)
	slices.SortStableFunc(decorated, compareDecorated)
	if len(decorated) == 0 {
		return "", nil
	}
	entry := decorated[0].entry
	sig, _ := entrySig(entry)
	return sig, entry
}

// rankKeys is one entry's precomputed eight-key sort tuple: status,
// severity, rigor, anchored, inBounds, path, line, sig.
type rankKeys struct {
	path     any
	line     any
	sig      any
	status   int
	severity int
	rigor    int
	anchored int
	inBounds int
}

// decoratedEntry pairs one entry with its precomputed sort keys.
type decoratedEntry struct {
	entry any
	keys  rankKeys
}

// decorateEntries applies the status/kind display filters and computes each
// surviving entry's keys exactly once.
func decorateEntries(entries []any, statusF, kindF string) []decoratedEntry {
	out := make([]decoratedEntry, 0, len(entries))
	for _, e := range entries {
		if statusF != StatusAll && StatusOf(e) != statusF {
			continue
		}
		if kindF != KindAll && KindOf(e) != kindF {
			continue
		}
		out = append(out, decoratedEntry{entry: e, keys: sortKeys(e)})
	}
	return out
}

// compareDecorated orders two decorated entries by their precomputed keys.
//
//nolint:gocritic // value params keep the sort allocation-free; a pointer key table would allocate per entry.
func compareDecorated(a, b decoratedEntry) int {
	return compareRankKeys(a.keys, b.keys)
}

// compareRankKeys orders two key tuples by the ranked sort order. The
// key precedence is the published contract: changing it changes report,
// next, and fix together.
//
//nolint:gocritic // value params keep the sort allocation-free; a pointer key table would allocate per entry.
func compareRankKeys(a, b rankKeys) int {
	if c := cmpInt(a.status, b.status); c != 0 {
		return c
	}
	if c := cmpInt(a.severity, b.severity); c != 0 {
		return c
	}
	if c := cmpInt(a.rigor, b.rigor); c != 0 {
		return c
	}
	if c := cmpInt(a.anchored, b.anchored); c != 0 {
		return c
	}
	if c := cmpInt(a.inBounds, b.inBounds); c != 0 {
		return c
	}
	if c := Cmp(a.path, b.path); c != 0 {
		return c
	}
	if c := Cmp(a.line, b.line); c != 0 {
		return c
	}
	return Cmp(a.sig, b.sig)
}

// sortKeys builds the eight ranked sort keys for one entry.
func sortKeys(e any) rankKeys {
	path := FieldOf(e, "path")
	if path == nil || path == false {
		path = ""
	}
	line := FieldOf(e, "line")
	if line == nil || line == false {
		line = 0
	}
	return rankKeys{
		status:   statusRank(StatusOf(e)),
		severity: sevRank(FieldOf(e, "severity")),
		rigor:    rigorRank(FieldOf(e, "rigor")),
		anchored: boolRank(FieldOf(e, "anchored")),
		inBounds: boolRank(FieldOf(e, "inBounds")),
		path:     path,
		line:     line,
		sig:      FieldOf(e, "sig"),
	}
}

// Locator ports the ranked locator: path[:line] when path is truthy
// (null/false read falsy; 0 and "" read truthy, like jq), else the heading
// fallback. Rendering follows \(...) interpolation (null reads "").
func Locator(entry any) string {
	path := FieldOf(entry, "path")
	if !Truthy(path) {
		return jq.Interp(entryHeading(entry))
	}
	locator := jq.Interp(path)
	if line := FieldOf(entry, "line"); Truthy(line) {
		locator += ":" + jq.Interp(line)
	}
	return locator
}

// entryHeading reads the heading fallback (nil or false read "?").
func entryHeading(entry any) any {
	heading := FieldOf(entry, "heading")
	if heading == nil || heading == false {
		return "?"
	}
	return heading
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
