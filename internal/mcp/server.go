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
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
)

// Protocol constants: the JSON-RPC version, the MCP revisions this server
// negotiates, the transport's frame cap, and the JSON-RPC error codes the
// subset uses.
const (
	jsonRPCVersion      = "2.0"
	latestProtocol      = "2025-06-18"
	maxFrameBytes       = 16 << 20
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
	lineBreak           = "\n"
	serverName          = "verdict"
	scannerInitialBytes = 64 * 1024
)

// supportedProtocols are the MCP revisions the server can speak.
var supportedProtocols = []string{"2024-11-05", "2025-03-26", latestProtocol}

// Config is the transport's injected environment: the version string, the
// prefs/core runtime the CLI resolved, the production command runners, and
// the stderr stream every progress line uses (stdout stays protocol-only).
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

	out      io.Writer
	writeMu  sync.Mutex
	writeErr error

	inflight sync.WaitGroup
	callMu   sync.Mutex
	calls    map[string]context.CancelFunc
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
	tools := allTools(cfg.ReadOnly, cfg.Operator)
	byName := make(map[string]*tool, len(tools))
	for _, t := range tools {
		byName[t.name] = t
	}
	return &Server{cfg: *cfg, tools: tools, byName: byName, calls: map[string]context.CancelFunc{}}
}

// Serve runs the newline-delimited JSON-RPC loop until stdin closes, then
// cancels in-flight calls, waits for them, and returns. Stdout carries only
// protocol frames.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.out = out
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, scannerInitialBytes), maxFrameBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		s.handleLine(ctx, line)
	}
	readErr := scanner.Err()
	// stdin closing ends the session, but let already-dispatched calls finish:
	// a host may send one tool call and close its pipe. Only then cancel.
	s.inflight.Wait()
	cancel()
	s.cancelAll()
	s.writeMu.Lock()
	writeErr := s.writeErr
	s.writeMu.Unlock()
	if readErr != nil {
		return &transportError{err: readErr}
	}
	return writeErr
}

// transportError seals a stdin read failure at the transport boundary.
type transportError struct {
	err error
}

// Error implements error.
func (e *transportError) Error() string { return "read stdin: " + e.err.Error() }

// Unwrap exposes the cause to [errors.Is].
func (e *transportError) Unwrap() error { return e.err }

// handleLine parses and dispatches one frame. Parse errors get a JSON-RPC
// error with a null id; notification-shaped garbage gets no response.
func (s *Server) handleLine(ctx context.Context, line []byte) {
	if !json.Valid(line) {
		s.writeError(nil, codeParse, "parse error")
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
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

// dispatch routes one request or notification. Methods outside the subset
// are method-not-found; notifications never receive a response.
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

// handleInitialize negotiates the MCP revision and advertises tools only.
func (s *Server) handleInitialize(req *rpcRequest) {
	if len(req.ID) == 0 {
		return
	}
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(req.Params) > 0 && string(req.Params) != jsonNull {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.writeError(req.ID, codeInvalidParams, "invalid initialize params")
			return
		}
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

// instructions is the one doctrine block every host sees before its first
// tool call: the review loop, the single terminal write, and the operator
// boundary that the tool list alone cannot state.
func (s *Server) instructions() string {
	lines := []string{
		"Review one pull request at a time: fetch, read .review/<pr>, write exactly one review.json, then post.",
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

// handleToolsCall validates the call envelope, then runs the tool on its
// own goroutine so the cancel notification can arrive while it works.
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
		s.writeError(req.ID, codeInvalidParams, "unknown tool: "+params.Name)
		return
	}
	callCtx, cancel := context.WithCancel(ctx)
	key := string(req.ID)
	s.callMu.Lock()
	s.calls[key] = cancel
	s.callMu.Unlock()
	s.inflight.Go(func() {
		defer func() {
			s.callMu.Lock()
			delete(s.calls, key)
			s.callMu.Unlock()
		}()
		defer cancel()
		result, rpcErr := s.call(callCtx, t, params.Arguments)
		if rpcErr != nil {
			s.writeError(req.ID, rpcErr.Code, rpcErr.Message)
			return
		}
		s.writeResult(req.ID, result)
	})
}

// call decodes arguments, injects the runtime, and translates the core
// refusal/success contract into a tool result.
func (s *Server) call(ctx context.Context, t *tool, raw json.RawMessage) (any, *rpcError) {
	req, err := decodeArgs(t, raw)
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	s.prepare(req, t)
	data, ref := t.invoke(ctx, req)
	if ref != nil {
		return refusalResult(ref), nil
	}
	return successResult(data), nil
}

// handleCancelled maps the cancel notification onto the in-flight call's
// context. Unknown ids are ignored.
func (s *Server) handleCancelled(req *rpcRequest) {
	var params struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if len(req.Params) == 0 || string(req.Params) == jsonNull {
		return
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params.RequestID) == 0 {
		return
	}
	s.callMu.Lock()
	cancel := s.calls[string(params.RequestID)]
	s.callMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// cancelAll cancels every in-flight call at stdin close.
func (s *Server) cancelAll() {
	s.callMu.Lock()
	defer s.callMu.Unlock()
	for _, cancel := range s.calls {
		cancel()
	}
}

// listTools renders the policy-filtered tool list with reflected schemas.
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

// prepare injects the session runtime, effective prefs, warnings, and
// adapter streams into a decoded request, using the shared request shape
// rather than a per-tool setter.
func (s *Server) prepare(raw any, t *tool) {
	rv := reflect.ValueOf(raw).Elem()
	rt := &core.Runtime{
		Env:       s.cfg.Env,
		Now:       s.cfg.Now,
		Progress:  stderrProgress{w: s.cfg.Stderr},
		Commander: s.commander(t),
	}
	root := s.cfg.Root
	for i := range rv.NumField() {
		sf := rv.Type().Field(i)
		fv := rv.Field(i)
		switch {
		case sf.Anonymous && sf.Type == readRequestType:
			read, ok := reflect.TypeAssert[*core.ReadRequest](fv.Addr())
			if !ok {
				continue
			}
			read.Runtime = rt
			read.Forces = &core.GlobalForces{}
			read.Root = &root
		case sf.Type == effectiveType:
			fv.Set(reflect.ValueOf(s.cfg.Effective))
		case sf.Name == "Warnings":
			fv.Set(reflect.ValueOf(append([]string{}, s.cfg.Warnings...)))
		case sf.Name == "Human", sf.Name == "Diag":
			fv.Set(reflect.ValueOf(s.cfg.Stderr))
		case sf.Name == "Environ":
			fv.Set(reflect.ValueOf(append([]string{}, s.cfg.Environ...)))
		case sf.Name == "JSONMode":
			fv.SetBool(true)
		}
	}
	derivePresence(rv)
}

// derivePresence fills the adapter-owned presence bits the CLI sets while
// parsing flags (HasBase/HasProvider/HasStash) from the decoded values.
func derivePresence(rv reflect.Value) {
	flags := rv.FieldByName("Flags")
	if !flags.IsValid() || flags.Kind() != reflect.Struct {
		return
	}
	pair := func(present, value string) {
		pf := flags.FieldByName(present)
		vf := flags.FieldByName(value)
		if pf.IsValid() && vf.IsValid() && vf.Kind() == reflect.String {
			pf.SetBool(vf.String() != "")
		}
	}
	pair("HasBase", "BaseSHA")
	pair("HasProvider", "Provider")
	pair("HasStash", "StashN")
}

// commander returns the production runner the verb's CLI adapter installs,
// or a test/embed override from Config.
func (s *Server) commander(t *tool) core.Commander {
	if c, ok := s.cfg.Commanders[t.name]; ok {
		return c
	}
	switch t.name {
	case "fetch", "fetch-local", "fix", "init", "post":
		return coreexec.SplitCommander{}
	case "verify":
		return coreexec.OSCommander{}
	case "ready", "doctor", "check-tree", "calibrate":
		return coreexec.SpawnCommander{}
	default:
		return nil
	}
}

// stderrProgress routes core's human/warn/diag observations to stderr; a
// protocol frame never carries them.
type stderrProgress struct {
	w io.Writer
}

// Human writes one human line.
func (p stderrProgress) Human(line string) { fmt.Fprintln(p.w, line) }

// Warn writes one warning line.
func (p stderrProgress) Warn(msg string) { fmt.Fprintf(p.w, "warn: %s\n", msg) }

// Diag writes one raw diagnostic line.
func (p stderrProgress) Diag(line string) { fmt.Fprintln(p.w, line) }

// negotiate picks the client's revision when supported, else the latest.
func negotiate(requested string) string {
	if slices.Contains(supportedProtocols, requested) {
		return requested
	}
	return latestProtocol
}

// parseCallParams validates the tools/call params object.
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

// successResult builds the MCP tool result: the CLI envelope's data as one
// compact text block plus structuredContent.
func successResult(data *jq.Obj) callToolResult {
	raw := rawMarshal(data)
	return callToolResult{
		Content:           []contentBlock{{Type: contentTypeText, Text: string(raw)}},
		StructuredContent: raw,
		IsError:           false,
	}
}

// refusalResult builds a typed refusal result: isError true, text
// `code: message\nhint`, and the same fields as structured content.
func refusalResult(ref *core.RefusalError) callToolResult {
	hint := core.HintFor(ref.Code)
	text := ref.Code + ": " + ref.Message + lineBreak + hint
	structured, err := json.Marshal(refusalContent{Code: ref.Code, Message: ref.Message, Hint: hint})
	if err != nil {
		structured = []byte("{}")
	}
	return callToolResult{
		Content:           []contentBlock{{Type: contentTypeText, Text: text}},
		StructuredContent: structured,
		IsError:           true,
	}
}

// rpcRequest is one JSON-RPC request or notification frame.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// rpcResponse is one JSON-RPC result or error frame.
type rpcResponse struct { //nolint:govet // field order mirrors the wire frame
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is the JSON-RPC error object.
type rpcError struct { //nolint:govet // field order mirrors the wire frame
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// callParams is the tools/call params subset.
type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// toolDescriptor is one tools/list entry.
type toolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema map[string]any  `json:"inputSchema"`
	Annotations toolAnnotations `json:"annotations"`
}

// toolAnnotations is the MCP hint block. Every field is always emitted, so a
// host never has to infer a protocol default.
type toolAnnotations struct {
	Title           string `json:"title"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	IdempotentHint  bool   `json:"idempotentHint"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

// toolsListResult is the tools/list result.
type toolsListResult struct {
	Tools []toolDescriptor `json:"tools"`
}

// contentBlock is one MCP text content block.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// callToolResult is the tools/call result.
type callToolResult struct {
	Content           []contentBlock  `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError"`
}

// refusalContent is the refusal's structured content.
type refusalContent struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

// initializeResult is the initialize result.
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

// writeResult emits a JSON-RPC result frame.
func (s *Server) writeResult(id json.RawMessage, result any) {
	s.writeFrame(rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Result: result})
}

// writeError emits a JSON-RPC error frame.
func (s *Server) writeError(id json.RawMessage, code int, message string) {
	s.writeFrame(rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Error: &rpcError{Code: code, Message: message}})
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
	if _, err := s.out.Write(raw); err != nil {
		if s.writeErr == nil {
			s.writeErr = fmt.Errorf("write stdout: %w", err)
		}
	}
}

// logf writes a diagnostic to stderr, never stdout.
func (s *Server) logf(format string, args ...any) {
	fmt.Fprintf(s.cfg.Stderr, "mcp: "+format+"\n", args...)
}
