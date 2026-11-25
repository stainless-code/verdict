package dashboard

import (
	"testing"
)

// press feeds a key sequence to a state and returns the last action.
func press(st *frameState, keys ...keyEvent) action {
	var last action
	for _, k := range keys {
		last = st.handle(k)
	}
	return last
}

func runeKey(r rune) keyEvent { return keyEvent{k: keyRune, r: r} }

// TestEnterOpensTheSelectedBundle moves the Queue cursor to the third row
// and presses Enter: Detail opens on that bundle and asks for a compose.
func TestEnterOpensTheSelectedBundle(t *testing.T) {
	t.Parallel()
	st := tabState(t, tabQueue, nil)
	st.live = true
	if got := press(st, runeKey('j'), runeKey('j'), keyEvent{k: keyEnter}); got != actReload {
		t.Fatalf("Enter = %v, want a reload", got)
	}
	want := target{root: "a1b2c3d4e5f6", bundle: "local-feature-branch-with-a-very-long-name"}
	if st.tab != tabDetail || st.target != want {
		t.Fatalf("tab %d target %+v, want Detail on %+v", st.tab, st.target, want)
	}
}

// TestEscClosesOverlaysBeforeQuitting proves Esc closes help, then the
// search, then steps back to the Queue, and only quits from the Queue.
func TestEscClosesOverlaysBeforeQuitting(t *testing.T) {
	t.Parallel()
	st := tabState(t, tabDetail, nil)
	esc := keyEvent{k: keyEscape}
	steps := []struct {
		key             keyEvent
		want            action
		tab             tab
		help, searching bool
	}{
		{runeKey('?'), actNone, tabDetail, true, false},
		{esc, actNone, tabDetail, false, false}, // closes help
		{runeKey('/'), actNone, tabDetail, false, true},
		{runeKey('x'), actNone, tabDetail, false, true},
		{esc, actNone, tabDetail, false, false},  // cancels the search
		{esc, actReload, tabQueue, false, false}, // back to the Queue
		{esc, actQuit, tabQueue, false, false},   // quits from the Queue
	}
	for i, s := range steps {
		got := st.handle(s.key)
		if got != s.want || st.tab != s.tab || st.help != s.help || st.searching != s.searching {
			t.Fatalf("step %d: action %v tab %d help %v searching %v; want %v on tab %d help %v searching %v",
				i, got, st.tab, st.help, st.searching, s.want, s.tab, s.help, s.searching)
		}
	}
}

// TestSearchFiltersTheList types `/auth` on the Detail tab: only the
// matching finding stays, and Enter keeps the filter while closing the input.
func TestSearchFiltersTheList(t *testing.T) {
	t.Parallel()
	st := tabState(t, tabDetail, nil)
	press(st, runeKey('/'), runeKey('a'), runeKey('u'), runeKey('t'), runeKey('h'), keyEvent{k: keyEnter})
	rows := st.visibleRows()
	if st.searching || st.search != "auth" || len(rows) != 1 || rows[0].sig != "c04232a1b2c3d4e5f6" {
		t.Fatalf("search %q searching %v rows %d, want one row for /auth", st.search, st.searching, len(rows))
	}
}

// TestPickerChoosesTheBaseline opens the Compare picker, moves down, and
// picks: the baseline is the second other bundle under the same root.
func TestPickerChoosesTheBaseline(t *testing.T) {
	t.Parallel()
	st := tabState(t, tabCompare, nil)
	if press(st, runeKey('b')); st.picker == nil {
		t.Fatal("b did not open the picker")
	}
	if got := press(st, runeKey('j'), keyEvent{k: keyEnter}); got != actReload || st.baseline != "4301" || st.picker != nil {
		t.Fatalf("pick = %v baseline %q picker %v, want a reload against 4301", got, st.baseline, st.picker)
	}
}

// TestSplitNeedsWidth proves `u` refuses below 100 columns with a note, and
// toggles at 100.
func TestSplitNeedsWidth(t *testing.T) {
	t.Parallel()
	st := tabState(t, tabDiff, nil)
	st.width = minCols
	press(st, runeKey('u'))
	if st.split || st.note != "split diff needs at least 100 columns" {
		t.Fatalf("split %v note %q at 80 columns", st.split, st.note)
	}
	st.width = splitCols
	if press(st, runeKey('u')); !st.split {
		t.Fatal("u did not split at 100 columns")
	}
}

// TestTabCyclesTheGrouping proves Tab walks Cost's groupings and Diff's files.
func TestTabCyclesTheGrouping(t *testing.T) {
	t.Parallel()
	st := tabState(t, tabCost, nil)
	if got := press(st, keyEvent{k: keyTab}); got != actReload || st.want().by != "week" {
		t.Fatalf("Tab on Cost = %v by %q, want a reload by week", got, st.want().by)
	}
	diff := tabState(t, tabDiff, nil)
	press(diff, keyEvent{k: keyTab})
	if rows := diff.visibleRows(); rows[diff.pos[tabDiff]].key != "src/auth_test.go" {
		t.Fatalf("Tab on Diff landed on %q, want the second file", rows[diff.pos[tabDiff]].key)
	}
}

// TestFilterRepoRecomputesTotals narrows the complete fixture to one repo:
// the totals are that repo's alone.
func TestFilterRepoRecomputesTotals(t *testing.T) {
	t.Parallel()
	s := filterRepo(mustLoad(completeFixture()), "acme/api")
	if len(s.rows) != 1 || s.counts == nil || *s.counts != (counts{bundles: 1, open: 3, deferred: 1, addressed: 2}) {
		t.Fatalf("filtered rows %d counts %+v, want acme/api's one bundle", len(s.rows), s.counts)
	}
	if p := filterRepo(mustLoad(partialFixture()), "acme/api"); p.counts != nil {
		t.Fatalf("a repo with a refused bundle summed to %+v, want withheld totals", *p.counts)
	}
}
