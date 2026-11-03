package hunks

import (
	"strconv"
	"strings"
)

// File is one `diff --git` section of a unified diff. One scan fills every
// derived value the report and fetch layers need, so each of them reads the
// diff once instead of rescanning it per file or per query. The slices and
// strings come first so the GC scans the fewest pointer bytes (govet
// fieldalignment), and consumers iterate the slice by index because the
// struct is larger than a register pair.
type File struct {
	Header     string   // the raw `diff --git` line
	OldPath    string   // a-side path, quote/space-safe
	Path       string   // b-side path, quote/space-safe
	Hunks      []Hunk   // parsed OLD/NEW ranges, in diff order
	AddedLines []string // added content lines without the leading "+"
	Size       int64    // section bytes, each line counting its newline
	Additions  int      // added content lines, the +++ header excluded
	Deletions  int      // removed content lines, the --- header excluded
	HunkCount  int      // "@@ " header lines (the overview hunk column)
	HunkMarks  int      // "@@"-prefixed lines (the coverage and hunk totals)
	StartLine  int      // 1-based line number of the header
	EndLine    int      // 1-based line number of the section's last line
}

// Files scans diffText once into one File per `diff --git` header. Content
// stats, hunk markers, and added lines belong to the section that carries
// them; lines before the first header contribute nothing.
func Files(diffText string) []File {
	if diffText == "" {
		return nil
	}
	s := &fileScan{cur: -1}
	for ln := range strings.SplitSeq(strings.TrimSuffix(diffText, newline), newline) {
		s.visit(ln)
	}
	s.finish()
	return s.files
}

// fileScan carries the section scan state across diff lines; its fields
// follow the order that keeps the GC's pointer scan shortest (govet
// fieldalignment).
type fileScan struct {
	name   string // hunk rows follow `rename to`, like Parse
	files  []File
	lineNo int // 1-based line being visited
	cur    int // open section index, -1 before the first header
}

// visit consumes one diff line: a header opens a section, and every other
// line either sizes the open section or contributes to it.
func (s *fileScan) visit(ln string) {
	s.lineNo++
	if strings.HasPrefix(ln, diffGitPrefix) {
		s.open(ln)
		return
	}
	if s.cur < 0 {
		return
	}
	s.files[s.cur].Size += int64(len(ln)) + 1
	s.count(ln)
}

// open closes the previous section and starts one for the header line.
func (s *fileScan) open(ln string) {
	if s.cur >= 0 {
		s.files[s.cur].EndLine = s.lineNo - 1
	}
	oldPath, newPath := HeaderPaths(ln)
	s.files = append(s.files, File{
		Header:    ln,
		OldPath:   oldPath,
		Path:      newPath,
		StartLine: s.lineNo,
		Size:      int64(len(ln)) + 1,
	})
	s.cur = len(s.files) - 1
	s.name = newPath
}

// count attributes one section line: hunk markers and rows, renames, and
// added or removed content.
func (s *fileScan) count(ln string) {
	f := &s.files[s.cur]
	switch {
	case strings.HasPrefix(ln, hunkHeader):
		f.HunkCount++
		f.HunkMarks++
		f.Hunks = append(f.Hunks, hunkRows(s.name, ln)...)
	case strings.HasPrefix(ln, "@@"):
		f.HunkMarks++
	case strings.HasPrefix(ln, renameTo):
		s.name = UnquotePath(strings.TrimPrefix(ln, renameTo))
	case addedContent(ln):
		f.Additions++
		f.AddedLines = append(f.AddedLines, ln[1:])
	case removedContent(ln):
		f.Deletions++
	}
}

// finish closes the last section at the final line.
func (s *fileScan) finish() {
	if s.cur >= 0 {
		s.files[s.cur].EndLine = s.lineNo
	}
}

// HeaderIndex renders the `grep -n "^diff --git"` toc line for the section.
func (f *File) HeaderIndex() string { return strconv.Itoa(f.StartLine) + ":" + f.Header }

// addedContent reports whether ln is an added content line (not the +++
// header).
func addedContent(ln string) bool {
	return strings.HasPrefix(ln, "+") && !strings.HasPrefix(ln, "+++")
}

// removedContent reports whether ln is a removed content line (not the ---
// header).
func removedContent(ln string) bool {
	return strings.HasPrefix(ln, "-") && !strings.HasPrefix(ln, "---")
}

// Diff scanning constants, mirroring the shell greps they replace.
const (
	// diffGitPrefix opens every file section.
	diffGitPrefix = "diff --git "
	// hunkHeader opens a counted hunk header.
	hunkHeader = "@@ "
	// renameTo renames the hunk rows of a section.
	renameTo = "rename to "
	// newline is the diff line terminator.
	newline = "\n"
)
