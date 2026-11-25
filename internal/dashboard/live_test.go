package dashboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stainless-code/verdict/internal/core/query"
)

// fakeTick is one read tick, the VTIME the real terminal waits.
const fakeTick = 100 * time.Millisecond

// fakeTTY scripts a terminal: each read waits one (virtual) tick and returns
// the next chunk (an empty chunk is an idle tick), and the output length is
// sampled at every read so a test can see what each tick wrote. Run it inside
// a synctest bubble: a compose job's result is then deterministically in by
// the next tick.
type fakeTTY struct {
	out       *bytes.Buffer
	resizes   map[int]bool // read index -> report a resize after it
	stops     map[int]bool // read index -> report Ctrl-Z after it
	onRead    func(n int)
	onSuspend func()
	readError error
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
	time.Sleep(fakeTick)
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

func runLiveFixture(ctx context.Context, term *fakeTTY, opts *options, src source) (*bytes.Buffer, error) {
	var out bytes.Buffer
	term.out = &out
	err := live(ctx, term, &out, pickLook("auto", "xterm"), opts, src)
	return &out, err
}

func newSource() fixtureSource {
	src := partialFixture()
	src.calls = &callLog{}
	return src
}

// TestLiveSessionSwitchesTabsAndRestores drives a whole session: the first
// compose lands, an idle tick writes nothing, `2` opens the Queue, `q`
// quits. The final screen is the settled Queue and the terminal is restored
// exactly once.
func TestLiveSessionSwitchesTabsAndRestores(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		term := &fakeTTY{cols: minCols, rows: minRows, chunks: []string{"", "", "2", "", "", "q"}}
		src := newSource()
		out, err := runLiveFixture(t.Context(), term, &options{}, src)
		if err != nil {
			t.Fatal(err)
		}
		if term.restores != 1 {
			t.Fatalf("terminal restored %d times, want 1", term.restores)
		}
		if s := term.sampled; len(s) < 3 || s[1] == s[0] || s[2] != s[1] {
			t.Fatalf("output length per read = %v, want the first compose to paint and the next idle tick to write nothing", s)
		}
		checkBrackets(t, out.String())
		scr := newScreen(minCols, minRows)
		scr.replay(t, out.Bytes())
		want := &frameState{snap: mustLoad(src), tab: tabQueue, live: true}
		scr.matches(t, compose(want, minCols, minRows, pickLook("auto", "xterm")), &paletteANSI)
	})
}

// TestLiveIdleTicksWriteNothing is the flicker contract in the live loop: once
// the compose lands, every idle tick writes zero bytes.
func TestLiveIdleTicksWriteNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		term := &fakeTTY{cols: minCols, rows: minRows, chunks: []string{"", "", "", "", "", "q"}}
		if _, err := runLiveFixture(t.Context(), term, &options{}, newSource()); err != nil {
			t.Fatal(err)
		}
		s := term.sampled
		for i := 2; i < len(s); i++ {
			if s[i] != s[1] {
				t.Fatalf("output length per read = %v, want no bytes after the compose landed", s)
			}
		}
	})
}

// TestLiveCoalescesAChangeBurst pushes 20 change events between two ticks:
// they cost exactly one recompose (one more Bundles read), not twenty.
func TestLiveCoalescesAChangeBurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := newSource()
		src.changes = make(chan query.Change, 32)
		term := &fakeTTY{cols: minCols, rows: minRows, chunks: []string{"", "", "", "", "", "q"}}
		term.onRead = burstAt(2, src.changes)
		if _, err := runLiveFixture(t.Context(), term, &options{}, src); err != nil {
			t.Fatal(err)
		}
		if got := src.calls.count("Bundles"); got != 2 {
			t.Fatalf("Bundles composed %d times, want 2 (the first view and one for the whole burst)", got)
		}
	})
}

// burstAt pushes 20 change events into the stream on read n.
func burstAt(n int, changes chan<- query.Change) func(int) {
	return func(read int) {
		if read != n {
			return
		}
		for range 20 {
			changes <- query.Change{Root: "a1b2c3d4e5f6"}
		}
	}
}

// TestLiveClosedChangeStreamDoesNotRecompose runs with the --no-watch shape,
// an already-closed change channel: it is set aside, not read as a change
// on every tick.
func TestLiveClosedChangeStreamDoesNotRecompose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := newSource()
		term := &fakeTTY{cols: minCols, rows: minRows, chunks: []string{"", "", "", "", "", "q"}}
		if _, err := runLiveFixture(t.Context(), term, &options{}, src); err != nil {
			t.Fatal(err)
		}
		if got := src.calls.count("Bundles"); got != 1 {
			t.Fatalf("Bundles composed %d times over idle ticks with a closed stream, want 1", got)
		}
	})
}

// TestLiveInputNeverWaitsOnACompose gives `r` a refresh that takes ten
// seconds: the keys after it switch tabs and quit on their own ticks, long
// before the compose would finish.
func TestLiveInputNeverWaitsOnACompose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := newSource()
		var slow atomic.Bool
		src.onBundles = slowWhen(&slow)
		start := time.Now()
		var quitAt time.Duration
		term := &fakeTTY{cols: minCols, rows: minRows, chunks: []string{"", "", "r", "2", "1", "q"}}
		term.onRead = func(n int) {
			slow.CompareAndSwap(false, n == 2)
			quitAt = elapsedAt(n, 5, start, quitAt)
		}
		if _, err := runLiveFixture(t.Context(), term, &options{}, src); err != nil {
			t.Fatal(err)
		}
		checkQuickQuit(t, quitAt, src.calls)
	})
}

// checkQuickQuit fails unless q was read within a second, r dropped the
// layer cache once, and the tab switches made while the slow compose ran
// started no second compose beside it (one job at a time).
func checkQuickQuit(t *testing.T, quitAt time.Duration, calls *callLog) {
	t.Helper()
	if quitAt == 0 || quitAt > time.Second {
		t.Fatalf("q was read %v into the session, want within a second despite a 10s compose", quitAt)
	}
	if calls.count("Invalidate") != 1 {
		t.Fatal("r did not drop the layer cache")
	}
	if got := calls.count("Bundles"); got != 2 {
		t.Fatalf("Bundles composed %d times, want 2: the first view and the slow refresh, nothing beside it", got)
	}
}

// slowWhen makes every Bundles read take ten seconds once slow is set.
func slowWhen(slow *atomic.Bool) func(ctx context.Context) {
	return func(ctx context.Context) {
		if !slow.Load() {
			return
		}
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
		}
	}
}

// elapsedAt records the time since start on read want, keeping prev otherwise.
func elapsedAt(read, want int, start time.Time, prev time.Duration) time.Duration {
	if read == want {
		return time.Since(start)
	}
	return prev
}

// runRecovering runs a live session that is expected to panic and returns
// the recovered value; the session's own error is reported if it returns.
func runRecovering(t *testing.T, term *fakeTTY, opts *options, src source) (recovered any) {
	t.Helper()
	defer func() { recovered = recover() }()
	if _, err := runLiveFixture(t.Context(), term, opts, src); err != nil {
		t.Errorf("live returned %v instead of panicking", err)
	}
	t.Error("live returned instead of panicking")
	return nil
}

// checkRestored fails unless the terminal was restored exactly once and the
// output ends with the restore sequence.
func checkRestored(t *testing.T, term *fakeTTY) {
	t.Helper()
	if term.restores != 1 || !strings.HasSuffix(term.out.String(), escShowCursor+escSyncEnd+escLeaveAlt) {
		t.Fatalf("%d restores, output ends %q; want one restore and the restore sequence",
			term.restores, term.out.String()[max(term.out.Len()-24, 0):])
	}
}

// TestLiveRestoresOnAJobPanic panics inside a compose goroutine, not the
// loop: the loop re-raises it, so the screen and terminal mode are restored
// before the panic leaves live, and the original message survives.
func TestLiveRestoresOnAJobPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := newSource()
		src.onDetail = func() { panic("boom in Detail") }
		term := &fakeTTY{cols: minCols, rows: minRows, chunks: []string{"", "", "", "q"}}
		got := runRecovering(t, term, &options{tab: tabDetail}, src)
		if msg := fmt.Sprint(got); !strings.Contains(msg, "dashboard compose: boom in Detail") {
			t.Fatalf("recovered %q, want the compose panic re-raised", msg)
		}
		checkRestored(t, term)
	})
}

// TestLiveRestoresOnALoopPanic keeps the PR A guarantee for a panic on the
// loop goroutine itself.
func TestLiveRestoresOnALoopPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		term := &fakeTTY{cols: minCols, rows: minRows}
		term.onRead = func(int) { panic("boom in the loop") }
		if got := runRecovering(t, term, &options{}, newSource()); fmt.Sprint(got) != "boom in the loop" {
			t.Fatalf("recovered %v, want the loop's panic", got)
		}
		checkRestored(t, term)
	})
}

func TestLiveResizeRepaints(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		term := &fakeTTY{cols: minCols, rows: minRows, chunks: []string{"", "", "", "q"}, resizes: map[int]bool{2: true}}
		if _, err := runLiveFixture(t.Context(), term, &options{}, newSource()); err != nil {
			t.Fatal(err)
		}
		if s := term.sampled; len(s) < 3 || s[2] == s[1] {
			t.Fatalf("output length per read = %v, want the tick after a resize to repaint", s)
		}
	})
}

// TestLiveCancelReturnsContextError proves SIGINT's cancel ends the loop with
// ctx.Err() and still restores the terminal.
func TestLiveCancelReturnsContextError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		term := &fakeTTY{cols: minCols, rows: minRows, chunks: []string{"", "", ""}}
		term.onRead = cancelAt(1, cancel)
		_, err := runLiveFixture(ctx, term, &options{}, newSource())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if term.restores != 1 {
			t.Fatalf("terminal restored %d times, want 1", term.restores)
		}
	})
}

// cancelAt cancels the session on read n, the way SIGINT would.
func cancelAt(n int, cancel context.CancelFunc) func(int) {
	return func(read int) {
		if read == n {
			cancel()
		}
	}
}

func TestLiveReadErrorRestores(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		errGone := errors.New("read: input/output error")
		term := &fakeTTY{cols: minCols, rows: minRows, readError: errGone}
		if _, err := runLiveFixture(t.Context(), term, &options{}, newSource()); !errors.Is(err, errGone) {
			t.Fatalf("err = %v, want the read error", err)
		}
		if term.restores != 1 {
			t.Fatalf("terminal restored %d times, want 1", term.restores)
		}
	})
}

// TestLiveSmallTerminalShowsResizePanel proves a terminal under 80×24 gets
// the one resize panel, not a squeezed frame.
func TestLiveSmallTerminalShowsResizePanel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		term := &fakeTTY{cols: 60, rows: 20, chunks: []string{"q"}}
		out, err := runLiveFixture(t.Context(), term, &options{}, newSource())
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
	})
}

// TestLiveSuspendHandsTheScreenBack proves Ctrl-Z leaves the alternate screen
// before the process stops and repaints the whole frame when it continues.
func TestLiveSuspendHandsTheScreenBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		term := &fakeTTY{cols: minCols, rows: minRows, chunks: []string{"", "", "q"}, stops: map[int]bool{2: true}}
		var atStop string
		term.onSuspend = func() { atStop = term.out.String() }
		out, err := runLiveFixture(t.Context(), term, &options{}, newSource())
		if err != nil {
			t.Fatal(err)
		}
		checkSuspend(t, term, atStop, out.String()[len(atStop):])
	})
}

// checkSuspend fails unless the session stopped once with the screen handed
// back and came back to the alternate screen with a full repaint.
func checkSuspend(t *testing.T, term *fakeTTY, atStop, after string) {
	t.Helper()
	if term.suspends != 1 || term.restores != 1 {
		t.Fatalf("suspends=%d restores=%d, want 1 and 1", term.suspends, term.restores)
	}
	if !strings.HasSuffix(atStop, escShowCursor+escSyncEnd+escLeaveAlt) {
		t.Fatalf("output at the stop ends %q, want the screen handed back", atStop[max(len(atStop)-24, 0):])
	}
	if !strings.HasPrefix(after, escEnterAlt+escHideCursor+escSyncBegin+sgrReset+escClear) {
		t.Fatalf("output after the stop starts %q, want the alternate screen and a full repaint", after[:min(len(after), 32)])
	}
}

// TestLiveClampsFlagsToTheTerminal proves --width/--height can only shrink
// the live frame: a 120×40 request on an 80×24 terminal draws 80×24, so no
// row wraps and the screen never scrolls.
func TestLiveClampsFlagsToTheTerminal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		term := &fakeTTY{cols: minCols, rows: minRows, chunks: []string{"", "q"}}
		out, err := runLiveFixture(t.Context(), term, &options{width: 120, height: 40}, newSource())
		if err != nil {
			t.Fatal(err)
		}
		newScreen(minCols, minRows).replay(t, out.Bytes()) // fails on any write past column 80 or row 24
	})
}

// TestLiveCopyWritesOSC52 selects the second finding on the Detail tab and
// presses y: the triage command goes to the terminal as one base64 OSC 52
// write, and no subprocess exists to run.
func TestLiveCopyWritesOSC52(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := newSource()
		term := &fakeTTY{cols: wideCols, rows: minRows, chunks: []string{"", "", "j", "y", "", "q"}}
		out, err := runLiveFixture(t.Context(), term, &options{tab: tabDetail}, src)
		if err != nil {
			t.Fatal(err)
		}
		want := escClipboard + "dmVyZGljdCB0cmlhZ2UgYWNtZS9hcGkgNDIzMiAtLXNpZyBjMTQyMzIgLS1zdGF0dXMgYWRkcmVzc2Vk" + escBell
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output lacks the OSC 52 write %q", want)
		}
		if src.calls.count("Copy") != 1 {
			t.Fatalf("Copy read %d times, want 1", src.calls.count("Copy"))
		}
		checkStatus(t, out.Bytes(), "copied the triage command for c14232 (OSC 52)")
	})
}

// TestLiveCopyRefusesAnOversizedPrompt makes the fix prompt larger than a
// terminal's OSC 52 limit: nothing is written, and the status says why
// instead of claiming a copy the terminal would drop.
func TestLiveCopyRefusesAnOversizedPrompt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := newSource()
		src.prompt = strings.Repeat("x", maxClipboard+1)
		term := &fakeTTY{cols: wideCols, rows: minRows, chunks: []string{"", "", "y", "", "q"}}
		out, err := runLiveFixture(t.Context(), term, &options{tab: tabDetail}, src)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), escClipboard) {
			t.Fatal("an oversized prompt was written to the clipboard")
		}
		checkStatus(t, out.Bytes(), "the fix prompt for c04232a1 is too large to copy (102401 bytes")
	})
}

// wideCols is a terminal wide enough that the status row never truncates.
const wideCols = 200

// checkStatus fails unless the status row of the last frame drawn on a
// wideCols terminal holds want.
func checkStatus(t *testing.T, out []byte, want string) {
	t.Helper()
	scr := newScreen(wideCols, minRows)
	scr.replay(t, out)
	if row := string(scr.runes[minRows-2]); !strings.Contains(row, want) {
		t.Fatalf("status row = %q, want it to hold %q", strings.TrimSpace(row), want)
	}
}

// TestKeepSize pins the size fallback for a terminal that reports nothing.
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
// read: only a key press that yields a character.
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

// TestStaleResultIsDropped hands the loop a Detail result from an older
// generation (the user has since opened another bundle): it is not
// installed, and the current view is marked for a fresh compose.
func TestStaleResultIsDropped(t *testing.T) {
	t.Parallel()
	src := newSource()
	st := &frameState{live: true, tab: tabDetail, target: target{root: "a1b2c3d4e5f6", bundle: "4301"}}
	v := &liveView{st: st, results: make(chan result, 1), gen: 2}
	old := runJob(t.Context(), src, &want{tab: tabDetail, target: target{root: "a1b2c3d4e5f6", bundle: "4232"}})
	old.gen = 1
	v.results <- old
	v.running = true
	if err := v.drainResults(); err != nil {
		t.Fatal(err)
	}
	if st.detail != nil || st.snap != nil || !v.stale || v.running {
		t.Fatalf("detail %v snap %v stale %v running %v; want the old result dropped and a recompose pending",
			st.detail, st.snap != nil, v.stale, v.running)
	}
}

// TestLiveJobPanicDuringQuitSurfaces panics a compose while the read that
// returns q is in flight: the loop quits before it drains the result, yet
// the panic still leaves live (after the terminal is restored), never lost.
func TestLiveJobPanicDuringQuitSurfaces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := newSource()
		release := make(chan struct{})
		src.onDetail = func() {
			<-release
			panic("boom while quitting")
		}
		term := &fakeTTY{cols: minCols, rows: minRows, chunks: []string{"", "", "q"}}
		term.onRead = closeAt(2, release)
		got := runRecovering(t, term, &options{tab: tabDetail}, src)
		if msg := fmt.Sprint(got); !strings.Contains(msg, "dashboard compose: boom while quitting") {
			t.Fatalf("recovered %q, want the compose panic re-raised after the quit", msg)
		}
		checkRestored(t, term)
	})
}

// closeAt closes ch on read n, releasing whatever waits on it.
func closeAt(n int, ch chan struct{}) func(int) {
	return func(read int) {
		if read == n {
			close(ch)
		}
	}
}
