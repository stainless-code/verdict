package lsp

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	sideRight       = "RIGHT"
	sideLeft        = "LEFT"
	anchorExact     = "exact"
	anchorHunk      = "hunk"
	anchorUnmapped  = "unmapped"
	kindAdd         = "add"
	kindDel         = "del"
	kindContext     = "context"
	suggestionFence = "```"
)

// record is the subset of one report item (a findings.json record plus its
// locator) the diagnostics read. Absent and null fields read as zero.
type record struct {
	Verification struct {
		Quote string `json:"quote"`
	} `json:"verification"`
	Suggestion *string `json:"suggestion"`
	Sig        string  `json:"sig"`
	Status     string  `json:"status"`
	Path       string  `json:"path"`
	Side       string  `json:"side"`
	Body       string  `json:"body"`
	Severity   string  `json:"severity"`
	Rigor      string  `json:"rigor"`
	Lens       string  `json:"lens"`
	Category   string  `json:"category"`
	Locator    string  `json:"locator"`
	Line       int     `json:"line"`
	StartLine  int     `json:"start_line"`
	Rank       int     `json:"rank"`
}

// diffFile is the subset of one query.Diff file row the join reads.
type diffFile struct {
	newSide   map[int]string
	Path      string     `json:"path"`
	Hunks     []diffHunk `json:"hunks"`
	IsBinary  bool       `json:"is_binary"`
	IsDeleted bool       `json:"is_deleted"`
}

// indexNewSide maps every NEW-side line number to its text. diffFiles calls
// it once per file as the diff is decoded, so a span lookup is one map read
// per line. Only add and context rows
// carry a new_no in the query diff wire.
func (f *diffFile) indexNewSide() {
	f.newSide = map[int]string{}
	for i := range f.Hunks {
		for _, l := range f.Hunks[i].Lines {
			if l.NewNo > 0 {
				f.newSide[l.NewNo] = l.Text
			}
		}
	}
}

type diffHunk struct {
	Lines    []diffLine `json:"lines"`
	NewStart int        `json:"new_start"`
	NewCount int        `json:"new_count"`
}

type diffLine struct {
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	OldNo int    `json:"old_no"`
	NewNo int    `json:"new_no"`
}

// finding is one record joined against the bundle's diff: the NEW-side span
// it anchors to and the hash of the text the bundle reviewed there.
type finding struct {
	path       string
	side       string
	anchor     string
	hunkSHA256 string
	rec        record
	start      int
	line       int
	suggestion bool
	truncated  bool
}

// joinFinding anchors one record. ok is false for a record no diagnostic can
// carry: a section finding, a lineless one, or one whose path is not a clean
// root-relative path (it is never published, whatever the store says).
func joinFinding(rec *record, files map[string]diffFile) (finding, bool) {
	if rec.Path == "" || rec.Line <= 0 || confineRel(rec.Path) != nil {
		return finding{}, false
	}
	f := finding{
		rec: *rec, path: rec.Path, side: sideOf(rec.Side), anchor: anchorUnmapped,
		start: spanStart(rec), line: rec.Line,
	}
	if file, ok := files[rec.Path]; ok && !file.IsBinary && !file.IsDeleted {
		if f.side == sideRight {
			anchorRight(&f, &file)
		} else {
			anchorLeft(&f, &file)
		}
	}
	f.suggestion = suggestionEligible(&f)
	return f, true
}

// sideOf normalizes a stored side: LEFT stays LEFT, anything else is RIGHT.
func sideOf(side string) string {
	if strings.EqualFold(side, sideLeft) {
		return sideLeft
	}
	return sideRight
}

// spanStart is the first line of a record's span: start_line when it is a
// valid lower bound, else the anchor line.
func spanStart(rec *record) int {
	if rec.StartLine > 0 && rec.StartLine <= rec.Line {
		return rec.StartLine
	}
	return rec.Line
}

// anchorRight marks a RIGHT-side span exact when every line of it is a
// NEW-side row of the reviewed diff.
func anchorRight(f *finding, file *diffFile) {
	if hash, ok := newSideHash(file, f.start, f.line); ok {
		f.anchor = anchorExact
		f.hunkSHA256 = hash
	}
}

// anchorLeft maps a LEFT-side (deleted-line) finding onto the NEW-side span
// of the hunk that deleted it: the working tree has no old line to point at.
// A pure-deletion hunk (`+N,0`) has no NEW side; git's N there is the line
// before the deletion, so the anchor is line N+1, the line that now stands
// where the deleted code was (line 1 for a deletion at the top of a file).
func anchorLeft(f *finding, file *diffFile) {
	for i := range file.Hunks {
		h := &file.Hunks[i]
		if !deletes(h, f.line) {
			continue
		}
		start, end := hunkSpan(h)
		f.start, f.line, f.anchor = start, end, anchorHunk
		if hash, ok := newSideHash(file, start, end); ok {
			f.hunkSHA256 = hash
		}
		return
	}
}

// hunkSpan is the NEW-side line span a LEFT finding in h anchors to.
func hunkSpan(h *diffHunk) (int, int) {
	if h.NewCount == 0 {
		line := h.NewStart + 1
		return line, line
	}
	start := max(h.NewStart, 1)
	return start, max(h.NewStart+h.NewCount-1, start)
}

func deletes(h *diffHunk, oldNo int) bool {
	for _, l := range h.Lines {
		if l.Kind == kindDel && l.OldNo == oldNo {
			return true
		}
	}
	return false
}

// newSideHash hashes the NEW-side text of lines start..end, joined by `\n`;
// ok is false when any line of the span is not in the diff.
func newSideHash(file *diffFile, start, end int) (string, bool) {
	texts := make([]string, 0, end-start+1)
	for n := start; n <= end; n++ {
		text, ok := newSideLine(file, n)
		if !ok {
			return "", false
		}
		texts = append(texts, text)
	}
	return textHash(texts), true
}

// newSideLine finds NEW-side line n through the file's index. The index is
// required: diffFiles builds it once per file when the diff is decoded, and
// an unindexed file anchors nothing.
func newSideLine(file *diffFile, n int) (string, bool) {
	text, ok := file.newSide[n]
	return text, ok
}

// textHash is the span hash both sides of the drift check use.
func textHash(lines []string) string {
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// suggestionEligible reports whether the record's suggestion could be applied
// as an edit: present, unfenced, on an exact RIGHT-side anchor with a hash.
func suggestionEligible(f *finding) bool {
	s := f.rec.Suggestion
	return s != nil && *s != "" && !strings.Contains(*s, suggestionFence) &&
		f.side == sideRight && f.anchor == anchorExact && f.hunkSHA256 != ""
}
