package lsp

import (
	"context"
	"encoding/json"
)

// positionEncodingUTF16 is LSP's mandatory default: every conforming client
// speaks it, so the server never negotiates another.
const positionEncodingUTF16 = "utf-16"

// capabilities advertises exactly what this build implements.
func capabilities() serverCapabilities {
	return serverCapabilities{
		PositionEncoding: positionEncodingUTF16,
		TextDocumentSync: textDocumentSync{
			OpenClose: true,
			Change:    textDocumentSyncFull,
			Save:      saveOptions{IncludeText: true},
		},
		ExecuteCommandProvider: executeCommandOptions{Commands: []string{CommandRefresh}},
	}
}

func (s *Server) handleInitialize(msg *rpcMessage, initialized, shutdown bool) {
	if len(msg.ID) == 0 {
		return
	}
	if initialized || shutdown {
		s.writeError(msg.ID, rpcInvalidRequest, "initialize was already received", nil)
		return
	}
	var params initializeParams
	if len(msg.Params) > 0 && string(msg.Params) != jsonNull {
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			s.writeError(msg.ID, rpcInvalidParams, "invalid initialize params: "+err.Error(), nil)
			return
		}
	}
	opts := decodeInitOptions(params.InitializationOptions)
	s.stateMu.Lock()
	s.initOpts = opts
	s.stateMu.Unlock()
	s.callMu.Lock()
	s.initialized = true
	s.callMu.Unlock()
	s.writeResult(msg.ID, initializeResult{
		Capabilities: capabilities(),
		ServerInfo:   serverInfo{Name: serverName, Version: s.cfg.Version},
	})
}

// decodeInitOptions reads the optional session preferences leniently: an
// unreadable value or an unknown key never fails initialize, and a key of the
// wrong type is ignored on its own.
func decodeInitOptions(raw json.RawMessage) initOptions {
	var wire map[string]json.RawMessage
	var opts initOptions
	if json.Unmarshal(raw, &wire) != nil {
		return opts
	}
	decodeOption(wire, "bundle", &opts.Bundle)
	decodeOption(wire, "showDeferred", &opts.ShowDeferred)
	return opts
}

// decodeOption sets *into from one key when the value has the right type.
func decodeOption[T any](wire map[string]json.RawMessage, key string, into *T) {
	var v T
	if raw, ok := wire[key]; ok && json.Unmarshal(raw, &v) == nil {
		*into = v
	}
}

// handleInitialized composes every root, publishes every path that carries
// a finding, and starts the watch pump.
func (s *Server) handleInitialized(ctx context.Context) {
	s.stateMu.Lock()
	for i := range s.roots {
		root := &s.roots[i]
		s.compose(ctx, root, true)
		s.republishRoot(root)
	}
	s.stateMu.Unlock()
	s.callMu.Lock()
	defer s.callMu.Unlock()
	if !s.shutdown {
		s.startPump(ctx)
	}
}

// handleShutdown refuses new requests, lets every in-flight call answer,
// stops the pump, then answers null. Every later request answers
// InvalidRequest; only `exit` is honored.
func (s *Server) handleShutdown(msg *rpcMessage) {
	s.stop(false)
	s.writeResult(msg.ID, nil)
}

// exitResult is the session's result on `exit`: nil after shutdown, the LSP
// contract's abnormal exit otherwise.
func (s *Server) exitResult() error {
	s.callMu.Lock()
	defer s.callMu.Unlock()
	if !s.shutdown {
		return ErrExitWithoutShutdown
	}
	return nil
}
