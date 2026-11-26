package acp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
)

// Method names the transport speaks. The query methods are ACP extension
// methods namespaced under verdict so they cannot collide with the protocol
// surface an editor already names.
const (
	methodInitialize        = "initialize"
	methodPing              = "ping"
	methodInitializedNotice = "notifications/initialized"
	methodCancelACP         = "$/cancel_request"
	methodCancelMCP         = "notifications/cancelled" //nolint:misspell // MCP spells the method "cancelled"
	methodBundlesList       = "verdict/bundles/list"
	methodFindingsList      = "verdict/findings/list"
	methodFindingsGet       = "verdict/findings/get"
	methodFindingsCopy      = "verdict/findings/copy"
	jsonNull                = "null"
)

// negotiationVersion answers one initialize request: only protocol 1 is
// spoken, so any requested integer answers 1 (the latest the agent supports).
func negotiationVersion(_ int) int { return latestProtocol }

func (s *Server) handleInitialize(req *rpcRequest) {
	if len(req.ID) == 0 {
		return
	}
	params, ok := decodeInitialize(req.Params)
	if !ok {
		s.writeError(req.ID, codeBadParams, "invalid initialize params: protocolVersion is required")
		return
	}
	s.writeResult(req.ID, initializeResult{
		ProtocolVersion: negotiationVersion(params.ProtocolVersion),
		Capabilities:    s.capabilities(),
		AgentInfo:       serverInfo{Name: serverName, Version: s.cfg.Version, Title: serverName},
		ServerInfo:      serverInfo{Name: serverName, Version: s.cfg.Version},
		AuthMethods:     []any{},
	})
}

// capabilities advertises only reads backed by the query layer: no session
// resume, no prompt turn, no filesystem or terminal tools, no MCP servers.
func (s *Server) capabilities() map[string]any {
	return map[string]any{
		"loadSession": false,
		"promptCapabilities": map[string]any{
			"image":           false,
			"audio":           false,
			"embeddedContext": false,
		},
		"mcpCapabilities": map[string]any{"http": false, "sse": false},
	}
}

type initializeParams struct {
	ProtocolVersion int `json:"protocolVersion"`
}

func decodeInitialize(raw json.RawMessage) (initializeParams, bool) {
	var params initializeParams
	if len(raw) == 0 || string(raw) == jsonNull {
		return params, false
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		return params, false
	}
	versionRaw, ok := wire["protocolVersion"]
	if !ok || len(versionRaw) == 0 || string(versionRaw) == jsonNull {
		return params, false
	}
	var version int
	if err := json.Unmarshal(versionRaw, &version); err != nil {
		return params, false
	}
	if version <= 0 {
		return params, false
	}
	params.ProtocolVersion = version
	return params, true
}

// decodeCancel reads one ACP/MCP cancellation params object and returns the
// request id it cancels. An absent id cancels nothing.
func decodeCancel(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == jsonNull {
		return "", false
	}
	var params struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || len(params.RequestID) == 0 {
		return "", false
	}
	return string(params.RequestID), true
}

func (s *Server) runQuery(ctx context.Context, req *rpcRequest) {
	switch req.Method {
	case methodBundlesList:
		s.handleBundles(ctx, req)
	case methodFindingsList:
		s.handleFindingsList(ctx, req)
	case methodFindingsGet:
		s.handleFindingsGet(ctx, req)
	case methodFindingsCopy:
		s.handleFindingsCopy(ctx, req)
	}
}

func (s *Server) handleBundles(ctx context.Context, req *rpcRequest) {
	pattern, ok := decodePattern(req.Params)
	if !ok {
		s.writeError(req.ID, codeBadParams, "invalid params: verdict/bundles/list accepts an optional string pattern")
		return
	}
	view, err := s.layer.Bundles(ctx, pattern)
	if err != nil {
		s.writeLayerError(req.ID, err)
		return
	}
	s.writeResult(req.ID, viewResult(view))
}

func (s *Server) handleFindingsList(ctx context.Context, req *rpcRequest) {
	bundle, ok := decodeBundle(req.Params)
	if !ok {
		s.writeError(req.ID, codeBadParams, "invalid params: verdict/findings/list requires a string bundle")
		return
	}
	view, err := s.layer.Detail(ctx, s.rootID, bundle)
	if err != nil {
		s.writeLayerError(req.ID, err)
		return
	}
	s.writeResult(req.ID, viewResult(view))
}

func (s *Server) handleFindingsGet(ctx context.Context, req *rpcRequest) {
	bundle, sig, ok := decodeBundleSig(req.Params)
	if !ok {
		s.writeError(req.ID, codeBadParams, "invalid params: verdict/findings/get requires string bundle and sig")
		return
	}
	view, err := s.layer.Show(ctx, s.rootID, bundle, sig)
	if err != nil {
		s.writeLayerError(req.ID, err)
		return
	}
	s.writeResult(req.ID, viewResult(view))
}

func (s *Server) handleFindingsCopy(ctx context.Context, req *rpcRequest) {
	bundle, sig, ok := decodeBundleSig(req.Params)
	if !ok {
		s.writeError(req.ID, codeBadParams, "invalid params: verdict/findings/copy requires string bundle and sig")
		return
	}
	view, err := s.layer.Copy(ctx, s.rootID, bundle, sig)
	if err != nil {
		s.writeLayerError(req.ID, err)
		return
	}
	s.writeResult(req.ID, viewResult(view))
}

// viewResult merges one composed view into the ACP result: the view's data
// fields verbatim, plus the envelope clock, repo, pr, next step, and warnings
// the transport renders. The result is pre-marshaled: a *jq.Obj marshals as
// its Fields/Keys struct, never as the object itself.
func viewResult(view *query.View) json.RawMessage {
	result := view.Data.Clone()
	result.Set("at", view.At)
	result.Set("repo", view.Repo)
	result.Set("pr", view.Pr)
	result.Set("next", view.Next)
	result.Set("warnings", core.StringValues(view.Warnings))
	return json.RawMessage(result.MarshalCompact())
}

// writeLayerError maps one layer failure onto a JSON-RPC error: a typed
// refusal and a missing root or bundle are invalid params (the client named
// something outside the served root), anything else is internal. The refusal
// code in data is the stable contract.
func (s *Server) writeLayerError(id json.RawMessage, err error) {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		s.writeErrorData(id, codeBadParams, ref.Code+": "+ref.Message, refusalData(ref))
		return
	}
	if errors.Is(err, query.ErrNotFound) {
		s.writeErrorData(id, codeBadParams, "not-found: no such bundle in this served root",
			map[string]any{"code": "not-found", "message": "no such bundle in this served root"})
		return
	}
	s.logf("query: %v", err)
	s.writeError(id, codeInternal, "internal error")
}

type refusalPayload struct {
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Hint     string   `json:"hint"`
	RetryAt  string   `json:"retry_at,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

func refusalData(ref *core.RefusalError) refusalPayload {
	return refusalPayload{
		Code:     ref.Code,
		Message:  ref.Message,
		Hint:     core.RefusalHint(ref),
		RetryAt:  ref.RetryAt,
		Warnings: ref.Warnings,
	}
}

// decodePattern reads the bundles/list params: an absent pattern lists every
// bundle; a non-string pattern is invalid.
func decodePattern(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == jsonNull {
		return "", true
	}
	var params struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return "", false
	}
	return params.Pattern, true
}

// decodeBundle reads the findings/list params: bundle is required.
func decodeBundle(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == jsonNull {
		return "", false
	}
	var params struct {
		Bundle string `json:"bundle"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || params.Bundle == "" {
		return "", false
	}
	return params.Bundle, true
}

// decodeBundleSig reads the findings/get and findings/copy params: bundle and
// sig are both required strings.
func decodeBundleSig(raw json.RawMessage) (bundle, sig string, ok bool) {
	if len(raw) == 0 || string(raw) == jsonNull {
		return "", "", false
	}
	var params struct {
		Bundle string `json:"bundle"`
		Sig    string `json:"sig"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || params.Bundle == "" || params.Sig == "" {
		return "", "", false
	}
	return params.Bundle, params.Sig, true
}

type initializeResult struct { //nolint:govet // field order mirrors the wire frame
	ProtocolVersion int            `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	AgentInfo       serverInfo     `json:"agentInfo"`
	ServerInfo      serverInfo     `json:"serverInfo"`
	AuthMethods     []any          `json:"authMethods"`
}

// serverInfo carries the agent identity the initialize response publishes,
// with both the ACP (`agentInfo`) and MCP-adjacent (`serverInfo`) spellings.
type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Title   string `json:"title,omitempty"`
}
