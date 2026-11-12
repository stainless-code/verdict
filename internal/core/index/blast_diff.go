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
	blastBinary    = "Binary files "
	blastHunkParts = 3
)

// blastChanges reads a unified diff into each file's changed new-side lines:
// every added line, and both sides of every deleted line because the removed
// text sat between them. A section with no line hunks (a rename, a mode
// change) keeps its path and selects every declaration instead.
func blastChanges(diff string) []blastChange {
	var out []blastChange
	cur := -1
	newLine := 0
	inHunk := false
	for ln := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(ln, blastDiffGit):
			out, cur = openBlastChange(out, ln)
			newLine = 0
			inHunk = false
		case cur < 0:
		case strings.HasPrefix(ln, blastHunk):
			inHunk = true
			newLine = blastHunkSeed(ln)
		case !inHunk && blastFileHeader(ln):
		case strings.HasPrefix(ln, blastNoEOL):
		case strings.HasPrefix(ln, blastAdded):
			out[cur].lines = append(out[cur].lines, newLine)
			newLine++
		case strings.HasPrefix(ln, blastDeleted):
			out[cur].lines = append(out[cur].lines, max(newLine-1, 1), max(newLine, 1))
		default:
			out[cur].binary = out[cur].binary || strings.HasPrefix(ln, blastBinary)
			newLine++
		}
	}
	return finishBlastChanges(out)
}

// blastFileHeader reports a "---" or "+++" file header, which only precedes a
// section's first hunk; inside a hunk the same prefixes are content lines.
func blastFileHeader(ln string) bool {
	return strings.HasPrefix(ln, "+++") || strings.HasPrefix(ln, "---")
}

// openBlastChange appends the section a "diff --git" line names and returns its
// index; a header with no path opens no section, so its lines are skipped.
func openBlastChange(out []blastChange, ln string) ([]blastChange, int) {
	_, p := hunks.HeaderPaths(ln)
	if p == "" {
		return out, -1
	}
	out = append(out, blastChange{path: p})
	return out, len(out) - 1
}

// blastHunkSeed reads the new-side line number a hunk's first content line
// carries. A header whose new-side count is zero names the line before the
// change, so that hunk's content starts one line later.
func blastHunkSeed(ln string) int {
	fields := strings.Fields(ln)
	if len(fields) < blastHunkParts {
		return 0
	}
	spec, ok := strings.CutPrefix(fields[2], "+")
	if !ok {
		return 0
	}
	startRaw, countRaw, _ := strings.Cut(spec, ",")
	start, err := strconv.Atoi(startRaw)
	if err != nil {
		return 0
	}
	if countRaw == "" {
		return start
	}
	count, err := strconv.Atoi(countRaw)
	if err != nil {
		return 0
	}
	if count == 0 {
		return start + 1
	}
	return start
}

// finishBlastChanges orders the files and their lines deterministically, and
// turns a section the diff gave no line hunks into a whole-file change: a
// rename or a mode change names no touched line, so every declaration counts
// and the caller warns that it made that call. A binary section stays empty
// because the index never parses it.
func finishBlastChanges(in []blastChange) []blastChange {
	type section struct {
		lines  []int
		binary bool
	}
	byPath := make(map[string]*section, len(in))
	for _, ch := range in {
		sec := byPath[ch.path]
		if sec == nil {
			sec = &section{}
			byPath[ch.path] = sec
		}
		sec.lines = append(sec.lines, ch.lines...)
		sec.binary = sec.binary || ch.binary
	}
	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	out := make([]blastChange, 0, len(paths))
	for _, p := range paths {
		sec := byPath[p]
		lines := uniqueLines(sec.lines)
		hunkless := len(lines) == 0 && !sec.binary
		out = append(out, blastChange{path: p, lines: lines, all: hunkless, hunkless: hunkless})
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
