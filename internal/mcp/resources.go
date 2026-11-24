package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"strconv"
	"strings"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/core/skill"
)

const (
	methodResourcesList          = "resources/list"
	methodResourcesRead          = "resources/read"
	methodResourcesTemplatesList = "resources/templates/list"
	resourceURIPrefix            = "verdict://skill/"
	codeResourceNotFound         = -32002
	mimeJSON                     = "application/json"
)

// queryStaticResources are the dashboard reads resources/list serves
// beside the skill payloads: the same query layer the query tools render.
var queryStaticResources = []resourceDescriptor{
	{URI: "verdict://roots", Name: "roots", MIMEType: mimeJSON},
	{URI: "verdict://bundles", Name: "bundles", MIMEType: mimeJSON},
	{URI: "verdict://health", Name: "health", MIMEType: mimeJSON},
	{URI: "verdict://cost", Name: "cost", MIMEType: mimeJSON},
}

// queryResourceTemplates are the per-bundle dashboard reads served as
// resource templates, one address per query layer view.
var queryResourceTemplates = []resourceTemplateDescriptor{
	{URITemplate: "verdict://findings/{bundle}", Name: "findings", MIMEType: mimeJSON},
	{URITemplate: "verdict://finding/{bundle}/{sig}", Name: "finding", MIMEType: mimeJSON},
	{URITemplate: "verdict://compare/{bundle}/{baseline}", Name: "compare", MIMEType: mimeJSON},
	{URITemplate: "verdict://cost/{by}", Name: "cost-by", MIMEType: mimeJSON},
	{URITemplate: "verdict://precision/{by}", Name: "precision-by", MIMEType: mimeJSON},
}

// queryResourceURIs lists the static dashboard reads in served order.
func queryResourceURIs() []string {
	out := make([]string, 0, len(queryStaticResources))
	for _, res := range queryStaticResources {
		out = append(out, res.URI)
	}
	return out
}

func (s *Server) handleResourcesList(ctx context.Context, req *rpcRequest) {
	if len(req.ID) == 0 {
		return
	}
	inv, ref := skill.List(ctx, &skill.Request{})
	if ref != nil {
		s.writeError(req.ID, codeInternalError, "resource list unavailable: "+ref.Message)
		return
	}
	refs := handoffRefPaths()
	resources := make([]resourceDescriptor, 0, len(inv.Files)+len(refs)+len(queryStaticResources))
	for _, name := range append(slices.Clone(inv.Files), refs...) {
		resources = append(resources, resourceDescriptor{
			URI:      resourceURIPrefix + name,
			Name:     name,
			MIMEType: resourceMIMEType(name),
		})
	}
	resources = append(resources, queryStaticResources...)
	s.writeResult(req.ID, resourcesListResult{Resources: resources})
}

func (s *Server) handleResourcesTemplatesList(req *rpcRequest) {
	if len(req.ID) == 0 {
		return
	}
	templates := make([]resourceTemplateDescriptor, 0, len(queryResourceTemplates))
	templates = append(templates, queryResourceTemplates...)
	s.writeResult(req.ID, resourcesTemplatesListResult{ResourceTemplates: templates})
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
	if !strings.HasPrefix(params.URI, resourceURIPrefix) {
		s.readDashboardResource(ctx, req, params.URI)
		return
	}
	name, valid := payloadPath(params.URI)
	if !valid {
		s.writeResourceNotFound(req.ID, params.URI)
		return
	}
	body, ok := readHandoffRef(name)
	if !ok {
		var ref *core.RefusalError
		body, ref = skill.ShowFile(ctx, &skill.Request{}, name)
		if ref != nil {
			s.writeResourceNotFound(req.ID, params.URI)
			return
		}
	}
	s.writeResult(req.ID, readResourceResult{Contents: []resourceContent{{
		URI:      params.URI,
		MIMEType: resourceMIMEType(name),
		Text:     string(body),
	}}})
}

// readDashboardResource composes one dashboard URI through the query layer.
// The URI was validated before the layer runs; a miss is resource-not-found,
// a layer refusal names its code as invalid params, and anything else is
// internal. The read never writes, spawns, or reaches the network.
func (s *Server) readDashboardResource(ctx context.Context, req *rpcRequest, uri string) {
	q, ok := parseQueryURI(uri)
	if !ok {
		s.writeResourceNotFound(req.ID, uri)
		return
	}
	layer, rootID := queryLayerFor(s.cfg.Root, s.cfg.Version, s.cfg.Now)
	view, err := queryResourceView(ctx, layer, rootID, q)
	if err != nil {
		s.writeDashboardError(req.ID, uri, err)
		return
	}
	s.writeResult(req.ID, readResourceResult{Contents: []resourceContent{{
		URI:      uri,
		MIMEType: mimeJSON,
		Text:     mergeView(view).MarshalCompact(),
	}}})
}

func (s *Server) writeDashboardError(id json.RawMessage, uri string, err error) {
	if errors.Is(err, query.ErrNotFound) {
		s.writeResourceNotFound(id, uri)
		return
	}
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		s.writeErrorData(id, codeInvalidParams, ref.Code+": "+ref.Message,
			map[string]any{"code": ref.Code, "message": ref.Message})
		return
	}
	s.logf("query resource: %v", err)
	s.writeError(id, codeInternalError, "internal error")
}

// handoffRefPaths publishes the same pull refs a fetched bundle writes, at
// the same URIs the MCP-served handoff names. An MCP client has no bundle and
// therefore no refs/ directory on disk; without these resources the lean
// core's capability map would point at files the client cannot open.
func handoffRefPaths() []string {
	refs := skillfs.HandoffRefs()
	paths := make([]string, 0, len(refs))
	for _, ref := range refs {
		paths = append(paths, ref.Path)
	}
	slices.Sort(paths)
	return paths
}

func readHandoffRef(name string) ([]byte, bool) {
	for _, ref := range skillfs.HandoffRefs() {
		if ref.Path != name {
			continue
		}
		body, err := skillfs.ReadRef(ref)
		return body, err == nil
	}
	return nil, false
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

// resourceTemplateDescriptor is one per-bundle dashboard read address, in
// the MCP resourceTemplates shape the clients substitute before reading.
type resourceTemplateDescriptor struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	MIMEType    string `json:"mimeType"`
}

type resourcesTemplatesListResult struct {
	ResourceTemplates []resourceTemplateDescriptor `json:"resourceTemplates"`
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
