package evidence

import (
	"path"
	"strconv"
	"strings"
	"unicode"
)

// Specification checklist limits, statuses, and failure reasons.
const (
	MaxSpecItems           = 60
	DefaultSpecCoverageMin = 80
	percentScale           = 100

	SpecStatusMet      = "met"
	SpecStatusViolated = "violated"
	SpecStatusNA       = "n/a"

	ReasonSpecItemUnknown      = "spec-item-unknown"
	ReasonSpecEvidenceNotCode  = "spec-evidence-not-code"
	ReasonSpecEvidenceMismatch = "spec-evidence-mismatch"

	minCodeFenceLen = 3
	minTablePipes   = 2
	tablePipe       = "|"
	tableJoinSep    = " | "
	idSeparator     = ":"
	backtickByte    = '`'
	tildeByte       = '~'
)

// A8 test-output classification deny-lists.
var (
	testOutputBasenameMarkers = []string{"_test.", ".test.", ".spec."}
	testOutputSegments        = map[string]struct{}{
		"test":          {},
		"tests":         {},
		"__tests__":     {},
		"testdata":      {},
		"fixtures":      {},
		"snapshots":     {},
		"__snapshots__": {},
	}
	testOutputExtensions = map[string]struct{}{
		".golden": {},
		".snap":   {},
		".log":    {},
	}
)

// SpecBlock is one specification source block passed to EnumerateSpecItems.
type SpecBlock struct {
	Text  string
	Index int
}

// SpecItem is one deterministic requirement enumerated from a spec block.
type SpecItem struct {
	ID         string
	Text       string
	BlockIndex int
	Line       int
}

// SpecCheckInput is one agent-supplied check against an enumerated spec item.
type SpecCheckInput struct {
	Item         string
	Status       string
	EvidencePath string
	Quote        string
	Line         int
}

// SpecCheckResult is the verification outcome for one spec check entry.
type SpecCheckResult struct {
	ItemID      string
	Item        string
	Status      string
	Reason      string
	QuoteReason string
	Index       int
	Valid       bool
}

// SpecCoverage summarizes spec-item verification and threshold coverage.
type SpecCoverage struct {
	Items       []SpecItem
	Checks      []SpecCheckResult
	Missing     []SpecItem
	Total       int
	Covered     int
	Threshold   int
	CoverageMet bool
	Satisfied   bool
}

// EnumerateSpecItems extracts deterministic checklist items from spec blocks
// per §4.2: skips nested Markdown code fences, counts bullets, numbered items,
// and table body rows (excluding header and separator rows), absorbs indented
// continuation lines, normalizes text, deduplicates across blocks keeping the
// first <block>:<line> ID.
func EnumerateSpecItems(blocks []SpecBlock) []SpecItem {
	seen := make(map[string]struct{})
	var items []SpecItem
	for _, blk := range blocks {
		items = enumerateSpecBlock(blk, seen, items)
	}
	return items
}

// IsTestOutputPath reports whether filePath is classified as test output under
// A8 (basename containing _test., .test., or .spec.; path segment test, tests,
// __tests__, testdata, fixtures, snapshots, or __snapshots__; extension
// .golden, .snap, or .log). Docs and examples are allowed.
func IsTestOutputPath(filePath string) bool {
	if filePath == "" {
		return false
	}
	norm := strings.ToLower(strings.ReplaceAll(filePath, "\\", "/"))
	if hasDeniedSegment(norm) {
		return true
	}
	return hasDeniedBasenameOrExt(path.Base(norm))
}

// hasDeniedSegment reports whether any slash-delimited segment of norm is in
// the A8 test-output segment deny-list.
func hasDeniedSegment(norm string) bool {
	for seg := range strings.SplitSeq(norm, "/") {
		if _, denied := testOutputSegments[seg]; denied {
			return true
		}
	}
	return false
}

// hasDeniedBasenameOrExt reports whether base matches an A8 test-output
// basename marker or extension.
func hasDeniedBasenameOrExt(base string) bool {
	if base == "." || base == "/" {
		return false
	}
	for _, marker := range testOutputBasenameMarkers {
		if strings.Contains(base, marker) {
			return true
		}
	}
	_, deniedExt := testOutputExtensions[path.Ext(base)]
	return deniedExt
}

// MeetsSpecCoverage reports whether covered/total meets minPercent using exact
// integer cross-multiplication (covered*100 >= minPercent*total). Zero items
// passes vacuously.
func MeetsSpecCoverage(covered, total, minPercent int) bool {
	if total == 0 {
		return true
	}
	return covered*percentScale >= minPercent*total
}

// VerifySpecChecks matches agent spec checks against enumerated items, verifies
// code evidence for met and violated entries with A2 and A8, and evaluates
// coverage against minPercent.
func VerifySpecChecks(idx *DiffIndex, items []SpecItem, checks []SpecCheckInput, minPercent int) SpecCoverage {
	byText := make(map[string]SpecItem, len(items))
	for _, item := range items {
		byText[item.Text] = item
	}

	results := make([]SpecCheckResult, len(checks))
	covered := make(map[string]struct{}, len(items))
	allValid := true
	for i, chk := range checks {
		res := verifyOneSpecCheck(idx, byText, i, chk)
		results[i] = res
		if !res.Valid {
			allValid = false
			continue
		}
		covered[res.ItemID] = struct{}{}
	}

	missing := missingSpecItems(items, covered)
	total := len(items)
	coveredCount := total - len(missing)
	covMet := MeetsSpecCoverage(coveredCount, total, minPercent)

	return SpecCoverage{
		Items:       slicesClone(items),
		Checks:      results,
		Missing:     missing,
		Total:       total,
		Covered:     coveredCount,
		Threshold:   minPercent,
		CoverageMet: covMet,
		Satisfied:   covMet && allValid,
	}
}

// verifyOneSpecCheck checks one SpecCheckInput against the item index and diff.
func verifyOneSpecCheck(idx *DiffIndex, byText map[string]SpecItem, index int, chk SpecCheckInput) SpecCheckResult {
	normItem := normalizeLine(strings.ReplaceAll(chk.Item, crlfSeq, diffNewline))
	item, ok := byText[normItem]
	if !ok {
		return SpecCheckResult{
			Index:  index,
			Item:   chk.Item,
			Status: chk.Status,
			Reason: ReasonSpecItemUnknown,
		}
	}
	base := SpecCheckResult{
		Index:  index,
		ItemID: item.ID,
		Item:   item.Text,
		Status: chk.Status,
	}
	if chk.Status == SpecStatusNA {
		base.Valid = true
		return base
	}
	if IsTestOutputPath(chk.EvidencePath) {
		base.Reason = ReasonSpecEvidenceNotCode
		return base
	}
	if !idx.HasPath(chk.EvidencePath) {
		base.Reason = ReasonSpecEvidenceNotCode
		base.QuoteReason = ReasonPathNotInBundle
		return base
	}
	qr := MatchQuote(idx, QuoteRequest{
		Path:  chk.EvidencePath,
		Line:  chk.Line,
		Quote: chk.Quote,
		Side:  SideRight,
	})
	if !qr.Verified() {
		base.Reason = ReasonSpecEvidenceMismatch
		base.QuoteReason = qr.Reason
		return base
	}
	base.Valid = true
	return base
}

// missingSpecItems returns enumerated items not present in covered, in order.
func missingSpecItems(items []SpecItem, covered map[string]struct{}) []SpecItem {
	var missing []SpecItem
	for _, item := range items {
		if _, ok := covered[item.ID]; !ok {
			missing = append(missing, item)
		}
	}
	return missing
}

// slicesClone copies a non-empty SpecItem slice so callers cannot mutate
// inputs.
func slicesClone(items []SpecItem) []SpecItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]SpecItem, len(items))
	copy(out, items)
	return out
}

// specFence tracks whether the spec scanner is inside a Markdown code fence.
type specFence struct {
	length int
	char   byte
	open   bool
}

// enumerateSpecBlock scans one SpecBlock's lines into deduplicated SpecItems.
func enumerateSpecBlock(blk SpecBlock, seen map[string]struct{}, items []SpecItem) []SpecItem {
	cleaned := strings.ReplaceAll(strings.ReplaceAll(blk.Text, crlfSeq, diffNewline), zeroWidthSpace, "")
	if cleaned == "" {
		return items
	}
	lines := strings.Split(strings.TrimSuffix(cleaned, diffNewline), diffNewline)
	var fence specFence
	for i := 0; i < len(lines); {
		text, nextIdx := nextSpecItem(lines, i, &fence)
		items = appendSpecItem(items, seen, blk.Index, i+1, text)
		i = nextIdx
	}
	return items
}

// nextSpecItem parses the item beginning at lines[i], if any, and returns the
// normalized text together with the next line index to scan.
func nextSpecItem(lines []string, i int, fence *specFence) (string, int) {
	ln := lines[i]
	if fence.step(ln) {
		return "", i + 1
	}
	if isBulletItem(ln) || isNumberedItem(ln) {
		return collectListItem(lines, i)
	}
	if isTableBodyRow(lines, i) {
		return formatTableRow(ln), i + 1
	}
	return "", i + 1
}

// isTableBodyRow reports whether lines[i] is a table row that is neither a
// separator nor a header row followed by a separator.
func isTableBodyRow(lines []string, i int) bool {
	if !isTableRow(lines[i]) {
		return false
	}
	return i+1 >= len(lines) || !isTableSeparator(lines[i+1])
}

// step updates fence state for ln and reports whether ln is a fence boundary
// or lies inside an open fence.
func (f *specFence) step(ln string) bool {
	if f.open {
		if isClosingFence(ln, f.char, f.length) {
			f.open = false
		}
		return true
	}
	if ch, n, ok := openingFence(ln); ok {
		f.open = true
		f.char = ch
		f.length = n
		return true
	}
	return false
}

// collectListItem absorbs indented continuation lines following a bullet or
// numbered item and returns the normalized text plus the next line index.
func collectListItem(lines []string, startIdx int) (string, int) {
	parts := []string{normalizeLine(lines[startIdx])}
	nextIdx := startIdx + 1
	for nextIdx < len(lines) && isContinuationLine(lines[nextIdx]) {
		parts = append(parts, normalizeLine(lines[nextIdx]))
		nextIdx++
	}
	return normalizeLine(strings.Join(parts, asciiSpace)), nextIdx
}

// appendSpecItem records a non-empty, previously unseen item with ID
// "<block>:<line>".
func appendSpecItem(items []SpecItem, seen map[string]struct{}, blockIdx, lineNo int, text string) []SpecItem {
	if text == "" {
		return items
	}
	if _, dup := seen[text]; dup {
		return items
	}
	seen[text] = struct{}{}
	return append(items, SpecItem{
		ID:         strconv.Itoa(blockIdx) + idSeparator + strconv.Itoa(lineNo),
		Text:       text,
		BlockIndex: blockIdx,
		Line:       lineNo,
	})
}

// isContinuationLine reports whether ln is an indented continuation of a
// preceding bullet or numbered item.
func isContinuationLine(ln string) bool {
	if ln == "" || (ln[0] != ' ' && ln[0] != '\t') {
		return false
	}
	if strings.TrimSpace(ln) == "" {
		return false
	}
	if _, _, ok := openingFence(ln); ok {
		return false
	}
	return !isBulletItem(ln) && !isNumberedItem(ln) && !isTableRow(ln) && !isTableSeparator(ln)
}

// openingFence parses a Markdown fenced-code opening line (3+ backticks or
// 3+ tildes after optional leading whitespace).
func openingFence(ln string) (byte, int, bool) {
	trimmed := strings.TrimLeft(ln, " \t")
	if len(trimmed) < minCodeFenceLen {
		return 0, 0, false
	}
	ch := trimmed[0]
	if ch != backtickByte && ch != tildeByte {
		return 0, 0, false
	}
	n := countLeadingByte(trimmed, ch)
	if n < minCodeFenceLen {
		return 0, 0, false
	}
	if ch == backtickByte && strings.ContainsRune(trimmed[n:], rune(backtickByte)) {
		return 0, 0, false
	}
	return ch, n, true
}

// isClosingFence reports whether ln closes a fence opened with (ch, minLen).
func isClosingFence(ln string, ch byte, minLen int) bool {
	trimmed := strings.TrimLeft(ln, " \t")
	n := countLeadingByte(trimmed, ch)
	if n < minLen {
		return false
	}
	return strings.TrimSpace(trimmed[n:]) == ""
}

// countLeadingByte counts consecutive leading occurrences of b in s.
func countLeadingByte(s string, b byte) int {
	n := 0
	for n < len(s) && s[n] == b {
		n++
	}
	return n
}

// isBulletItem matches `^\s*[-*+]\s+\S`.
func isBulletItem(ln string) bool {
	trimmed := strings.TrimLeftFunc(ln, unicode.IsSpace)
	if len(trimmed) < 2 {
		return false
	}
	switch trimmed[0] {
	case '-', '*', '+':
	default:
		return false
	}
	return hasSpaceThenContent(trimmed[1:])
}

// isNumberedItem matches `^\s*\d+[.)]\s+\S`.
func isNumberedItem(ln string) bool {
	trimmed := strings.TrimLeftFunc(ln, unicode.IsSpace)
	digits := 0
	for digits < len(trimmed) && trimmed[digits] >= asciiDigitMin && trimmed[digits] <= asciiDigitMax {
		digits++
	}
	if digits == 0 || digits >= len(trimmed) {
		return false
	}
	if trimmed[digits] != '.' && trimmed[digits] != ')' {
		return false
	}
	return hasSpaceThenContent(trimmed[digits+1:])
}

// hasSpaceThenContent reports whether s begins with at least one whitespace
// rune followed by at least one non-whitespace rune.
func hasSpaceThenContent(s string) bool {
	if s == "" {
		return false
	}
	afterSpace := strings.TrimLeftFunc(s, unicode.IsSpace)
	return len(afterSpace) < len(s) && afterSpace != ""
}

// isTableRow reports whether ln has at least two pipes and is not a separator
// row.
func isTableRow(ln string) bool {
	return strings.Count(ln, tablePipe) >= minTablePipes && !isTableSeparator(ln)
}

// isTableSeparator matches a Markdown table alignment separator row
// (`^[\s:|-]+$` containing both `-` and `|`).
func isTableSeparator(ln string) bool {
	hasDash := false
	hasPipe := false
	for _, r := range ln {
		switch {
		case r == '-':
			hasDash = true
		case r == '|':
			hasPipe = true
		case r == ':' || unicode.IsSpace(r):
		default:
			return false
		}
	}
	return hasDash && hasPipe
}

// formatTableRow trims outer pipes, normalizes each cell, and rejoins cells
// with " | ". Returns empty string if every cell is empty.
func formatTableRow(ln string) string {
	trimmed := strings.TrimSpace(ln)
	trimmed = strings.TrimPrefix(trimmed, tablePipe)
	trimmed = strings.TrimSuffix(trimmed, tablePipe)
	parts := strings.Split(trimmed, tablePipe)
	hasContent := false
	for i, p := range parts {
		parts[i] = normalizeLine(p)
		if parts[i] != "" {
			hasContent = true
		}
	}
	if !hasContent {
		return ""
	}
	return normalizeLine(strings.Join(parts, tableJoinSep))
}
