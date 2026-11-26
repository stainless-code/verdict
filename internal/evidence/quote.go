package evidence

import (
	"math"
	"strings"
	"unicode"
)

// A2 quote-matching bounds and tuning constants.
const (
	MaxQuoteLines    = 64
	MaxQuoteBytes    = 8192
	WindowSlop       = 3
	MinFragmentRunes = 12

	zeroWidthSpace = "\u200b"
	crlfSeq        = "\r\n"
	asciiSpace     = " "
)

// QuoteRequest names the diff path, side, line window, and quote text to
// verify against a DiffIndex.
type QuoteRequest struct {
	Path      string
	Side      string
	Quote     string
	Line      int
	StartLine int
}

// QuoteResult reports whether a quote verified against full.diff bytes.
type QuoteResult struct {
	Status    string
	Reason    string
	HunkKey   string
	MatchFrom int
	MatchTo   int
}

// Verified reports whether the quote matched the diff under A2 rules.
func (r QuoteResult) Verified() bool {
	return r.Status == StatusVerified
}

// NormalizeQuote applies the §2.3 rule 1 normalization: replace CRLF with LF,
// delete U+200B, collapse every run of Unicode whitespace per line to one ASCII
// space, trim both ends of each line, drop empty leading and trailing lines,
// and join the remaining lines with LF.
func NormalizeQuote(s string) (string, []string) {
	cleaned := strings.ReplaceAll(strings.ReplaceAll(s, crlfSeq, diffNewline), zeroWidthSpace, "")
	raw := strings.Split(cleaned, diffNewline)
	lines := make([]string, len(raw))
	for i, ln := range raw {
		lines[i] = normalizeLine(ln)
	}
	start := 0
	for start < len(lines) && lines[start] == "" {
		start++
	}
	end := len(lines)
	for end > start && lines[end-1] == "" {
		end--
	}
	trimmed := lines[start:end]
	if len(trimmed) == 0 {
		return "", nil
	}
	return strings.Join(trimmed, diffNewline), trimmed
}

// MatchQuote verifies req against idx using the A2 quote matcher.
func MatchQuote(idx *DiffIndex, req QuoteRequest) QuoteResult {
	normText, quoteLines := NormalizeQuote(req.Quote)
	if len(quoteLines) > MaxQuoteLines || len(normText) > MaxQuoteBytes {
		return unverified(ReasonQuoteTooLarge)
	}
	if !validRange(req.StartLine, req.Line) {
		return unverified(ReasonRangeInvalid)
	}
	df, side, reason := resolveQuoteTarget(idx, req)
	if reason != "" {
		return unverified(reason)
	}
	startLine, winStart, winEnd := quoteWindow(req.StartLine, req.Line)
	if res, matched := matchInSingleHunk(df, side, quoteLines, winStart, winEnd); matched {
		return res
	}
	return diagnoseQuoteMismatch(df, side, quoteLines, startLine, req.Line, winStart, winEnd)
}

// resolveQuoteTarget validates the path, side, head presence, and line floor.
func resolveQuoteTarget(idx *DiffIndex, req QuoteRequest) (*DiffFile, string, string) {
	df, pathReason := idx.File(req.Path)
	if pathReason != "" {
		return nil, "", pathReason
	}
	side, ok := canonicalSide(req.Side)
	if !ok {
		return nil, "", ReasonLineNotInBundle
	}
	if side == SideRight && df.AbsentAtHead() {
		return nil, "", ReasonFileAbsentAtHead
	}
	if req.Line < 1 {
		return nil, "", ReasonLineNotInBundle
	}
	return df, side, ""
}

// validRange reports whether the cited range is one a review can mean: a
// start_line is optional, but it is never negative and never follows the line
// it starts the quote from. An invalid range is reported, never silently
// repaired into the line it was meant to bound.
func validRange(startLine, line int) bool {
	if startLine < 0 {
		return false
	}
	return startLine == 0 || startLine <= line
}

// quoteWindow computes the effective start line and the [start-3, line+3]
// overlap window.
func quoteWindow(reqStart, reqLine int) (startLine, winStart, winEnd int) {
	startLine = reqStart
	if startLine <= 0 {
		startLine = reqLine
	}
	return startLine, subClampedMin(startLine, WindowSlop, 1), addClampedMax(reqLine, WindowSlop)
}

// unverified builds an unverified QuoteResult with the given machine reason.
func unverified(reason string) QuoteResult {
	return QuoteResult{Status: StatusUnverified, Reason: reason}
}

// canonicalSide defaults an empty side to RIGHT and validates RIGHT/LEFT. The
// empty default stays because a QuoteRequest cannot tell an absent side from an
// empty one and a finding's absent side means RIGHT; a present empty side is
// refused where it is parsed (record's side check, the cross check, and the
// verdicts.json parser), never here.
func canonicalSide(side string) (string, bool) {
	if side == "" || side == SideRight {
		return SideRight, true
	}
	if side == SideLeft {
		return SideLeft, true
	}
	return "", false
}

// matchInSingleHunk checks each hunk of df on side for a contiguous A2 match
// overlapping [winStart, winEnd].
func matchInSingleHunk(df *DiffFile, side string, quoteLines []string, winStart, winEnd int) (QuoteResult, bool) {
	if len(quoteLines) == 0 {
		return QuoteResult{}, false
	}
	for i := range df.Hunks {
		h := &df.Hunks[i]
		lines, hStart, hEnd := hunkSideLines(h, side)
		if len(lines) == 0 || !rangesOverlap(hStart, hEnd, winStart, winEnd) {
			continue
		}
		if mFrom, mTo, ok := matchLinesInHunk(lines, hStart, quoteLines, winStart, winEnd); ok {
			return QuoteResult{
				Status:    StatusVerified,
				HunkKey:   h.Key(),
				MatchFrom: mFrom,
				MatchTo:   mTo,
			}, true
		}
	}
	return QuoteResult{}, false
}

// diagnoseQuoteMismatch picks the most specific A2 failure reason after no
// single hunk matched on the requested side.
func diagnoseQuoteMismatch(
	df *DiffFile,
	side string,
	quoteLines []string,
	startLine, line, winStart, winEnd int,
) QuoteResult {
	// A cited line between two hunks is not in the bundle; only a range that
	// really spans two hunks can cross them.
	if !lineInsideHunk(df, side, line) {
		return unverified(ReasonLineNotInBundle)
	}
	if crossesHunks(df, side, quoteLines, startLine, line, winStart, winEnd) {
		return unverified(ReasonRangeCrossesHunks)
	}
	if side == SideRight && matchesDeletedLine(df, quoteLines, winStart, winEnd) {
		return unverified(ReasonDeletedLine)
	}
	if !anyHunkOverlaps(df, side, winStart, winEnd) {
		return unverified(ReasonLineNotInBundle)
	}
	return unverified(ReasonTextDiffers)
}

// lineInsideHunk reports whether the cited line falls inside one hunk's span
// on side. A line in the gap between two hunks is not in the bundle at all.
func lineInsideHunk(df *DiffFile, side string, line int) bool {
	for i := range df.Hunks {
		lines, hStart, hEnd := hunkSideLines(&df.Hunks[i], side)
		if len(lines) > 0 && line >= hStart && line <= hEnd {
			return true
		}
	}
	return false
}

// crossesHunks reports whether the cited range [startLine, line] touches two or
// more hunks on side, or a multi-line quote matches across a hunk boundary in
// the window. A quote of fewer than two lines crosses nothing: an empty one
// falls through to text-differs.
func crossesHunks(df *DiffFile, side string, quoteLines []string, startLine, line, winStart, winEnd int) bool {
	if startLine < line && countOverlappingHunks(df, side, startLine, line) >= 2 {
		return true
	}
	if len(quoteLines) < 2 {
		return false
	}
	return matchAcrossHunks(df, side, quoteLines, winStart, winEnd)
}

// countOverlappingHunks returns how many hunks on side overlap [from, to].
func countOverlappingHunks(df *DiffFile, side string, from, to int) int {
	n := 0
	for i := range df.Hunks {
		lines, hStart, hEnd := hunkSideLines(&df.Hunks[i], side)
		if len(lines) > 0 && rangesOverlap(hStart, hEnd, from, to) {
			n++
		}
	}
	return n
}

// stitchedLine tracks one line's origin across concatenated hunks of a file.
type stitchedLine struct {
	text    string
	lineNo  int
	hunkIdx int
}

// matchAcrossHunks reports whether quoteLines matches contiguously across two
// or more hunks in df on side, with the matched line span overlapping
// [winStart, winEnd].
func matchAcrossHunks(df *DiffFile, side string, quoteLines []string, winStart, winEnd int) bool {
	stream := stitchHunkLines(df, side)
	n := len(quoteLines)
	if len(stream) < n {
		return false
	}
	for i := 0; i <= len(stream)-n; i++ {
		if crossesWindow(stream[i:i+n], quoteLines, winStart, winEnd) {
			return true
		}
	}
	return false
}

// crossesWindow reports whether window spans two distinct hunks, overlaps
// [winStart, winEnd], and matches quoteLines.
func crossesWindow(window []stitchedLine, quoteLines []string, winStart, winEnd int) bool {
	last := len(window) - 1
	if window[0].hunkIdx == window[last].hunkIdx {
		return false
	}
	if !rangesOverlap(window[0].lineNo, window[last].lineNo, winStart, winEnd) {
		return false
	}
	return matchStitchedAt(window, quoteLines)
}

// stitchHunkLines concatenates all hunk lines on side in diff order.
func stitchHunkLines(df *DiffFile, side string) []stitchedLine {
	var stream []stitchedLine
	for hi := range df.Hunks {
		lines, hStart, _ := hunkSideLines(&df.Hunks[hi], side)
		for j, text := range lines {
			stream = append(stream, stitchedLine{
				text:    normalizeLine(text),
				lineNo:  addClampedMax(hStart, j),
				hunkIdx: hi,
			})
		}
	}
	return stream
}

// matchStitchedAt applies the multi-line match rule to one stitched slice.
func matchStitchedAt(window []stitchedLine, quoteLines []string) bool {
	last := len(quoteLines) - 1
	if !matchMultiLineEdges(window[0].text, window[last].text, quoteLines) {
		return false
	}
	for j := 1; j < last; j++ {
		if window[j].text != quoteLines[j] {
			return false
		}
	}
	return true
}

// matchesDeletedLine reports whether quoteLines matches OLD-side lines
// containing at least one removed (`-`) line inside a hunk relevant to
// [winStart, winEnd].
func matchesDeletedLine(df *DiffFile, quoteLines []string, winStart, winEnd int) bool {
	if len(quoteLines) == 0 {
		return false
	}
	for i := range df.Hunks {
		if hunkMatchesDeleted(&df.Hunks[i], quoteLines, winStart, winEnd) {
			return true
		}
	}
	return false
}

// hunkMatchesDeleted checks one hunk for a deleted-line match in scope for
// [winStart, winEnd].
func hunkMatchesDeleted(h *DiffHunk, quoteLines []string, winStart, winEnd int) bool {
	if len(h.OldLines) == 0 {
		return false
	}
	newOverlaps := len(h.NewLines) > 0 && rangesOverlap(h.NewStart, h.NewEnd, winStart, winEnd)
	oldOverlaps := rangesOverlap(h.OldStart, h.OldEnd, winStart, winEnd)
	if !newOverlaps && !oldOverlaps {
		return false
	}
	return matchDeletedInHunk(h, quoteLines, winStart, winEnd, newOverlaps)
}

// matchDeletedInHunk scans one hunk's OLD lines for a match of quoteLines that
// touches at least one removed (`-`) line.
func matchDeletedInHunk(h *DiffHunk, quoteLines []string, winStart, winEnd int, hunkNewOverlaps bool) bool {
	n := len(quoteLines)
	if len(h.OldLines) < n {
		return false
	}
	for i := 0; i <= len(h.OldLines)-n; i++ {
		if deletedMatchAt(h, i, quoteLines, winStart, winEnd, hunkNewOverlaps) {
			return true
		}
	}
	return false
}

// deletedMatchAt tests one OLD-side window inside h for a match touching a
// deleted (`-`) line.
func deletedMatchAt(h *DiffHunk, i int, quoteLines []string, winStart, winEnd int, hunkNewOverlaps bool) bool {
	n := len(quoteLines)
	mFrom := addClampedMax(h.OldStart, i)
	mTo := addClampedMax(h.OldStart, i+n-1)
	if !hunkNewOverlaps && !rangesOverlap(mFrom, mTo, winStart, winEnd) {
		return false
	}
	return hasDeletedInSlice(h.OldIsDeleted[i:i+n]) && matchLinesAt(h.OldLines[i:i+n], quoteLines)
}

// hasDeletedInSlice reports whether any flag in flags is true.
func hasDeletedInSlice(flags []bool) bool {
	for _, del := range flags {
		if del {
			return true
		}
	}
	return false
}

// anyHunkOverlaps reports whether at least one hunk of df on side overlaps
// [winStart, winEnd].
func anyHunkOverlaps(df *DiffFile, side string, winStart, winEnd int) bool {
	return countOverlappingHunks(df, side, winStart, winEnd) > 0
}

// hunkSideLines returns the raw lines and inclusive line span of h on side.
func hunkSideLines(h *DiffHunk, side string) ([]string, int, int) {
	if side == SideLeft {
		if len(h.OldLines) == 0 {
			return nil, 0, 0
		}
		return h.OldLines, h.OldStart, addClampedMax(h.OldStart, len(h.OldLines)-1)
	}
	if len(h.NewLines) == 0 {
		return nil, 0, 0
	}
	return h.NewLines, h.NewStart, addClampedMax(h.NewStart, len(h.NewLines)-1)
}

// matchLinesInHunk searches hunkLines (starting at hunkStart) for a contiguous
// A2 match of quoteLines whose matched line range overlaps [winStart, winEnd].
func matchLinesInHunk(hunkLines []string, hunkStart int, quoteLines []string, winStart, winEnd int) (int, int, bool) {
	n := len(quoteLines)
	if n == 0 || len(hunkLines) < n {
		return 0, 0, false
	}
	for i := 0; i <= len(hunkLines)-n; i++ {
		mFrom := addClampedMax(hunkStart, i)
		mTo := addClampedMax(hunkStart, i+n-1)
		if !rangesOverlap(mFrom, mTo, winStart, winEnd) {
			continue
		}
		if matchLinesAt(hunkLines[i:i+n], quoteLines) {
			return mFrom, mTo, true
		}
	}
	return 0, 0, false
}

// matchLinesAt applies the A2 single-line or multi-line rule to an equal-length
// slice of raw hunk lines against pre-normalized quoteLines.
func matchLinesAt(rawLines, quoteLines []string) bool {
	if len(quoteLines) == 1 {
		return matchSingleLine(rawLines[0], quoteLines[0])
	}
	return matchMultiLine(rawLines, quoteLines)
}

// matchSingleLine checks one normalized quote line against one raw hunk line:
// substring when >= MinFragmentRunes non-space runes, else whole-line equality.
func matchSingleLine(rawLine, q string) bool {
	actual := normalizeLine(rawLine)
	if nonSpaceRunes(q) >= MinFragmentRunes {
		return strings.Contains(actual, q)
	}
	return actual == q
}

// matchMultiLine checks a multi-line quote under A2/A2b: first line suffix,
// interior lines exact, last line prefix; any partial edge requires at least
// MinFragmentRunes non-space characters across the quote.
func matchMultiLine(rawLines, quoteLines []string) bool {
	last := len(quoteLines) - 1
	if !matchMultiLineEdges(normalizeLine(rawLines[0]), normalizeLine(rawLines[last]), quoteLines) {
		return false
	}
	for j := 1; j < last; j++ {
		if normalizeLine(rawLines[j]) != quoteLines[j] {
			return false
		}
	}
	return true
}

// matchMultiLineEdges verifies the first-line suffix, last-line prefix, and
// A2b minimum non-space character count when either edge is partial.
func matchMultiLineEdges(firstActual, lastActual string, quoteLines []string) bool {
	firstQuote := quoteLines[0]
	lastQuote := quoteLines[len(quoteLines)-1]
	if !strings.HasSuffix(firstActual, firstQuote) || !strings.HasPrefix(lastActual, lastQuote) {
		return false
	}
	if firstActual == firstQuote && lastActual == lastQuote {
		return true
	}
	return totalNonSpaceRunes(quoteLines) >= MinFragmentRunes
}

// totalNonSpaceRunes counts non-whitespace runes across all lines of a quote.
func totalNonSpaceRunes(lines []string) int {
	total := 0
	for _, ln := range lines {
		total += nonSpaceRunes(ln)
	}
	return total
}

// normalizeLine strips U+200B, collapses runs of Unicode whitespace to one
// ASCII space, and trims both ends.
func normalizeLine(s string) string {
	s = strings.ReplaceAll(s, zeroWidthSpace, "")
	return strings.Join(strings.Fields(s), asciiSpace)
}

// nonSpaceRunes counts non-whitespace Unicode code points in s.
func nonSpaceRunes(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

// rangesOverlap reports whether closed intervals [aStart, aEnd] and
// [bStart, bEnd] share at least one integer.
func rangesOverlap(aStart, aEnd, bStart, bEnd int) bool {
	return aStart <= bEnd && bStart <= aEnd
}

// addClampedMax returns a + b, saturating at [math.MaxInt] on overflow.
func addClampedMax(a, b int) int {
	if b > 0 && a > math.MaxInt-b {
		return math.MaxInt
	}
	return a + b
}

// subClampedMin returns max(floor, a - b) without underflowing below
// [math.MinInt].
func subClampedMin(a, b, floor int) int {
	if a < floor+b {
		return floor
	}
	return a - b
}
