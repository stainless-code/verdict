package dashboard

import "github.com/stainless-code/verdict/internal/core/query"

// handle applies one keystroke. The help overlay, the baseline picker, and
// `/` search each own the keyboard while open, so Esc closes them instead of
// leaving the dashboard.
func (st *frameState) handle(ev keyEvent) action {
	st.note = ""
	switch {
	case st.help:
		return st.helpKey(ev)
	case st.picker != nil:
		return st.pickerKey(ev)
	case st.searching:
		return st.searchKey(ev)
	default:
		return st.normalKey(ev)
	}
}

func (st *frameState) helpKey(ev keyEvent) action {
	if ev.k == keyEscape || (ev.k == keyRune && ev.r == '?') {
		st.help = false
	}
	if ev.k == keyRune && ev.r == 'q' {
		return actQuit
	}
	return actNone
}

func (st *frameState) searchKey(ev keyEvent) action {
	switch ev.k {
	case keyEscape:
		st.searching, st.search = false, ""
	case keyEnter:
		st.searching = false
	case keyBackspace:
		if r := []rune(st.search); len(r) > 0 {
			st.search = string(r[:len(r)-1])
		}
	case keyRune:
		st.search += string(ev.r)
	default:
	}
	st.pos[st.tab] = 0
	return actNone
}

func (st *frameState) pickerKey(ev keyEvent) action {
	p := st.picker
	switch ev.k {
	case keyEscape:
		st.picker = nil
	case keyUp, keyDown:
		p.pos = max(0, min(p.pos+arrowDelta(ev.k), len(p.rows)-1))
	case keyEnter:
		st.picker = nil
		if len(p.rows) > 0 {
			st.baseline, st.cmp = p.rows[p.pos], nil
			return actReload
		}
	case keyRune:
		return st.pickerRune(ev.r)
	default:
	}
	return actNone
}

func (st *frameState) pickerRune(r rune) action {
	switch r {
	case 'j':
		return st.pickerKey(keyEvent{k: keyDown})
	case 'k':
		return st.pickerKey(keyEvent{k: keyUp})
	case 'q':
		return actQuit
	}
	return actNone
}

func arrowDelta(k key) int {
	if k == keyUp {
		return -1
	}
	return 1
}

// normalKey is the main keymap.
func (st *frameState) normalKey(ev keyEvent) action {
	switch ev.k {
	case keyRune:
		return st.runeKey(ev.r)
	case keyUp, keyDown:
		st.move(arrowDelta(ev.k))
	case keyPgUp:
		st.move(-st.halfPage())
	case keyPgDn:
		st.move(st.halfPage())
	case keyHome:
		st.pos[st.tab] = 0
	case keyEnd:
		st.move(toEnd)
	case keyEnter:
		return st.open()
	case keyTab:
		return st.nextPane()
	case keyEscape:
		return st.back()
	default:
	}
	return actNone
}

// toEnd moves the cursor past any list's last row; move clamps it there.
const toEnd = 1 << 30

// runeKeys are the single-rune bindings that only move or toggle state.
var runeKeys = map[rune]func(st *frameState) action{
	'j': func(st *frameState) action { st.move(1); return actNone },
	'k': func(st *frameState) action { st.move(-1); return actNone },
	'g': func(st *frameState) action { st.pos[st.tab] = 0; return actNone },
	'G': func(st *frameState) action { st.move(toEnd); return actNone },
	'q': func(*frameState) action { return actQuit },
	'r': func(*frameState) action { return actRefresh },
	'y': func(st *frameState) action { return st.copy() },
	'?': func(st *frameState) action { st.help = true; return actNone },
	'/': func(st *frameState) action { st.searching, st.search = true, ""; return actNone },
	'u': func(st *frameState) action { return st.toggleSplit() },
	'b': func(st *frameState) action { return st.openPicker() },
}

func (st *frameState) runeKey(r rune) action {
	if r >= '1' && r < '1'+rune(tabCount) {
		return st.switchTab(tab(r - '1'))
	}
	if f, ok := runeKeys[r]; ok {
		return f(st)
	}
	return actNone
}

func (st *frameState) switchTab(t tab) action {
	if st.tab == t {
		return actNone
	}
	st.tab, st.tabErr, st.search = t, "", ""
	return actReload
}

// open is Enter: a Queue row opens that bundle's Detail, a Detail row its
// Diff, and the Overview opens the Queue.
func (st *frameState) open() action {
	switch st.tab {
	case tabOverview:
		return st.switchTab(tabQueue)
	case tabQueue:
		if r, ok := st.selected(); ok && r.target != nil {
			st.retarget(*r.target)
			return st.switchTab(tabDetail)
		}
	case tabDetail:
		return st.switchTab(tabDiff)
	default:
	}
	return actNone
}

// back is Esc: clear a filter, else step back to the Queue from a bundle
// tab, else quit (the PR A behavior on Overview and Queue).
func (st *frameState) back() action {
	switch {
	case st.search != "":
		st.search = ""
		return actNone
	case st.tab >= tabDetail && st.tab <= tabCompare:
		return st.switchTab(tabQueue)
	default:
		return actQuit
	}
}

// nextPane is Tab: the next file on the Diff, the next grouping on Cost
// and Precision.
func (st *frameState) nextPane() action {
	switch st.tab {
	case tabDiff:
		st.pos[tabDiff] = nextFileRow(st.visibleRows(), st.pos[tabDiff])
	case tabCost:
		st.costBy = (st.costBy + 1) % len(query.CostDimensions())
		st.pos[tabCost] = 0
		return actReload
	case tabPrecision:
		st.precBy = (st.precBy + 1) % len(query.PrecisionDimensions())
		st.pos[tabPrecision] = 0
		return actReload
	default:
	}
	return actNone
}

// nextFileRow is the index of the first file header after from, wrapping.
func nextFileRow(rows []row, from int) int {
	for i := 1; i <= len(rows); i++ {
		j := (from + i) % len(rows)
		if rows[j].sig == fileHeaderMark {
			return j
		}
	}
	return from
}

func (st *frameState) toggleSplit() action {
	if st.tab != tabDiff {
		return actNone
	}
	if !st.split && st.lastWidth() < splitCols {
		st.note = "split diff needs at least 100 columns"
		return actNone
	}
	st.split = !st.split
	return actNone
}

func (st *frameState) openPicker() action {
	if st.tab != tabCompare || st.snap == nil {
		return actNone
	}
	p := &picker{}
	for _, r := range st.snap.rows {
		if r.root == st.target.root && r.bundle != st.target.bundle && !r.refused {
			p.rows = append(p.rows, r.bundle)
		}
	}
	if len(p.rows) == 0 {
		st.note = "no other bundle under this root to compare against"
		return actNone
	}
	st.picker = p
	return actNone
}

func (st *frameState) copy() action {
	if st.tab != tabDetail {
		return actNone
	}
	if r, ok := st.selected(); ok && r.sig != "" {
		return actCopy
	}
	return actNone
}

// retarget points the bundle tabs at another bundle: its views, cursors, and
// baseline belong to the old one and are dropped until the new ones arrive.
func (st *frameState) retarget(t target) {
	if st.target == t {
		return
	}
	st.target, st.baseline = t, ""
	st.detail, st.diff, st.cmp = nil, nil, nil
	st.pos[tabDetail], st.pos[tabDiff], st.pos[tabCompare] = 0, 0, 0
}
