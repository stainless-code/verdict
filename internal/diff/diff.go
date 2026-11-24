// Package diff is verdict's pure-Go unified-diff engine: a bundle
// full.diff parser, a histogram-style line diff with an indent heuristic,
// and a word-level intraline pass. It is stdlib-only so the serve read
// model can render the change without third-party dependencies.
package diff

import (
	"errors"
	"strconv"
	"strings"
)

const (
	// MaxDiffBytes caps one full.diff read so a pathological bundle degrades
	// gracefully instead of OOMing the serve process.
	MaxDiffBytes = 10 << 20 // 10 MiB
	// MaxLines caps one file's line-diff work.
	MaxLines = 200000
	// MaxWordLines caps adjacent deleted/added pairing inside a hunk.
	MaxWordLines = 64
	// MaxWordRunes caps one line's word-diff work.
	MaxWordRunes = 8192
	// gitNull marks an absent side in a new or deleted file header.
	gitNull = "/dev/null"
	// headerFieldCount is the minimum hunk header field count.
	headerFieldCount = 3
	// generatedMarker is the generated-file declaration without its suffix.
	generatedMarker = "// Code generated"
	// noEditSuffix completes the generated-file declaration.
	noEditSuffix = "DO NOT EDIT"
)

// ErrTooLarge is returned when input crosses a bound.
var ErrTooLarge = errors.New("diff: input over limit")

// LineKind classifies one diff line.
type LineKind int

const (
	// KindContext is an unchanged line inside a hunk.
	KindContext LineKind = iota
	// KindAdd is an added line inside a hunk.
	KindAdd
	// KindDel is a deleted line inside a hunk.
	KindDel
)

// Line is one hunk row.
type Line struct {
	Text  string   `json:"text"`
	Kind  LineKind `json:"kind"`
	OldNo int      `json:"old_no,omitempty"`
	NewNo int      `json:"new_no,omitempty"`
}

// Hunk is one @@ section. Field order is the published property order: lines
// render before metadata in the serve payload.
type Hunk struct { //nolint:govet // field order is the published property order
	Lines     []Line `json:"lines"`
	Header    string `json:"header"`
	OldStart  int    `json:"old_start"`
	OldCount  int    `json:"old_count"`
	NewStart  int    `json:"new_start"`
	NewCount  int    `json:"new_count"`
	GapBefore int    `json:"gap_before"`
}

// FileDiff is one diff --git section. Field order is the published property
// order: hunks and paths render before counters in the serve payload.
type FileDiff struct { //nolint:govet // field order is the published property order
	Hunks        []Hunk `json:"hunks,omitempty"`
	OldPath      string `json:"old_path"`
	NewPath      string `json:"new_path"`
	PreviousPath string `json:"previous_path"`
	Index        string `json:"index,omitempty"`
	OldMode      string `json:"old_mode,omitempty"`
	NewMode      string `json:"new_mode,omitempty"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	IsBinary     bool   `json:"is_binary"`
	IsNew        bool   `json:"is_new"`
	IsDeleted    bool   `json:"is_deleted"`
	IsRename     bool   `json:"is_rename"`
	IsModeOnly   bool   `json:"is_mode_only"`
	Generated    bool   `json:"generated"`
}

// Parse parses bundle full.diff text. It handles diff --git, rename and copy
// headers, binary markers, new and deleted files, mode-only changes, and
// no-newline markers. Input over MaxDiffBytes is truncated to the bound
// and reported with ErrTooLarge; parsing still returns what fit.
func Parse(text string) ([]FileDiff, error) {
	var err error
	if len(text) > MaxDiffBytes {
		text = text[:MaxDiffBytes]
		err = ErrTooLarge
	}
	p := &parser{cur: -1}
	for _, ln := range splitLines(text) {
		p.feed(ln)
	}
	finalize(p.files)
	if len(p.files) == 0 {
		return nil, err
	}
	return p.files, err
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func finalize(files []FileDiff) {
	for i := range files {
		if isModeOnly(&files[i]) {
			files[i].IsModeOnly = true
		}
		if fileGenerated(&files[i]) {
			files[i].Generated = true
		}
	}
}

func isModeOnly(f *FileDiff) bool {
	return f.OldMode != "" && f.NewMode != "" && len(f.Hunks) == 0 &&
		f.Additions == 0 && f.Deletions == 0
}

func fileGenerated(f *FileDiff) bool {
	return slicesContainsGenerated(f.Hunks)
}

func slicesContainsGenerated(hunks []Hunk) bool {
	for i := range hunks {
		if hunkGenerated(&hunks[i]) {
			return true
		}
	}
	return false
}

func hunkGenerated(h *Hunk) bool {
	for i := range h.Lines {
		if lineGenerated(h.Lines[i].Text) {
			return true
		}
	}
	return false
}

func lineGenerated(text string) bool {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, generatedMarker) {
		return false
	}
	return strings.Contains(t, noEditSuffix)
}

// parser carries the section scan state across diff lines. Field order
// mirrors hunks/fileScan grouping so the GC scans the fewest pointer bytes.
type parser struct { //nolint:govet // scanner state order mirrors hunks/fileScan
	files  []FileDiff
	hunk   *Hunk
	cur    int
	oldNo  int
	newNo  int
	oldRem int
	newRem int
}

func (p *parser) feed(ln string) {
	if p.inHunk() && isHunkContentPrefix(ln) {
		p.contentLine(ln)
		return
	}
	if p.openFile(ln) {
		return
	}
	if p.cur < 0 {
		return
	}
	if p.metaLine(ln) {
		return
	}
	if p.hunkLine(ln) {
		return
	}
	p.contentLine(ln)
}

func (p *parser) inHunk() bool {
	return p.hunk != nil && (p.oldRem > 0 || p.newRem > 0)
}

func isHunkContentPrefix(ln string) bool {
	if ln == "" {
		return true
	}
	c := ln[0]
	return c == '+' || c == '-' || c == ' ' || c == '\\'
}

func (p *parser) openFile(ln string) bool {
	if !strings.HasPrefix(ln, "diff --git ") {
		return false
	}
	p.files = append(p.files, FileDiff{})
	p.cur = len(p.files) - 1
	p.hunk = nil
	p.oldRem, p.newRem = 0, 0
	oldP, newP := headerPaths(ln)
	p.files[p.cur].OldPath = oldP
	p.files[p.cur].NewPath = newP
	if renamedPaths(oldP, newP) {
		p.files[p.cur].IsRename = true
		p.files[p.cur].PreviousPath = oldP
	}
	return true
}

func renamedPaths(oldP, newP string) bool {
	if oldP == newP || oldP == "" || newP == "" {
		return false
	}
	return oldP != gitNull && newP != gitNull
}

func (p *parser) metaLine(ln string) bool {
	if strings.HasPrefix(ln, "index ") {
		p.markIndex(ln)
		return true
	}
	if p.markPath(ln, "--- ", true) {
		return true
	}
	if p.markPath(ln, "+++ ", false) {
		return true
	}
	return p.metaModes(ln) || p.metaRenames(ln) || p.metaBinary(ln)
}

func (p *parser) markIndex(ln string) {
	p.files[p.cur].Index = strings.TrimSpace(strings.TrimPrefix(ln, "index "))
}

func (p *parser) markPath(ln, prefix string, oldSide bool) bool {
	if !strings.HasPrefix(ln, prefix) {
		return false
	}
	trimmed := strings.TrimSpace(strings.TrimPrefix(ln, prefix))
	path := stripAB(unquote(trimmed))
	if path == gitNull {
		p.markNullSide(oldSide)
		return true
	}
	if oldSide {
		p.files[p.cur].OldPath = path
	} else {
		p.files[p.cur].NewPath = path
	}
	return true
}

func (p *parser) markNullSide(oldSide bool) {
	if oldSide {
		p.files[p.cur].IsNew = true
	} else {
		p.files[p.cur].IsDeleted = true
	}
}

func (p *parser) metaModes(ln string) bool {
	if strings.HasPrefix(ln, "new file mode") {
		p.files[p.cur].IsNew = true
		p.files[p.cur].NewMode = strings.TrimSpace(strings.TrimPrefix(ln, "new file mode "))
		return true
	}
	if strings.HasPrefix(ln, "deleted file mode") {
		p.files[p.cur].IsDeleted = true
		p.files[p.cur].OldMode = strings.TrimSpace(strings.TrimPrefix(ln, "deleted file mode "))
		return true
	}
	return p.markMode(ln, "old mode", true) || p.markMode(ln, "new mode", false)
}

func (p *parser) markMode(ln, prefix string, oldSide bool) bool {
	if !strings.HasPrefix(ln, prefix) {
		return false
	}
	mode := strings.TrimSpace(strings.TrimPrefix(ln, prefix))
	if oldSide {
		p.files[p.cur].OldMode = mode
	} else {
		p.files[p.cur].NewMode = mode
	}
	return true
}

func (p *parser) metaRenames(ln string) bool {
	if strings.HasPrefix(ln, "rename from ") {
		p.files[p.cur].IsRename = true
		p.files[p.cur].PreviousPath = unquote(strings.TrimSpace(strings.TrimPrefix(ln, "rename from ")))
		return true
	}
	if strings.HasPrefix(ln, "rename to ") {
		p.files[p.cur].IsRename = true
		p.files[p.cur].NewPath = unquote(strings.TrimSpace(strings.TrimPrefix(ln, "rename to ")))
		return true
	}
	if after, ok := strings.CutPrefix(ln, "copy from "); ok {
		p.files[p.cur].PreviousPath = unquote(strings.TrimSpace(after))
		return true
	}
	return p.markCopyTo(ln)
}

func (p *parser) markCopyTo(ln string) bool {
	if !strings.HasPrefix(ln, "copy to ") {
		return false
	}
	p.files[p.cur].NewPath = unquote(strings.TrimSpace(strings.TrimPrefix(ln, "copy to ")))
	return true
}

func (p *parser) metaBinary(ln string) bool {
	if strings.HasPrefix(ln, "Binary files ") || ln == "GIT binary patch" {
		p.files[p.cur].IsBinary = true
		return true
	}
	if strings.HasPrefix(ln, "Binary") {
		p.files[p.cur].IsBinary = true
		return true
	}
	return false
}

func (p *parser) hunkLine(ln string) bool {
	if !strings.HasPrefix(ln, "@@ ") {
		return false
	}
	hs, hc, ns, nc, ok := parseHunkHeader(ln)
	if !ok {
		return true
	}
	next := Hunk{Header: ln, OldStart: hs, OldCount: hc, NewStart: ns, NewCount: nc}
	p.files[p.cur].Hunks = append(p.files[p.cur].Hunks, next)
	p.hunk = &p.files[p.cur].Hunks[len(p.files[p.cur].Hunks)-1]
	p.oldNo, p.newNo = hs, ns
	p.oldRem, p.newRem = hc, nc
	return true
}

func (p *parser) contentLine(ln string) {
	if strings.HasPrefix(ln, `\`) {
		return
	}
	if p.hunk == nil || !isContentLine(ln) {
		return
	}
	p.appendContent(ln)
}

func isContentLine(ln string) bool {
	return ln == "" || strings.HasPrefix(ln, "+") ||
		strings.HasPrefix(ln, "-") || strings.HasPrefix(ln, " ")
}

func (p *parser) appendContent(ln string) {
	switch {
	case strings.HasPrefix(ln, "+"):
		p.appendAdded(ln[1:])
	case strings.HasPrefix(ln, "-"):
		p.appendDeleted(ln[1:])
	default:
		p.appendContext(ln)
	}
}

func (p *parser) appendAdded(body string) {
	p.hunk.Lines = append(p.hunk.Lines, Line{Kind: KindAdd, Text: body, NewNo: p.newNo})
	p.newNo++
	p.files[p.cur].Additions++
	if p.newRem > 0 {
		p.newRem--
	}
}

func (p *parser) appendDeleted(body string) {
	p.hunk.Lines = append(p.hunk.Lines, Line{Kind: KindDel, Text: body, OldNo: p.oldNo})
	p.oldNo++
	p.files[p.cur].Deletions++
	if p.oldRem > 0 {
		p.oldRem--
	}
}

func (p *parser) appendContext(ln string) {
	body := ""
	if ln != "" {
		body = ln[1:]
	}
	p.hunk.Lines = append(p.hunk.Lines, Line{Kind: KindContext, Text: body, OldNo: p.oldNo, NewNo: p.newNo})
	p.oldNo++
	p.newNo++
	if p.oldRem > 0 {
		p.oldRem--
	}
	if p.newRem > 0 {
		p.newRem--
	}
}

func stripAB(path string) string {
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		return path[2:]
	}
	return path
}

func unquote(s string) string {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	if u, err := strconv.Unquote(s); err == nil {
		return u
	}
	return s[1 : len(s)-1]
}

func headerPaths(line string) (string, string) {
	rest := strings.TrimPrefix(line, "diff --git ")
	if tok, after, ok := cutQuoted(rest); ok {
		return headerAfterQuoted(tok, after)
	}
	if before, after, ok := strings.CutLast(rest, " b/"); ok {
		return stripAB(strings.TrimSpace(before)), after
	}
	return stripAB(strings.TrimSpace(rest)), ""
}

func headerAfterQuoted(tok, after string) (string, string) {
	oldP := stripAB(unquote(tok))
	after = strings.TrimLeft(after, " \t")
	if !strings.HasPrefix(after, "\"") {
		return oldP, stripAB(strings.TrimSpace(after))
	}
	t2, _, _ := cutQuoted(after)
	return oldP, stripAB(unquote(t2))
}

func cutQuoted(s string) (string, string, bool) {
	if s == "" || s[0] != '"' {
		return "", s, false
	}
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '"' {
			return s[:i+1], s[i+1:], true
		}
	}
	return s, "", true
}

func parseHunkHeader(ln string) (int, int, int, int, bool) {
	// @@ -old[,count] +new[,count] @@ [section]
	fields := strings.Fields(ln)
	if len(fields) < headerFieldCount {
		return 0, 0, 0, 0, false
	}
	a := strings.TrimPrefix(fields[1], "-")
	b := strings.TrimPrefix(fields[2], "+")
	oldS, oldC := splitComma(a)
	newS, newC := splitComma(b)
	return atoiOrZero(oldS), atoiOrZero(oldC), atoiOrZero(newS), atoiOrZero(newC), true
}

func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err == nil {
		return n
	}
	return 0
}

func splitComma(s string) (string, string) {
	if before, after, ok := strings.Cut(s, ","); ok {
		return before, after
	}
	return s, "1"
}

// ComputeGaps fills GapBefore between hunks from line numbers.
func ComputeGaps(files []FileDiff) {
	for i := range files {
		computeFileGaps(&files[i])
	}
}

func computeFileGaps(f *FileDiff) {
	for j := range f.Hunks {
		f.Hunks[j].GapBefore = gapFor(f, j)
	}
}

func gapFor(f *FileDiff, j int) int {
	cur := &f.Hunks[j]
	if j == 0 {
		return max(0, cur.OldStart-1)
	}
	prev := &f.Hunks[j-1]
	return max(0, cur.OldStart-(prev.OldStart+prev.OldCount))
}
