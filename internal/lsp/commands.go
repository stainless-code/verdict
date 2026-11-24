package lsp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// codeBadParams is the registered refusal for executeCommand params this
// server cannot act on: an unknown command or malformed arguments.
const codeBadParams = "bad-params"

// handleExecuteCommand runs one advertised command. Only verdict.refresh
// exists in this build; anything else refuses with the advertised set.
func (s *Server) handleExecuteCommand(ctx context.Context, msg *rpcMessage) {
	var params executeCommandParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		s.refuseParams(msg.ID, "workspace/executeCommand params are not an object: "+err.Error())
		return
	}
	switch params.Command {
	case CommandRefresh:
		s.handleRefresh(ctx, msg)
	default:
		s.refuseParams(msg.ID, "unknown command "+quote(params.Command)+
			"; this server advertises "+strings.Join(capabilities().ExecuteCommandProvider.Commands, ", "))
	}
}

// handleRefresh drops every cached view, reselects and recomposes every
// root, and republishes. It is the escape hatch for a filesystem whose
// mtimes the watcher cannot see.
func (s *Server) handleRefresh(ctx context.Context, msg *rpcMessage) {
	s.layer.Invalidate()
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	result := refreshResult{At: core.NowUTC(s.cfg.Now()), Roots: []refreshRow{}}
	for i := range s.roots {
		root := &s.roots[i]
		s.compose(ctx, root, true)
		if ctx.Err() != nil {
			s.writeError(msg.ID, rpcRequestCancelled, "request canceled", nil)
			return
		}
		published := s.republishRoot(root)
		state := s.states[root.ID]
		result.Roots = append(result.Roots, refreshRow{
			Root: root.ID, Bundle: state.bundle, Findings: len(state.items), Published: published,
		})
	}
	s.writeResult(msg.ID, result)
}

// refuseParams answers InvalidParams with the registered refusal code and
// its hint.
func (s *Server) refuseParams(id json.RawMessage, message string) {
	s.writeError(id, rpcInvalidParams, codeBadParams+": "+message, errorData{
		Code:    codeBadParams,
		Message: message,
		Hint:    core.HintFor(codeBadParams),
	})
}

func quote(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(raw)
}
