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
// operator-supplied directories; a nil Layer builds the production query
// layer over them.
type Config struct { //nolint:govet // the grouping documents the injection surface; alignment is irrelevant
	Version    string
	Roots      []string
	Bundle     string
	Check      string
	MaxBundles int
	Interval   time.Duration
	Sweep      time.Duration
	NoWatch    bool
	Now        func() time.Time
	Stderr     io.Writer
	Layer      ReadModel
}

// Server is one stdio LSP session. A process serves exactly one: `exit` or
// stdin EOF ends it. It never daemonizes and has no listener.
type Server struct { //nolint:govet // the grouping separates config, protocol state, and the composed state
	cfg   Config
	layer ReadModel
	roots []servedRoot

	out      io.Writer
	writeMu  sync.Mutex
	writeErr error
	closed   bool

	callMu      sync.Mutex
	calls       map[string]context.CancelFunc
	inflight    sync.WaitGroup
	initialized bool
	shutdown    bool

	pumpCancel context.CancelFunc
	pumpDone   chan struct{}

	// stateMu guards everything below: the read loop, the watch pump, and a
	// refresh call all compose and publish under it, so frames for one
	// trigger never interleave with another's.
	stateMu  sync.Mutex
	initOpts initOptions
	states   map[string]*bundleState
	docs     map[string]*document
	docKeys  map[string]string
	sent     map[string]published
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
	c.Stderr = &lockedWriter{dst: c.Stderr}
	roots := make([]servedRoot, 0, len(c.Roots))
	seen := map[string]bool{}
	for _, r := range c.Roots {
		root := newServedRoot(osRoot(r), runtime.GOOS == "windows")
		if seen[root.ID] {
			continue
		}
		seen[root.ID] = true
		roots = append(roots, root)
	}
	layer := c.Layer
	if layer == nil {
		layer = productionLayer(&c, roots)
	}
	return &Server{
		cfg:     c,
		layer:   layer,
		roots:   roots,
		calls:   map[string]context.CancelFunc{},
		states:  map[string]*bundleState{},
		docs:    map[string]*document{},
		docKeys: map[string]string{},
		sent:    map[string]published{},
	}
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

// Preflight validates an explicit --bundle before the session starts: it must
// resolve under at least one served root. The refusal is the CLI's to render.
func (s *Server) Preflight() *core.RefusalError {
	if s.cfg.Bundle == "" {
		return nil
	}
	var last error
	for i := range s.roots {
		_, _, err := s.layer.ResolveBundle(s.roots[i].ID, s.cfg.Bundle)
		if err == nil {
			return nil
		}
		last = err
	}
	if ref, ok := errors.AsType[*core.RefusalError](last); ok {
		return ref
	}
	return &core.RefusalError{
		Code:    codeMissingBundle,
		Message: fmt.Sprintf("lsp: --bundle %s names no bundle under any served root", s.cfg.Bundle),
	}
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
	s.stop(true)
	s.writeMu.Lock()
	s.closed = true
	writeErr := s.writeErr
	s.writeMu.Unlock()
	if readErr != nil {
		return readErr
	}
	return writeErr
}

// stop ends every background activity: no new call starts, in-flight calls
// are canceled (when cancel is set) and awaited, and the watch pump exits.
// `shutdown` drains instead of canceling, so a request the client sent
// before it still gets its real answer; a dead stdin or a signal cancels.
func (s *Server) stop(cancel bool) {
	s.callMu.Lock()
	s.shutdown = true
	if cancel {
		for _, c := range s.calls {
			c()
		}
	}
	s.callMu.Unlock()
	s.inflight.Wait()
	s.stopPump()
}

// startPump consumes the layer's invalidations until stopPump. Each change
// recomposes the named root and republishes its documents.
func (s *Server) startPump(ctx context.Context) {
	if s.cfg.NoWatch || s.pumpCancel != nil {
		return
	}
	pumpCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.pumpCancel, s.pumpDone = cancel, done
	changes := s.layer.Changes(pumpCtx)
	go func() {
		defer close(done)
		for change := range changes {
			s.onChange(pumpCtx, &change)
		}
	}()
}

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
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	for i := range s.roots {
		root := &s.roots[i]
		if root.ID != change.Root {
			continue
		}
		s.compose(ctx, root, true)
		s.republishRoot(root)
	}
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
