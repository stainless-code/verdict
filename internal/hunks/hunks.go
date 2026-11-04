// Package hunks indexes diffs into side-aware hunk ranges and validates
// review anchors keep-biased: anything provably outside a hunk moves to the
// body, everything ambiguous (unknown path, non-numeric line) stays inline
// with a warning.
package hunks

import (
	"math"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

// Hunk is one side-aware hunk range, 1-based inclusive, mirroring one
// hunks.tsv row: file, side (NEW or OLD), start, end.
type Hunk struct {
	File  string
	Side  string
	Start int
	End   int
}

// Row renders the hunks.tsv line for the hunk.
func (h Hunk) Row() string {
	return h.File + "\t" + h.Side + "\t" + strconv.Itoa(h.Start) + "\t" + strconv.Itoa(h.End) + "\n"
}

// Parse indexes diffText the way build_hunks_tsv's awk does: every section's
// OLD/NEW ranges, flattened. Zero-count sides contribute nothing.
func Parse(diffText string) []Hunk {
	files := Files(diffText)
	var out []Hunk
	for i := range files {
		out = append(out, files[i].Hunks...)
	}
	return out
}

// hunkRows parses one @@ header into OLD/NEW rows. A path carrying a tab,
// newline or carriage return (git C-quotes them, the unquoting restores them)
// yields no rows: written into hunks.tsv it would shift fields or forge whole
// rows, and the keep-biased reading of an unindexed path is the safe one.
func hunkRows(file, ln string) []Hunk {
	f := strings.Fields(ln)
	if len(f) < minHeaderFields || strings.ContainsAny(file, "\t\r\n") {
		return nil
	}
	oldStart, oldCount := parseRange(strings.TrimPrefix(f[1], "-"))
	newStart, newCount := parseRange(strings.TrimPrefix(f[2], "+"))
	var out []Hunk
	if oldCount > 0 {
		out = append(out, Hunk{File: file, Side: "OLD", Start: oldStart, End: spanEnd(oldStart, oldCount)})
	}
	if newCount > 0 {
		out = append(out, Hunk{File: file, Side: "NEW", Start: newStart, End: spanEnd(newStart, newCount)})
	}
	return out
}

// spanEnd is the inclusive end of a count-line span from start (count > 0),
// saturating at the largest int instead of wrapping negative.
func spanEnd(start, count int) int {
	if start > math.MaxInt-count+1 {
		return math.MaxInt
	}
	return start + count - 1
}

// parseRange splits "start[,count]": a missing count reads 1 (awk's
// o[2]=="" branch), and numbers follow awk's leading-numeric-prefix rule.
func parseRange(s string) (int, int) {
	start, count := s, "1"
	if pre, post, ok := strings.Cut(s, ","); ok {
		start, count = pre, post
		if count == "" {
			count = "1"
		}
	}
	startN, _ := jq.PrefixInt(start)
	countN, _ := jq.PrefixInt(count)
	return startN, countN
}

// Indexed reports whether any hunk names file (the keep-biased first gate:
// unknown paths stay inline with a warning).
func Indexed(hunks []Hunk, file string) bool {
	for _, h := range hunks {
		if h.File == file {
			return true
		}
	}
	return false
}

// Contains reports whether [start, end] sits inside one hunk of file/side.
func Contains(hunks []Hunk, file, side string, start, end int) bool {
	for _, h := range hunks {
		if h.File == file && h.Side == side && h.Start <= start && end <= h.End {
			return true
		}
	}
	return false
}

// Format renders hunks as hunks.tsv bytes.
func Format(hunks []Hunk) string {
	var sb strings.Builder
	for _, h := range hunks {
		sb.WriteString(h.Row())
	}
	return sb.String()
}

const (
	minHeaderFields = 3
)
