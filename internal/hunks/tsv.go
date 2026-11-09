package hunks

import (
	"os"
	"strconv"
	"strings"
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
		if line == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) != numCols {
			continue
		}
		start, errS := strconv.Atoi(cols[colStart])
		end, errE := strconv.Atoi(cols[colEnd])
		if errS != nil || errE != nil || start < 1 || end < start {
			continue
		}
		if cols[colSide] != "OLD" && cols[colSide] != "NEW" {
			continue
		}
		out = append(out, Hunk{File: cols[colFile], Side: cols[colSide], Start: start, End: end})
	}
	return out
}

// Load reads dir/hunks.tsv. A missing file is not an error: a bundle built by
// hand may predate the snapshot, and the caller decides whether to rebuild it.
func Load(dir string) ([]Hunk, error) {
	raw, err := os.ReadFile(dir + "/hunks.tsv")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return ParseTSV(string(raw)), nil
}
