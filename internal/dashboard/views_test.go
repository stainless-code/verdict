package dashboard

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/golden"
	"github.com/stainless-code/verdict/internal/jq"
)

func frame(src source, tb tab, w, h int, cs charset) string {
	return compose(&frameState{snap: mustLoad(src), tab: tb}, w, h, look{pal: &paletteMono, cs: cs}).lines()
}

// TestViewGoldens pins every shipped tab at the 80×24 minimum, plus the
// complete-totals Overview at 100×30 with a wide-rune path and the ASCII
// glyph set TERM=dumb selects.
func TestViewGoldens(t *testing.T) {
	t.Parallel()
	golden.Check(t, "overview-80x24", frame(partialFixture(), tabOverview, minCols, minRows, charsetUnicode))
	golden.Check(t, "queue-80x24", frame(partialFixture(), tabQueue, minCols, minRows, charsetUnicode))
	golden.Check(t, "overview-complete-100x30", frame(completeFixture(), tabOverview, 100, 30, charsetUnicode))
	golden.Check(t, "queue-ascii-80x24", frame(completeFixture(), tabQueue, minCols, minRows, charsetASCII))
}

// TestFrameCarriesNoStrangerEscape proves a repo name or refusal message
// holding ESC sequences reaches the frame as plain spaces and text: the
// forged clear-screen and the OSC 52 clipboard write are both inert.
func TestFrameCarriesNoStrangerEscape(t *testing.T) {
	t.Parallel()
	for tb := range tabCount {
		st := tabState(t, tb, hostileState)
		poison(st)
		checkFrameInert(t, tb, compose(st, 120, 30, look{pal: &paletteMono, cs: charsetUnicode}).lines())
	}
	if got := frame(partialFixture(), tabQueue, 120, 30, charsetUnicode); !strings.Contains(got, "acme/api [2Jforged") {
		t.Fatalf("queue frame lost the flattened repo text:\n%s", got)
	}
}

// hostile is what a stranger can put in any field: an OSC 52 clipboard
// write, a C1 CSI, a clear screen, a carriage return, a tab, and a line
// separator.
const hostile = "\x1b]52;c;eA==\x07\u009b2J\x1b[2J\r\t\u2028end"

// hostileState points every bundle tab at one bundle and gives Compare a
// baseline, so each tab has a decoded view for poison to write into.
func hostileState(st *frameState) {
	st.target, st.baseline = target{root: "a1b2c3d4e5f6", bundle: "4232"}, "4301"
}

// poison writes the hostile string into every decoded field the active tab
// draws. It dereferences that tab's view unconditionally, so a tab whose view
// failed to load fails the test instead of passing with nothing poisoned.
func poison(st *frameState) {
	st.snap.roots[0].path += hostile
	for i := range st.snap.rows {
		st.snap.rows[i].bundle += hostile
		st.snap.rows[i].model += hostile
	}
	switch st.tab {
	case tabDetail:
		st.detail.repo, st.detail.head = hostile, hostile
		st.detail.items[0].body += hostile
		st.detail.items[0].locator += hostile
	case tabDiff:
		st.diff.lanes[0].lane += hostile
		st.diff.files[0].path += hostile
		st.diff.files[0].hunks[0].header += hostile
		st.diff.files[0].hunks[0].lines[0].text += hostile
	case tabCompare:
		st.cmp.baseline += hostile
		st.cmp.buckets[0][0].claim += hostile
		st.cmp.buckets[0][0].path += hostile
	case tabCost:
		poisonTable(st.cost)
	case tabPrecision:
		poisonTable(st.prec)
	default:
	}
}

func poisonTable(tbl *tableData) {
	tbl.by, tbl.note = hostile, hostile
	tbl.rows[0][0] += hostile
}

// checkFrameInert fails unless a frame holds no control or separator rune
// but the newline that ends each row: the rule a stranger's text must not
// break on any tab.
func checkFrameInert(t *testing.T, tb tab, frame string) {
	t.Helper()
	for _, r := range frame {
		if r != '\n' && core.IsLineBreak(r) {
			t.Fatalf("tab %d frame carries control %U:\n%q", tb, r, frame)
		}
	}
}

// TestQueueOverflowCountsTheRest proves rows past the body end in one
// "… N more" line rather than running into the footer.
func TestQueueOverflowCountsTheRest(t *testing.T) {
	t.Parallel()
	snap := mustLoad(partialFixture())
	for len(snap.rows) < 40 {
		snap.rows = append(snap.rows, snap.rows[0])
	}
	lines := strings.Split(compose(&frameState{snap: snap, tab: tabQueue}, minCols, minRows,
		look{pal: &paletteMono, cs: charsetUnicode}).lines(), "\n")
	const lastBody = minRows - footerRows - 1
	if got := strings.TrimSpace(lines[lastBody]); got != "… 24 more" {
		t.Fatalf("last body row = %q, want %q", got, "… 24 more")
	}
	if !strings.HasPrefix(lines[lastBody+1], "────") {
		t.Fatalf("row under the body = %q, want the footer rule", lines[lastBody+1])
	}
}

// TestComposingFrame pins the frame drawn before the first compose returns.
func TestComposingFrame(t *testing.T) {
	t.Parallel()
	lines := strings.Split(compose(&frameState{live: true}, minCols, minRows,
		look{pal: &paletteMono, cs: charsetUnicode}).lines(), "\n")
	if got := lines[bodyTop]; got != " composing…" {
		t.Fatalf("body = %q, want the composing marker", got)
	}
	if got := lines[minRows-1]; got != " 1-7 tabs  Enter queue  r refresh  ? help  q quit" {
		t.Fatalf("key row = %q", got)
	}
}

// TestTextOverAWideRuneMendsBothHalves proves a write that lands on half of a
// wide rune blanks the other half: a row never keeps a leader without its
// second cell, which would shift every cell after it on a real terminal.
func TestTextOverAWideRuneMendsBothHalves(t *testing.T) {
	t.Parallel()
	g := newGrid(minCols, 2)
	g.text(0, 0, 10, "日本語", rolePlain, charsetUnicode)
	g.text(1, 0, 10, "x", rolePlain, charsetUnicode) // into 日's second half
	g.text(0, 1, 10, "日本語", rolePlain, charsetUnicode)
	g.text(0, 1, 10, "abc", rolePlain, charsetUnicode) // over 日 and 本's first half
	if got, want := g.lines(), " x本語\nabc 語\n"; got != want {
		t.Fatalf("lines = %q, want %q", got, want)
	}
}

// TestTextKeepsCombiningMarks proves a decomposed accent survives the cell
// grid: "cafe" + U+0301 reads "café" in the plain frame and on the replayed
// screen, while a mark with no base in its write is dropped.
func TestTextKeepsCombiningMarks(t *testing.T) {
	t.Parallel()
	g := newGrid(minCols, minRows)
	g.text(2, 1, 20, "cafe\u0301 ok", rolePlain, charsetUnicode)
	g.text(2, 2, 20, "\u0301orphan", rolePlain, charsetUnicode)
	if got := strings.Split(g.lines(), "\n")[1:3]; got[0] != "  cafe\u0301 ok" || got[1] != "  orphan" {
		t.Fatalf("rows = %q, want the accent kept on e and the orphan mark dropped", got)
	}
	var out strings.Builder
	p := &painter{out: &out, pal: &paletteMono}
	if err := p.paint(g); err != nil {
		t.Fatal(err)
	}
	scr := newScreen(minCols, minRows)
	scr.replay(t, []byte(out.String()))
	scr.matches(t, g, &paletteMono)
}

// TestOverviewLabelsStoreErrors proves a store-level failure (no bundle: the
// root's store could not be listed) is named by its root and counted apart
// from refused bundles.
func TestOverviewLabelsStoreErrors(t *testing.T) {
	t.Parallel()
	src := partialFixture()
	errs, _ := src.bundles.Data.Get("errors")
	list, ok := errs.([]any)
	if !ok {
		t.Fatalf("fixture errors = %T, want []any", errs)
	}
	src.bundles.Data.Set("errors", append(list, jq.NewObj(
		"root", "a1b2c3d4e5f6", "bundle", "", "code", "corrupt-store", "message", "state is not a directory")))
	got := frame(src, tabOverview, 100, minRows, charsetUnicode)
	for _, want := range []string{"   ! store a1b2c3d4e5f6  corrupt-store  state is not a directory", "1 refused bundle  1 store error"} {
		if !strings.Contains(got, want) {
			t.Fatalf("frame lacks %q:\n%s", want, got)
		}
	}
}

// TestTextCapsCombiningMarksAtTwo pins the per-cell bound: two marks on one
// base are kept, a third is dropped, so a stacked "Zalgo" string cannot grow a
// cell (or a repaint) without limit.
func TestTextCapsCombiningMarksAtTwo(t *testing.T) {
	t.Parallel()
	g := newGrid(minCols, minRows)
	g.text(2, 1, 20, "á̂ two", rolePlain, charsetUnicode)
	g.text(2, 2, 20, "á̂̃ three", rolePlain, charsetUnicode)
	got := strings.Split(g.lines(), "\n")[1:3]
	if got[0] != "  á̂ two" || got[1] != "  á̂ three" {
		t.Fatalf("rows = %q, want both marks kept and the third dropped", got)
	}
}

// tabState composes one tab through the same job --once runs, so a golden
// shows exactly what the layer's view decodes to.
func tabState(t *testing.T, tb tab, mutate func(st *frameState)) *frameState {
	t.Helper()
	src := partialFixture()
	st := &frameState{tab: tb}
	if mutate != nil {
		mutate(st)
	}
	res := runJob(t.Context(), src, pickTarget(st))
	if res.snap == nil {
		t.Fatalf("compose failed: %v", res.err)
	}
	st.snap = filterRepo(res.snap, st.repo)
	installView(st, &res.want, res.view, res.err)
	return st
}

func tabFrame(t *testing.T, tb tab, w, h int, mutate func(st *frameState)) string {
	t.Helper()
	return compose(tabState(t, tb, mutate), w, h, look{pal: &paletteMono, cs: charsetUnicode}).lines()
}

// TestTabGoldens pins every PR B tab at the 80×24 minimum, the split diff at
// 100 columns, the help overlay, and the baseline picker.
func TestTabGoldens(t *testing.T) {
	t.Parallel()
	golden.Check(t, "detail-80x24", tabFrame(t, tabDetail, minCols, minRows, nil))
	golden.Check(t, "diff-80x24", tabFrame(t, tabDiff, minCols, minRows, nil))
	golden.Check(t, "diff-split-100x30", tabFrame(t, tabDiff, splitCols, 30, func(st *frameState) { st.split = true }))
	golden.Check(t, "compare-80x24", tabFrame(t, tabCompare, minCols, minRows, func(st *frameState) {
		st.target, st.baseline = target{root: "a1b2c3d4e5f6", bundle: "4232"}, "4301"
	}))
	golden.Check(t, "compare-nobaseline-80x24", tabFrame(t, tabCompare, minCols, minRows, nil))
	golden.Check(t, "cost-80x24", tabFrame(t, tabCost, minCols, minRows, nil))
	golden.Check(t, "precision-80x24", tabFrame(t, tabPrecision, minCols, minRows, nil))
	golden.Check(t, "help-80x24", tabFrame(t, tabQueue, minCols, minRows, func(st *frameState) { st.help = true }))
	golden.Check(t, "picker-80x24", tabFrame(t, tabCompare, minCols, minRows, func(st *frameState) {
		st.target = target{root: "a1b2c3d4e5f6", bundle: "4232"}
		st.picker = &picker{rows: []string{"local-feature-branch-with-a-very-long-name", "4301"}, pos: 1}
	}))
}

// TestDetailRefusalShowsInTheTab opens the refused bundle: its Detail body
// is the refusal, and the rest of the frame still draws.
func TestDetailRefusalShowsInTheTab(t *testing.T) {
	t.Parallel()
	got := tabFrame(t, tabDetail, minCols, minRows, func(st *frameState) {
		st.target = target{root: "a1b2c3d4e5f6", bundle: "77"}
	})
	if !strings.Contains(got, " 77: bundle-unreadable: findings.json is a FIFO") || !strings.Contains(got, "[3 Detail]") {
		t.Fatalf("frame lacks the refusal in the Detail body:\n%s", got)
	}
}

// FuzzDecode feeds arbitrary JSON to every view decoder and draws the
// result: a payload of any shape decodes to rows or to nothing, never a
// panic, and the frame still holds no control byte.
func FuzzDecode(f *testing.F) {
	for _, seed := range []string{
		`{}`, `{"rows":[{"open":1}],"counts":null}`, `{"report":{"items":[{"body":"\u001b[2J"}]}}`,
		`{"files":[{"hunks":[{"lines":[{"kind":"add","new_no":-3}]}]}]}`, `{"cost":{"rows":[{"est_usd":1e308}]}}`, `[1,2]`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(checkDecodeAny)
}

func checkDecodeAny(t *testing.T, raw []byte) {
	v, err := jq.DecodeOrdered(raw)
	if err != nil {
		return
	}
	data := asObj(v)
	view := &query.View{Data: data}
	st := &frameState{
		snap: decode(view, view), detail: decodeDetail(view), diff: decodeDiff(view),
		cmp: decodeCompare(view), cost: decodeCost(view), prec: decodePrecision(view),
	}
	for tb := range tabCount {
		st.tab = tb
		checkFrameInert(t, tb, compose(st, minCols, minRows, look{pal: &paletteMono, cs: charsetUnicode}).lines())
	}
}

// TestOnceFramesNameNoKeys proves a --once frame never tells its reader to
// press a key it cannot press, while the live frame keeps the hint.
func TestOnceFramesNameNoKeys(t *testing.T) {
	t.Parallel()
	for _, live := range []bool{false, true} {
		frames := hintFrames(t, live)
		for _, hint := range []string{"press b", "press Enter"} {
			if strings.Contains(frames, hint) != live {
				t.Fatalf("live=%v: %q present=%v, want %v:\n%s", live, hint, !live, live, frames)
			}
		}
	}
	if frames := hintFrames(t, false); !strings.Contains(frames, "no readable bundle to show") {
		t.Fatalf("--once Detail with no bundle lacks its plain message:\n%s", frames)
	}
}

// hintFrames composes the two frames that carry a key hint in the live
// view: Compare with no baseline, and Detail with no readable bundle.
func hintFrames(t *testing.T, live bool) string {
	t.Helper()
	cmp := tabState(t, tabCompare, nil)
	cmp.live = live
	empty := &frameState{tab: tabDetail, live: live, snap: &snapshot{}}
	lk := look{pal: &paletteMono, cs: charsetUnicode}
	return compose(cmp, minCols, minRows, lk).lines() + compose(empty, minCols, minRows, lk).lines()
}
