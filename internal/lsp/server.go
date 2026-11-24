// Package lsp is verdict's stdio Language Server: a zero-dependency LSP 3.17
// adapter that publishes a review bundle's findings as editor diagnostics. It
// owns framing, the lifecycle, URI confinement, and the finding-to-diagnostic
// mapping only; every fact comes from the typed read model in
// internal/core/query. The server reads no workspace file (document text
// arrives from the client), writes nothing, spawns nothing, and never
// composes on a keystroke: diagnostics move on open, save, a bundle watch
// change, and the explicit verdict.refresh command.
package lsp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
)

// Session bounds. Each is a refusal or a truncation with a log line, never a
// silent drop.
const (
	// DefaultMaxRoots and MaxRoots bound the served roots.
	DefaultMaxRoots = 8
	MaxRoots        = 16
	// maxInflight is how many requests run at once; maxQueued more wait in
	// FIFO order, and a request past both answers ServerCancelled.
	maxInflight = 16
	maxQueued   = 256
	// maxDocs bounds the open documents the server tracks.
	maxDocs = 512
	// maxFindingsTracked bounds the findings one bundle contributes; past
	// it the highest-ranked findings are kept and the rest are reported cut.
	maxFindingsTracked = 20_000
	// shutdownGrace is how long `shutdown` lets in-flight requests finish
	// before canceling the rest.
	shutdownGrace = 5 * time.Second
)

// ErrExitWithoutShutdown is Serve's result when the client sent `exit`
// before `shutdown`: the LSP contract's abnormal exit, which the CLI maps to
// exit code 1.
var ErrExitWithoutShutdown error = exitError{}

type exitError struct{}

func (exitError) Error() string { return "exit received before shutdown" }

// ReadModel is the query surface the server reads. *query.Layer implements
// it; tests substitute it to observe what the server asks for.
type ReadModel interface {
	Bundles(ctx context.Context, pattern string) (*query.View, error)
	Detail(ctx context.Context, rootID, bundle string) (*query.View, error)
	Diff(ctx context.Context, rootID, bundle string) (*query.View, error)
	ResolveBundle(rootID, bundle string) (string, string, error)
	Invalidate()
	Changes(ctx context.Context) <-chan query.Change
}

// Config is one session's injected environment. Roots are absolute,
// operator-supplied directories the CLI already validated; when Roots is
// empty the client's workspace folders are served instead, falling back to
// FallbackRoot (the checkout the process started in, if any). A nil Layer
// builds the production query layer over the served roots.
type Config struct { //nolint:govet // the grouping documents the injection surface; alignment is irrelevant
	Version      string
	Roots        []string
	FallbackRoot string
	MaxRoots     int
	Bundle       string
	Check        string
	MaxBundles   int
	Interval     time.Duration
	Sweep        time.Duration
	NoWatch      bool
	Now          func() time.Time
	Stderr       io.Writer
	Layer        ReadModel

	// lim overrides the session bounds; tests shrink them.
	lim limits
	// eval resolves a path's physical spelling; tests fake it with
	// Windows-shaped inputs. nil is filepath.EvalSymlinks.
	eval func(string) (string, error)
}

// limits are the session bounds a Server enforces.
type limits struct {
	grace      time.Duration
	docs       int
	findings   int
	frameBytes int
	inflight   int
	queued     int
}

func (l limits) withDefaults() limits {
	def := limits{
		grace: shutdownGrace, docs: maxDocs, findings: maxFindingsTracked,
		frameBytes: maxFrameBytes, inflight: maxInflight, queued: maxQueued,
	}
	if l.grace > 0 {
		def.grace = l.grace
	}
	for _, pair := range []struct{ src, dst *int }{
		{&l.docs, &def.docs},
		{&l.findings, &def.findings},
		{&l.frameBytes, &def.frameBytes},
		{&l.inflight, &def.inflight},
		{&l.queued, &def.queued},
	} {
		if *pair.src > 0 {
			*pair.dst = *pair.src
		}
	}
	return def
}

// Server is one stdio LSP session. A process serves exactly one: `exit` or
// stdin EOF ends it. It never daemonizes and has no listener.
type Server struct { //nolint:govet // the grouping separates config, protocol state, and the composed state
	cfg     Config
	lim     limits
	windows bool

	out      io.Writer
	writeMu  sync.Mutex
	writeErr error
	closed   bool

	callMu      sync.Mutex
	calls       map[string]context.CancelFunc
	inflight    sync.WaitGroup
	queue       jobQueue
	background  sync.WaitGroup
	initialized bool
	shutdown    bool
	exited      bool
	ended       bool
	sawCancel   bool

	pumpCancel context.CancelFunc
	pumpDone   chan struct{}

	// composeMu serializes compositions (the slow bundle reads); a
	// composition holds it but never stateMu while it reads.
	composeMu sync.Mutex

	// stateMu guards everything below: the read loop, the watch pump, and a
	// refresh call publish under it, so frames for one trigger never
	// interleave with another's. It is only ever held briefly.
	stateMu   sync.Mutex
	pending   map[string]bool
	uriKeys   map[string]string
	layer     ReadModel
	roots     []servedRoot
	initOpts  initOptions
	states    map[string]*bundleState
	docs      map[string]*document
	docBytes  int
	sent      map[string]published
	pendLogs  []logMessageParams
	workspace bool
}

// New builds a server for one session. Config values are normalized here so
// Serve and tests share the same defaults.
func New(cfg *Config) *Server {
	if cfg == nil {
		cfg = &Config{}
	}
	c := *cfg
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Stderr == nil {
		c.Stderr = io.Discard
	}
	if c.eval == nil {
		c.eval = evalOS
	}
	if c.MaxRoots <= 0 || c.MaxRoots > MaxRoots {
		c.MaxRoots = DefaultMaxRoots
	}
	c.Stderr = &lockedWriter{dst: c.Stderr}
	lim := c.lim.withDefaults()
	s := &Server{
		cfg:       c,
		lim:       lim,
		windows:   runtime.GOOS == "windows",
		calls:     map[string]context.CancelFunc{},
		queue:     jobQueue{limit: lim.inflight, capacity: lim.queued},
		states:    map[string]*bundleState{},
		docs:      map[string]*document{},
		uriKeys:   map[string]string{},
		pending:   map[string]bool{},
		sent:      map[string]published{},
		workspace: len(c.Roots) == 0,
	}
	s.setRoots(s.rootsOf(c.Roots))
	return s
}

// rootsOf derives the served form of absolute OS paths, deduplicated by
// identity and bounded by MaxRoots.
func (s *Server) rootsOf(paths []string) []servedRoot {
	roots := make([]servedRoot, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		root := newServedRoot(osRoot(p), s.windows)
		if seen[root.ID] || len(roots) >= s.cfg.MaxRoots {
			continue
		}
		if phys, ok := physicalPath(root.slash, s.windows, s.cfg.eval); ok && root.host == "" {
			root.physical = phys
		}
		seen[root.ID] = true
		roots = append(roots, root)
	}
	return roots
}

// setRoots installs a root set and the read model over it.
func (s *Server) setRoots(roots []servedRoot) {
	s.roots = roots
	if s.cfg.Layer != nil {
		s.layer = s.cfg.Layer
		return
	}
	s.layer = productionLayer(&s.cfg, roots)
}

// productionLayer builds one query read model over every served root. The
// watcher runs only while the session pumps it (Changes is consumed from
// `initialized` until `shutdown`).
func productionLayer(cfg *Config, roots []servedRoot) *query.Layer {
	qroots := make([]query.Root, 0, len(roots))
	for _, r := range roots {
		qroots = append(qroots, query.Root{Path: r.Path})
	}
	return query.New(query.Snapshot{
		Version: cfg.Version,
		Roots:   qroots,
		Runtime: &core.Runtime{Now: cfg.Now},
	}, query.Options{
		Check: cfg.Check, MaxBundles: cfg.MaxBundles,
		Interval: cfg.Interval, Sweep: cfg.Sweep, NoWatch: cfg.NoWatch, Clock: cfg.Now,
	})
}

// Preflight validates an explicit --bundle against explicit --root values
// before the session starts: it must resolve under at least one. With
// client workspace folders the roots are not known yet, so the check runs
// at initialize and warns instead. The refusal is the CLI's to render.
func (s *Server) Preflight() *core.RefusalError {
	if s.cfg.Bundle == "" || s.workspace {
		return nil
	}
	last := s.resolveAnywhere(s.cfg.Bundle)
	if last == nil {
		return nil
	}
	if ref, ok := errors.AsType[*core.RefusalError](last); ok {
		return ref
	}
	return &core.RefusalError{Code: codeMissingBundle, Message: missingBundleMessage(s.cfg.Bundle)}
}

// resolveAnywhere returns nil when bundle resolves under some root, else the
// last resolution failure.
func (s *Server) resolveAnywhere(bundle string) error {
	last := query.ErrNotFound
	for i := range s.roots {
		_, _, err := s.layer.ResolveBundle(s.roots[i].ID, bundle)
		if err == nil {
			return nil
		}
		last = err
	}
	return last
}

func missingBundleMessage(bundle string) string {
	return fmt.Sprintf("lsp: --bundle %s names no bundle under any served root — run verdict fetch-local "+
		"<owner/repo> <slug> (or verdict fetch for a PR) to create it, then start verdict lsp again", bundle)
}

// codeMissingBundle is the registered refusal for a --bundle that resolves
// nowhere.
const codeMissingBundle = "missing-bundle"

// Serve runs the session until `exit`, stdin EOF, or ctx cancellation. The
// caller owns the reader, and a blocked read can outlive Serve. Stdout
// carries only protocol frames.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	return s.serve(ctx, in, out, nil)
}

// ServeInterruptible runs Serve with a reader whose Close promptly unblocks
// Read. On cancellation it closes the reader and waits for the read loop.
func (s *Server) ServeInterruptible(ctx context.Context, in io.ReadCloser, out io.Writer) error {
	return s.serve(ctx, in, out, in.Close)
}

func (s *Server) serve(ctx context.Context, in io.Reader, out io.Writer, interrupt func() error) error {
	s.out = out
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	read := make(chan error, 1)
	go func() { read <- s.readLoop(ctx, in) }()
	var readErr error
	select {
	case readErr = <-read:
	case <-ctx.Done():
		if interrupt != nil {
			closeErr := interrupt()
			_ = closeErr // the reader is gone either way; the cancel is the result
			canceledReadErr := <-read
			_ = canceledReadErr
		}
	}
	readErr = s.noteCancel(ctx, readErr)
	if s.exitedClean() {
		// `exit` after `shutdown`: the drain still answers what the client
		// sent before shutdown, bounded by the grace period.
		s.background.Wait()
	}
	s.stop()
	s.background.Wait()
	s.writeMu.Lock()
	s.closed = true
	writeErr := s.writeErr
	s.writeMu.Unlock()
	if readErr != nil {
		return readErr
	}
	return writeErr
}

// noteCancel records a cancel seen either way (the select, or a read it
// interrupted) and drops the cancel from the read result: Canceled reports
// it, and still reports false when the session had ended first.
func (s *Server) noteCancel(ctx context.Context, readErr error) error {
	if ctx.Err() == nil {
		return readErr
	}
	s.callMu.Lock()
	s.sawCancel = true
	s.callMu.Unlock()
	if errors.Is(readErr, ctx.Err()) {
		return nil
	}
	return readErr
}

// Canceled reports whether Serve ended because its context was canceled
// before the session ended on its own (exit, stdin EOF, or a framing
// failure). A cancel that lands after the session already ended is not a
// cancellation of it: the CLI maps only a true one to exit 3.
func (s *Server) Canceled() bool {
	s.callMu.Lock()
	defer s.callMu.Unlock()
	return s.sawCancel && !s.ended
}

// markEnded records that the read loop ended for a reason of its own.
func (s *Server) markEnded() {
	s.callMu.Lock()
	s.ended = true
	s.callMu.Unlock()
}

// exitedClean reports an `exit` read after `shutdown`.
func (s *Server) exitedClean() bool {
	s.callMu.Lock()
	defer s.callMu.Unlock()
	return s.exited && s.shutdown
}

// stop ends every background activity once the read loop is done: no new
// call starts, every in-flight or queued call is canceled and awaited, and
// the watch pump exits.
func (s *Server) stop() {
	s.callMu.Lock()
	s.shutdown = true
	s.callMu.Unlock()
	s.cancelCalls()
	s.inflight.Wait()
	s.stopPump()
}

// cancelCalls cancels every in-flight and queued call.
func (s *Server) cancelCalls() {
	s.callMu.Lock()
	defer s.callMu.Unlock()
	for _, c := range s.calls {
		c()
	}
}

// drain waits for in-flight calls for up to the grace period, then cancels
// the rest (they answer RequestCancelled) and waits for them to answer.
func (s *Server) drain() {
	done := make(chan struct{})
	go func() {
		s.inflight.Wait()
		close(done)
	}()
	grace := time.NewTimer(s.lim.grace)
	defer grace.Stop()
	select {
	case <-done:
	case <-grace.C:
		s.cancelCalls()
		<-done
	}
}

// startPump consumes the layer's invalidations until stopPump. Each change
// recomposes the named root and republishes. The caller holds callMu.
func (s *Server) startPump(ctx context.Context) {
	if s.cfg.NoWatch || s.pumpCancel != nil || s.shutdown {
		return
	}
	pumpCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.pumpCancel, s.pumpDone = cancel, done
	s.stateMu.Lock()
	changes := s.layer.Changes(pumpCtx)
	s.stateMu.Unlock()
	go func() {
		defer close(done)
		for change := range changes {
			s.onChange(pumpCtx, &change)
		}
	}()
}

// stopPump stops the pump and waits for it. It never runs under stateMu: the
// pump takes stateMu for each change.
func (s *Server) stopPump() {
	s.callMu.Lock()
	cancel, done := s.pumpCancel, s.pumpDone
	s.pumpCancel, s.pumpDone = nil, nil
	s.callMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// onChange recomposes the root a watch change names. A change for a bundle
// other than the selected one still reselects: a new bundle may now win.
func (s *Server) onChange(ctx context.Context, change *query.Change) {
	s.composeMu.Lock()
	defer s.composeMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	s.stateMu.Lock()
	roots := s.rootsSnapshot()
	s.stateMu.Unlock()
	for i := range roots {
		if roots[i].ID == change.Root {
			s.compose(ctx, &roots[i], true)
		}
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.republishAll()
}

// deferCompose recomposes roots once the composition in flight finishes,
// then republishes. A notification that met a busy composition uses it
// instead of waiting on the read loop; a root already pending is not queued
// twice.
func (s *Server) deferCompose(ctx context.Context, roots []servedRoot) {
	queued := s.claimPending(roots)
	if len(queued) == 0 {
		return
	}
	s.background.Go(func() {
		s.composeMu.Lock()
		defer s.composeMu.Unlock()
		reselect := s.releasePending(queued)
		for i := range queued {
			s.compose(ctx, &queued[i], reselect[queued[i].ID])
		}
		s.stateMu.Lock()
		defer s.stateMu.Unlock()
		s.republishAll()
	})
}

// claimPending marks roots pending and returns the ones not already queued.
func (s *Server) claimPending(roots []servedRoot) []servedRoot {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	var queued []servedRoot
	for i := range roots {
		if !s.pending[roots[i].ID] {
			s.pending[roots[i].ID] = true
			queued = append(queued, roots[i])
		}
	}
	return queued
}

// releasePending clears the pending marks and reports which roots were never
// composed (they reselect).
func (s *Server) releasePending(roots []servedRoot) map[string]bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	reselect := map[string]bool{}
	for i := range roots {
		delete(s.pending, roots[i].ID)
		reselect[roots[i].ID] = s.states[roots[i].ID] == nil
	}
	return reselect
}

// lockedWriter serializes one session's stderr: the read loop, the pump,
// and a refresh call can each log.
type lockedWriter struct {
	dst io.Writer
	mu  sync.Mutex
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.dst.Write(p)
	if err != nil {
		// Diagnostics are best effort: no caller can act on a failed stderr
		// write, so the error stops here.
		return n, nil
	}
	return n, nil
}
