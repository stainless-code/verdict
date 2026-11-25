package dashboard

import (
	"strconv"
	"strings"
)

// Frame geometry. The layout is a fixed budget, not a solver: 80×24 is the
// smallest frame every tab fits, and anything smaller gets one resize panel
// instead of an uneven squeeze.
const (
	minCols = 80
	minRows = 24
	// maxCols and maxRows bound a --width/--height frame's allocation.
	maxCols = 1000
	maxRows = 500

	rowHeader = 0
	rowTabs   = 1
	rowRule   = 2
	bodyTop   = 3
	// footerRows are the rule, status, and key rows under the body.
	footerRows = 3

	margin  = 1
	indent  = 3
	numCol  = 6 // one count column: a gap plus up to five digits
	headCol = 8 // a short commit SHA
	markCol = 2 // the refused marker and its gap
	space   = " "
	// readOnly closes the header; its leading blank keeps a gap after a
	// truncated scope.
	readOnly = " read-only "

	// refusedShare is the fraction (1/n) of the width a refused bundle's name
	// and code may each take on the Overview.
	refusedShare = 3
	// The Queue's three text columns split the flexible width in percent.
	bundleShare = 40
	repoShare   = 35
	percent     = 100
)

// The Queue's eight columns, in order.
const (
	colBundle = iota
	colRepo
	colOpen
	colDeferred
	colAddressed
	colTerminal
	colModel
	colHead
	colCount
)

// compose draws one whole frame. It reads the snapshot and writes cells;
// nothing here knows about escape sequences or the terminal.
func compose(st *frameState, w, h int, lk look) *grid {
	g := newGrid(w, h)
	if w < minCols || h < minRows {
		for y, line := range []string{
			"verdict dashboard needs at least " + strconv.Itoa(minCols) + "×" + strconv.Itoa(minRows),
			"this terminal is " + strconv.Itoa(w) + "×" + strconv.Itoa(h),
			"resize it, or press q",
		} {
			g.text(0, y, w, line, roleWarn, lk.cs)
		}
		return g
	}
	st.width, st.page = w, h-bodyTop-footerRows
	drawHeader(g, st, lk.cs)
	drawTabs(g, st.tab, lk.cs)
	g.rule(rowRule, lk.cs)
	drawBody(g, st, box{top: bodyTop, rows: st.page}, lk.cs)
	g.rule(h-footerRows, lk.cs)
	drawStatus(g, st, h-2, lk.cs)
	drawKeys(g, st, h-1, lk.cs)
	return g
}

// drawBody draws the active tab, or the overlay that owns the keyboard.
func drawBody(g *grid, st *frameState, b box, cs charset) {
	switch {
	case st.help:
		drawHelp(g, b, cs)
	case st.picker != nil:
		drawPicker(g, st.picker, b, cs)
	case st.snap == nil:
		g.text(margin, b.top, g.w, "composing…", roleMuted, cs)
	case st.tab == tabOverview:
		drawOverview(g, st.snap, b, cs)
	default:
		drawTab(g, st, b, cs)
	}
}

// box is the body's row range.
type box struct{ top, rows int }

func drawHeader(g *grid, st *frameState, cs charset) {
	x := margin + g.text(margin, rowHeader, g.w, "verdict dashboard", roleTitle, cs)
	if st.snap != nil {
		x += g.text(x, rowHeader, g.w, space+string(cs.sep)+space, roleMuted, cs)
		g.text(x, rowHeader, g.w-x-len(readOnly)-margin-1, scopeLabel(st.snap), rolePlain, cs)
	}
	g.right(rowHeader, readOnly, roleMuted, cs)
}

// scopeLabel names what the dashboard reads: the one served root's path, or
// how many roots discovery found.
func scopeLabel(s *snapshot) string {
	if len(s.roots) == 1 {
		return s.roots[0].path
	}
	return plural(len(s.roots), "root")
}

// drawTabs brackets the active tab, so the selection survives a frame with no
// attributes at all (--once, NO_COLOR, a golden).
func drawTabs(g *grid, active tab, cs charset) {
	x := margin
	for i, label := range tabLabels {
		text, rl := space+label+space, rolePlain
		if tab(i) == active {
			text, rl = "["+label+"]", roleTab
		}
		x += g.text(x, rowTabs, g.w, text, rl, cs) + 1
	}
}

func drawStatus(g *grid, st *frameState, y int, cs charset) {
	s := st.snap
	if s == nil {
		return
	}
	at := "at " + s.at + space
	g.right(y, at, roleMuted, cs)
	x, end := margin, g.w-1-stringWidth(at)-1 // the left items stop a cell short of the clock
	put := func(text string, rl role) {
		x += g.text(x, y, max(end-x, 0), text, rl, cs) + 2
	}
	switch {
	case s.partial:
		put("partial: totals withheld", roleWarn)
	default:
		put("complete", roleOK)
	}
	bundles, stores := s.failureCounts()
	if bundles > 0 {
		put(plural(bundles, "refused bundle"), roleBad)
	}
	if stores > 0 {
		put(plural(stores, "store error"), roleBad)
	}
	if len(s.warnings) > 0 {
		put(plural(len(s.warnings), "warning"), roleWarn)
	}
	for _, extra := range st.statusExtras() {
		put(extra.text, extra.role)
	}
}

type statusItem struct {
	text string
	role role
}

// statusExtras are the live session's transient states, after the
// snapshot's own: a compose in flight, the `/` filter, a one-shot note.
func (st *frameState) statusExtras() []statusItem {
	var out []statusItem
	if st.composing {
		out = append(out, statusItem{"composing…", roleMuted})
	}
	if st.searching || st.search != "" {
		out = append(out, statusItem{"/" + st.search, roleTitle})
	}
	if st.note != "" {
		out = append(out, statusItem{st.note, roleWarn})
	}
	return out
}

// tabHints are each tab's key row, the six or so keys that tab answers.
var tabHints = [tabCount]string{
	"1-7 tabs  Enter queue  r refresh  ? help  q quit",
	"j/k move  Enter detail  / search  r refresh  ? help  q quit",
	"j/k move  Enter diff  y copy fix  / search  Esc back  ? help",
	"j/k scroll  Tab next file  u split  / search  Esc back  ? help",
	"j/k move  b baseline  / search  Esc back  ? help  q quit",
	"j/k move  Tab grouping  / search  r refresh  ? help  q quit",
	"j/k move  Tab grouping  / search  r refresh  ? help  q quit",
}

func drawKeys(g *grid, st *frameState, y int, cs charset) {
	hint := "--once snapshot; run verdict dashboard in a terminal for the live view"
	if st.live {
		hint = tabHints[st.tab]
	}
	g.text(margin, y, g.w-margin-1, hint, roleMuted, cs)
}

// drawOverview draws the portfolio totals, the served roots, and the bundles
// the layer refused.
func drawOverview(g *grid, s *snapshot, b box, cs charset) {
	y := b.top
	end := b.top + b.rows
	g.text(margin, y, g.w, "Portfolio", roleTitle, cs)
	y++
	drawTotals(g, s.counts, y, cs)
	y += 2
	y = drawRoots(g, s.roots, y, end, cs)
	if len(s.refused) == 0 || y+1 >= end {
		return
	}
	y++
	g.text(margin, y, g.w, "Refused", roleTitle, cs)
	y++
	list(g, len(s.refused), y, end, cs, func(i, row int) {
		ref := s.refused[i]
		x := indent + g.text(indent, row, g.w, "! ", roleBad, cs)
		x += g.text(x, row, g.w/refusedShare, ref.label(), rolePlain, cs) + 2
		x += g.text(x, row, g.w/refusedShare, ref.code, roleBad, cs) + 2
		g.text(x, row, g.w-x-margin, ref.message, roleMuted, cs)
	})
}

// drawTotals prints the five portfolio counts. When the layer withheld them
// (a refused or truncated bundle makes any sum a lie) each shows the unknown
// glyph, never a smaller number.
func drawTotals(g *grid, c *counts, y int, cs charset) {
	names := [...]string{"bundles", statusOpen, "deferred", "addressed", "terminal"}
	var values [len(names)]string
	for i := range values {
		values[i] = string(cs.unknown)
	}
	if c != nil {
		for i, n := range [...]int{c.bundles, c.open, c.deferred, c.addressed, c.terminal} {
			values[i] = strconv.Itoa(n)
		}
	}
	x := indent
	for i, name := range names {
		x += g.text(x, y, g.w, name+space, roleMuted, cs)
		x += g.text(x, y, g.w, values[i], countRole(c != nil, names[i] == statusOpen, values[i]), cs) + indent
	}
}

func countRole(known, isOpen bool, value string) role {
	switch {
	case !known:
		return roleWarn
	case isOpen && value != "0":
		return roleWarn
	default:
		return rolePlain
	}
}

func drawRoots(g *grid, roots []rootRow, y, end int, cs charset) int {
	g.text(margin, y, g.w, "Roots", roleTitle, cs)
	g.right(y, "bundles ", roleMuted, cs)
	y++
	if len(roots) == 0 {
		g.text(indent, y, g.w, "no repositories found", roleMuted, cs)
		return y + 1
	}
	return list(g, len(roots), y, end, cs, func(i, row int) {
		r := roots[i]
		x := indent + g.text(indent, row, g.w, r.id, roleMuted, cs) + 2
		g.text(x, row, g.w-x-numCol-margin-1, r.path, rolePlain, cs)
		g.right(row, strconv.Itoa(r.bundles)+space, rolePlain, cs)
	})
}

// list draws up to the rows left before end, giving the last one to a
// "… N more" line when the items do not fit; it returns the next free row.
func list(g *grid, n, y, end int, cs charset, draw func(i, row int)) int {
	room := end - y
	shown := n
	if n > room {
		shown = max(room-1, 0)
	}
	for i := range shown {
		draw(i, y+i)
	}
	y += shown
	if shown < n && y < end {
		g.text(indent, y, g.w, string(cs.ellipsis)+space+strconv.Itoa(n-shown)+" more", roleMuted, cs)
		y++
	}
	return y
}

// queueCols is the Queue column plan for one width: the three text columns
// share what the fixed count and head columns leave.
type queueCols struct {
	bundle, repo, model int
}

func planQueue(w int) queueCols {
	// The gaps after bundle and repo, before and after model, then the head.
	const fixed = margin + markCol + 1 + 1 + (colTerminal-colOpen+1)*numCol + 2 + 1 + headCol + margin
	flex := w - fixed
	bundle := flex * bundleShare / percent
	repo := flex * repoShare / percent
	return queueCols{bundle: bundle, repo: repo, model: flex - bundle - repo}
}

// queueCells is one bundle's column text and the role its counts take: a
// refused bundle shows the unknown glyph in every count, never a zero.
func queueCells(r *bundleRow, unknown string) ([colCount]string, role) {
	cells := [colCount]string{
		colBundle: r.bundle, colRepo: orDash(r.repo, unknown), colModel: orDash(r.model, unknown),
		colOpen: unknown, colDeferred: unknown, colAddressed: unknown, colTerminal: unknown, colHead: unknown,
	}
	if r.refused {
		return cells, roleMuted
	}
	cells[colOpen], cells[colDeferred] = strconv.Itoa(r.open), strconv.Itoa(r.deferred)
	cells[colAddressed], cells[colTerminal] = strconv.Itoa(r.addressed), strconv.Itoa(r.terminal)
	cells[colHead] = orDash(shortHead(r.head), unknown)
	if r.open > 0 {
		return cells, roleWarn
	}
	return cells, rolePlain
}

// queueRow lays one row's eight cells on the column plan; the counts are
// right-aligned so digits line up.
func queueRow(g *grid, y int, cols queueCols, c *[colCount]string, text, counts role, cs charset) {
	x := margin + markCol
	for i, width := range [...]int{cols.bundle, cols.repo} {
		g.text(x, y, width, c[i], text, cs)
		x += width + 1
	}
	for i := colOpen; i <= colTerminal; i++ {
		v := truncate(c[i], numCol-1, cs.ellipsis)
		g.text(x+numCol-stringWidth(v), y, numCol, v, counts, cs)
		x += numCol
	}
	x += 2
	g.text(x, y, cols.model, c[colModel], text, cs)
	x += cols.model + 1
	g.text(x, y, headCol, c[colHead], roleMuted, cs)
}

func shortHead(head string) string {
	if len(head) > headCol {
		return head[:headCol]
	}
	return head
}

func orDash(s, dash string) string {
	if strings.TrimSpace(s) == "" {
		return dash
	}
	return s
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + space + noun + "s"
}

// failureCounts splits the tolerated failures into refused bundles and
// store-level errors (a root whose store could not be listed at all).
func (s *snapshot) failureCounts() (bundles, stores int) {
	for _, f := range s.refused {
		if f.bundle == "" {
			stores++
		} else {
			bundles++
		}
	}
	return bundles, stores
}

// label names what failed: the bundle, or the root for a store-level error.
func (f *refusedRow) label() string {
	if f.bundle == "" {
		return "store " + f.root
	}
	return f.bundle
}
