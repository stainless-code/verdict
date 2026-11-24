package coverage

// A delta bundle's incremental.diff is a unified diff from the previous head to
// the current one, while prior-coverage.json holds the previous head's line
// numbers. Grading one against the other without translating the coordinates
// credits whichever lines now sit at those numbers: one line inserted above a
// prior range silently moves the whole credit up, so an unattested line reads
// as read and an attested one as a gap. The translation here is exact — every
// surviving prior line maps through the diff's own hunks — and it fails closed:
// a file whose section is binary, a rename, or malformed carries no prior
// credit at all.

import (
	"math"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/hunks"
)

// Section and hunk markers the translation reads.
const (
	// hunkOpenPrefix opens a hunk header inside a file section.
	hunkOpenPrefix = "@@"
	// contextLine, addedLine, removedLine, and noNewlineEscape open the four
	// hunk body line kinds: context survives, removed and added do not, and
	// the escape line carries no content at all.
	contextLine     = ' '
	addedLine       = '+'
	removedLine     = '-'
	noNewlineEscape = '\\'
	// unmappedMarkers open a section line whose coordinates cannot be
	// followed: binary content, or a file that changed paths.
	binaryFileMark  = "Binary files "
	binaryPatchMark = "GIT binary patch"
	renameFromMark  = "rename from "
	renameToMark    = "rename to "
	deletedFileMark = "+++ /dev/null"
)

// shiftSegment is one run of prior-head lines that survives into the current
// head: prior line n in [start, end] is current line n+delta.
type shiftSegment struct{ start, end, delta int }

// fileShift is one file's prior-head to current-head translation. failed marks
// a section the parser could not map, which withholds that file's credit
// instead of guessing at its coordinates.
type fileShift struct {
	segments []shiftSegment
	failed   bool
}

// hunkHead is one parsed `@@ -oldStart,oldCount +newStart,newCount @@` header.
type hunkHead struct{ oldStart, oldCount, newStart, newCount int }

// translate maps prior-head ranges into current-head ranges, dropping every
// line the delta deleted or re-modified. A refused section maps to nothing.
func (fs *fileShift) translate(ranges []Range) []Range {
	if fs == nil || fs.failed {
		return nil
	}
	var out []Range
	for _, r := range ranges {
		out = append(out, fs.translateOne(r)...)
	}
	return merge(out)
}

// translateOne maps one prior range through the segments that survived.
func (fs *fileShift) translateOne(r Range) []Range {
	var out []Range
	for _, seg := range fs.segments {
		if lo, hi := max(r.Start, seg.start), min(r.End, seg.end); lo <= hi {
			out = append(out, Range{Start: lo + seg.delta, End: hi + seg.delta})
		}
	}
	return out
}

// loadIncrementalShifts reads the bundle's delta marker into one coordinate
// translation per file it names. ok is false when there is no marker at all:
// the bundle is then a full round and prior credit stays out of grading.
func loadIncrementalShifts(dir string) (map[string]*fileShift, bool) {
	raw, err := os.ReadFile(dir + incrementalFile)
	if err != nil {
		return nil, false
	}
	return parseShifts(string(raw))
}

// parseShifts reads every file section of a delta diff into its translation. A
// section whose header names no path is skipped: no recorded prior entry can
// name the empty path, so it credits nothing either way.
func parseShifts(raw string) (map[string]*fileShift, bool) {
	_, sections := hunks.Sections(raw)
	if len(sections) == 0 {
		return nil, false
	}
	shifts := make(map[string]*fileShift, len(sections))
	for _, section := range sections {
		file, shift := parseSection(section)
		if file != "" {
			shifts[file] = shift
		}
	}
	return shifts, true
}

// parseSection reads one `diff --git` section into its file's translation, or
// marks it failed when its coordinates cannot be mapped.
func parseSection(section string) (string, *fileShift) {
	lines := strings.Split(strings.TrimSuffix(section, "\n"), "\n")
	_, file := hunks.HeaderPaths(lines[0])
	if file == "" {
		return "", nil
	}
	shift := &fileShift{}
	if unmappable(lines) || !shift.read(lines[1:]) {
		shift.failed = true
		shift.segments = nil
	}
	return file, shift
}

// unmappable reports a section whose line coordinates the translation cannot
// follow: binary content, or a file that changed paths, where every recorded
// line number belongs to content the current head no longer holds.
func unmappable(lines []string) bool {
	return slices.ContainsFunc(lines, unmappableLine)
}

// unmappableLine reports one section line that refuses the translation. A
// deleted file belongs here: the head holds none of its lines, so crediting the
// recorded ranges would point at whatever content took their place.
func unmappableLine(ln string) bool {
	return strings.HasPrefix(ln, binaryFileMark) || strings.HasPrefix(ln, binaryPatchMark) ||
		strings.HasPrefix(ln, renameFromMark) || strings.HasPrefix(ln, renameToMark) ||
		strings.HasPrefix(ln, deletedFileMark)
}

// hunkParse is one hunk header with the body lines that follow it. The body
// slice comes first so the GC scans the fewest pointer bytes (fieldalignment).
type hunkParse struct {
	body []string
	head hunkHead
}

// read walks the section's hunks and records the prior lines that survive: the
// unchanged runs between hunks and the context runs inside them. It reports
// false when a hunk body disagrees with its own header, because a body that
// does not add up cannot place any line.
func (fs *fileShift) read(lines []string) bool {
	parsed, ok := hunkHeads(lines)
	if !ok {
		return false
	}
	oldNext, newNext := 1, 1
	for _, h := range parsed {
		if !fs.apply(h, oldNext, newNext) {
			return false
		}
		oldNext, newNext = h.head.resume()
	}
	// Everything past the last hunk is unchanged, so it shifts by the running
	// delta and stays mappable to the end of the file.
	fs.keep(oldNext, math.MaxInt, newNext-oldNext)
	return true
}

// apply records one hunk: the unchanged run before it, then its own context
// lines. A hunk that ends before the last consumed line rewrites lines twice —
// the delta is not a diff of one file's two states — so nothing can be placed.
// A pure insertion may name the last consumed line, which is why the bound is
// the gap end rather than the hunk's old start.
func (fs *fileShift) apply(h hunkParse, oldNext, newNext int) bool {
	if h.head.gapEnd() < oldNext-1 {
		return false
	}
	fs.keep(oldNext, h.head.gapEnd(), newNext-oldNext)
	return fs.keepContext(h.head, h.body)
}

// hunkHeads collects the section's hunk headers with the body lines that follow
// each one. A header that will not parse refuses the whole section.
func hunkHeads(lines []string) ([]hunkParse, bool) {
	var out []hunkParse
	for i := 0; i < len(lines); {
		if !strings.HasPrefix(lines[i], hunkOpenPrefix) {
			i++
			continue
		}
		head, ok := parseHunkHeader(lines[i])
		if !ok {
			return nil, false
		}
		body, end := hunkBody(lines, i+1)
		out = append(out, hunkParse{head: head, body: body})
		i = end
	}
	return out, true
}

// keep records one unchanged run: prior lines [start, end] keep their order and
// move by delta.
func (fs *fileShift) keep(start, end, delta int) {
	if end >= start {
		fs.segments = append(fs.segments, shiftSegment{start: start, end: end, delta: delta})
	}
}

// keepContext records the hunk's context lines through one run cursor: a
// context line survives where the diff puts it, a removed line is invalidated,
// and an added line exists only in the current head, so it can never carry
// prior credit.
func (fs *fileShift) keepContext(head hunkHead, body []string) bool {
	run := &hunkRun{old: head.oldStart, new: head.newStart}
	for _, ln := range body {
		if !run.feed(ln) {
			return false
		}
	}
	run.flush()
	fs.segments = append(fs.segments, run.segments...)
	return run.oldUsed == head.oldCount && run.newUsed == head.newCount
}

// hunkRun accumulates one hunk body into the context segments that survive.
type hunkRun struct {
	cur              *shiftSegment
	segments         []shiftSegment
	old, new         int
	oldUsed, newUsed int
}

// feed consumes one body line, reporting false for a line no hunk body holds.
func (r *hunkRun) feed(ln string) bool {
	switch ln[0] {
	case noNewlineEscape:
		return true
	case contextLine:
		r.context()
	case removedLine:
		r.removed()
	case addedLine:
		r.added()
	default:
		return false
	}
	return true
}

// context carries one context line: both heads advance, and the line joins the
// current run (whose delta cannot change while context is contiguous).
func (r *hunkRun) context() {
	if r.cur == nil {
		r.cur = &shiftSegment{start: r.old, end: r.old, delta: r.new - r.old}
	}
	r.cur.end = r.old
	r.old, r.new = r.old+1, r.new+1
	r.oldUsed, r.newUsed = r.oldUsed+1, r.newUsed+1
}

// removed consumes one deleted line, which ends the run: the line has no
// current-head counterpart to carry credit to.
func (r *hunkRun) removed() {
	r.flush()
	r.old, r.oldUsed = r.old+1, r.oldUsed+1
}

// added consumes one added line, which ends the run: the line is new, so no
// prior credit can reach it.
func (r *hunkRun) added() {
	r.flush()
	r.new, r.newUsed = r.new+1, r.newUsed+1
}

// flush closes the open context run, if any.
func (r *hunkRun) flush() {
	if r.cur != nil {
		r.segments = append(r.segments, *r.cur)
		r.cur = nil
	}
}

// gapEnd is the last prior-head line the section leaves untouched before this
// hunk. A pure insertion (old count 0, `@@ -K,0 +M,N @@`) sits after line K, so
// K itself is unchanged; every other hunk starts at a line it rewrites.
func (h hunkHead) gapEnd() int {
	if h.oldCount == 0 {
		return h.oldStart
	}
	return h.oldStart - 1
}

// resume is the first prior-head and current-head line after the hunk. Either
// span may be empty: a zero count names the line before the hunk's position,
// so its following line is start+1 — that is what makes a pure insertion leave
// old line K alone and shift everything past it.
func (h hunkHead) resume() (int, int) {
	return spanEnd(h.oldStart, h.oldCount), spanEnd(h.newStart, h.newCount)
}

// spanEnd is the first line after one span, by the count-zero convention above.
func spanEnd(start, count int) int {
	if count == 0 {
		return start + 1
	}
	return start + count
}

// hunkBody returns the body lines starting at start, and the index one past
// them: a line that opens no body entry ends the body.
func hunkBody(lines []string, start int) ([]string, int) {
	end := start
	for end < len(lines) && isBodyLine(lines[end]) {
		end++
	}
	return lines[start:end], end
}

// isBodyLine reports whether ln belongs to a hunk body. A context line that is
// itself empty is written as a single space, so an empty line is never a body
// line.
func isBodyLine(ln string) bool {
	if ln == "" {
		return false
	}
	switch ln[0] {
	case contextLine, addedLine, removedLine, noNewlineEscape:
		return true
	default:
		return false
	}
}

// parseHunkHeader reads `@@ -oldStart,oldCount +newStart,newCount @@`, with the
// trailing section heading optional. A header short of either span is refused.
func parseHunkHeader(ln string) (hunkHead, bool) {
	fields := strings.Fields(ln)
	if len(fields) < minHunkFields {
		return hunkHead{}, false
	}
	oldStart, oldCount, okOld := parseHunkSpan(fields[1], "-")
	newStart, newCount, okNew := parseHunkSpan(fields[2], "+")
	if !okOld || !okNew {
		return hunkHead{}, false
	}
	return hunkHead{oldStart: oldStart, oldCount: oldCount, newStart: newStart, newCount: newCount}, true
}

// parseHunkSpan reads one "-start[,count]" span. A missing count means a
// one-line span, which is what git writes for a hunk touching a single line.
func parseHunkSpan(tok, side string) (int, int, bool) {
	if !strings.HasPrefix(tok, side) {
		return 0, 0, false
	}
	span := tok[len(side):]
	start, count := span, "1"
	if pre, post, ok := strings.Cut(span, ","); ok {
		start, count = pre, post
	}
	startN, errStart := strconv.Atoi(start)
	countN, errCount := strconv.Atoi(count)
	if errStart != nil || errCount != nil || startN < 0 || countN < 0 {
		return 0, 0, false
	}
	if startN > maxSpanLine || countN > maxSpanLine {
		return 0, 0, false
	}
	return startN, countN, true
}

// maxSpanLine bounds a parsed span. A real hunk is far smaller, and the bound
// keeps the resume arithmetic clear of overflow on every target.
const maxSpanLine = 1 << 29

// minHunkFields is how many fields a header needs before both spans are there:
// the `@@` marker, the old span, and the new span.
const minHunkFields = 3
