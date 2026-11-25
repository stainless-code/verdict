package dashboard

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func overviewGrid(w, h int) *grid {
	return compose(&frameState{snap: mustLoad(partialFixture()), live: true}, w, h, pickLook("auto", "xterm"))
}

// TestPaintFullFrameReplaysToTheGrid proves the first paint draws every cell
// the composer built, glyph and color, through nothing but the vocabulary the
// interpreter accepts.
func TestPaintFullFrameReplaysToTheGrid(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p := &painter{out: &out, pal: &paletteANSI}
	g := overviewGrid(minCols, minRows)
	if err := p.paint(g); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out.Bytes(), []byte(escSyncBegin+"\x1b[0m"+escClear)) {
		t.Fatalf("first frame starts %q, want a synchronized full clear", out.Bytes()[:min(out.Len(), 20)])
	}
	if !bytes.HasSuffix(out.Bytes(), []byte(escSyncEnd)) {
		t.Fatalf("first frame does not end the synchronized update: %q", out.Bytes()[max(out.Len()-20, 0):])
	}
	scr := newScreen(minCols, minRows)
	scr.replay(t, out.Bytes())
	scr.matches(t, g, &paletteANSI)
	if scr.sync {
		t.Fatal("the synchronized update is still open after the frame")
	}
}

// TestPaintIdleFrameWritesNothing is the flicker contract: repainting the
// frame the terminal already shows writes zero bytes.
func TestPaintIdleFrameWritesNothing(t *testing.T) {
	t.Parallel()
	out := &countingWriter{}
	p := &painter{out: out, pal: &paletteANSI}
	if err := p.paint(overviewGrid(minCols, minRows)); err != nil {
		t.Fatal(err)
	}
	before := out.writes
	if err := p.paint(overviewGrid(minCols, minRows)); err != nil {
		t.Fatal(err)
	}
	if out.writes != before || out.Len() != out.afterFirst {
		t.Fatalf("idle frame made %d write(s) of %d byte(s), want 0", out.writes-before, out.Len()-out.afterFirst)
	}
}

// TestPaintOneCellChangeWritesOneSpan proves the diff is cell-level: one
// changed cell costs one cursor move and a few bytes, and the screen ends up
// equal to the new frame.
func TestPaintOneCellChangeWritesOneSpan(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p := &painter{out: &out, pal: &paletteANSI}
	first := overviewGrid(minCols, minRows)
	if err := p.paint(first); err != nil {
		t.Fatal(err)
	}
	scr := newScreen(minCols, minRows)
	scr.replay(t, out.Bytes())
	out.Reset()
	scr.cups = 0

	second := overviewGrid(minCols, minRows)
	second.cells[5*minCols+10] = cell{r: 'X', role: roleBad}
	if err := p.paint(second); err != nil {
		t.Fatal(err)
	}
	const budget = 40
	if out.Len() == 0 || out.Len() > budget {
		t.Fatalf("one-cell change wrote %d bytes (%q), want 1..%d", out.Len(), out.Bytes(), budget)
	}
	scr.replay(t, out.Bytes())
	if scr.cups != 1 {
		t.Fatalf("one-cell change moved the cursor %d times, want 1", scr.cups)
	}
	scr.matches(t, second, &paletteANSI)
}

// TestPaintWideRuneChange proves a wide rune swapped for another is written
// once at its leader and leaves its neighbors alone.
func TestPaintWideRuneChange(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p := &painter{out: &out, pal: &paletteMono}
	cs := charsetUnicode
	a := newGrid(minCols, minRows)
	a.text(4, 1, 10, "ab日c", rolePlain, cs)
	if err := p.paint(a); err != nil {
		t.Fatal(err)
	}
	scr := newScreen(minCols, minRows)
	scr.replay(t, out.Bytes())
	out.Reset()

	b := newGrid(minCols, minRows)
	b.text(4, 1, 10, "ab本c", rolePlain, cs)
	if err := p.paint(b); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), escSyncBegin+"\x1b[2;7H本"+escSyncEnd; got != want {
		t.Fatalf("wide change wrote %q, want %q", got, want)
	}
	scr.replay(t, out.Bytes())
	scr.matches(t, b, &paletteMono)
}

// TestPaintResizeRepaintsWhole proves a frame of a new size clears and redraws
// rather than diffing against cells that no longer line up.
func TestPaintResizeRepaintsWhole(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p := &painter{out: &out, pal: &paletteANSI}
	if err := p.paint(overviewGrid(minCols, minRows)); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	bigger := overviewGrid(100, 30)
	if err := p.paint(bigger); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), escSyncBegin+"\x1b[0m"+escClear) {
		t.Fatalf("resized frame starts %q, want a full clear", out.String()[:20])
	}
	scr := newScreen(100, 30)
	scr.replay(t, out.Bytes())
	scr.matches(t, bigger, &paletteANSI)
}

// TestPaintWriteErrorForcesFullRepaint proves a frame that failed to write is
// not remembered as shown: the next paint starts from a clear screen.
func TestPaintWriteErrorForcesFullRepaint(t *testing.T) {
	t.Parallel()
	w := &failingWriter{}
	p := &painter{out: w, pal: &paletteANSI}
	if err := p.paint(overviewGrid(minCols, minRows)); err == nil {
		t.Fatal("paint over a failing writer reported no error")
	}
	if p.front != nil {
		t.Fatal("a failed frame was kept as the shown frame")
	}
}

// TestSessionRestoreRunsOnce proves restore writes the three leave sequences
// in order and resets the terminal mode once, however many paths reach it.
func TestSessionRestoreRunsOnce(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	term := &fakeTTY{cols: minCols, rows: minRows}
	s := &session{term: term, out: &out}
	for range 3 {
		if err := s.restore(); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := out.String(), "\x1b[?25h\x1b[?2026l\x1b[?1049l"; got != want {
		t.Fatalf("restore wrote %q, want %q", got, want)
	}
	if term.restores != 1 {
		t.Fatalf("terminal mode restored %d times, want 1", term.restores)
	}
}

// countingWriter counts Write calls and records the length after the first.
type countingWriter struct {
	bytes.Buffer
	writes     int
	afterFirst int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.writes++
	n, err := c.Buffer.Write(p)
	if c.writes == 1 {
		c.afterFirst = c.Len()
	}
	return n, err
}

type failingWriter struct{}

var errWrite = errors.New("write: broken pipe")

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }
