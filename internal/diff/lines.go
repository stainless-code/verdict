package diff

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// myersCellCap bounds the LCS table size for the Myers fallback.
	myersCellCap = 4_000_000
	// tokenCellCap bounds the word-token LCS table size.
	tokenCellCap = 200_000
)

// EditKind classifies one line-diff edit group.
type EditKind int

const (
	// EditEqual is an unchanged run of lines.
	EditEqual EditKind = iota
	// EditInsert is an added run of lines.
	EditInsert
	// EditDelete is a deleted run of lines.
	EditDelete
)

// Edit is a run of lines with the same kind.
type Edit struct {
	Lines []string `json:"lines"`
	Kind  EditKind `json:"kind"`
}

// DiffLines computes a histogram-style line diff with a Myers fallback and
// an indent heuristic that slides hunks to natural blank or indentation
// boundaries. It returns equal, insert, and delete runs covering a then b.
func DiffLines(a, b []string) []Edit { //nolint:revive // STA-395 names DiffLines.
	return linesDiff(a, b, true)
}

// LinesWithHeuristic is the mutation-proof seam: with useHeuristic false the
// indent slide is skipped. DiffLines always passes true.
func LinesWithHeuristic(a, b []string, useHeuristic bool) []Edit {
	return linesDiff(a, b, useHeuristic)
}

func linesDiff(a, b []string, useHeuristic bool) []Edit {
	a = boundedLines(a)
	b = boundedLines(b)
	edits := histogramDiff(a, b)
	if useHeuristic {
		return slideEdits(edits)
	}
	return edits
}

func boundedLines(in []string) []string {
	if len(in) > MaxLines {
		return in[:MaxLines]
	}
	return in
}

// histogramDiff picks a low-occurrence line as an anchor and diffs the two
// halves around it. This mirrors git's histogram flavor without a full
// port and matches git's hunks on ordinary text.
func histogramDiff(a, b []string) []Edit {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	if len(a) == 0 {
		return singleEdit(EditInsert, b)
	}
	if len(b) == 0 {
		return singleEdit(EditDelete, a)
	}
	if linesEqual(a, b) {
		return singleEdit(EditEqual, a)
	}
	return anchoredDiff(a, b)
}

func singleEdit(kind EditKind, lines []string) []Edit {
	return []Edit{{Kind: kind, Lines: append([]string(nil), lines...)}}
}

func anchoredDiff(a, b []string) []Edit {
	anchor, ok := histogramAnchor(a, b)
	if !ok {
		return myersDiff(a, b)
	}
	ai, bi := indexLine(a, anchor), indexLine(b, anchor)
	if ai < 0 || bi < 0 {
		return myersDiff(a, b)
	}
	out := histogramDiff(a[:ai], b[:bi])
	out = append(out, Edit{Kind: EditEqual, Lines: []string{anchor}})
	out = append(out, histogramDiff(a[ai+1:], b[bi+1:])...)
	return coalesce(out)
}

func histogramAnchor(a, b []string) (string, bool) { //nolint:revive // single-loop anchor search is inherently linear
	countA, countB := countLines(a), countLines(b)
	firstA, firstB := firstPositions(a), firstPositions(b)
	best, bestTotal, bestPA, bestPB := "", -1, 0, 0
	seen := map[string]bool{}
	for _, line := range a {
		if seen[line] || isBlank(line) {
			continue
		}
		seen[line] = true
		total, ok := anchorTotal(line, countA, countB)
		if !ok {
			continue
		}
		pa, pb := firstA[line], firstB[line]
		if isBetter(total, pa, pb, bestTotal, bestPA, bestPB) {
			best, bestTotal, bestPA, bestPB = line, total, pa, pb
		}
	}
	return best, bestTotal >= 0
}

func anchorTotal(line string, countA, countB map[string]int) (int, bool) {
	ca, okA := countA[line]
	cb, okB := countB[line]
	if !okA || !okB {
		return 0, false
	}
	return ca + cb, true
}

func isBetter(total, pa, pb, bestTotal, bestPA, bestPB int) bool {
	if bestTotal < 0 || total < bestTotal {
		return true
	}
	if total != bestTotal {
		return false
	}
	return pa < bestPA || (pa == bestPA && pb < bestPB)
}

func firstPositions(lines []string) map[string]int {
	m := map[string]int{}
	for i, s := range lines {
		if _, ok := m[s]; !ok {
			m[s] = i
		}
	}
	return m
}

func countLines(lines []string) map[string]int {
	counts := map[string]int{}
	for i := range lines {
		counts[lines[i]]++
	}
	return counts
}

func indexLine(lines []string, want string) int {
	for i := range lines {
		if lines[i] == want {
			return i
		}
	}
	return -1
}

func linesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// myersDiff computes a minimal edit script with an LCS table. It is the
// fallback when histogram finds no anchor.
func myersDiff(a, b []string) []Edit {
	if len(a) == 0 {
		return singleEdit(EditInsert, b)
	}
	if len(b) == 0 {
		return singleEdit(EditDelete, a)
	}
	if coarseFallback(a, b) {
		return coarseEdits(a, b)
	}
	lcs := lcsTable(a, b)
	return walkLCS(a, b, lcs)
}

func coarseFallback(a, b []string) bool {
	return int64(len(a))*int64(len(b)) > myersCellCap
}

func coarseEdits(a, b []string) []Edit {
	return []Edit{
		{Kind: EditDelete, Lines: append([]string(nil), a...)},
		{Kind: EditInsert, Lines: append([]string(nil), b...)},
	}
}

func lcsTable(a, b []string) []int {
	n, m := len(a), len(b)
	stride := m + 1
	lcs := make([]int, (n+1)*stride)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			lcs[i*stride+j] = bestNext(a, b, lcs, stride, i, j)
		}
	}
	return lcs
}

func bestNext(a, b []string, lcs []int, stride, i, j int) int {
	if a[i] == b[j] {
		return 1 + lcs[(i+1)*stride+(j+1)]
	}
	if lcs[(i+1)*stride+j] >= lcs[i*stride+(j+1)] {
		return lcs[(i+1)*stride+j]
	}
	return lcs[i*stride+(j+1)]
}

func walkLCS(a, b []string, lcs []int) []Edit {
	var out []Edit
	stride := len(b) + 1
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		out, i, j = walkStep(out, a, b, lcs, stride, i, j)
	}
	for i < len(a) {
		out = appendRun(out, EditDelete, a[i])
		i++
	}
	for j < len(b) {
		out = appendRun(out, EditInsert, b[j])
		j++
	}
	return coalesce(out)
}

func walkStep(out []Edit, a, b []string, lcs []int, stride, i, j int) ([]Edit, int, int) {
	switch {
	case a[i] == b[j]:
		return appendRun(out, EditEqual, a[i]), i + 1, j + 1
	case lcs[(i+1)*stride+j] >= lcs[i*stride+(j+1)]:
		return appendRun(out, EditDelete, a[i]), i + 1, j
	default:
		return appendRun(out, EditInsert, b[j]), i, j + 1
	}
}

func appendRun(edits []Edit, kind EditKind, line string) []Edit {
	if len(edits) > 0 && edits[len(edits)-1].Kind == kind {
		edits[len(edits)-1].Lines = append(edits[len(edits)-1].Lines, line)
		return edits
	}
	return append(edits, Edit{Kind: kind, Lines: []string{line}})
}

func coalesce(edits []Edit) []Edit {
	var out []Edit
	for i := range edits {
		if len(edits[i].Lines) == 0 {
			continue
		}
		out = mergeEdit(out, edits[i])
	}
	return out
}

func mergeEdit(out []Edit, e Edit) []Edit {
	if len(out) > 0 && out[len(out)-1].Kind == e.Kind {
		out[len(out)-1].Lines = append(out[len(out)-1].Lines, e.Lines...)
		return out
	}
	return append(out, Edit{Kind: e.Kind, Lines: append([]string(nil), e.Lines...)})
}

// flatLine is one line plus its edit kind for sliding.
type flatLine struct {
	line string
	kind EditKind
}

// slideEdits slides each change block to a natural blank or indentation
// boundary, a compact stand-in for git's indent heuristic. Dropping it
// shifts hunks and fails the mutation test.
func slideEdits(edits []Edit) []Edit {
	flat := flatten(edits)
	slideBlocks(flat)
	return regroup(flat)
}

func flatten(edits []Edit) []flatLine {
	var flat []flatLine
	for i := range edits {
		for j := range edits[i].Lines {
			flat = append(flat, flatLine{line: edits[i].Lines[j], kind: edits[i].Kind})
		}
	}
	return flat
}

func regroup(flat []flatLine) []Edit {
	var out []Edit
	for i := range flat {
		out = appendRun(out, flat[i].kind, flat[i].line)
	}
	return out
}

func slideBlocks(flat []flatLine) {
	i := 0
	for i < len(flat) {
		if flat[i].kind == EditEqual {
			i++
			continue
		}
		end := blockEnd(flat, i)
		i = slideBlockLeft(flat, i, end)
	}
}

func blockEnd(flat []flatLine, start int) int {
	end := start
	for end < len(flat) && flat[end].kind != EditEqual {
		end++
	}
	return end
}

func slideBlockLeft(flat []flatLine, start, end int) int {
	for canSlide(flat, start, end) {
		start, end = slideOnce(flat, start, end)
	}
	return end
}

func canSlide(flat []flatLine, start, end int) bool {
	if start == 0 || flat[start-1].kind != EditEqual {
		return false
	}
	prev, last := flat[start-1], flat[end-1]
	if prev.line != last.line && !isBlank(prev.line) {
		return false
	}
	if !isBlank(prev.line) && indentOf(prev.line) >= indentOf(last.line) {
		return false
	}
	return findPartner(flat, start, end, prev.line) >= 0
}

func findPartner(flat []flatLine, start, end int, want string) int {
	for k := end - 1; k >= start; k-- {
		if flat[k].line == want {
			return k
		}
	}
	return -1
}

func slideOnce(flat []flatLine, start, end int) (int, int) {
	prev := flat[start-1]
	partner := findPartner(flat, start, end, prev.line)
	which := flat[partner].kind
	flat[partner].kind = EditEqual
	flat[start-1] = flatLine{line: prev.line, kind: which}
	return start - 1, end - 1
}

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }

func indentOf(s string) int {
	n := 0
	for _, r := range s {
		if r != ' ' && r != '\t' {
			break
		}
		n++
	}
	return n
}

// WordSpan marks one token span as changed or unchanged. Start and End are
// byte offsets into the original line; Text is the decoded segment.
type WordSpan struct {
	Text    string `json:"text"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Changed bool   `json:"changed"`
}

// token splits a line on word boundaries.
type token struct {
	text  string
	start int
	end   int
}

// tokenize splits a line on word boundaries: runs of alphanumeric plus '_'
// are word tokens; every other single rune is its own token. Offsets are
// byte offsets.
func tokenize(line string) []token {
	if line == "" {
		return nil
	}
	scanner := &tokenScanner{line: line, start: -1}
	for off, r := range line {
		scanner.step(off, r)
	}
	scanner.finish()
	return scanner.out
}

type tokenScanner struct {
	line  string
	out   []token
	start int
}

func (s *tokenScanner) step(off int, r rune) {
	if isWordRune(r) {
		s.begin(off)
		return
	}
	s.flush(off)
	s.out = appendSep(s.out, s.line, off)
}

func (s *tokenScanner) begin(off int) {
	if s.start < 0 {
		s.start = off
	}
}

func (s *tokenScanner) flush(end int) {
	if s.start < 0 {
		return
	}
	s.out = append(s.out, token{text: s.line[s.start:end], start: s.start, end: end})
	s.start = -1
}

func (s *tokenScanner) finish() {
	s.flush(len(s.line))
}

func appendSep(out []token, line string, off int) []token {
	_, sz := utf8.DecodeRuneInString(line[off:])
	return append(out, token{text: line[off : off+sz], start: off, end: off + sz})
}

// isWordRune reports whether r can be part of a word token.
func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// WordSpans pairs two adjacent lines and marks changed tokens. Tokens
// outside the LCS are changed. Very long lines degrade to all-changed.
func WordSpans(oldLine, newLine string) (oldSpans, newSpans []WordSpan) {
	if tooLongForWordDiff(oldLine, newLine) {
		return allChanged(oldLine), allChanged(newLine)
	}
	return tokensCompared(oldLine, newLine)
}

func tooLongForWordDiff(oldLine, newLine string) bool {
	return utf8.RuneCountInString(oldLine) > MaxWordRunes ||
		utf8.RuneCountInString(newLine) > MaxWordRunes
}

func allChanged(line string) []WordSpan {
	if line == "" {
		return nil
	}
	return []WordSpan{{Text: line, Start: 0, End: len(line), Changed: true}}
}

func tokensCompared(oldLine, newLine string) (oldSpans, newSpans []WordSpan) {
	a, b := tokenize(oldLine), tokenize(newLine)
	if len(a) == 0 && len(b) == 0 {
		return nil, nil
	}
	if tokenFallback(a, b) {
		return allChanged(oldLine), allChanged(newLine)
	}
	keepA, keepB := tokenKeepMask(a, b)
	return spansFrom(a, keepA), spansFrom(b, keepB)
}

func tokenFallback(a, b []token) bool {
	return int64(len(a))*int64(len(b)) > tokenCellCap
}

func tokenKeepMask(a, b []token) ([]bool, []bool) {
	lcs := tokenLCS(a, b)
	keepA, keepB := make([]bool, len(a)), make([]bool, len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		i, j = advanceToken(a, b, lcs, keepA, keepB, i, j)
	}
	return keepA, keepB
}

// advanceToken steps one token-LCS position using a switch.
func advanceToken(a, b []token, lcs []int, keepA, keepB []bool, i, j int) (int, int) {
	stride := len(b) + 1
	switch {
	case a[i].text == b[j].text:
		keepA[i] = true
		keepB[j] = true
		return i + 1, j + 1
	case lcs[(i+1)*stride+j] >= lcs[i*stride+(j+1)]:
		return i + 1, j
	default:
		return i, j + 1
	}
}

func tokenLCS(a, b []token) []int {
	n, m := len(a), len(b)
	stride := m + 1
	lcs := make([]int, (n+1)*stride)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			lcs[i*stride+j] = tokenBest(a, b, lcs, stride, i, j)
		}
	}
	return lcs
}

func tokenBest(a, b []token, lcs []int, stride, i, j int) int {
	if a[i].text == b[j].text {
		return 1 + lcs[(i+1)*stride+(j+1)]
	}
	if lcs[(i+1)*stride+j] >= lcs[i*stride+(j+1)] {
		return lcs[(i+1)*stride+j]
	}
	return lcs[i*stride+(j+1)]
}

func spansFrom(tokens []token, keep []bool) []WordSpan {
	var out []WordSpan
	for i := range tokens {
		t := tokens[i]
		out = append(out, WordSpan{Text: t.text, Start: t.start, End: t.end, Changed: !keep[i]})
	}
	return out
}

// PairWordSpans pairs adjacent deleted and added lines inside a hunk.
// Only a contiguous deletion run immediately followed by a contiguous
// addition run pairs; a context line breaks the block and any unpaired
// line across it is never paired.
func PairWordSpans(lines []Line) (map[int][]WordSpan, map[int][]WordSpan) {
	oldOut, newOut := map[int][]WordSpan{}, map[int][]WordSpan{}
	var delIdx, addIdx []int
	flush := func() {
		if len(delIdx) == 0 && len(addIdx) == 0 {
			return
		}
		pairBlock(lines, delIdx, addIdx, oldOut, newOut)
		delIdx, addIdx = nil, nil
	}
	for i := range lines {
		switch lines[i].Kind {
		case KindDel:
			delIdx = append(delIdx, i)
		case KindAdd:
			addIdx = append(addIdx, i)
		default:
			flush()
		}
	}
	flush()
	return oldOut, newOut
}

func pairBlock(lines []Line, delIdx, addIdx []int, oldOut, newOut map[int][]WordSpan) {
	dels := bounded(delIdx, MaxWordLines)
	adds := bounded(addIdx, MaxWordLines)
	eligibleAdds := addsAfterFirstDel(dels, adds)
	pairs := min(len(dels), len(eligibleAdds))
	for k := range pairs {
		o, nw := WordSpans(lines[dels[k]].Text, lines[eligibleAdds[k]].Text)
		oldOut[dels[k]] = o
		newOut[eligibleAdds[k]] = nw
	}
	if len(eligibleAdds) > pairs {
		starUnpaired(newOut, lines, eligibleAdds[pairs:])
	}
	// Adds that appear before any del in the block stay unpaired.
	if len(adds) > len(eligibleAdds) {
		prefix := adds[:len(adds)-len(eligibleAdds)]
		starUnpaired(newOut, lines, prefix)
	}
	starUnpaired(oldOut, lines, dels[pairs:])
}

func addsAfterFirstDel(dels, adds []int) []int {
	if len(dels) == 0 || len(adds) == 0 {
		return nil
	}
	firstDel := dels[0]
	var out []int
	for _, idx := range adds {
		if idx > firstDel {
			out = append(out, idx)
		}
	}
	return out
}

func starUnpaired(out map[int][]WordSpan, lines []Line, idx []int) {
	for _, n := range idx {
		if lines[n].Text != "" {
			out[n] = allChanged(lines[n].Text)
		}
	}
}

func bounded(in []int, n int) []int {
	if len(in) > n {
		return in[:n]
	}
	return in
}
