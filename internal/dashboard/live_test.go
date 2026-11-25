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
	stops     map[int]bool // read index -> report Ctrl-Z after it
	onSuspend func()
	chunks    []string
	sampled   []int
	cols      int
	rows      int
	reads     int
	restores  int
	suspends  int
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

func (f *fakeTTY) stopRequested() bool { return f.stops[f.reads] }

func (f *fakeTTY) suspend(context.Context) error {
	f.suspends++
	if f.onSuspend != nil {
		f.onSuspend()
	}
	return nil
}

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

// TestKeyState drives whole read sequences through the key handler. A lone
// Esc quits one tick later, unless the next read continues a sequence a
// terminal split across two reads (an arrow's `[A`).
func TestKeyState(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		reads   []string
		start   tab
		wantTab tab
		quitAt  int // index of the read that quits, -1 for none
	}{
		{"digit 2", []string{"2"}, tabOverview, tabQueue, -1},
		{"digit 1", []string{"1"}, tabQueue, tabOverview, -1},
		{"q", []string{"q"}, tabOverview, tabOverview, 0},
		{"lone Esc then idle", []string{"\x1b", ""}, tabQueue, tabQueue, 1},
		{"lone Esc then a key", []string{"\x1b", "x"}, tabQueue, tabQueue, 1},
		{"arrow split after ESC", []string{"\x1b", "[A", ""}, tabQueue, tabQueue, -1},
		{"SS3 split after ESC", []string{"\x1b", "OP", ""}, tabQueue, tabQueue, -1},
		{"Home's 1 is not a key", []string{"\x1b[1~"}, tabQueue, tabQueue, -1},
		{"whole arrow", []string{"\x1b[A"}, tabQueue, tabQueue, -1},
		{"other key", []string{"x"}, tabQueue, tabQueue, -1},
	}
	for _, c := range cases {
		st := &frameState{tab: c.start}
		if quitAt := quitRead(st, c.reads); quitAt != c.quitAt || st.tab != c.wantTab {
			t.Errorf("%s: quit at read %d, tab %d; want read %d, tab %d", c.name, quitAt, st.tab, c.quitAt, c.wantTab)
		}
	}
}

// quitRead feeds reads to one key handler and returns the index of the read
// that quit, or -1.
func quitRead(st *frameState, reads []string) int {
	kb := &keyState{}
	for i, read := range reads {
		if kb.handle(st, []byte(read)) {
			return i
		}
	}
	return -1
}

// TestLiveSuspendHandsTheScreenBack proves Ctrl-Z leaves the alternate screen
// before the process stops and repaints the whole frame when it continues.
func TestLiveSuspendHandsTheScreenBack(t *testing.T) {
	t.Parallel()
	term := &fakeTTY{cols: minCols, rows: minRows, chunks: []string{"", "", "q"}, stops: map[int]bool{1: true}}
	var atStop string
	term.onSuspend = func() { atStop = term.out.String() }
	out, err := runLiveFixture(t.Context(), term, &options{})
	if err != nil {
		t.Fatal(err)
	}
	if term.suspends != 1 || term.restores != 1 {
		t.Fatalf("suspends=%d restores=%d, want 1 and 1", term.suspends, term.restores)
	}
	if !strings.HasSuffix(atStop, escShowCursor+escSyncEnd+escLeaveAlt) {
		t.Fatalf("output at the stop ends %q, want the screen handed back", atStop[max(len(atStop)-24, 0):])
	}
	after := out.String()[len(atStop):]
	if !strings.HasPrefix(after, escEnterAlt+escHideCursor+escSyncBegin+sgrReset+escClear) {
		t.Fatalf("output after the stop starts %q, want the alternate screen and a full repaint", after[:min(len(after), 32)])
	}
}

// TestLiveClampsFlagsToTheTerminal proves --width/--height can only shrink
// the live frame: a 120×40 request on an 80×24 terminal draws 80×24, so no
// row wraps and the screen never scrolls.
func TestLiveClampsFlagsToTheTerminal(t *testing.T) {
	t.Parallel()
	term := &fakeTTY{cols: minCols, rows: minRows, chunks: []string{"q"}}
	out, err := runLiveFixture(t.Context(), term, &options{width: 120, height: 40})
	if err != nil {
		t.Fatal(err)
	}
	newScreen(minCols, minRows).replay(t, out.Bytes()) // fails on any write past column 80 or row 24
}

func TestKeepSize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		lastCols, lastRows, cols, rows int
		ok                             bool
		wantCols, wantRows             int
	}{
		{80, 24, 0, 0, false, 80, 24},
		{80, 24, 0, 0, true, 80, 24}, // a terminal mid-resize reports 0x0
		{100, 30, 120, 40, true, 120, 40},
		{100, 30, 120, 0, true, 100, 30},
	}
	for _, c := range cases {
		cols, rows := keepSize(c.lastCols, c.lastRows, c.cols, c.rows, c.ok)
		if cols != c.wantCols || rows != c.wantRows {
			t.Errorf("keepSize(%v) = %dx%d, want %dx%d", c, cols, rows, c.wantCols, c.wantRows)
		}
	}
}

// TestKeyInput pins which Windows console records end the read tick with a
// read: only a key press that yields a character. Anything else would leave
// ReadFile blocked until the next key, freezing resize and cancel.
func TestKeyInput(t *testing.T) {
	t.Parallel()
	const focus, mouse = 0x0010, 0x0002
	cases := []struct {
		name   string
		events []consoleEvent
		want   bool
	}{
		{"nothing", nil, false},
		{"focus only", []consoleEvent{{kind: focus}}, false},
		{"mouse only", []consoleEvent{{kind: mouse}}, false},
		{"key release", []consoleEvent{{kind: consoleKeyEvent, char: 'a'}}, false},
		{"bare shift", []consoleEvent{{kind: consoleKeyEvent, keyDown: true}}, false},
		{"focus then a", []consoleEvent{{kind: focus}, {kind: consoleKeyEvent, char: 'a', keyDown: true}}, true},
	}
	for _, c := range cases {
		if got := keyInput(c.events); got != c.want {
			t.Errorf("%s: keyInput = %v, want %v", c.name, got, c.want)
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
