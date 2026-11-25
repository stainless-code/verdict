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

// tab is one dashboard view. The interactive loop switches between them on
// their digit; --once draws the Overview.
type tab uint8

const (
	tabOverview tab = iota
	tabQueue
)

var tabLabels = [...]string{tabOverview: "1 Overview", tabQueue: "2 Queue"}

// frameState is everything a frame is composed from.
type frameState struct {
	snap *snapshot // nil while the first compose runs
	tab  tab
	live bool // an interactive session (key hints) rather than a --once snapshot
}

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
	drawHeader(g, st, lk.cs)
	drawTabs(g, st.tab, lk.cs)
	g.rule(rowRule, lk.cs)
	body := box{top: bodyTop, rows: h - bodyTop - footerRows}
	switch {
	case st.snap == nil:
		g.text(margin, body.top, w, "composing…", roleMuted, lk.cs)
	case st.tab == tabQueue:
		drawQueue(g, st.snap, body, lk.cs)
	default:
		drawOverview(g, st.snap, body, lk.cs)
	}
	g.rule(h-footerRows, lk.cs)
	drawStatus(g, st.snap, h-2, lk.cs)
	drawKeys(g, st.live, h-1, lk.cs)
	return g
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

func drawStatus(g *grid, s *snapshot, y int, cs charset) {
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
	if len(s.refused) > 0 {
		put(plural(len(s.refused), "refused bundle"), roleBad)
	}
	if len(s.warnings) > 0 {
		put(plural(len(s.warnings), "warning"), roleWarn)
	}
}

func drawKeys(g *grid, live bool, y int, cs charset) {
	hint := "--once snapshot; run verdict dashboard in a terminal for the live view"
	if live {
		hint = "1 overview   2 queue   q quit"
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
		x += g.text(x, row, g.w/refusedShare, ref.bundle, rolePlain, cs) + 2
		x += g.text(x, row, g.w/refusedShare, ref.code, roleBad, cs) + 2
		g.text(x, row, g.w-x-margin, ref.message, roleMuted, cs)
	})
}

// drawTotals prints the five portfolio counts. When the layer withheld them
// (a refused or truncated bundle makes any sum a lie) each shows the unknown
// glyph, never a smaller number.
func drawTotals(g *grid, c *counts, y int, cs charset) {
	names := [...]string{"bundles", "open", "deferred", "addressed", "terminal"}
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
		x += g.text(x, y, g.w, values[i], countRole(c != nil, names[i] == "open", values[i]), cs) + indent
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

// drawQueue draws one row per bundle in the layer's order. A refused bundle
// keeps its row, marked `!`, with every count unknown.
func drawQueue(g *grid, s *snapshot, b box, cs charset) {
	cols := planQueue(g.w)
	queueRow(g, b.top, cols, &[colCount]string{"BUNDLE", "REPO", "OPEN", "DEFER", "ADDR", "TERM", "MODEL", "HEAD"},
		roleMuted, roleMuted, cs)
	if len(s.rows) == 0 {
		g.text(indent, b.top+1, g.w, "no review bundles under .verdict/state", roleMuted, cs)
		return
	}
	list(g, len(s.rows), b.top+1, b.top+b.rows, cs, func(i, y int) {
		r := s.rows[i]
		if r.refused {
			g.text(margin, y, markCol, "!", roleBad, cs)
		}
		cells, countsRole := queueCells(&r, string(cs.unknown))
		queueRow(g, y, cols, &cells, rolePlain, countsRole, cs)
	})
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
