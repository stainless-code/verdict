package mcp

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	skillfs "github.com/stainless-code/verdict"
)

const (
	methodPromptsList = "prompts/list"
	methodPromptsGet  = "prompts/get"
)

// promptDescriptions is the host-facing copy for the tasks in skillfs.Tasks.
var promptDescriptions = map[string]string{
	"address-reviews":    "Address review threads with verified fixes and one reply per thread.",
	"fix-ci":             "Diagnose a PR-introduced CI failure, fix it, and push the fix with a summary.",
	"incremental-review": "Review only the commits added since a prior review, with a no-review note for trivial deltas.",
	"review":             "Review a fetched PR or local diff and write one review.json.",
	"validate":           "Validate candidate findings against the bundle without changing it.",
}

func (s *Server) handlePromptsList(req *rpcRequest) {
	if len(req.ID) == 0 {
		return
	}
	prompts := make([]promptDescriptor, 0, len(promptDescriptions))
	for _, task := range skillfs.Tasks() {
		prompts = append(prompts, promptDescriptor{
			Name:        task,
			Description: promptDescriptions[task],
		})
	}
	s.writeResult(req.ID, promptsListResult{Prompts: prompts})
}

func (s *Server) handlePromptsGet(req *rpcRequest) {
	if len(req.ID) == 0 {
		return
	}
	params, ok := decodeParams[promptNameParams](req.Params)
	if !ok || params.Name == "" {
		s.writeInvalidPrompt(req.ID, "")
		return
	}
	body, err := skillfs.TaskProcedure(params.Name)
	if err != nil {
		if _, ok := errors.AsType[*skillfs.UnknownTaskError](err); ok {
			s.writeInvalidPrompt(req.ID, params.Name)
			return
		}
		s.writeError(req.ID, codeInternalError, "prompt unavailable: "+err.Error())
		return
	}
	s.writeResult(req.ID, getPromptResult{
		Description: promptDescriptions[params.Name],
		Messages: []promptMessage{{
			Role:    "user",
			Content: contentBlock{Type: contentTypeText, Text: string(body)},
		}},
	})
}

// MCP 2025-06-18 § server/prompts error handling: an invalid prompt name is
// -32602. The message names the served tasks because this server has no
// prompt arguments.
func (s *Server) writeInvalidPrompt(id json.RawMessage, name string) {
	message := "missing prompt name; valid tasks: " + strings.Join(skillfs.Tasks(), ", ")
	if name != "" {
		message = "unknown prompt " + strconv.Quote(name) + "; valid tasks: " + strings.Join(skillfs.Tasks(), ", ")
	}
	s.writeError(id, codeInvalidParams, message)
}

type promptNameParams struct {
	Name string `json:"name"`
}

type promptDescriptor struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type promptsListResult struct {
	Prompts []promptDescriptor `json:"prompts"`
}

type promptMessage struct {
	Role    string       `json:"role"`
	Content contentBlock `json:"content"`
}

type getPromptResult struct {
	Description string          `json:"description"`
	Messages    []promptMessage `json:"messages"`
}
