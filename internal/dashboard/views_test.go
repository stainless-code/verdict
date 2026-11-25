package dashboard

import (
	"strings"
	"testing"

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
	for _, tb := range []tab{tabOverview, tabQueue} {
		got := frame(partialFixture(), tb, 120, 30, charsetUnicode)
		if strings.ContainsAny(got, "\x1b\x07") {
			t.Fatalf("tab %d frame carries a control byte:\n%q", tb, got)
		}
	}
	if got := frame(partialFixture(), tabQueue, 120, 30, charsetUnicode); !strings.Contains(got, "acme/api [2Jforged") {
		t.Fatalf("queue frame lost the flattened repo text:\n%s", got)
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
	if got := lines[minRows-1]; got != " 1 overview   2 queue   q quit" {
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

// TestFrameTextFoldsBidiControls proves a diff line or title carrying a bidi
// override cannot reach the frame: U+202E would reorder the text after it on
// a bidi-capable terminal (Trojan Source), so every directional control
// becomes a space, in the painted frame and the plain --once text alike.
func TestFrameTextFoldsBidiControls(t *testing.T) {
	t.Parallel()
	g := newGrid(minCols, 2)
	g.text(0, 0, minCols, "a\u202eb\u202cc\u2066d\u2069e\u200ef\u061cg", rolePlain, charsetUnicode)
	if got := g.lines(); !strings.Contains(got, "a b c d e f g") || strings.ContainsAny(got, "\u202e\u202c\u2066\u2069\u200e\u061c") {
		t.Fatalf("frame = %q; want the bidi controls folded to spaces", got)
	}
}
