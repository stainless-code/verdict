package hunks

import "strings"

// Sections splits diffText at its `diff --git` headers: the bytes before the
// first header (git writes none for an ordinary diff, but a hand-made or
// combined one may), then one string per file section, each holding exactly the
// lines the diff gave it. Joining the preamble and every section in the input
// order reproduces diffText byte for byte, which is what lets a chunked bundle
// carry the same sections in another order without losing a hunk.
func Sections(diffText string) (preamble string, sections []string) {
	if diffText == "" {
		return "", nil
	}
	var split sectionSplit
	for line := range strings.SplitAfterSeq(diffText, newline) {
		split.line(line)
	}
	return split.done()
}

// sectionSplit accumulates the split as the scan walks the diff, so the loop
// body stays one decision per line.
type sectionSplit struct {
	before   strings.Builder
	current  strings.Builder
	sections []string
	open     bool
}

// line files one diff line: a header closes the open section and opens the next
// one, and everything else lands in the section it belongs to.
func (s *sectionSplit) line(line string) {
	switch {
	case line == "":
	case strings.HasPrefix(line, diffGitPrefix):
		s.close()
		s.open = true
		s.current.WriteString(line)
	case s.open:
		s.current.WriteString(line)
	default:
		s.before.WriteString(line)
	}
}

// close seals the open section, if any.
func (s *sectionSplit) close() {
	if s.open {
		s.sections = append(s.sections, s.current.String())
		s.current.Reset()
	}
	s.open = false
}

// done seals the last section and reports the preamble and the sections.
func (s *sectionSplit) done() (string, []string) {
	s.close()
	return s.before.String(), s.sections
}
