// Package acp is verdict's stdio ACP transport: a thin JSON-RPC 2.0 server
// that answers editor-agnostic Agent Client Protocol (ACP) v1 requests over
// stdin/stdout by reading the typed core query layer only. It owns protocol
// framing and result translation only; every read lives in internal/core. The
// process dies with its host: there is no daemon, no listener, and no network
// surface, and it never invokes a model, writes the disk, or mutates state.
package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	jsonRPCVersion = "2.0"
	latestProtocol = 1
	maxFrameBytes  = 16 << 20
	lineBreak      = "\n"
	serverName     = "verdict"
	scannerBytes   = 64 * 1024
	codeParse      = -32700
	codeInvalidReq = -32600
	codeNoMethod   = -32601
	codeBadParams  = -32602
	codeInternal   = -32603
)

// Layer is the read model the transport serves. The query package implements
// it; tests substitute it to observe confinement and cancellation.
type Layer interface {
	Bundles(ctx context.Context, pattern string) (*query.View, error)
	Detail(ctx context.Context, rootID, bundle string) (*query.View, error)
	Show(ctx context.Context, rootID, bundle, sig string) (*query.View, error)
	Copy(ctx context.Context, rootID, bundle, sig string) (*query.View, error)
}

// Config is one session's injected environment: the version string, the
// served root, the query-layer tuning, and the stderr stream progress uses
// (stdout stays protocol-only). A nil Layer builds the production query layer
// over Root.
type Config struct { //nolint:govet // the grouping documents the injection surface; alignment is irrelevant
	Version    string
	Root       string
	Now        func() time.Time
	Stderr     io.Writer
	Check      string
	MaxBundles int
	Layer      Layer
}

// Server is one stdio ACP session. A process serves exactly one session:
// stdin EOF ends it. It never daemonizes and has no listener.
type Server struct { //nolint:govet // the grouping separates config, the layer, and protocol state
	cfg    Config
	layer  Layer
	rootID string

	out      io.Writer
	writeMu  sync.Mutex
	writeErr error
	inflight sync.WaitGroup
	callMu   sync.Mutex
	calls    map[string]context.CancelFunc
	stopping bool
	closed   bool
}

// New builds a server for one session. Config values are normalized here so
// Serve and tests share the same defaults.
func New(cfg *Config) *Server {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Stderr == nil {
		cfg.Stderr = io.Discard
	}
	cfg.Stderr = &diagWriter{dst: cfg.Stderr}
	layer := cfg.Layer
	if layer == nil {
		layer = productionLayer(cfg)
	}
	rootID := query.RootID(absRoot(cfg.Root))
	return &Server{
		cfg:    *cfg,
		layer:  layer,
		rootID: rootID,
		calls:  map[string]context.CancelFunc{},
	}
}

// productionLayer builds the query read model over the configured root with
// the watcher off: a stdio process may not leak a background goroutine.
func productionLayer(cfg *Config) *query.Layer {
	return query.New(query.Snapshot{
		Version: cfg.Version,
		Roots:   []query.Root{{Path: cfg.Root}},
		Runtime: &core.Runtime{Now: cfg.Now},
	}, query.Options{Check: cfg.Check, MaxBundles: cfg.MaxBundles, NoWatch: true, Clock: cfg.Now})
}

// absRoot makes the served root absolute, falling back to the clean spelling
// when the OS cannot answer (the layer normalizes the same way).
func absRoot(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return filepath.Clean(root)
	}
	return abs
}

// Serve runs the newline-delimited JSON-RPC loop until stdin closes or ctx is
// canceled, then cancels in-flight calls and waits for them. The caller owns
// the reader, and a blocked read can outlive Serve. Stdout carries only
// protocol frames.
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
			_ = closeErr
			canceledReadErr := <-read
			_ = canceledReadErr
		}
	}
	s.callMu.Lock()
	s.stopping = true
	s.callMu.Unlock()
	s.inflight.Wait()
	cancel()
	s.cancelAll()
	s.writeMu.Lock()
	s.closed = true
	writeErr := s.writeErr
	s.writeMu.Unlock()
	if readErr != nil {
		return readErr
	}
	return writeErr
}

func (s *Server) readLoop(ctx context.Context, in io.Reader) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, scannerBytes), maxFrameBytes)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		s.handleLine(ctx, line)
	}
	if err := scanner.Err(); err != nil {
		return &transportError{err: err}
	}
	return nil
}

type transportError struct {
	err error
}

func (e *transportError) Error() string { return "read stdin: " + e.err.Error() }

func (e *transportError) Unwrap() error { return e.err }

func (s *Server) handleLine(ctx context.Context, line []byte) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		if _, syntax := errors.AsType[*json.SyntaxError](err); syntax {
			s.writeError(nil, codeParse, "parse error")
			return
		}
		s.writeError(nil, codeInvalidReq, "invalid request")
		return
	}
	if req.JSONRPC != jsonRPCVersion || req.Method == "" {
		if len(req.ID) == 0 {
			return
		}
		s.writeError(req.ID, codeInvalidReq, "invalid request")
		return
	}
	s.dispatch(ctx, &req)
}

func (s *Server) dispatch(ctx context.Context, req *rpcRequest) {
	switch req.Method {
	case methodInitialize:
		s.handleInitialize(req)
	case methodPing:
		if len(req.ID) > 0 {
			s.writeResult(req.ID, map[string]any{})
		}
	case methodInitializedNotice:
		// The initialized notification needs no state: this subset has no
		// server-to-client requests.
	case methodCancelACP, methodCancelMCP:
		s.handleCancelled(req)
	case methodBundlesList, methodFindingsList, methodFindingsGet, methodFindingsCopy:
		s.handleQuery(ctx, req)
	default:
		if len(req.ID) > 0 {
			s.writeError(req.ID, codeNoMethod, "method not found: "+req.Method)
		}
	}
}

func (s *Server) handleQuery(ctx context.Context, req *rpcRequest) {
	if len(req.ID) == 0 {
		return
	}
	callCtx, cancel := context.WithCancel(ctx)
	key := string(req.ID)
	s.callMu.Lock()
	defer s.callMu.Unlock()
	if s.stopping {
		cancel()
		return
	}
	s.calls[key] = cancel
	s.inflight.Go(func() {
		defer func() {
			s.callMu.Lock()
			delete(s.calls, key)
			s.callMu.Unlock()
		}()
		defer cancel()
		s.runQuery(callCtx, req)
	})
}

func (s *Server) handleCancelled(req *rpcRequest) {
	params, ok := decodeCancel(req.Params)
	if !ok {
		return
	}
	s.callMu.Lock()
	cancel := s.calls[params]
	s.callMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Server) cancelAll() {
	s.callMu.Lock()
	defer s.callMu.Unlock()
	for _, cancel := range s.calls {
		cancel()
	}
}

func (s *Server) writeResult(id json.RawMessage, result any) {
	s.writeFrame(rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Result: result})
}

func (s *Server) writeError(id json.RawMessage, code int, message string) {
	s.writeFrame(rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Error: &rpcError{Code: code, Message: message}})
}

func (s *Server) writeErrorData(id json.RawMessage, code int, message string, data any) {
	s.writeFrame(rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Error: &rpcError{Code: code, Message: message, Data: data}})
}

// writeFrame serializes one frame under the write lock. A failed write is
// remembered and returned from Serve. HTML escaping stays off so query text
// (findings carry `>` and markdown) keeps the same bytes on every surface.
func (s *Server) writeFrame(resp rpcResponse) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(resp); err != nil {
		s.logf("marshal response: %v", err)
		return
	}
	raw := bytes.TrimSuffix(jq.EscapeControls(buf.Bytes()), []byte(lineBreak))
	raw = append(raw, '\n')
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed {
		return
	}
	if _, err := s.out.Write(raw); err != nil {
		if s.writeErr == nil {
			s.writeErr = fmt.Errorf("write stdout: %w", err)
		}
	}
}

func (s *Server) logf(format string, args ...any) {
	fmt.Fprintf(s.cfg.Stderr, "acp: "+format+"\n", args...)
}

// diagWriter serializes one session's diagnostic stream. Every concurrent
// query shares stderr through this writer, so each log write takes the same
// lock and no line interleaves. Stdout frames keep their own path and ordering.
type diagWriter struct {
	dst io.Writer
	mu  sync.Mutex
}

func (w *diagWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.dst.Write(p)
	if err != nil {
		// Diagnostics are best effort: no log caller can act on a failed
		// stderr write, so the error stops here rather than failing the
		// request whose result is already being produced.
		return n, nil
	}
	return n, nil
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct { //nolint:govet // field order mirrors the wire frame
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct { //nolint:govet // field order mirrors the wire frame
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}
