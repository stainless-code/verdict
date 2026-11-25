package dashboard

import (
	"strings"

	"github.com/stainless-code/verdict/internal/core/query"
)

// tab is one dashboard view, selected by its digit.
type tab uint8

// The seven tabs, in key order.
const (
	tabOverview tab = iota
	tabQueue
	tabDetail
	tabDiff
	tabCompare
	tabCost
	tabPrecision
	tabCount
)

var tabLabels = [tabCount]string{
	"1 Overview", "2 Queue", "3 Detail", "4 Diff", "5 Compare", "6 Cost", "7 Precision",
}

// tabNames are the --tab spellings, one per tab.
var tabNames = [tabCount]string{"overview", "queue", "detail", "diff", "compare", "cost", "precision"}

// splitCols is the narrowest frame a side-by-side diff fits: two code
// columns of useful width plus their gutters.
const splitCols = 100

// frameState is everything a frame is composed from. The loop goroutine owns
// it; compose goroutines never touch it, they hand results back.
type frameState struct {
	snap      *snapshot // nil until the first compose returns
	detail    *detailData
	diff      *diffData
	cmp       *compareData
	cost      *tableData
	prec      *tableData
	picker    *picker
	target    target
	repo      string // the [repo] filter
	baseline  string
	tabErr    string // the active tab's last refusal, drawn in its body
	note      string // a one-shot status line (copied, split needs width)
	search    string
	pos       [tabCount]int // selected row per tab
	page      int           // body rows in the last frame, for half-page moves
	width     int           // columns in the last frame, for the split check
	costBy    int
	precBy    int
	tab       tab
	live      bool
	split     bool
	help      bool
	searching bool
	composing bool
}

// picker is the Compare tab's baseline chooser over the Queue's bundles.
type picker struct {
	rows []string
	pos  int
}

// row is one selectable line of a tab's list. key is what `/` matches;
// target and sig are what Enter and `y` act on.
type row struct {
	draw   func(g *grid, y int, cs charset)
	target *target
	key    string
	sig    string
}

// want is what one compose job loads: the snapshot plus the active tab's
// view for the current target, baseline, and grouping.
type want struct {
	target   target
	baseline string
	by       string
	repo     string
	tab      tab
}

func (st *frameState) want() want {
	w := want{tab: st.tab, target: st.target, baseline: st.baseline}
	switch st.tab {
	case tabCost:
		w.by = query.CostDimensions()[st.costBy]
	case tabPrecision:
		w.by = query.PrecisionDimensions()[st.precBy]
	default:
	}
	return w
}

// action is what a keystroke asks of the loop beyond changing state.
type action uint8

const (
	actNone    action = iota
	actQuit           // leave the session, exit 0
	actReload         // the want changed: compose the new view
	actRefresh        // `r`: drop the layer cache, then compose
	actCopy           // `y`: copy the selected finding's prompt
)

// visibleRows is the active tab's list after the `/` filter.
func (st *frameState) visibleRows() []row {
	all := tabRows(st)
	if st.search == "" {
		return all
	}
	q := strings.ToLower(st.search)
	var out []row
	for _, r := range all {
		if r.key != "" && strings.Contains(strings.ToLower(r.key), q) {
			out = append(out, r)
		}
	}
	return out
}

// selected is the row under the cursor on the active tab, if any.
func (st *frameState) selected() (row, bool) {
	rows := st.visibleRows()
	if len(rows) == 0 {
		return row{}, false
	}
	return rows[min(st.pos[st.tab], len(rows)-1)], true
}

// move shifts the active tab's cursor by delta rows, clamped to the list.
func (st *frameState) move(delta int) {
	n := len(st.visibleRows())
	st.pos[st.tab] = max(0, min(st.pos[st.tab]+delta, n-1))
}

func (st *frameState) halfPage() int { return max(st.page/2, 1) }

func (st *frameState) lastWidth() int { return st.width }
