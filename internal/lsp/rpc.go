package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

const (
	jsonRPCVersion = "2.0"
	serverName     = "verdict"
	jsonNull       = "null"
	readBufBytes   = 64 << 10
)

// readLoop reads frames until EOF, `exit`, a framing failure, or ctx
// cancellation. Notifications run inline, in arrival order, so a publish is
// always emitted before the next frame is read; requests that can block run
// on their own goroutine, cancellable by `$/cancelRequest`.
func (s *Server) readLoop(ctx context.Context, in io.Reader) error {
	r := bufio.NewReaderSize(in, readBufBytes)
	for ctx.Err() == nil {
		body, err := readFrame(r)
		if err != nil {
			return s.readEnded(ctx, err)
		}
		if done, err := s.handleBody(ctx, body); done {
			s.markEnded()
			return err
		}
	}
	return nil
}

// readEnded is the read loop's result when a read failed: none when the
// cancel interrupted it (Serve reports the cancel), else the session ended
// on its own and readFailure maps why.
func (s *Server) readEnded(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.markEnded()
	return s.readFailure(err)
}

// readFailure maps a framing failure: a clean EOF between frames ends the
// session like `exit` after `shutdown`; a malformed header answers one error
// frame and stops, because the stream cannot be resynchronized.
func (s *Server) readFailure(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}
	if fe, ok := errors.AsType[*frameError](err); ok {
		code := rpcParseError
		if errors.Is(fe, errFrameTooLarge) {
			code = rpcInvalidRequest
		}
		s.writeError(nil, code, fe.Error(), nil)
		return &transportError{err: fe}
	}
	return &transportError{err: err}
}

// transportError is a stdin failure Serve returns.
type transportError struct {
	err error
}

func (e *transportError) Error() string { return "read stdin: " + e.err.Error() }

func (e *transportError) Unwrap() error { return e.err }

// handleBody decodes one frame body and dispatches it. done reports that
// the session ends, with err nil for a clean `exit`.
func (s *Server) handleBody(ctx context.Context, body []byte) (bool, error) {
	msg, ok := s.decodeMessage(body)
	if !ok {
		return false, nil
	}
	if msg.Method == methodExit {
		return true, s.exitResult()
	}
	s.dispatch(ctx, msg)
	return false, nil
}

// decodeMessage parses one body into a request or notification, answering
// the JSON-RPC error itself when it cannot.
func (s *Server) decodeMessage(body []byte) (*rpcMessage, bool) {
	msg, code, text := parseMessage(body)
	if code == 0 {
		return msg, msg != nil
	}
	var id json.RawMessage
	if msg != nil {
		id = msg.ID
	}
	s.writeError(id, code, text, nil)
	return nil, false
}

// parseMessage decodes one body. A zero code with a message is valid; a zero
// code without one is a malformed notification, which JSON-RPC answers with
// silence; a non-zero code is the error to answer, to the message's id when
// it carries one.
func parseMessage(body []byte) (*rpcMessage, int, string) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, rpcParseError, "parse error: a frame body must be one JSON object"
	}
	var msg rpcMessage
	if err := json.Unmarshal(trimmed, &msg); err != nil {
		if _, syntax := errors.AsType[*json.SyntaxError](err); syntax {
			return nil, rpcParseError, "parse error"
		}
		return nil, rpcInvalidRequest, "invalid request"
	}
	return validateMessage(&msg)
}

// validateMessage checks the JSON-RPC envelope of one decoded message.
func validateMessage(msg *rpcMessage) (*rpcMessage, int, string) {
	if string(msg.ID) == jsonNull {
		msg.ID = nil
	}
	if msg.JSONRPC == jsonRPCVersion && msg.Method != "" {
		return msg, 0, ""
	}
	if len(msg.ID) == 0 {
		return nil, 0, ""
	}
	return msg, rpcInvalidRequest, "invalid request"
}

// dispatch routes one message through the lifecycle gate.
func (s *Server) dispatch(ctx context.Context, msg *rpcMessage) {
	isRequest := len(msg.ID) > 0
	s.callMu.Lock()
	initialized, shutdown := s.initialized, s.shutdown
	s.callMu.Unlock()
	switch {
	case msg.Method == methodInitialize:
		s.handleInitialize(msg, initialized, shutdown)
	case shutdown && msg.Method == methodCancel:
		// A drain still honors cancellation of the calls it waits for.
		s.handleCancel(msg)
	case shutdown:
		if isRequest {
			s.writeError(msg.ID, rpcInvalidRequest, "server is shutting down", nil)
		}
	case !initialized:
		if isRequest {
			s.writeError(msg.ID, rpcNotInitialized, "server not initialized", nil)
		}
	case isRequest:
		s.dispatchRequest(ctx, msg)
	default:
		s.dispatchNotification(ctx, msg)
	}
}

func (s *Server) dispatchRequest(ctx context.Context, msg *rpcMessage) {
	switch msg.Method {
	case methodShutdown:
		s.handleShutdown(msg)
	case methodExecuteCommand:
		s.goCall(ctx, msg, s.handleExecuteCommand)
	default:
		s.writeError(msg.ID, rpcMethodNotFound, "method not found: "+msg.Method, nil)
	}
}

// dispatchNotification runs one notification inline. Unknown notifications,
// including every `$/` protocol notification but cancel, are ignored.
func (s *Server) dispatchNotification(ctx context.Context, msg *rpcMessage) {
	switch msg.Method {
	case methodInitialized:
		s.handleInitialized(ctx)
	case methodCancel:
		s.handleCancel(msg)
	case methodDidOpen:
		s.handleDidOpen(ctx, msg)
	case methodDidChange:
		s.handleDidChange(msg)
	case methodDidSave:
		s.handleDidSave(ctx, msg)
	case methodDidClose:
		s.handleDidClose(msg)
	case methodDidChangeRoots:
		s.handleDidChangeRoots(ctx, msg)
	default:
		// Unknown notifications are ignored by contract.
	}
}

// goCall runs one request on the bounded dispatcher with a context
// `$/cancelRequest` can cancel, queued or running. A request past both the
// in-flight and queue bounds answers ServerCancelled at once.
func (s *Server) goCall(ctx context.Context, msg *rpcMessage, fn func(context.Context, *rpcMessage)) {
	callCtx, cancel := context.WithCancel(ctx)
	key := string(msg.ID)
	s.callMu.Lock()
	defer s.callMu.Unlock()
	if s.shutdown {
		cancel()
		s.writeError(msg.ID, rpcInvalidRequest, "server is shutting down", nil)
		return
	}
	s.inflight.Add(1)
	accepted := s.queue.submit(func() {
		defer s.inflight.Done()
		defer func() {
			s.callMu.Lock()
			delete(s.calls, key)
			s.callMu.Unlock()
		}()
		defer cancel()
		if callCtx.Err() != nil {
			s.writeError(msg.ID, rpcRequestCancelled, "request canceled", nil)
			return
		}
		fn(callCtx, msg)
	})
	if !accepted {
		s.inflight.Done()
		cancel()
		s.writeError(msg.ID, rpcServerCancelled, fmt.Sprintf("server busy: %d requests running and %d queued",
			s.lim.inflight, s.lim.queued), nil)
		return
	}
	s.calls[key] = cancel
}

func (s *Server) handleCancel(msg *rpcMessage) {
	var params cancelParams
	if err := json.Unmarshal(msg.Params, &params); err != nil || len(params.ID) == 0 {
		return
	}
	s.callMu.Lock()
	cancel := s.calls[string(params.ID)]
	s.callMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Server) writeResult(id json.RawMessage, result any) {
	s.writeMessage(rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Result: result})
}

func (s *Server) writeError(id json.RawMessage, code int, message string, data any) {
	if len(id) == 0 {
		id = json.RawMessage(jsonNull)
	}
	s.writeMessage(rpcErrorResponse{JSONRPC: jsonRPCVersion, ID: id, Error: &rpcError{Code: code, Message: message, Data: data}})
}

func (s *Server) notify(method string, params any) {
	s.writeMessage(rpcNotification{JSONRPC: jsonRPCVersion, Method: method, Params: params})
}

// writeMessage frames one message under the write lock. A failed write is
// remembered and returned from Serve. HTML escaping stays off so finding text
// keeps the same bytes on every surface.
func (s *Server) writeMessage(v any) {
	body, err := marshalMessage(v)
	if err != nil {
		fmt.Fprintf(s.cfg.Stderr, "lsp: marshal message: %v\n", err)
		return
	}
	s.writeFrame(body)
}

// marshalMessage renders one message body.
func marshalMessage(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, &ioError{op: "marshal message", err: err}
	}
	return bytes.TrimSuffix(jq.EscapeControls(buf.Bytes()), []byte("\n")), nil
}

// marshalNotification renders one notification body.
func marshalNotification(method string, params any) ([]byte, error) {
	return marshalMessage(rpcNotification{JSONRPC: jsonRPCVersion, Method: method, Params: params})
}

// writeFrame frames and writes one rendered body under the write lock.
func (s *Server) writeFrame(body []byte) {
	frame := encodeFrame(body)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed || s.out == nil {
		return
	}
	if _, err := s.out.Write(frame); err != nil && s.writeErr == nil {
		s.writeErr = fmt.Errorf("write stdout: %w", err)
	}
}

// logf writes one line to stderr and mirrors it to the client's log.
func (s *Server) logf(kind int, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	fmt.Fprintln(s.cfg.Stderr, "lsp: "+strings.TrimPrefix(text, "verdict: "))
	s.notify(methodLogMessage, logMessageParams{Type: kind, Message: text})
}

// queueLogf holds a log line until flushLogs: during initialize nothing may
// precede the initialize answer. It takes only logMu, so root validation can
// queue lines without holding stateMu.
func (s *Server) queueLogf(kind int, format string, args ...any) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.pendLogs = append(s.pendLogs, logMessageParams{Type: kind, Message: fmt.Sprintf(format, args...)})
}

// flushLogs emits the queued log lines in order.
func (s *Server) flushLogs() {
	s.logMu.Lock()
	pending := s.pendLogs
	s.pendLogs = nil
	s.logMu.Unlock()
	for _, l := range pending {
		s.logf(l.Type, "%s", l.Message)
	}
}
