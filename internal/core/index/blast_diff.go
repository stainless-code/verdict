package indexcore

import (
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/hunks"
)

// Unified-diff prefixes blastChanges reads.
const (
	blastDiffGit   = "diff --git "
	blastHunk      = "@@ "
	blastDeleted   = "-"
	blastAdded     = "+"
	blastNoEOL     = `\`
	blastHunkParts = 3
)

// blastChanges reads a unified diff into each file's changed new-side lines:
// every added line, and both sides of every deleted line because the removed
// text sat between them. A file with no content change (a rename, a binary)
// keeps its path and no lines.
//

func blastChanges(diff string) []blastChange {
	var out []blastChange
	cur := -1
	newLine := 0
	for ln := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(ln, blastDiffGit):
			_, p := hunks.HeaderPaths(ln)
			if p == "" {
				cur = -1
				continue
			}
			out = append(out, blastChange{path: p})
			cur, newLine = len(out)-1, 0
		case cur < 0:
		case strings.HasPrefix(ln, blastHunk):
			newLine = blastHunkStart(ln)
		case strings.HasPrefix(ln, "+++"), strings.HasPrefix(ln, "---"):
		case strings.HasPrefix(ln, blastNoEOL):
		case strings.HasPrefix(ln, blastAdded):
			out[cur].lines = append(out[cur].lines, newLine)
			newLine++
		case strings.HasPrefix(ln, blastDeleted):
			out[cur].lines = append(out[cur].lines, max(newLine, 1), newLine+1)
		default:
			newLine++
		}
	}
	return finishBlastChanges(out)
}

// blastHunkStart reads one hunk header's new-side start line.
func blastHunkStart(ln string) int {
	fields := strings.Fields(ln)
	if len(fields) < blastHunkParts {
		return 0
	}
	spec, ok := strings.CutPrefix(fields[2], "+")
	if !ok {
		return 0
	}
	start, _, _ := strings.Cut(spec, ",")
	n, err := strconv.Atoi(start)
	if err != nil {
		return 0
	}
	return n
}

// finishBlastChanges orders the files and their lines deterministically.
func finishBlastChanges(in []blastChange) []blastChange {
	byPath := make(map[string][]int, len(in))
	for _, ch := range in {
		byPath[ch.path] = append(byPath[ch.path], ch.lines...)
	}
	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	out := make([]blastChange, 0, len(paths))
	for _, p := range paths {
		out = append(out, blastChange{path: p, lines: uniqueLines(byPath[p])})
	}
	return out
}

// uniqueLines sorts one file's touched lines and drops the repeats.
func uniqueLines(lines []int) []int {
	slices.Sort(lines)
	out := lines[:0]
	for i, line := range lines {
		if i == 0 || line != lines[i-1] {
			out = append(out, line)
		}
	}
	return out
}
