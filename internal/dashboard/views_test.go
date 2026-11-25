package dashboard

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/golden"
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
