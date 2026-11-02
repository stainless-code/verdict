// Package diagram ports validate_behavioral_diagram: grammar-level validation
// plus opt-in gating of the agent-drawn mermaid block. It warns and drops
// with a note, never failing the run — like the basis and evidence garnish.
package diagram

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Markers and allowlist mirror the shell globals.
const (
	Start      = "<!-- verdict-diagram:start -->"
	End        = "<!-- verdict-diagram:end -->"
	Allowlist  = "flowchart graph mindmap sequenceDiagram stateDiagram gitGraph quadrantChart classDiagram erDiagram gantt"
	NotePrefix = "> Diagram omitted: "
	docsRef    = " — see prompts/diagrams.md."
)

// Status carries the DIAGRAM_* globals for the envelope tail.
type Status struct {
	State   string
	Detail  string
	Keyword string
	Note    string
	Blocks  int

	Wrapped   bool
	Collapsed bool
}

// Result is the validated body plus status and ordered warnings.
type Result struct {
	Body   string
	Warns  []string
	Status Status
}

var (
	mermaidOpen = regexp.MustCompile("^[ \t]{0,3}```mermaid([ \t].*)?$")
	fenceClose  = regexp.MustCompile("^[ \t]{0,3}```[ \t]*$")
	detailsOpen = regexp.MustCompile("^[ \t]*<details([ \t>]|$)")
	detailsShut = regexp.MustCompile("^[ \t]*</details[ \t]*>$")
	blankLine   = regexp.MustCompile("^[ \t]*$")
	commentLine = regexp.MustCompile("^[ \t]*%%")
	dashLine    = regexp.MustCompile("^[ \t]*---[ \t]*$")
	mermaidAny  = regexp.MustCompile("(?m)^[ \t]{0,3}```mermaid([ \t].*)?$")
	singleClose = regexp.MustCompile("```[ \t]*$")
)

// Validate ports validate_behavioral_diagram. behavioral/collapsed carry the
// EFFECTIVE_PUBLISH_DIAGRAMS_BEHAVIORAL/EXPANDED knobs; marker is the footer
// divider that drop-notes land before.
func Validate(body string, behavioral, expanded bool, marker string) Result {
	st := Status{State: "absent", Detail: "no diagram candidates", Collapsed: true}
	body = stripOwnNotes(body)
	if !mermaidAny.MatchString(body) && !strings.Contains(body, Start) {
		return Result{Body: body, Status: st}
	}
	bodyLines := strings.Split(body, "\n")
	sc := newScanner()
	for nr, line := range bodyLines {
		sc.feed(nr+1, line)
	}
	sc.finish()
	cands := buildCands(sc.fences, sc.regions)
	if len(cands) == 0 {
		return Result{Body: body, Status: st}
	}
	fenceInRegion := map[int]int{}
	for _, f := range sc.fences {
		fenceInRegion[f.region]++
	}
	chk := &checker{cands: cands, regions: sc.regions, fenceInRegion: fenceInRegion, behavioral: behavioral, expanded: expanded, st: &st}
	chk.run()
	return Result{Body: renderBody(bodyLines, marker, chk.dels, chk.notes, chk.dline, chk.dopen), Status: st, Warns: chk.warns}
}

// stripOwnNotes drops our own one-line drop notes (pasted-output
// idempotency: they never stack).
func stripOwnNotes(body string) string {
	lines := strings.Split(body, "\n")
	kept := lines[:0:0]
	for _, ln := range lines {
		if strings.HasPrefix(ln, NotePrefix) {
			continue
		}
		kept = append(kept, ln)
	}
	return strings.Join(kept, "\n")
}

// fence is one mermaid fenced block found by the scanner.
type fence struct {
	kw      string
	open    int
	close   int
	region  int
	details int
}

// region is one marked wrapper region found by the scanner.
type region struct {
	idx, start, end int
}

// scanner ports the awk pre-pass: it records fenced mermaid blocks and
// marked regions with their details context.
type scanner struct {
	kw       string
	fences   []fence
	regions  []region
	fopen    int
	fregion  int
	fdetails int
	lastDet  int
	regionN  int
	rstart   int
	inFence  bool
	inRegion bool
}

// newScanner builds an empty scanner.
func newScanner() *scanner {
	return &scanner{}
}

// feed consumes one body line.
func (sc *scanner) feed(n int, line string) {
	t := strings.Trim(line, " \t")
	switch {
	case !sc.inFence && t == Start:
		sc.openRegion(n)
	case sc.inRegion && !sc.inFence && t == End:
		sc.regions = append(sc.regions, region{sc.regionN, sc.rstart, n})
		sc.inRegion = false
	case sc.feedDetails(n, line):
	case !sc.inFence && mermaidOpen.MatchString(line):
		sc.openFence(n, line)
	case sc.inFence:
		sc.feedFence(n, line)
	}
}

// openRegion opens a marked region.
func (sc *scanner) openRegion(n int) {
	sc.inRegion = true
	sc.regionN++
	sc.rstart = n
}

// feedDetails consumes details open/close lines, reporting true when handled.
func (sc *scanner) feedDetails(n int, line string) bool {
	if sc.inFence {
		return false
	}
	switch {
	case detailsOpen.MatchString(line):
		sc.lastDet = n
		return true
	case detailsShut.MatchString(line):
		sc.lastDet = 0
		return true
	default:
		return false
	}
}

// openFence opens a fenced block unless it is a single-line fence (open+
// close on one line), which GitHub never renders as a block.
func (sc *scanner) openFence(n int, line string) {
	rest := line
	i := 0
	for i < len(rest) && i < maxFenceIndent && (rest[i] == ' ' || rest[i] == '\t') {
		i++
	}
	rest = rest[i+maxFenceIndent:]
	if singleClose.MatchString(rest) {
		return
	}
	sc.inFence = true
	sc.fopen = n
	sc.fregion = 0
	if sc.inRegion {
		sc.fregion = sc.regionN
	}
	sc.fdetails = sc.lastDet
	sc.kw = ""
}

// feedFence consumes one line inside a fenced block.
func (sc *scanner) feedFence(n int, line string) {
	if fenceClose.MatchString(line) {
		sc.closeFence(n)
		return
	}
	if sc.kw == "" && !blankLine.MatchString(line) && !commentLine.MatchString(line) && !dashLine.MatchString(line) {
		if f := strings.Fields(line); len(f) > 0 {
			sc.kw = f[0]
		}
	}
}

// closeFence closes the open fenced block at line n (0 when unterminated).
func (sc *scanner) closeFence(n int) {
	if sc.kw == "" {
		sc.kw = emptyKeyword
	}
	sc.fences = append(sc.fences, fence{kw: sc.kw, open: sc.fopen, close: n, region: sc.fregion, details: sc.fdetails})
	sc.inFence = false
}

// finish closes trailing unterminated constructs.
func (sc *scanner) finish() {
	if sc.inFence {
		sc.closeFence(0)
	}
	if sc.inRegion {
		sc.regions = append(sc.regions, region{sc.regionN, sc.rstart, 0})
	}
}

// cand is one droppable diagram candidate: a fenced block (F) or a bare
// marked region (R).
type cand struct {
	line    string
	kind    string
	kw      string
	sortKey int
	o       int
	c       int
	rg      int
	dl      int
}

// fenceCand builds the candidate for one fenced block.
func fenceCand(f fence) cand {
	line := fmt.Sprintf("%d F %d %d %d %s %d", f.open, f.open, f.close, f.region, f.kw, f.details)
	return cand{line: line, kind: "F", kw: f.kw, sortKey: f.open, o: f.open, c: f.close, rg: f.region, dl: f.details}
}

// regionCand builds the candidate for one bare marked region.
func regionCand(r region) cand {
	line := fmt.Sprintf("%d R %d %d", r.start, r.start, r.end)
	return cand{line: line, kind: "R", kw: "", sortKey: r.start, o: r.start, c: r.end, rg: 0, dl: 0}
}

// buildCands assembles the sorted drop candidates from fences and bare
// marked regions.
func buildCands(fences []fence, regions []region) []cand {
	var cands []cand
	fenceInRegion := map[int]int{}
	for _, f := range fences {
		fenceInRegion[f.region]++
	}
	for _, f := range fences {
		cands = append(cands, fenceCand(f))
	}
	for _, r := range regions {
		if fenceInRegion[r.idx] > 0 {
			continue
		}
		cands = append(cands, regionCand(r))
	}
	slices.SortStableFunc(cands, func(a, b cand) int {
		if a.sortKey != b.sortKey {
			return a.sortKey - b.sortKey
		}
		return strings.Compare(a.line, b.line)
	})
	return cands
}

// checker applies the behavioral gating: drop reasons, kept-block detail,
// and the deletion spans.
type checker struct {
	allow         map[string]bool
	st            *Status
	fenceInRegion map[int]int
	firstNote     string
	keyword       string
	regions       []region
	warns         []string
	notes         []string
	dels          []span
	cands         []cand
	dline         int
	dopen         int
	behavioral    bool
	expanded      bool
	keptOne       bool
}

// span is a deleted line range.
type span struct{ s, e int }

// hugeLine stands in for an unterminated block end.
const hugeLine = 1000000000

// run applies the gating.
func (ck *checker) run() {
	ck.allow = allowSet()
	if !ck.behavioral {
		ck.stripAll()
		return
	}
	ck.keepFirst()
}

// allowSet builds the allowed-keyword set.
func allowSet() map[string]bool {
	allow := map[string]bool{}
	for w := range strings.FieldsSeq(Allowlist) {
		allow[w] = true
	}
	return allow
}

// stripAll drops every candidate when behavioral diagrams are opted out.
func (ck *checker) stripAll() {
	for _, cd := range ck.cands {
		s, e := ck.spanFor(cd.kind, cd.o, cd.c, cd.rg)
		ck.dels = append(ck.dels, span{s, e})
	}
	n := len(ck.cands)
	ck.warn(fmt.Sprintf("stripped %d behavioral diagram block(s) — "+
		"publish.diagrams.behavioral is off (opt-in knob, renders only when set)", n))
	ck.st.State = "stripped"
	ck.st.Blocks = n
	ck.st.Detail = fmt.Sprintf("stripped %d block(s): publish.diagrams.behavioral is off", n)
}

// keepFirst walks the candidates, keeping the first valid block.
func (ck *checker) keepFirst() {
	n := 0
	for _, cd := range ck.cands {
		n++
		if ck.dropCandidate(&cd) {
			continue
		}
		ck.keepCandidate(&cd)
	}
	ck.st.Blocks = n
	ck.st.Keyword = ck.keyword
	if ck.keptOne {
		ck.st.State = "kept"
	} else {
		ck.st.State = "dropped"
		ck.st.Detail = "dropped: " + ck.firstNote
	}
}

// dropCandidate drops one invalid candidate, reporting true when dropped.
func (ck *checker) dropCandidate(cd *cand) bool {
	switch {
	case cd.kind == "R":
		ck.drop(cd, "marked block carries no mermaid fence",
			"dropping behavioral diagram block: marked block carries no mermaid fence — kept a note")
		return true
	case cd.c == 0:
		ck.drop(cd, "unbalanced fence (no closing fence)",
			"dropping behavioral diagram: unbalanced fence (missing closing fence) — kept a note")
		return true
	case cd.kw == emptyKeyword:
		ck.drop(cd, "empty fence (no diagram lines)",
			"dropping behavioral diagram: empty fence (no diagram lines) — kept a note")
		return true
	case !ck.allow[cd.kw]:
		ck.drop(cd, fmt.Sprintf("unknown keyword '%s' (want one of: %s)", cd.kw, Allowlist),
			fmt.Sprintf("dropping behavioral diagram: unknown keyword '%s' (not in the GitHub-rendered subset) — kept a note", cd.kw))
		return true
	case ck.keptOne:
		ck.drop(cd, "one-diagram cap: kept the first block",
			"dropping extra behavioral diagram (one-diagram cap, kept the first) — kept a note")
		return true
	default:
		return false
	}
}

// drop deletes one candidate with its note and warning.
func (ck *checker) drop(cd *cand, reason, warning string) {
	s, e := ck.spanFor(markerFenced, cd.o, cd.c, cd.rg)
	ck.dels = append(ck.dels, span{s, e})
	ck.notes = append(ck.notes, NotePrefix+reason+docsRef)
	if ck.firstNote == "" {
		ck.firstNote = reason
	}
	if ck.st.Note == "" {
		ck.st.Note = NotePrefix + reason + docsRef
	}
	ck.warn(warning)
}

// keepCandidate keeps the first valid block.
func (ck *checker) keepCandidate(cd *cand) {
	ck.keptOne = true
	ck.keyword = cd.kw
	wrappedNote, markedNote := "", ""
	if cd.dl != 0 {
		ck.st.Wrapped = true
		ck.dline = cd.dl
		if ck.expanded {
			ck.dopen = 2
			ck.st.Collapsed = false
		} else {
			ck.dopen = 1
			ck.st.Collapsed = true
		}
	} else {
		ck.warn("behavioral diagram not wrapped in collapsed <details> — keeping as-is (wrap it per prompts/diagrams.md)")
		wrappedNote = ", unwrapped"
	}
	if cd.rg == 0 {
		ck.warn("behavioral diagram outside the marked wrapper — keeping as-is (wrap it per prompts/diagrams.md)")
		markedNote = ", unmarked"
	}
	if ck.st.Collapsed {
		ck.st.Detail = fmt.Sprintf("kept %s (collapsed%s%s)", cd.kw, wrappedNote, markedNote)
	} else {
		ck.st.Detail = fmt.Sprintf("kept %s (open%s%s)", cd.kw, wrappedNote, markedNote)
	}
}

// spanFor maps a candidate to its deletion span: lone fences in a singly
// occupied region widen to the region.
func (ck *checker) spanFor(kind string, o, c, rg int) (int, int) {
	if kind == "F" && rg != 0 && ck.fenceInRegion[rg] == 1 {
		if s, e, ok := ck.regionSpan(rg, c); ok {
			return s, e
		}
	}
	return o, hugeIfZero(c)
}

// regionSpan widens a lone fence to its region span.
func (ck *checker) regionSpan(rg, c int) (int, int, bool) {
	for _, r := range ck.regions {
		if r.idx == rg {
			return r.start, hugeIfZero(r.end), true
		}
	}
	return 0, hugeIfZero(c), false
}

// hugeIfZero stands huge in for an unterminated end.
func hugeIfZero(e int) int {
	if e == 0 {
		return hugeLine
	}
	return e
}

// warn records a warning.
func (ck *checker) warn(m string) {
	ck.warns = append(ck.warns, m)
}

// renderBody deletes the dropped spans and plants the drop notes.
func renderBody(bodyLines []string, marker string, dels []span, notes []string, dline, dopen int) string {
	rb := &bodyRenderer{marker: marker, dels: dels, notes: notes, dline: dline, dopen: dopen}
	for nr, line := range bodyLines {
		rb.feed(nr+1, line)
	}
	return rb.finish()
}

// bodyRenderer rebuilds the body: dropped spans cut, notes planted.
type bodyRenderer struct {
	marker string
	dels   []span
	notes  []string
	out    []string
	dline  int
	dopen  int
	noted  bool
}

// feed consumes one body line.
func (rb *bodyRenderer) feed(n int, line string) {
	if inSpans(rb.dels, n) {
		return
	}
	if !rb.noted && line == rb.marker {
		rb.out = append(rb.out, rb.notes...)
		rb.noted = true
	}
	if rb.dline > 0 && n == rb.dline {
		rb.out = append(rb.out, detailsOpenTag(rb.dopen))
		return
	}
	rb.out = append(rb.out, line)
}

// finish appends trailing notes and joins the body.
func (rb *bodyRenderer) finish() string {
	if !rb.noted {
		rb.out = append(rb.out, rb.notes...)
	}
	return strings.Join(rb.out, newline)
}

// detailsOpenTag renders the wrapper opening for the kept diagram.
func detailsOpenTag(dopen int) string {
	if dopen == 2 {
		return "<details open>"
	}
	return "<details>"
}

// inSpans reports whether line n falls in a deletion span.
func inSpans(dels []span, n int) bool {
	for _, d := range dels {
		if n >= d.s && n <= d.e {
			return true
		}
	}
	return false
}

// Diagram scanning constants.
const (
	// maxFenceIndent is the CommonMark allowance for fenced blocks (0-3 spaces).
	maxFenceIndent = 3
	// markerFenced tags candidates found inside fenced blocks.
	markerFenced = "F"
	// emptyKeyword is the drop-note keyword for empty diagrams.
	emptyKeyword = "EMPTY"
	// newline joins rendered body lines.
	newline = "\n"
)
