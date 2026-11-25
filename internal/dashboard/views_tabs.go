package dashboard

import (
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/promptkeys"
)

// fileHeaderMark tags a Diff file header row, so Tab can find the next file.
// It can never be a finding sig.
const fileHeaderMark = "\x00file"

// Tab layout widths.
const (
	sevCol      = 4  // a severity badge and its gap
	statusCol   = 11 // a finding status and its gap
	evidenceCol = 8  // the rung bar, the quote mark, and a gap
	locCol      = 22 // a path:line before the finding's first line
	lineNoCol   = 5  // one diff line number
	paneRows    = 4  // the Detail pane: a rule and three body lines
	minListRows = 4  // the Detail pane only appears when the list keeps this many
	tableDimCol = 24 // a Cost or Precision grouping value
	tableNumCol = 12 // one Cost or Precision number

	// The Detail header's lines: bundle and gate, the verdict counts, the
	// gate dimensions, then the column labels.
	detailVerdictRow = 1
	detailDimsRow    = 2
	detailColsRow    = 3
	detailHeadRows   = 4

	// sideGutter is a split half's sign column and its gaps.
	sideGutter = 3

	statusOpen = "open"
	kindAdd    = "add"
	kindDel    = "del"
)

// queueHeader labels the Queue's columns.
var queueHeader = [colCount]string{"BUNDLE", "REPO", "OPEN", "DEFER", "ADDR", "TERM", "MODEL", "HEAD"}

// emptyText is each list tab's line when it has no rows.
var emptyText = [tabCount]string{
	tabQueue:     "no review bundles under .verdict/state",
	tabDetail:    "no findings in this bundle",
	tabDiff:      "the bundle's diff is empty",
	tabCompare:   "no findings in any bucket",
	tabCost:      "no cost recorded yet",
	tabPrecision: "no findings to measure yet",
}

// drawTab draws a list tab: its fixed header lines, then a window of rows
// that keeps the cursor visible. A tab whose view refused shows why instead.
func drawTab(g *grid, st *frameState, b box, cs charset) {
	if st.tabErr != "" {
		g.text(margin, b.top, g.w-2*margin, st.tabErr, roleBad, cs)
		return
	}
	head, ok := drawTabHeader(g, st, b, cs)
	if !ok {
		return
	}
	end := b.top + b.rows
	if st.tab == tabDetail && b.rows-head-paneRows >= minListRows {
		end -= paneRows
		drawDetailPane(g, st, end, cs)
	}
	rows := st.visibleRows()
	if len(rows) == 0 {
		empty := emptyText[st.tab]
		if st.search != "" {
			empty = "no rows match /" + st.search
		}
		g.text(indent, b.top+head, g.w, empty, roleMuted, cs)
		return
	}
	drawRows(g, rows, b.top+head, end, st, cs)
}

// drawRows draws the window of rows that keeps the cursor in view. Rows past
// the window end in a "… N more" line. Only the live view marks the cursor.
func drawRows(g *grid, rows []row, top, end int, st *frameState, cs charset) {
	pos := min(st.pos[st.tab], len(rows)-1)
	start, shown := window(len(rows), end-top, pos)
	for i := range shown {
		y := top + i
		rows[start+i].draw(g, y, cs)
		if st.live && start+i == pos {
			g.highlight(y, cs)
		}
	}
	if rest := len(rows) - start - shown; rest > 0 {
		g.text(indent, top+shown, g.w, string(cs.ellipsis)+space+strconv.Itoa(rest)+" more", roleMuted, cs)
	}
}

// window is the first row and the row count that fit room lines with pos
// visible; one line is kept for "… N more" while rows remain below.
func window(n, room, pos int) (start, shown int) {
	if n <= room {
		return 0, n
	}
	shown = max(room-1, 0)
	start = max(0, pos-shown+1)
	if start+room >= n {
		return n - room, room
	}
	return start, shown
}

// highlight paints row y as the cursor row, with a text marker in the left
// margin for frames that carry no attributes.
func (g *grid) highlight(y int, cs charset) {
	row := g.cells[y*g.w : (y+1)*g.w]
	for x := range row {
		row[x].role = roleSel
	}
	row[0] = cell{r: cs.mark, role: roleSel}
}

// drawTabHeader draws the active tab's fixed lines and reports how many it
// used; ok is false when the tab has nothing to list yet.
func drawTabHeader(g *grid, st *frameState, b box, cs charset) (int, bool) {
	switch st.tab {
	case tabQueue:
		queueRow(g, b.top, planQueue(g.w), &queueHeader, roleMuted, roleMuted, cs)
		return 1, true
	case tabDetail:
		return drawDetailHeader(g, st, b, cs)
	case tabDiff:
		return drawDiffHeader(g, st, b, cs)
	case tabCompare:
		return drawCompareHeader(g, st, b, cs)
	default:
		return drawTableHeader(g, st, b, cs)
	}
}

// pending draws the placeholder for a tab whose view has not arrived.
func pending(g *grid, b box, st *frameState, cs charset) (int, bool) {
	text := "composing…"
	if st.target.bundle == "" && st.tab != tabCost && st.tab != tabPrecision {
		text = "no bundle selected: pick one on the Queue (2) and press Enter"
	}
	g.text(margin, b.top, g.w, text, roleMuted, cs)
	return 0, false
}

// tabRows builds the active tab's list.
func tabRows(st *frameState) []row {
	switch st.tab {
	case tabQueue:
		return queueRows(st.snap)
	case tabDetail:
		return detailRows(st.detail)
	case tabDiff:
		return diffRows(st.diff, st.split)
	case tabCompare:
		return compareRows(st.cmp)
	case tabCost:
		return tableRows(st.cost)
	case tabPrecision:
		return tableRows(st.prec)
	default:
		return nil
	}
}

func queueRows(s *snapshot) []row {
	if s == nil {
		return nil
	}
	rows := make([]row, 0, len(s.rows))
	for _, r := range s.rows {
		rows = append(rows, row{
			key:    r.bundle + space + r.repo,
			target: &target{root: r.root, bundle: r.bundle},
			draw: func(g *grid, y int, cs charset) {
				if r.refused {
					g.text(margin, y, markCol, "!", roleBad, cs)
				}
				cells, countsRole := queueCells(&r, string(cs.unknown))
				queueRow(g, y, planQueue(g.w), &cells, rolePlain, countsRole, cs)
			},
		})
	}
	return rows
}

// sevBadge is a severity's shape: `!!!`, `!!`, `!` for the three published
// tiers in core.TierValues order, `-` for untagged.
func sevBadge(sev string) (string, role) {
	switch core.TierRank(sev) {
	case 0:
		return "!!!", roleBad
	case 1:
		return "!!", roleWarn
	case len(core.TierValues) - 1:
		return "!", rolePlain
	default:
		return "-", roleMuted
	}
}

// strongerSeverity keeps the higher-ranked of two severities.
func strongerSeverity(a, b string) string {
	if a == "" || core.TierRank(b) < core.TierRank(a) {
		return b
	}
	return a
}

// evidenceBadge is the two evidence axes: the rigor rung as a five-cell bar
// (promptkeys.RigorValues order, said to reproduced), then the quote check.
func evidenceBadge(rigor, quote string, cs charset) string {
	reached := slices.Index(promptkeys.RigorValues, rigor) + 1
	bar := strings.Repeat(string(cs.rungOn), reached) +
		strings.Repeat(string(cs.rungOff), len(promptkeys.RigorValues)-reached)
	switch quote {
	case "verified":
		return bar + string(cs.verified)
	case "unverified":
		return bar + "?"
	default:
		return bar + space
	}
}

func detailRows(d *detailData) []row {
	if d == nil {
		return nil
	}
	rows := make([]row, 0, len(d.items))
	for _, it := range d.items {
		rows = append(rows, row{
			key: it.locator + space + it.body,
			sig: it.sig,
			draw: func(g *grid, y int, cs charset) {
				badge, rl := sevBadge(it.sev)
				x := margin + markCol
				g.text(x, y, sevCol, badge, rl, cs)
				g.text(x+sevCol, y, statusCol-1, it.status, statusRole(it.status), cs)
				g.text(x+sevCol+statusCol, y, evidenceCol, evidenceBadge(it.rigor, it.quote, cs), roleMuted, cs)
				x += sevCol + statusCol + evidenceCol
				g.text(x, y, locCol, it.locator, rolePlain, cs)
				x += locCol + 2
				g.text(x, y, g.w-x-margin, firstLine(it.body), rolePlain, cs)
			},
		})
	}
	return rows
}

func statusRole(status string) role {
	if status == statusOpen {
		return roleWarn
	}
	return roleMuted
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func drawDetailHeader(g *grid, st *frameState, b box, cs charset) (int, bool) {
	d := st.detail
	if d == nil {
		return pending(g, b, st, cs)
	}
	sep := space + string(cs.sep) + space
	x := margin + g.text(margin, b.top, g.w, d.bundle, roleTitle, cs)
	unknown := string(cs.unknown)
	scope := sep + orDash(d.repo, unknown) + sep + "head " + orDash(shortHead(d.head), unknown) + sep
	x += g.text(x, b.top, g.w, scope, rolePlain, cs)
	gate, rl := "gate —", roleMuted
	if d.gatePassed != nil {
		gate, rl = "gate FAIL", roleBad
		if *d.gatePassed {
			gate, rl = "gate PASS", roleOK
		}
	}
	g.text(x, b.top, g.w-x-margin, gate, rl, cs)
	counts := make([]string, len(verdictKeys))
	for i, k := range verdictKeys {
		counts[i] = k + space + strconv.Itoa(d.verdict[i])
	}
	g.text(indent, b.top+detailVerdictRow, g.w, strings.Join(counts, "   "), rolePlain, cs)
	dims := make([]string, 0, len(d.dims))
	for _, dim := range d.dims {
		dims = append(dims, dim.id+space+dim.status)
	}
	g.text(indent, b.top+detailDimsRow, g.w-indent-margin, strings.Join(dims, sep), roleMuted, cs)
	x = margin + markCol
	for _, h := range [...]struct {
		text  string
		width int
	}{{"SEV", sevCol}, {"STATUS", statusCol}, {"RIGOR", evidenceCol}, {"LOCATION", locCol + 2}, {"FINDING", g.w}} {
		g.text(x, b.top+detailColsRow, h.width, h.text, roleMuted, cs)
		x += h.width
	}
	return detailHeadRows, true
}

// drawDetailPane shows the selected finding's full body under the list.
func drawDetailPane(g *grid, st *frameState, top int, cs charset) {
	g.rule(top, cs)
	r, ok := st.selected()
	if !ok || st.detail == nil {
		return
	}
	var body string
	for _, it := range st.detail.items {
		if it.sig == r.sig {
			body = it.locator + "  " + it.body
		}
	}
	for i, line := range wrap(body, g.w-2*margin, paneRows-1) {
		g.text(margin, top+1+i, g.w-2*margin, line, rolePlain, cs)
	}
}

// wrap breaks text into at most limit lines of width cells, honoring its
// own newlines; a word longer than a line is cut by the grid's truncation.
func wrap(text string, width, limit int) []string {
	var out []string
	for para := range strings.SplitSeq(text, "\n") {
		out = append(out, wrapPara(para, width)...)
		if len(out) >= limit {
			return out[:limit]
		}
	}
	return out
}

// wrapPara greedily fills lines of width cells with one paragraph's words.
func wrapPara(para string, width int) []string {
	var out []string
	line := ""
	for word := range strings.FieldsSeq(para) {
		switch {
		case line == "":
			line = word
		case stringWidth(line)+1+stringWidth(word) > width:
			out = append(out, line)
			line = word
		default:
			line += space + word
		}
	}
	return append(out, line)
}

func drawDiffHeader(g *grid, st *frameState, b box, cs charset) (int, bool) {
	d := st.diff
	if d == nil {
		return pending(g, b, st, cs)
	}
	parts := make([]string, 0, len(d.lanes))
	for _, l := range d.lanes {
		parts = append(parts, l.lane+space+strconv.Itoa(l.files)+"f +"+strconv.Itoa(l.adds)+"/-"+strconv.Itoa(l.dels))
	}
	mode := "unified"
	if st.split && g.w >= splitCols {
		mode = "split"
	}
	g.right(b.top, mode+space, roleMuted, cs)
	g.text(margin, b.top, g.w-len(mode)-indent, strings.Join(parts, space+string(cs.sep)+space), roleTitle, cs)
	return 1, true
}

// diffRows flattens every file into rows: a header per file and hunk, then
// one row per line (unified) or per old/new pair (split).
func diffRows(d *diffData, split bool) []row {
	if d == nil {
		return nil
	}
	var rows []row
	for _, f := range d.files {
		rows = append(rows, row{key: f.path, sig: fileHeaderMark, draw: func(g *grid, y int, cs charset) {
			g.text(margin, y, g.w-2*margin, string(cs.rule)+string(cs.rule)+space+f.path+"  "+f.lane+
				"  +"+strconv.Itoa(f.adds)+"/-"+strconv.Itoa(f.dels), roleTitle, cs)
		}})
		for _, h := range f.hunks {
			rows = append(rows, hunkRows(f.path, h, split)...)
		}
	}
	return rows
}

func hunkRows(path string, h diffHunk, split bool) []row {
	header := h.header
	rows := []row{{key: path, draw: func(g *grid, y int, cs charset) {
		g.text(indent, y, g.w-indent-margin, header, roleMuted, cs)
	}}}
	if split {
		for _, p := range pairLines(h.lines) {
			rows = append(rows, row{key: path, draw: func(g *grid, y int, cs charset) { drawSplitLine(g, y, p, cs) }})
		}
		return rows
	}
	for _, l := range h.lines {
		rows = append(rows, row{key: path, draw: func(g *grid, y int, cs charset) { drawUnifiedLine(g, y, &l, cs) }})
	}
	return rows
}

// lineLook is a diff line's sign and role. The sign keeps a mono frame
// readable: color is never the only signal.
func lineLook(kind string) (string, role) {
	switch kind {
	case kindAdd:
		return "+", roleAdd
	case kindDel:
		return "-", roleDel
	default:
		return space, rolePlain
	}
}

func lineNo(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func drawUnifiedLine(g *grid, y int, l *diffLine, cs charset) {
	drawFindingMark(g, y, l.sev, cs)
	sign, rl := lineLook(l.kind)
	x := markCol
	for _, no := range [...]int{l.oldNo, l.newNo} {
		s := lineNo(no)
		g.text(x+lineNoCol-len(s), y, lineNoCol, s, roleMuted, cs)
		x += lineNoCol + 1
	}
	g.text(x, y, 2, sign, rl, cs)
	g.text(x+2, y, g.w-x-2-margin, l.text, rl, cs)
}

// drawFindingMark flags a line a finding is anchored on, in its role.
func drawFindingMark(g *grid, y int, sev string, cs charset) {
	if sev == "" {
		return
	}
	_, rl := sevBadge(sev)
	g.text(margin, y, 1, string(cs.mark), rl, cs)
}

// linePair is one split-view row: the old side, the new side, either nil.
type linePair struct {
	old, new *diffLine
}

// pairLines lines up a hunk for the split view: context on both sides, and
// each run of deletions beside the run of additions that follows it.
func pairLines(lines []diffLine) []linePair {
	var out []linePair
	for i := 0; i < len(lines); {
		var pairs []linePair
		pairs, i = nextPairs(lines, i)
		out = append(out, pairs...)
	}
	return out
}

// nextPairs pairs the context line or the change run starting at i.
func nextPairs(lines []diffLine, i int) ([]linePair, int) {
	if lines[i].kind != kindDel && lines[i].kind != kindAdd {
		return []linePair{{old: &lines[i], new: &lines[i]}}, i + 1
	}
	dels, adds, next := changeRun(lines, i)
	pairs := make([]linePair, max(len(dels), len(adds)))
	for j := range pairs {
		if j < len(dels) {
			pairs[j].old = dels[j]
		}
		if j < len(adds) {
			pairs[j].new = adds[j]
		}
	}
	return pairs, next
}

// changeRun collects the deletions then additions starting at i.
func changeRun(lines []diffLine, i int) (dels, adds []*diffLine, next int) {
	for ; i < len(lines) && lines[i].kind == kindDel; i++ {
		dels = append(dels, &lines[i])
	}
	for ; i < len(lines) && lines[i].kind == kindAdd; i++ {
		adds = append(adds, &lines[i])
	}
	return dels, adds, i
}

func drawSplitLine(g *grid, y int, p linePair, cs charset) {
	half := (g.w - markCol) / 2
	if p.new != nil {
		drawFindingMark(g, y, p.new.sev, cs)
	}
	drawHalf(g, y, markCol, half-1, p.old, true, cs)
	drawHalf(g, y, markCol+half, half-margin, p.new, false, cs)
}

func drawHalf(g *grid, y, x, width int, l *diffLine, old bool, cs charset) {
	if l == nil {
		return
	}
	no := l.newNo
	if old {
		no = l.oldNo
	}
	sign, rl := lineLook(l.kind)
	s := lineNo(no)
	g.text(x+lineNoCol-len(s), y, lineNoCol, s, roleMuted, cs)
	g.text(x+lineNoCol+1, y, 2, sign, rl, cs)
	g.text(x+lineNoCol+sideGutter, y, width-lineNoCol-sideGutter, l.text, rl, cs)
}

func drawCompareHeader(g *grid, st *frameState, b box, cs charset) (int, bool) {
	if st.baseline == "" {
		if st.target.bundle == "" {
			return pending(g, b, st, cs)
		}
		g.text(margin, b.top, g.w, "compare "+st.target.bundle+" against a baseline: press b to pick one", roleMuted, cs)
		return 0, false
	}
	d := st.cmp
	if d == nil {
		return pending(g, b, st, cs)
	}
	g.text(margin, b.top, g.w, st.target.bundle+" against "+d.baseline+" ("+plural(d.found, "finding")+")", roleTitle, cs)
	parts := make([]string, len(compareBuckets))
	for i, name := range compareBuckets {
		parts[i] = strings.ReplaceAll(name, "_", space) + space + strconv.Itoa(len(d.buckets[i]))
	}
	g.text(indent, b.top+1, g.w, strings.Join(parts, "   "), rolePlain, cs)
	return 2, true
}

func compareRows(d *compareData) []row {
	if d == nil {
		return nil
	}
	var rows []row
	for i, name := range compareBuckets {
		rows = append(rows, bucketRows(name, d.buckets[i])...)
	}
	return rows
}

// bucketRows is one compare bucket: its label, then one row per finding.
// An empty bucket draws nothing; the header counts it.
func bucketRows(name string, items []compareRow) []row {
	if len(items) == 0 {
		return nil
	}
	label := strings.ToUpper(strings.ReplaceAll(name, "_", space))
	rows := []row{{draw: func(g *grid, y int, cs charset) { g.text(margin, y, g.w, label, roleTitle, cs) }}}
	for _, c := range items {
		loc := c.path + ":" + strconv.Itoa(c.line)
		rows = append(rows, row{key: loc + space + c.claim, draw: func(g *grid, y int, cs charset) {
			badge, rl := sevBadge(c.sev)
			x := margin + markCol
			g.text(x, y, sevCol, badge, rl, cs)
			g.text(x+sevCol, y, statusCol-1, c.status, statusRole(c.status), cs)
			x += sevCol + statusCol
			g.text(x, y, locCol, loc, rolePlain, cs)
			x += locCol + 2
			g.text(x, y, g.w-x-margin, firstLine(c.claim), rolePlain, cs)
		}})
	}
	return rows
}

func drawTableHeader(g *grid, st *frameState, b box, cs charset) (int, bool) {
	t := st.cost
	if st.tab == tabPrecision {
		t = st.prec
	}
	if t == nil {
		return pending(g, b, st, cs)
	}
	g.text(margin, b.top, g.w-2*margin, "by "+t.by+space+string(cs.sep)+space+t.note, roleTitle, cs)
	tableLine(g, b.top+1, append([]string{strings.ToUpper(t.by)}, t.header...), roleMuted, cs)
	return 2, true
}

func tableRows(t *tableData) []row {
	if t == nil {
		return nil
	}
	rows := make([]row, 0, len(t.rows)+1)
	for _, r := range t.rows {
		rows = append(rows, row{key: r[0], draw: func(g *grid, y int, cs charset) { tableLine(g, y, r, rolePlain, cs) }})
	}
	totals := t.totals
	rows = append(rows, row{draw: func(g *grid, y int, cs charset) { tableLine(g, y, totals, roleTitle, cs) }})
	return rows
}

// tableLine lays one row: the grouping value, then right-aligned numbers.
func tableLine(g *grid, y int, cells []string, rl role, cs charset) {
	if len(cells) == 0 {
		return
	}
	numCol := min(tableNumCol, (g.w-tableDimCol-markCol-margin)/max(len(cells)-1, 1))
	x := margin + markCol
	g.text(x, y, tableDimCol-1, cells[0], rl, cs)
	x += tableDimCol
	for _, c := range cells[1:] {
		v := truncate(orDash(c, string(cs.unknown)), numCol-1, cs.ellipsis)
		g.text(x+numCol-stringWidth(v), y, numCol, v, rl, cs)
		x += numCol
	}
}

// helpLines are the `?` overlay: every key, the vocabulary, the exit codes.
var helpLines = []string{
	"Keys",
	"  1-7 tab  j/k or arrows move  g/G or Home/End first/last  Ctrl-D/U half page",
	"  Enter open: Queue to Detail, Detail to Diff   Esc back, or clear the search",
	"  / search this list   Tab next file (Diff) or grouping (Cost, Precision)",
	"  u split/unified diff (100+ columns)   b pick the Compare baseline",
	"  y copy the finding's fix prompt (OSC 52)   r refresh   ? help   q quit",
	"Severity  !!! critical  !! important  ! informational  - untagged",
	"Rigor     said < pointed < walked < ran < reproduced, then the quote check:",
	"          verified, ? unverified, blank when unchecked",
	"Exit      0 quit   1 refused   2 usage   3 Ctrl-C or SIGTERM",
	"Read-only: nothing here writes a store, posts, or runs a command.",
	"Esc or ? closes this help.",
}

func drawHelp(g *grid, b box, cs charset) {
	for i, line := range helpLines {
		if i >= b.rows {
			return
		}
		rl := rolePlain
		if i == 0 {
			rl = roleTitle
		}
		g.text(margin, b.top+i, g.w-2*margin, line, rl, cs)
	}
}

func drawPicker(g *grid, p *picker, b box, cs charset) {
	g.text(margin, b.top, g.w, "baseline bundle (Enter picks, Esc cancels)", roleTitle, cs)
	rows := make([]row, len(p.rows))
	for i, name := range p.rows {
		rows[i] = row{draw: func(g *grid, y int, cs charset) { g.text(indent, y, g.w-indent, name, rolePlain, cs) }}
	}
	st := &frameState{live: true, tab: tabCompare}
	st.pos[tabCompare] = p.pos
	drawRows(g, rows, b.top+1, b.top+b.rows, st, cs)
}
