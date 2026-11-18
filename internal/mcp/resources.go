package mcp

import (
	"context"
	"encoding/json"
	"path"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core/skill"
)

const (
	methodResourcesList  = "resources/list"
	methodResourcesRead  = "resources/read"
	resourceURIPrefix    = "verdict://skill/"
	codeResourceNotFound = -32002
)

func (s *Server) handleResourcesList(ctx context.Context, req *rpcRequest) {
	if len(req.ID) == 0 {
		return
	}
	inv, ref := skill.List(ctx, &skill.Request{})
	if ref != nil {
		s.writeError(req.ID, codeInternalError, "resource list unavailable: "+ref.Message)
		return
	}
	resources := make([]resourceDescriptor, 0, len(inv.Files))
	for _, name := range inv.Files {
		resources = append(resources, resourceDescriptor{
			URI:      resourceURIPrefix + name,
			Name:     name,
			MIMEType: resourceMIMEType(name),
		})
	}
	s.writeResult(req.ID, resourcesListResult{Resources: resources})
}

func (s *Server) handleResourcesRead(ctx context.Context, req *rpcRequest) {
	if len(req.ID) == 0 {
		return
	}
	params, ok := decodeParams[resourceURIParams](req.Params)
	if !ok {
		s.writeResourceNotFound(req.ID, "")
		return
	}
	name, valid := payloadPath(params.URI)
	if !valid {
		s.writeResourceNotFound(req.ID, params.URI)
		return
	}
	body, ref := skill.ShowFile(ctx, &skill.Request{}, name)
	if ref != nil {
		s.writeResourceNotFound(req.ID, params.URI)
		return
	}
	s.writeResult(req.ID, readResourceResult{Contents: []resourceContent{{
		URI:      params.URI,
		MIMEType: resourceMIMEType(name),
		Text:     string(body),
	}}})
}

// payloadPath accepts exactly one embedded payload path after the served URI
// prefix. Query markers, escapes, absolute paths, and dot segments are refused
// before any service call, so no read can resolve outside the payload.
func payloadPath(uri string) (string, bool) {
	name, ok := strings.CutPrefix(uri, resourceURIPrefix)
	if !ok || name == "" || strings.ContainsAny(name, "?#%\\") {
		return "", false
	}
	if path.Clean(name) != name || strings.HasPrefix(name, "/") ||
		name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return "", false
	}
	return name, true
}

func resourceMIMEType(name string) string {
	switch path.Ext(name) {
	case ".md":
		return "text/markdown"
	case ".json":
		return "application/json"
	default:
		return "text/plain"
	}
}

// MCP 2025-06-18 § server/resources error handling: a missing resource is
// -32002, and the spec's example carries the URI in the message and error data.
func (s *Server) writeResourceNotFound(id json.RawMessage, uri string) {
	s.writeErrorData(id, codeResourceNotFound, "resource not found: "+strconv.Quote(uri),
		resourceNotFoundData{URI: uri})
}

type resourceURIParams struct {
	URI string `json:"uri"`
}

type resourceDescriptor struct {
	URI      string `json:"uri"`
	Name     string `json:"name"`
	MIMEType string `json:"mimeType"`
}

type resourcesListResult struct {
	Resources []resourceDescriptor `json:"resources"`
}

type resourceContent struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType"`
	Text     string `json:"text"`
}

type readResourceResult struct {
	Contents []resourceContent `json:"contents"`
}

type resourceNotFoundData struct {
	URI string `json:"uri"`
}
