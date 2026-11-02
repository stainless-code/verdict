// Package hunks indexes diffs into side-aware hunk ranges and validates
// review anchors keep-biased: anything provably outside a hunk moves to the
// body, everything ambiguous (unknown path, non-numeric line) stays inline
// with a warning.
package hunks

import (
	"strconv"
	"strings"
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

// Parse indexes diffText the way build_hunks_tsv's awk does: `diff --git`
// headers name the file (greedy through the last " b/"), `rename to` headers
// rename it, and `@@` headers contribute OLD/NEW ranges. Zero-count sides
// contribute nothing.
func Parse(diffText string) []Hunk {
	var out []Hunk
	file := ""
	for ln := range strings.SplitSeq(diffText, "\n") {
		switch {
		case strings.HasPrefix(ln, "diff --git "):
			file = diffFile(ln)
		case strings.HasPrefix(ln, "rename to "):
			file = strings.TrimPrefix(ln, "rename to ")
		case strings.HasPrefix(ln, "@@ "):
			out = append(out, hunkRows(file, ln)...)
		}
	}
	return out
}

// diffFile strips the `diff --git a/... b/` prefix greedily through the last
// " b/", exactly like the awk sub(). Quoted paths keep their trailing quote,
// same as the shell (bug-for-bug: validation keys match the same mangling).
func diffFile(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	if _, after, ok := strings.CutLast(rest, gitBPrefix); ok {
		return after
	}
	return rest
}

// hunkRows parses one @@ header into OLD/NEW rows.
func hunkRows(file, ln string) []Hunk {
	f := strings.Fields(ln)
	if len(f) < minHeaderFields {
		return nil
	}
	oldStart, oldCount := parseRange(strings.TrimPrefix(f[1], "-"))
	newStart, newCount := parseRange(strings.TrimPrefix(f[2], "+"))
	var out []Hunk
	if oldCount > 0 {
		out = append(out, Hunk{File: file, Side: "OLD", Start: oldStart, End: oldStart + oldCount - 1})
	}
	if newCount > 0 {
		out = append(out, Hunk{File: file, Side: "NEW", Start: newStart, End: newStart + newCount - 1})
	}
	return out
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
	return awkNum(start), awkNum(count)
}

// awkNum converts like awk's string-to-number: the longest leading numeric
// prefix, else 0. Headers are canonical in practice; this only matters for
// hand-crafted degenerate diffs.
func awkNum(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0
	}
	return n
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

// Hunk header parsing constants.
const (
	// minHeaderFields is the minimum @@-header field count.
	minHeaderFields = 3
	// gitBPrefix is the " b/" separator in `diff --git` lines.
	gitBPrefix = " b/"
)
