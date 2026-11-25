package dashboard

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeTTY scripts a terminal: each read returns the next chunk (an empty
// chunk is an idle tick), and the output length is sampled at every read so
// a test can see what each tick wrote.
type fakeTTY struct {
	out       *bytes.Buffer
	resizes   map[int]bool // read index -> report a resize after it
	onRead    func(n int)
	readError error
	chunks    []string
	sampled   []int
	cols      int
	rows      int
	reads     int
	restores  int
}

func (f *fakeTTY) size() (cols, rows int, err error) { return f.cols, f.rows, nil }

func (f *fakeTTY) read(p []byte) (int, error) {
	if f.out != nil {
		f.sampled = append(f.sampled, f.out.Len())
	}
	if f.onRead != nil {
		f.onRead(f.reads)
	}
	if f.readError != nil {
		return 0, f.readError
	}
	if f.reads >= len(f.chunks) {
		return copy(p, "q"), nil
	}
	chunk := f.chunks[f.reads]
	f.reads++
	return copy(p, chunk), nil
}

func (f *fakeTTY) resized() bool { return f.resizes[f.reads] }

func (f *fakeTTY) restore() error {
	f.restores++
	return nil
}

func runLiveFixture(ctx context.Context, term *fakeTTY, opts *options) (*bytes.Buffer, error) {
	var out bytes.Buffer
	term.out = &out
	src := partialFixture()
	err := live(ctx, term, &out, pickLook("auto", "xterm"), opts,
		func(ctx context.Context) (*snapshot, error) { return load(ctx, src) })
	return &out, err
}

// TestLiveSessionSwitchesTabsAndRestores drives a whole session: an idle
// tick, `2`, another idle tick, `q`. The idle ticks write nothing, the final
// screen is the Queue, and the terminal is restored exactly once.
func TestLiveSessionSwitchesTabsAndRestores(t *testing.T) {
	t.Parallel()
	term := &fakeTTY{cols: minCols, rows: minRows, chunks: []string{"", "2", "", "q"}}
	out, err := runLiveFixture(t.Context(), term, &options{})
	if err != nil {
		t.Fatal(err)
	}
	if term.restores != 1 {
		t.Fatalf("terminal restored %d times, want 1", term.restores)
	}
	if s := term.sampled; len(s) != 4 || s[1] != s[0] || s[3] != s[2] || s[2] == s[1] {
		t.Fatalf("output length per read = %v, want idle ticks to write nothing and `2` to repaint", s)
	}
	checkBrackets(t, out.String())
	scr := newScreen(minCols, minRows)
	scr.replay(t, out.Bytes())
	if scr.alt || scr.cursorHidden || scr.sync {
		t.Fatalf("after quit alt=%v hidden=%v sync=%v, want all false", scr.alt, scr.cursorHidden, scr.sync)
	}
	want := compose(&frameState{snap: mustLoad(partialFixture()), tab: tabQueue, live: true}, minCols, minRows,
		pickLook("auto", "xterm"))
	scr.matches(t, want, &paletteANSI)
}

// TestLiveResizeRepaints proves a reported resize repaints the whole frame
// even though nothing in it changed.
func TestLiveResizeRepaints(t *testing.T) {
	t.Parallel()
	term := &fakeTTY{cols: minCols, rows: minRows, chunks: []string{"", "", "q"}, resizes: map[int]bool{1: true}}
	if _, err := runLiveFixture(t.Context(), term, &options{}); err != nil {
		t.Fatal(err)
	}
	if s := term.sampled; len(s) != 3 || s[1] == s[0] {
		t.Fatalf("output length per read = %v, want the tick after a resize to repaint", s)
	}
}

// TestLiveCancelReturnsContextError proves SIGINT's cancel ends the loop with
// ctx.Err() and still restores the terminal.
func TestLiveCancelReturnsContextError(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	term := &fakeTTY{cols: minCols, rows: minRows, chunks: []string{"", "", ""}}
	term.onRead = func(n int) {
		if n == 1 {
			cancel()
		}
	}
	_, err := runLiveFixture(ctx, term, &options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if term.restores != 1 {
		t.Fatalf("terminal restored %d times, want 1", term.restores)
	}
}

// TestLiveRestoresOnPanic proves a panic unwinding through the session still
// leaves the alternate screen and resets the terminal, and is not swallowed.
func TestLiveRestoresOnPanic(t *testing.T) {
	t.Parallel()
	term := &fakeTTY{cols: minCols, rows: minRows}
	var out bytes.Buffer
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the panic was swallowed")
			}
		}()
		err := live(t.Context(), term, &out, pickLook("auto", "xterm"), &options{},
			func(context.Context) (*snapshot, error) { panic("compose blew up") })
		t.Errorf("live returned %v instead of panicking", err)
	}()
	if term.restores != 1 || !strings.HasSuffix(out.String(), escShowCursor+escSyncEnd+escLeaveAlt) {
		t.Fatalf("after a panic: %d restores, output ends %q", term.restores, out.String())
	}
}

// TestLiveReadErrorRestores proves a terminal read failure ends the session
// with that error, restored.
func TestLiveReadErrorRestores(t *testing.T) {
	t.Parallel()
	errGone := errors.New("read: input/output error")
	term := &fakeTTY{cols: minCols, rows: minRows, readError: errGone}
	if _, err := runLiveFixture(t.Context(), term, &options{}); !errors.Is(err, errGone) {
		t.Fatalf("err = %v, want the read error", err)
	}
	if term.restores != 1 {
		t.Fatalf("terminal restored %d times, want 1", term.restores)
	}
}

// TestLiveSmallTerminalShowsResizePanel proves a terminal under 80×24 gets
// the one resize panel, not a squeezed frame.
func TestLiveSmallTerminalShowsResizePanel(t *testing.T) {
	t.Parallel()
	term := &fakeTTY{cols: 60, rows: 20, chunks: []string{"q"}}
	out, err := runLiveFixture(t.Context(), term, &options{})
	if err != nil {
		t.Fatal(err)
	}
	scr := newScreen(60, 20)
	scr.replay(t, out.Bytes())
	for y, want := range []string{"verdict dashboard needs at least 80×24", "this terminal is 60×20", "resize it, or press q"} {
		if got := strings.TrimRight(string(scr.runes[y]), " "); got != want {
			t.Fatalf("row %d = %q, want %q", y, got, want)
		}
	}
}

func TestHandleKeys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		keys    string
		start   tab
		wantTab tab
		quit    bool
	}{
		{"2", tabOverview, tabQueue, false},
		{"1", tabQueue, tabOverview, false},
		{"q", tabOverview, tabOverview, true},
		{"\x1b", tabQueue, tabQueue, true},
		{"\x1b[1~", tabQueue, tabQueue, false}, // Home: its '1' is not the Overview key
		{"\x1b[A", tabQueue, tabQueue, false},  // an arrow is not Esc
		{"x", tabQueue, tabQueue, false},
	}
	for _, c := range cases {
		st := &frameState{tab: c.start}
		if quit := handleKeys(st, []byte(c.keys)); quit != c.quit || st.tab != c.wantTab {
			t.Errorf("keys %q: quit=%v tab=%d, want quit=%v tab=%d", c.keys, quit, st.tab, c.quit, c.wantTab)
		}
	}
}

// checkBrackets fails unless the session opened the alternate screen with a
// hidden cursor and closed with the restore sequence.
func checkBrackets(t *testing.T, out string) {
	t.Helper()
	if !strings.HasPrefix(out, escEnterAlt+escHideCursor) {
		t.Fatalf("session starts %q, want the alternate screen with a hidden cursor", out[:min(len(out), 16)])
	}
	if !strings.HasSuffix(out, escShowCursor+escSyncEnd+escLeaveAlt) {
		t.Fatalf("session ends %q, want the restore sequence", out[max(len(out)-24, 0):])
	}
}
