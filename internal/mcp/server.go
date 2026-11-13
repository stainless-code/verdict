// Package mcp is verdict's stdio MCP transport: a thin JSON-RPC 2.0 server
// that maps MCP tools onto the typed core services. It owns protocol
// framing, input-schema reflection, and result translation only; every
// verb lives in internal/core. The process dies with its host: there is no
// daemon, no listener, and no network surface.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	jsonRPCVersion = "2.0"
	latestProtocol = "2025-06-18"
	maxFrameBytes  = 16 << 20
	// maxResultTextBytes caps the text block of one tool result. The data stays
	// whole in structuredContent; only the text an agent reads is budgeted.
	maxResultTextBytes  = 256 << 10
	methodInitialize    = "initialize"
	methodInitialized   = "notifications/initialized"
	methodPing          = "ping"
	methodToolsList     = "tools/list"
	methodToolsCall     = "tools/call"
	methodCancelled     = "notifications/cancelled" //nolint:misspell // MCP spells the method "cancelled"
	codeParse           = -32700
	codeInvalidRequest  = -32600
	codeMethodNotFound  = -32601
	codeInvalidParams   = -32602
	contentTypeText     = "text"
	refusalTextLines    = 3
	lineBreak           = "\n"
	serverName          = "verdict"
	scannerInitialBytes = 64 * 1024
)

// supportedProtocols are the MCP revisions the server can speak.
var supportedProtocols = []string{"2024-11-05", "2025-03-26", latestProtocol}

// Config is the transport's injected environment: the version string, the
// prefs/core runtime the CLI resolved, and the stderr stream every progress
// line uses (stdout stays protocol-only). Commanders overrides a tool's
// production runner by name; it is a test seam, while the registry owns the
// production wiring.
type Config struct { //nolint:govet // the grouping documents the injection surface; alignment is irrelevant
	Version    string
	Env        map[string]string
	Environ    []string
	Now        func() time.Time
	Effective  core.Effective
	Warnings   []string
	Root       string
	ReadOnly   bool
	Operator   bool
	Stderr     io.Writer
	Commanders map[string]core.Commander
}

// Server is one stdio MCP session. A process serves exactly one session:
// stdin EOF ends it. It never daemonizes and has no listener.
type Server struct { //nolint:govet // the grouping separates config, tools, and protocol state
	cfg    Config
	tools  []*tool
	byName map[string]*tool
	hidden map[string]hiddenTool

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
	if cfg.Env == nil {
		cfg.Env = map[string]string{}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Effective == (core.Effective{}) {
		cfg.Effective = core.Defaults()
	}
	if cfg.Stderr == nil {
		cfg.Stderr = io.Discard
	}
	cfg.Stderr = &diagWriter{dst: cfg.Stderr}
	tools := allTools(cfg.ReadOnly, cfg.Operator)
	applyCommanderOverrides(tools, cfg.Commanders)
	byName := make(map[string]*tool, len(tools))
	for _, t := range tools {
		byName[t.name] = t
	}
	return &Server{
		cfg:    *cfg,
		tools:  tools,
		byName: byName,
		hidden: hiddenTools(cfg.ReadOnly, cfg.Operator),
		calls:  map[string]context.CancelFunc{},
	}
}

type hiddenTool struct {
	requires string
}

func hiddenTools(readOnly, operator bool) map[string]hiddenTool {
	out := map[string]hiddenTool{}
	for _, t := range allTools(false, true) {
		if exposed(t, readOnly, operator) {
			continue
		}
		out[t.name] = hiddenTool{requires: toolRequires(t, readOnly)}
	}
	return out
}

func toolRequires(t *tool, readOnly bool) string {
	switch {
	case t.operatorOnly && readOnly:
		return "a server started with --operator and without --read-only"
	case t.operatorOnly:
		return "a server started with --operator"
	default:
		return "a server started without --read-only"
	}
}

func applyCommanderOverrides(tools []*tool, overrides map[string]core.Commander) {
	for _, t := range tools {
		if commander, ok := overrides[t.name]; ok {
			t.deps.commander = commander
		}
	}
}

// Serve runs the newline-delimited JSON-RPC loop until stdin closes or ctx is
// canceled, then cancels in-flight calls, waits for them, and returns. Stdout
// carries only protocol frames.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.out = out
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	read := make(chan error, 1)
	go func() { read <- s.readLoop(ctx, in) }()
	var readErr error
	select {
	case readErr = <-read:
	case <-ctx.Done():
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
	scanner.Buffer(make([]byte, scannerInitialBytes), maxFrameBytes)
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
		s.writeError(nil, codeInvalidRequest, "invalid request")
		return
	}
	if req.JSONRPC != jsonRPCVersion || req.Method == "" {
		if len(req.ID) == 0 {
			return
		}
		s.writeError(req.ID, codeInvalidRequest, "invalid request")
		return
	}
	s.dispatch(ctx, &req)
}

func (s *Server) dispatch(ctx context.Context, req *rpcRequest) {
	switch req.Method {
	case methodInitialize:
		s.handleInitialize(req)
	case methodInitialized:
		// The initialized notification needs no state: this subset has no
		// server-to-client requests.
	case methodPing:
		s.writeResult(req.ID, map[string]any{})
	case methodToolsList:
		s.writeResult(req.ID, s.listTools())
	case methodToolsCall:
		s.handleToolsCall(ctx, req)
	case methodCancelled:
		s.handleCancelled(req)
	default:
		if len(req.ID) > 0 {
			s.writeError(req.ID, codeMethodNotFound, "method not found: "+req.Method)
		}
	}
}

func (s *Server) handleInitialize(req *rpcRequest) {
	if len(req.ID) == 0 {
		return
	}
	params, ok := decodeParams[initializeParams](req.Params)
	if !ok {
		s.writeError(req.ID, codeInvalidParams, "invalid initialize params")
		return
	}
	s.writeResult(req.ID, initializeResult{
		ProtocolVersion: negotiate(params.ProtocolVersion),
		Capabilities:    map[string]any{"tools": map[string]any{"listChanged": false}},
		ServerInfo: serverInfo{
			Name:         serverName,
			Version:      s.cfg.Version,
			Experimental: true,
		},
		Instructions: s.instructions(),
	})
}

func (s *Server) instructions() string {
	lines := []string{
		"Review one pull request at a time: fetch, read .verdict/state/<pr>, write exactly one review.json, then post.",
		"Use report, next, or show to rank and inspect findings; refusals carry a hint, so follow it.",
		"post is the only terminal write and never writes a review note; dryRun previews it.",
		"Never call triage while reviewing a PR: propose status changes through triage[] in review.json.",
	}
	if s.cfg.Operator {
		return strings.Join(append(lines,
			"This server runs with --operator, so triage is available for human-owned status flips."), lineBreak)
	}
	return strings.Join(append(lines,
		"triage is not on this surface; it is available only when the server starts with --operator."), lineBreak)
}

func (s *Server) handleToolsCall(ctx context.Context, req *rpcRequest) {
	if len(req.ID) == 0 {
		return
	}
	params, err := parseCallParams(req.Params)
	if err != nil {
		s.writeError(req.ID, codeInvalidParams, err.Error())
		return
	}
	t, ok := s.byName[params.Name]
	if !ok {
		s.writeHiddenToolError(req.ID, params.Name)
		return
	}
	request, ok := s.decodeCall(t, req.ID, params.Arguments)
	if !ok {
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
		s.writeResult(req.ID, s.call(callCtx, t, request))
	})
}

func (s *Server) decodeCall(t *tool, id, raw json.RawMessage) (any, bool) {
	if t.decode == nil {
		return nil, true
	}
	request, err := t.decode(raw)
	if err != nil {
		s.writeError(id, codeInvalidParams, err.Error())
		return nil, false
	}
	return request, true
}

func (s *Server) call(ctx context.Context, t *tool, request any) any {
	if t.operatorWrite != nil && t.operatorWrite(request) && !s.cfg.Operator {
		return refusalResult(&core.RefusalError{
			Code:    codeBadFlags,
			Message: "threads resolve requires a server started with --operator",
		})
	}
	if t.wire != nil {
		if err := t.wire(ctx, s.bindings(t), request); err != nil {
			return refusalResult(rootResolutionRefusal(err))
		}
	}
	data, ref := t.invoke(ctx, request)
	if ref != nil {
		return refusalResult(ref)
	}
	return successResult(t.name, data)
}

func (s *Server) writeHiddenToolError(id json.RawMessage, name string) {
	hidden, known := s.hidden[name]
	if !known {
		s.writeError(id, codeInvalidParams, "unknown tool: "+name)
		return
	}
	if s.cfg.ReadOnly {
		s.writeResult(id, refusalResult(&core.RefusalError{
			Code:    "read-only-tool",
			Message: "tool \"" + name + "\" writes local state; it requires " + hidden.requires,
		}))
		return
	}
	s.writeErrorData(id, codeInvalidParams,
		"tool \""+name+"\" is hidden: it requires "+hidden.requires,
		hiddenToolContent{Tool: name, Hidden: true, Requires: hidden.requires})
}

func (s *Server) bindings(t *tool) *bindings {
	return &bindings{
		env:       s.cfg.Env,
		now:       s.cfg.Now,
		progress:  stderrProgress{w: s.cfg.Stderr},
		root:      s.cfg.Root,
		effective: s.cfg.Effective,
		warnings:  s.cfg.Warnings,
		environ:   s.cfg.Environ,
		stderr:    s.cfg.Stderr,
		commander: t.deps.commander,
	}
}

func (s *Server) handleCancelled(req *rpcRequest) {
	params, ok := decodeParams[cancelParams](req.Params)
	if !ok || len(params.RequestID) == 0 {
		return
	}
	s.callMu.Lock()
	cancel := s.calls[string(params.RequestID)]
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

func (s *Server) listTools() toolsListResult {
	out := make([]toolDescriptor, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, toolDescriptor{
			Name:        t.name,
			Description: t.description,
			InputSchema: inputSchema(t),
			Annotations: toolAnnotations{
				Title:           t.title,
				ReadOnlyHint:    t.readOnly,
				DestructiveHint: t.destructive,
				IdempotentHint:  t.idempotent,
				OpenWorldHint:   t.openWorld,
			},
		})
	}
	return toolsListResult{Tools: out}
}

// diagWriter serializes one session's diagnostic stream. Every concurrent
// tool call shares stderr through this writer, so each progress, tool and log
// write takes the same lock and no line interleaves. Stdout frames keep their
// own path and ordering.
type diagWriter struct {
	dst io.Writer
	mu  sync.Mutex
}

func (w *diagWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.dst.Write(p)
	if err != nil {
		// Diagnostics are best effort: no progress or log caller can act on a
		// failed stderr write, so the error stops here rather than failing the
		// tool call whose result is already being produced.
		return n, nil
	}
	return n, nil
}

type stderrProgress struct {
	w io.Writer
}

func (p stderrProgress) Human(line string) { fmt.Fprintln(p.w, line) }

func (p stderrProgress) Warn(msg string) { fmt.Fprintf(p.w, "warn: %s\n", msg) }

func (p stderrProgress) Diag(line string) { fmt.Fprintln(p.w, line) }

func negotiate(requested string) string {
	if slices.Contains(supportedProtocols, requested) {
		return requested
	}
	return latestProtocol
}

func decodeParams[T any](raw json.RawMessage) (T, bool) {
	var params T
	if len(raw) == 0 || string(raw) == jsonNull {
		return params, true
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return params, false
	}
	return params, true
}

func parseCallParams(raw json.RawMessage) (callParams, error) {
	var params callParams
	if len(raw) == 0 || string(raw) == jsonNull {
		return params, paramErrorf("missing tools/call params")
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return params, paramErrorf("invalid tools/call params: %v", err)
	}
	if params.Name == "" {
		return params, paramErrorf("missing tool name")
	}
	return params, nil
}

func successResult(tool string, data *jq.Obj) callToolResult {
	raw := rawMarshal(data)
	if len(raw) <= maxResultTextBytes {
		return callToolResult{
			Content:           []contentBlock{{Type: contentTypeText, Text: string(raw)}},
			StructuredContent: raw,
			IsError:           false,
		}
	}
	return callToolResult{
		Content:           []contentBlock{{Type: contentTypeText, Text: truncationSummary(tool, data, len(raw))}},
		StructuredContent: raw,
		IsError:           false,
	}
}

func truncationSummary(tool string, data *jq.Obj, size int) string {
	pairs := []any{
		"truncated", true,
		"tool", tool,
		"bytes", size,
		"limit", maxResultTextBytes,
	}
	artifact, _ := data.Get("dir")
	path, ok := artifact.(string)
	if ok && path != "" {
		pairs = append(pairs, "artifact", path, "next",
			"structuredContent carries the full result; read it from the bundle at "+path+
				" on disk, or narrow the call (for example report --check/--status or "+
				"show --sig) for a smaller text block")
	} else {
		pairs = append(pairs, "next", "structuredContent carries the full result; "+
			"narrow the call to get a smaller text block")
	}
	return jq.NewObj(pairs...).MarshalCompact()
}

// rootResolutionRefusal reports a failed repository-root resolution as a
// served refusal: a tool call must not receive a request anchored at the
// working directory when the probe could not verify the root.
func rootResolutionRefusal(err error) *core.RefusalError {
	return &core.RefusalError{Code: "not-a-repo", Message: err.Error()}
}

func refusalResult(ref *core.RefusalError) callToolResult {
	hint := core.HintFor(ref.Code)
	// code, hint, and retry_at are joined beside one warn: line per warning.
	lines := make([]string, 0, refusalTextLines+len(ref.Warnings))
	lines = append(lines, ref.Code+": "+ref.Message, hint)
	for _, w := range ref.Warnings {
		lines = append(lines, "warn: "+w)
	}
	if ref.RetryAt != "" {
		lines = append(lines, "retry_at: "+ref.RetryAt)
	}
	text := strings.Join(lines, lineBreak)
	structured, err := json.Marshal(refusalContent{
		Code: ref.Code, Message: ref.Message, Hint: hint, RetryAt: ref.RetryAt,
		Warnings: ref.Warnings,
		Problems: ref.Problems, ProblemsTruncated: ref.ProblemsTruncated,
	})
	if err != nil {
		structured = []byte("{}")
	}
	return callToolResult{
		Content:           []contentBlock{{Type: contentTypeText, Text: text}},
		StructuredContent: structured,
		IsError:           true,
	}
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

type hiddenToolContent struct { //nolint:govet // field order mirrors the wire frame
	Tool     string `json:"tool"`
	Hidden   bool   `json:"hidden"`
	Requires string `json:"requires"`
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

// cancelParams is the notifications/cancelled params subset.
//
//nolint:misspell // MCP spells the method "cancelled"
type cancelParams struct {
	RequestID json.RawMessage `json:"requestId"`
}

type toolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema map[string]any  `json:"inputSchema"`
	Annotations toolAnnotations `json:"annotations"`
}

type toolAnnotations struct {
	Title           string `json:"title"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	IdempotentHint  bool   `json:"idempotentHint"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

type toolsListResult struct {
	Tools []toolDescriptor `json:"tools"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callToolResult struct {
	Content           []contentBlock  `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError"`
}

// refusalContent is the structuredContent every tool's refusal carries. It is
// additive by contract: a field is dropped with omitempty rather than omitted,
// and warnings ride along so the refusal does not lose what the verb already
// warned about before it stopped.
type refusalContent struct {
	Code              string         `json:"code"`
	Message           string         `json:"message"`
	Hint              string         `json:"hint"`
	RetryAt           string         `json:"retry_at,omitempty"`
	Warnings          []string       `json:"warnings,omitempty"`
	Problems          []core.Problem `json:"problems,omitempty"`
	ProblemsTruncated int            `json:"problems_truncated,omitempty"`
}

type initializeResult struct { //nolint:govet // field order mirrors the wire frame
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      serverInfo     `json:"serverInfo"`
	Instructions    string         `json:"instructions"`
}

// serverInfo carries the experimental marker required until 1.0.
type serverInfo struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Experimental bool   `json:"experimental"`
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
// remembered and returned from Serve. HTML escaping stays off so the text
// block and structuredContent keep the same bytes (post bodies carry `>` and
// markdown; encoding/json would otherwise escape it in one place only).
func (s *Server) writeFrame(resp rpcResponse) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(resp); err != nil {
		s.logf("marshal response: %v", err)
		return
	}
	raw := bytes.TrimSuffix(buf.Bytes(), []byte(lineBreak))
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
	fmt.Fprintf(s.cfg.Stderr, "mcp: "+format+"\n", args...)
}
