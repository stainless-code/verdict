// Package evidence verifies agent review claims against pinned bundle bytes:
// diff-line quote matching (A2), per-hunk read receipts (A3), deterministic
// specification-item enumeration (§4.2), and test-output classification (A8b).
// Every entry point is pure over bytes and strings with no I/O or git calls.
package evidence

import (
	"cmp"
	"math"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/hunks"
)

// Quote verification sides, statuses, and machine reasons.
const (
	SideRight = "RIGHT"
	SideLeft  = "LEFT"

	StatusVerified   = "verified"
	StatusUnverified = "unverified"

	ReasonPathNotInBundle   = "path-not-in-bundle"
	ReasonAmbiguousPath     = "ambiguous-path"
	ReasonLineNotInBundle   = "line-not-in-bundle"
	ReasonFileAbsentAtHead  = "file-absent-at-head"
	ReasonRangeCrossesHunks = "range-crosses-hunks"
	ReasonDeletedLine       = "deleted-line"
	ReasonQuoteTooLarge     = "quote-too-large"
	ReasonTextDiffers       = "text-differs"
	ReasonRangeInvalid      = "range-invalid"
)

// Diff line markers and header prefixes.
const (
	diffNewline         = "\n"
	diffHunkPrefix      = "@@ "
	diffRenameFrom      = "rename from "
	diffRenameTo        = "rename to "
	diffDeletedFileMode = "deleted file mode "
	diffPlusDevNull     = "+++ /dev/null"
	diffPlusHeader      = "+++ "
	diffNoNewlinePrefix = "\\"
	diffSideOld         = "OLD"
	diffSideNew         = "NEW"
	diffMinHeaderFields = 3
	diffUnsafeRowChars  = "\t\r\n"
	asciiDigitMin       = '0'
	asciiDigitMax       = '9'
	decimalBase         = 10
)

// DiffHunk is one unified-diff hunk with its parsed line text on both sides
// and its canonical hunks.tsv rows.
type DiffHunk struct {
	File         string
	NewRow       string
	OldRow       string
	NewLines     []string
	OldLines     []string
	OldIsDeleted []bool
	NewStart     int
	NewEnd       int
	OldStart     int
	OldEnd       int
}

// Key returns the A3 canonical receipt key for the hunk: the NEW hunks.tsv row
// (without its trailing newline) when the NEW side is non-empty, else the OLD
// row.
func (h *DiffHunk) Key() string {
	if h.NewRow != "" {
		return h.NewRow
	}
	return h.OldRow
}

// DiffFile holds the indexed line maps and hunks of one `diff --git` section.
type DiffFile struct {
	OldPath      string
	Path         string
	Canonical    string
	NewLines     map[int]string
	OldLines     map[int]string
	Hunks        []DiffHunk
	Deleted      bool
	HasNewHeader bool
}

// AbsentAtHead reports whether the file does not exist at the head revision
// (a deletion diff with OLD lines only or an explicit deletion marker).
func (f *DiffFile) AbsentAtHead() bool {
	if f == nil {
		return false
	}
	if f.Deleted {
		return true
	}
	return !f.HasNewHeader && len(f.NewLines) == 0 && len(f.OldLines) > 0
}

// DiffIndex is the pure in-memory line and hunk index parsed from full.diff
// bytes.
type DiffIndex struct {
	byPath    map[string][]*DiffFile
	byHunkKey map[string]*DiffHunk
	files     []*DiffFile
}

// IndexDiff parses full.diff bytes into per-file OLD and NEW line maps and
// per-hunk boundaries, using internal/hunks for C-quoted and renamed paths.
// CRLF line endings are normalized once here: a carriage return that reached a
// path would make every hunk row unsafe and the diff unverifiable.
func IndexDiff(diff []byte) *DiffIndex {
	idx := &DiffIndex{
		byPath:    make(map[string][]*DiffFile),
		byHunkKey: make(map[string]*DiffHunk),
	}
	if len(diff) == 0 {
		return idx
	}
	text := normalizeDiffText(diff)
	sections := hunks.Files(text)
	if len(sections) == 0 {
		return idx
	}
	lines := strings.Split(strings.TrimSuffix(text, diffNewline), diffNewline)
	for i := range sections {
		df := indexSection(&sections[i], lines)
		idx.addFile(df)
	}
	return idx
}

// File looks up path in the index. It returns the matching section and an empty
// reason on a unique match, or nil with ReasonPathNotInBundle or
// ReasonAmbiguousPath.
func (d *DiffIndex) File(path string) (*DiffFile, string) {
	if d == nil {
		return nil, ReasonPathNotInBundle
	}
	matches := d.byPath[path]
	switch len(matches) {
	case 0:
		return nil, ReasonPathNotInBundle
	case 1:
		return matches[0], ""
	default:
		return nil, ReasonAmbiguousPath
	}
}

// HasPath reports whether path resolves unambiguously to a section in d.
func (d *DiffIndex) HasPath(path string) bool {
	_, reason := d.File(path)
	return reason == ""
}

// HunkCount returns how many hunks the index parsed, exempt or not. A bundle
// carrying hunks whose paths cannot be keyed still reports them, so coverage
// can tell an empty diff from an unkeyable one.
func (d *DiffIndex) HunkCount() int {
	if d == nil {
		return 0
	}
	n := 0
	for _, f := range d.files {
		n += len(f.Hunks)
	}
	return n
}

// HunkByKey looks up a hunk by its A3 canonical receipt key.
func (d *DiffIndex) HunkByKey(key string) (*DiffHunk, bool) {
	if d == nil {
		return nil, false
	}
	h, ok := d.byHunkKey[key]
	return h, ok
}

// ReceiptKeys returns the deduplicated A3 canonical receipt keys for all
// non-exempt hunks, ordered by (file, side, start, end).
func (d *DiffIndex) ReceiptKeys(exempt func(string) bool) []string {
	keys, _ := d.partitionReceiptKeys(exempt)
	return keys
}

// HasHunkIndexDrift reports whether a bundle's hunks.tsv snapshot disagrees
// with hunks.Parse(full.diff). The diff is normalized first, so a CRLF bundle
// whose snapshot was taken from LF bytes does not read as drift.
func HasHunkIndexDrift(diff []byte, snapshot []hunks.Hunk) bool {
	return !slices.Equal(hunks.Parse(normalizeDiffText(diff)), snapshot)
}

// normalizeDiffText converts one diff's CRLF line endings to LF, the only
// normalization every derivation (paths, hunk rows, line text) needs.
func normalizeDiffText(diff []byte) string {
	return strings.ReplaceAll(string(diff), crlfSeq, diffNewline)
}

// addFile registers df under all of its distinct paths and indexes its hunks
// by their A3 receipt keys.
func (d *DiffIndex) addFile(df *DiffFile) {
	d.files = append(d.files, df)
	for _, p := range distinctPaths(df.OldPath, df.Path, df.Canonical) {
		d.byPath[p] = append(d.byPath[p], df)
	}
	for i := range df.Hunks {
		key := df.Hunks[i].Key()
		if key == "" {
			continue
		}
		if _, exists := d.byHunkKey[key]; !exists {
			d.byHunkKey[key] = &df.Hunks[i]
		}
	}
}

// distinctPaths returns non-empty paths in order without duplicates.
func distinctPaths(paths ...string) []string {
	var out []string
	for _, p := range paths {
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// receiptKeyEntry pairs a canonical key with its structured sort fields.
type receiptKeyEntry struct {
	file  string
	side  string
	key   string
	start int
	end   int
}

// partitionReceiptKeys collects unique non-exempt receipt keys sorted by
// (file, side, start, end) and returns the count of unique exempt keys.
func (d *DiffIndex) partitionReceiptKeys(exempt func(string) bool) ([]string, int) {
	if d == nil {
		return nil, 0
	}
	seen := make(map[string]struct{})
	var entries []receiptKeyEntry
	exemptCount := 0
	for _, f := range d.files {
		entries, exemptCount = collectFileReceiptKeys(f, exempt, seen, entries, exemptCount)
	}
	slices.SortFunc(entries, compareReceiptKeyEntries)
	keys := make([]string, len(entries))
	for i, e := range entries {
		keys[i] = e.key
	}
	return keys, exemptCount
}

// collectFileReceiptKeys adds one file's unique hunk keys to entries or the
// exempt count.
func collectFileReceiptKeys(
	f *DiffFile,
	exempt func(string) bool,
	seen map[string]struct{},
	entries []receiptKeyEntry,
	exemptCount int,
) ([]receiptKeyEntry, int) {
	for i := range f.Hunks {
		entry, ok := nextUniqueReceiptKey(&f.Hunks[i], seen)
		if !ok {
			continue
		}
		if exempt != nil && exempt(entry.file) {
			exemptCount++
			continue
		}
		entries = append(entries, entry)
	}
	return entries, exemptCount
}

// nextUniqueReceiptKey returns h's receipt key entry if non-empty and unseen.
func nextUniqueReceiptKey(h *DiffHunk, seen map[string]struct{}) (receiptKeyEntry, bool) {
	entry, ok := makeReceiptKeyEntry(h)
	if !ok {
		return receiptKeyEntry{}, false
	}
	if _, dup := seen[entry.key]; dup {
		return receiptKeyEntry{}, false
	}
	seen[entry.key] = struct{}{}
	return entry, true
}

// makeReceiptKeyEntry builds the sort record for one hunk's A3 key.
func makeReceiptKeyEntry(h *DiffHunk) (receiptKeyEntry, bool) {
	if h.NewRow != "" {
		return receiptKeyEntry{
			file:  h.File,
			side:  diffSideNew,
			key:   h.NewRow,
			start: h.NewStart,
			end:   h.NewEnd,
		}, true
	}
	if h.OldRow != "" {
		return receiptKeyEntry{
			file:  h.File,
			side:  diffSideOld,
			key:   h.OldRow,
			start: h.OldStart,
			end:   h.OldEnd,
		}, true
	}
	return receiptKeyEntry{}, false
}

// compareReceiptKeyEntries orders keys by (file, side, start, end).
func compareReceiptKeyEntries(a, b receiptKeyEntry) int {
	if c := cmp.Compare(a.file, b.file); c != 0 {
		return c
	}
	if c := cmp.Compare(a.side, b.side); c != 0 {
		return c
	}
	if c := cmp.Compare(a.start, b.start); c != 0 {
		return c
	}
	return cmp.Compare(a.end, b.end)
}

// sectionScanner scans one `diff --git` section into a DiffFile.
type sectionScanner struct {
	df     *DiffFile
	cur    *DiffHunk
	oldCur int
	newCur int
	oldRem int
	newRem int
	inHunk bool
}

// indexSection parses one hunks.File section's slice of diff lines.
func indexSection(sec *hunks.File, allLines []string) *DiffFile {
	oldPath, newPath := hunks.HeaderPaths(sec.Header)
	df := &DiffFile{
		OldPath:   oldPath,
		Path:      newPath,
		Canonical: newPath,
		NewLines:  make(map[int]string),
		OldLines:  make(map[int]string),
	}
	startIdx := max(0, sec.StartLine-1)
	endIdx := min(len(allLines), sec.EndLine)
	if startIdx >= endIdx {
		return df
	}
	sc := &sectionScanner{df: df}
	for _, ln := range allLines[startIdx+1 : endIdx] {
		sc.visitLine(ln)
	}
	sc.flushHunk()
	return df
}

// visitLine routes one line inside a diff section to either header or hunk body
// handling.
func (s *sectionScanner) visitLine(ln string) {
	if strings.HasPrefix(ln, diffHunkPrefix) {
		s.openHunk(ln)
		return
	}
	if s.inHunk {
		s.visitHunkLine(ln)
		return
	}
	s.visitHeaderLine(ln)
}

// visitHeaderLine records rename and deletion metadata before the first hunk.
func (s *sectionScanner) visitHeaderLine(ln string) {
	switch {
	case strings.HasPrefix(ln, diffRenameFrom):
		s.df.OldPath = hunks.UnquotePath(strings.TrimPrefix(ln, diffRenameFrom))
	case strings.HasPrefix(ln, diffRenameTo):
		s.df.Canonical = hunks.UnquotePath(strings.TrimPrefix(ln, diffRenameTo))
	case strings.HasPrefix(ln, diffDeletedFileMode):
		s.df.Deleted = true
	case ln == diffPlusDevNull:
		s.df.Deleted = true
	case strings.HasPrefix(ln, diffPlusHeader):
		s.df.HasNewHeader = true
	}
}

// openHunk finalizes any open hunk and starts a new one from an `@@ ` header.
func (s *sectionScanner) openHunk(ln string) {
	s.flushHunk()
	oldStart, oldCount, newStart, newCount, ok := parseHunkHeader(ln)
	if !ok {
		return
	}
	h := newDiffHunk(s.df.Canonical, oldStart, oldCount, newStart, newCount)
	s.cur = &h
	s.oldCur = oldStart
	s.newCur = newStart
	s.oldRem = oldCount
	s.newRem = newCount
	s.inHunk = oldCount > 0 || newCount > 0
}

// newDiffHunk initializes a DiffHunk's range metadata and canonical TSV rows.
func newDiffHunk(file string, oldStart, oldCount, newStart, newCount int) DiffHunk {
	h := DiffHunk{File: file}
	safeRow := !strings.ContainsAny(file, diffUnsafeRowChars)
	if oldCount > 0 {
		h.OldStart = oldStart
		h.OldEnd = spanEnd(oldStart, oldCount)
		h.OldRow = maybeHunkRow(safeRow, file, diffSideOld, h.OldStart, h.OldEnd)
	}
	if newCount > 0 {
		h.NewStart = newStart
		h.NewEnd = spanEnd(newStart, newCount)
		h.NewRow = maybeHunkRow(safeRow, file, diffSideNew, h.NewStart, h.NewEnd)
	}
	return h
}

// maybeHunkRow returns the canonical TSV row when safeRow is true.
func maybeHunkRow(safeRow bool, file, side string, start, end int) string {
	if !safeRow {
		return ""
	}
	return formatHunkRow(file, side, start, end)
}

// visitHunkLine appends one hunk body line to OLD and/or NEW indices, skipping
// `\ No newline at end of file` markers.
func (s *sectionScanner) visitHunkLine(ln string) {
	if strings.HasPrefix(ln, diffNoNewlinePrefix) {
		return
	}
	s.consumeHunkLine(ln)
	if s.oldRem <= 0 && s.newRem <= 0 {
		s.inHunk = false
	}
}

// consumeHunkLine dispatches a single non-marker hunk line by its diff prefix.
func (s *sectionScanner) consumeHunkLine(ln string) {
	if ln == "" {
		if s.oldRem > 0 && s.newRem > 0 {
			s.addContext("")
		}
		return
	}
	switch ln[0] {
	case ' ':
		s.addContext(ln[1:])
	case '+':
		s.tryAddNew(ln[1:])
	case '-':
		s.tryAddOld(ln[1:], true)
	}
}

// tryAddNew appends a NEW-side line if the hunk still expects NEW lines.
func (s *sectionScanner) tryAddNew(text string) {
	if s.newRem > 0 {
		s.addNew(text)
	}
}

// tryAddOld appends an OLD-side line if the hunk still expects OLD lines.
func (s *sectionScanner) tryAddOld(text string, deleted bool) {
	if s.oldRem > 0 {
		s.addOld(text, deleted)
	}
}

// addContext records a shared context line on both OLD and NEW sides.
func (s *sectionScanner) addContext(text string) {
	if s.oldRem > 0 {
		s.addOld(text, false)
	}
	if s.newRem > 0 {
		s.addNew(text)
	}
}

// addOld records one OLD-side line and advances the OLD cursor.
func (s *sectionScanner) addOld(text string, deleted bool) {
	s.cur.OldLines = append(s.cur.OldLines, text)
	s.cur.OldIsDeleted = append(s.cur.OldIsDeleted, deleted)
	s.df.OldLines[s.oldCur] = text
	if s.oldCur < math.MaxInt {
		s.oldCur++
	}
	s.oldRem--
}

// addNew records one NEW-side line and advances the NEW cursor.
func (s *sectionScanner) addNew(text string) {
	s.cur.NewLines = append(s.cur.NewLines, text)
	s.df.NewLines[s.newCur] = text
	if s.newCur < math.MaxInt {
		s.newCur++
	}
	s.newRem--
}

// flushHunk appends the active hunk to the section if one is open.
func (s *sectionScanner) flushHunk() {
	if s.cur == nil {
		return
	}
	s.df.Hunks = append(s.df.Hunks, *s.cur)
	s.cur = nil
	s.inHunk = false
}

// formatHunkRow renders one hunks.tsv row string without the trailing newline.
func formatHunkRow(file, side string, start, end int) string {
	h := hunks.Hunk{File: file, Side: side, Start: start, End: end}
	return strings.TrimSuffix(h.Row(), diffNewline)
}

// parseHunkHeader extracts the old and new start/count ranges from an `@@ `
// header, mirroring internal/hunks.
func parseHunkHeader(ln string) (oldStart, oldCount, newStart, newCount int, ok bool) {
	f := strings.Fields(ln)
	if len(f) < diffMinHeaderFields {
		return 0, 0, 0, 0, false
	}
	oldStart, oldCount = parseRange(strings.TrimPrefix(f[1], "-"))
	newStart, newCount = parseRange(strings.TrimPrefix(f[2], "+"))
	return oldStart, oldCount, newStart, newCount, true
}

// parseRange splits "start[,count]" with missing count defaulting to 1.
func parseRange(s string) (int, int) {
	startStr, countStr := s, "1"
	if pre, post, ok := strings.Cut(s, ","); ok {
		startStr, countStr = pre, post
		if countStr == "" {
			countStr = "1"
		}
	}
	return prefixInt(startStr), prefixInt(countStr)
}

// prefixInt parses the leading non-negative decimal integer prefix, saturating
// at [math.MaxInt] on overflow.
func prefixInt(s string) int {
	n := 0
	for i := range len(s) {
		c := s[i]
		if c < asciiDigitMin || c > asciiDigitMax {
			break
		}
		d := int(c - asciiDigitMin)
		if n > (math.MaxInt-d)/decimalBase {
			return math.MaxInt
		}
		n = n*decimalBase + d
	}
	return n
}

// spanEnd computes the inclusive end of a count-line span from start,
// saturating at [math.MaxInt] instead of wrapping negative.
func spanEnd(start, count int) int {
	if start > math.MaxInt-count+1 {
		return math.MaxInt
	}
	return start + count - 1
}
