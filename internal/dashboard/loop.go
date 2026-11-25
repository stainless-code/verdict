package dashboard

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"runtime/debug"
	"strconv"
	"sync"

	"github.com/stainless-code/verdict/internal/core/query"
)

// escClipboard opens an OSC 52 clipboard write; escBell ends it. The payload
// is base64, so the copied text cannot inject a sequence of its own.
const (
	escClipboard = "\x1b]52;c;"
	escBell      = "\x07"
	// maxClipboard bounds one copy. Terminals cap OSC 52 (often near 100 KB)
	// and drop a longer one without a word, so a larger text is refused here
	// with a note instead of a "copied" that never happened.
	maxClipboard = 100 << 10
)

// result is one compose job's outcome, handed from its goroutine to the loop.
type result struct {
	snap  *snapshot
	view  *query.View
	err   error
	panic any // a panic in the job, re-raised on the loop goroutine
	want  want
	gen   uint64
}

// live runs one interactive session. The loop goroutine owns every piece of
// UI state: it paints, polls one key tick, and drains the change stream and
// finished jobs without ever waiting on either. Composition runs in job
// goroutines, one at a time; a job's panic is handed back and re-raised on
// this goroutine, after the terminal is restored, whether it arrives while
// the loop runs or in the tick the user quits.
func live(ctx context.Context, t tty, out io.Writer, lk look, opts *options, src source) (err error) {
	s := &session{term: t, out: out}
	jobCtx, stopJobs := context.WithCancel(ctx)
	v := &liveView{
		term: t, opts: opts, lk: lk, s: s, src: src, out: out,
		st:      &frameState{live: true, tab: opts.tab, repo: opts.repo},
		p:       &painter{out: out, pal: lk.pal},
		keys:    make([]byte, keyBuf),
		results: make(chan result, 1),
	}
	defer func() {
		stopJobs()
		if rerr := s.restore(); err == nil {
			err = rerr
		}
		v.jobs.Wait()
		v.raiseLatePanic()
	}()
	if err := s.enter(); err != nil {
		return err
	}
	v.changes = src.Changes(jobCtx)
	v.launch(jobCtx)
	for {
		done, err := v.tick(ctx, jobCtx)
		if done || err != nil {
			return err
		}
	}
}

// liveView is one session's loop state. Only the loop goroutine touches it.
type liveView struct { //nolint:govet // grouped by role: terminal, data, jobs
	term    tty
	src     source
	out     io.Writer
	opts    *options
	st      *frameState
	p       *painter
	s       *session
	changes <-chan query.Change
	// results holds at most one value: one job runs at a time, and the loop
	// takes its result before launching the next, so a send never blocks.
	results chan result
	keys    []byte
	lk      look
	dec     decoder
	jobs    sync.WaitGroup
	gen     uint64 // bumped when the wanted view changes; older results are dropped
	running bool   // a job is in flight
	stale   bool   // compose again when the running job returns
}

func (v *liveView) paint() error {
	cols, rows, err := frameSize(v.term, v.opts)
	if err != nil {
		return err
	}
	return v.p.paint(compose(v.st, cols, rows, v.lk))
}

// tick paints whatever changed, then waits one read tick for keys, then
// takes in whatever arrived meanwhile. done reports a quit, a cancel, or a
// terminal failure.
func (v *liveView) tick(ctx, jobCtx context.Context) (bool, error) {
	if err := v.paint(); err != nil {
		return true, err
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	n, err := v.term.read(v.keys)
	if err != nil {
		return true, err
	}
	if v.handleKeys(jobCtx, v.dec.decode(v.keys[:n])) {
		return true, nil
	}
	if err := v.absorb(jobCtx); err != nil {
		return true, err
	}
	if v.term.resized() {
		v.p.invalidate()
	}
	return v.suspendIfAsked(ctx)
}

// absorb takes in what arrived during the tick: changes mark the view stale,
// a finished job installs its views, and a stale view with no job running
// starts the next compose.
func (v *liveView) absorb(jobCtx context.Context) error {
	v.drainChanges()
	if err := v.drainResults(); err != nil {
		return err
	}
	if v.stale && !v.running {
		v.launch(jobCtx)
	}
	return nil
}

// handleKeys applies one tick's keys; true means quit.
func (v *liveView) handleKeys(jobCtx context.Context, events []keyEvent) bool {
	for _, ev := range events {
		switch v.st.handle(ev) {
		case actQuit:
			return true
		case actReload:
			v.gen++
			v.stale = true
		case actRefresh:
			v.src.Invalidate()
			v.stale = true
		case actCopy:
			v.copySelected(jobCtx)
		default:
		}
	}
	return false
}

// drainChanges coalesces every change the layer has published since the
// last tick (the layer already debounces a burst to one) into one recompose.
// A closed stream (--no-watch, or the layer gave up) is set to nil: a
// closed channel is always ready and would spin the loop.
func (v *liveView) drainChanges() {
	for v.changes != nil {
		select {
		case _, ok := <-v.changes:
			if !ok {
				v.changes = nil
				return
			}
			v.stale = true
		default:
			return
		}
	}
}

// drainResults takes a finished job, if any. A result for a view the user
// has since left (an older generation) is dropped and the current view is
// composed instead; a job's panic is re-raised here, on the loop goroutine.
func (v *liveView) drainResults() error {
	select {
	case r := <-v.results:
		v.running = false
		v.st.composing = false
		if r.panic != nil {
			panic(r.panic)
		}
		if r.gen != v.gen {
			v.stale = true
			return nil
		}
		return v.apply(&r)
	default:
		return nil
	}
}

// apply installs a job's views. The first snapshot failing ends the session
// (there is nothing to draw); later failures show in the tab instead.
func (v *liveView) apply(r *result) error {
	if r.snap == nil {
		if v.st.snap == nil {
			return r.err
		}
		v.st.note = r.err.Error()
		return nil
	}
	v.st.snap = filterRepo(r.snap, v.st.repo)
	installView(v.st, &r.want, r.view, r.err)
	return nil
}

// launch starts one compose job for the current view. One runs at a time:
// keys and changes that arrive meanwhile mark the view stale, so a burst
// costs one more compose, not one per event.
func (v *liveView) launch(ctx context.Context) {
	v.stale, v.running, v.st.composing = false, true, true
	w := pickTarget(v.st)
	gen := v.gen
	v.jobs.Go(func() {
		defer v.recoverJob(gen)
		r := runJob(ctx, v.src, w)
		r.gen = gen
		v.deliver(&r)
	})
}

// recoverJob hands a job's panic to the loop with its stack. The loop
// re-raises it on its next tick, or, if the user quit first, live re-raises
// it after restoring the terminal: a compose panic is never lost.
func (v *liveView) recoverJob(gen uint64) {
	p := recover()
	if p == nil {
		return
	}
	v.results <- result{panic: fmt.Sprintf("dashboard compose: %v\n%s", p, debug.Stack()), gen: gen}
}

func (v *liveView) deliver(r *result) {
	v.results <- *r
}

// raiseLatePanic re-raises a job panic the loop left undrained because the
// session ended in the same tick; any other late result is dropped.
func (v *liveView) raiseLatePanic() {
	select {
	case r := <-v.results:
		if r.panic != nil {
			panic(r.panic)
		}
	default:
	}
}

// copySelected puts the selected finding's fix prompt (or its triage
// command) on the clipboard through OSC 52: an escape to this terminal,
// never a subprocess.
func (v *liveView) copySelected(ctx context.Context) {
	r, ok := v.st.selected()
	if !ok {
		return
	}
	view, err := v.src.Copy(ctx, v.st.target.root, v.st.target.bundle, r.sig)
	if err != nil {
		v.st.note = "copy: " + err.Error()
		return
	}
	text, what := copyText(view)
	if len(text) > maxClipboard {
		v.st.note = "the " + what + " for " + shortHead(r.sig) + " is too large to copy (" +
			strconv.Itoa(len(text)) + " bytes; the terminal clipboard takes " + strconv.Itoa(maxClipboard) + ")"
		return
	}
	payload := base64.StdEncoding.EncodeToString([]byte(text))
	if _, err := io.WriteString(v.out, escClipboard+payload+escBell); err != nil {
		v.st.note = "copy: " + err.Error()
		return
	}
	v.st.note = "copied the " + what + " for " + shortHead(r.sig) + " (OSC 52)"
}

// suspendIfAsked honors Ctrl-Z: the shell gets its screen and cooked mode
// back while the job is stopped, and the frame repaints in full on return.
func (v *liveView) suspendIfAsked(ctx context.Context) (done bool, err error) {
	if !v.term.stopRequested() {
		return false, nil
	}
	if err := v.s.suspend(ctx); err != nil {
		return true, err
	}
	v.p.invalidate()
	return false, nil
}
