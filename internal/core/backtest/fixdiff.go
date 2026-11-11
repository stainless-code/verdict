package backtest

import (
	"regexp"
	"strconv"
	"strings"
)

// diffLine is one old-side line a fix deleted, at the line number it held
// before the fix.
type diffLine struct {
	Path string
	Text string
	Line int
}

// fixDiff is the parsed body of `git show -U0`: the deleted lines, the added
// line texts, and whether git reported a binary file.
type fixDiff struct {
	Deleted []diffLine
	Added   []string
	Binary  bool
}

// hunkHeaderRe matches one unified-diff hunk header. A missing count reads 1.
var hunkHeaderRe = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@`)

// parseFixHunks parses `git show -U0 --no-renames` output. It never fails: a
// malformed line ends the hunk it appears in, so fuzzed input yields fewer
// lines rather than a panic.
//
//nolint:revive,cyclop // one branch per unified-diff line shape reads truer than a state machine
func parseFixHunks(diff string) fixDiff {
	var out fixDiff
	oldPath, oldLine := "", 0
	inHunk := false
	for line := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			oldPath, inHunk = "", false
		case !inHunk && strings.HasPrefix(line, "--- "):
			oldPath = diffPath(strings.TrimPrefix(line, "--- "), "a/")
		case !inHunk && strings.HasPrefix(line, "+++ "):
			// The new path is irrelevant: deleted lines belong to the old side.
		case strings.HasPrefix(line, "Binary files "), line == "GIT binary patch":
			out.Binary = true
			inHunk = false
		case strings.HasPrefix(line, "@@ "):
			start, ok := hunkStart(line)
			if !ok {
				inHunk = false
				continue
			}
			oldLine, inHunk = start, true
		case !inHunk:
			// Diff metadata before the first hunk.
		case strings.HasPrefix(line, "-"):
			if oldPath != "" && oldLine > 0 {
				out.Deleted = append(out.Deleted, diffLine{Path: oldPath, Line: oldLine, Text: strings.TrimPrefix(line, "-")})
			}
			oldLine++
		case strings.HasPrefix(line, "+"):
			out.Added = append(out.Added, strings.TrimPrefix(line, "+"))
		case strings.HasPrefix(line, " "):
			oldLine++
		case strings.HasPrefix(line, `\`):
			// "\ No newline at end of file" carries no line.
		default:
			inHunk = false
		}
	}
	return out
}

// hunkStart reads the old-side start line from a hunk header.
func hunkStart(line string) (int, bool) {
	match := hunkHeaderRe.FindStringSubmatch(line)
	if match == nil {
		return 0, false
	}
	start, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return start, true
}

// diffPath cleans one `---`/`+++` path: drops the a/ or b/ prefix, unquotes a
// git-quoted name, and reads /dev/null as absent.
func diffPath(raw, prefix string) string {
	path := strings.TrimSpace(raw)
	if path == "/dev/null" {
		return ""
	}
	if unquoted, ok := unquoteGitPath(path); ok {
		path = unquoted
	}
	return strings.TrimPrefix(path, prefix)
}

// unquoteGitPath unquotes a C-style git path; an unparseable quote is returned
// with its delimiters stripped.
func unquoteGitPath(path string) (string, bool) {
	if len(path) < 2 || path[0] != '"' || path[len(path)-1] != '"' {
		return path, false
	}
	if unquoted, err := strconv.Unquote(path); err == nil {
		return unquoted, true
	}
	return path[1 : len(path)-1], true
}
