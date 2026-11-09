package hunks

import (
	"os"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

// TSV columns, in the order fetch writes them.
const (
	colFile  = 0
	colSide  = 1
	colStart = 2
	colEnd   = 3
	numCols  = 4
)

// ParseTSV reads hunks.tsv bytes into rows: file, side, start, end, tab
// separated. A row with the wrong column count, a side that is neither OLD nor
// NEW, or a non-numeric or reversed range is skipped rather than guessed — the
// keep-biased reading is the one that leaves a changed line unattested, which
// the coverage note and gate report instead of hiding.
func ParseTSV(text string) []Hunk {
	var out []Hunk
	for line := range strings.SplitSeq(strings.TrimSuffix(text, "\n"), "\n") {
		if h, ok := parseRow(line); ok {
			out = append(out, h)
		}
	}
	return out
}

// parseRow accepts exactly one well-formed row: a blank line, a wrong column
// count, an unknown side, or a non-numeric or reversed range reports false
// instead of being guessed at.
func parseRow(line string) (Hunk, bool) {
	if line == "" {
		return Hunk{}, false
	}
	cols := strings.Split(line, "\t")
	if len(cols) != numCols {
		return Hunk{}, false
	}
	start, errS := strconv.Atoi(cols[colStart])
	end, errE := strconv.Atoi(cols[colEnd])
	if errS != nil || errE != nil || start < 1 || end < start {
		return Hunk{}, false
	}
	if cols[colSide] != "OLD" && cols[colSide] != "NEW" {
		return Hunk{}, false
	}
	return Hunk{File: cols[colFile], Side: cols[colSide], Start: start, End: end}, true
}

// Load reads dir/hunks.tsv. A missing file is not an error: a bundle built by
// hand may predate the snapshot, and the caller decides whether to rebuild it.
func Load(dir string) ([]Hunk, error) {
	raw, err := os.ReadFile(dir + "/hunks.tsv")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, jq.Sealed("read "+dir+"/hunks.tsv", err)
	}
	return ParseTSV(string(raw)), nil
}
