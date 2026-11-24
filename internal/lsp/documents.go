package lsp

import (
	"context"
	"encoding/json"
)

// rootsFor lists the served roots that confine one client URI. Nested roots
// can each confine the same file; a URI under no root has none.
func (s *Server) rootsFor(uri string) []*servedRoot {
	var out []*servedRoot
	for i := range s.roots {
		if _, err := s.roots[i].relFromURI(uri); err == nil {
			out = append(out, &s.roots[i])
		}
	}
	return out
}

// handleDidOpen tracks the buffer and publishes its file's merged set. A
// document under no served root is not tracked; past maxDocs open documents
// a new one is refused with a log line rather than evicting another.
func (s *Server) handleDidOpen(ctx context.Context, msg *rpcMessage) {
	var params didOpenParams
	if json.Unmarshal(msg.Params, &params) != nil {
		return
	}
	item := params.TextDocument
	key, ok := fileKeyOfURI(item.URI, s.windows)
	if !ok {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	roots := s.rootsFor(item.URI)
	if len(roots) == 0 {
		return
	}
	doc := s.admitDoc(key, item.URI)
	if doc == nil {
		return
	}
	doc.uri, doc.version = item.URI, item.Version
	s.setText(doc, &item.Text)
	for _, root := range roots {
		if s.states[root.ID] == nil {
			s.compose(ctx, root, true)
		}
	}
	s.publishFile(key, item.URI)
}

// admitDoc returns the tracked document for key, tracking a new one when the
// open-document bound allows; past it the open is refused with a log line
// rather than evicting another document.
func (s *Server) admitDoc(key, uri string) *document {
	if doc := s.docs[key]; doc != nil {
		return doc
	}
	if len(s.docs) >= s.lim.docs {
		s.logf(msgWarning, "verdict: %d documents are open, the most this server tracks; %s gets no "+
			"diagnostics until another closes", len(s.docs), quoteLog(uri))
		return nil
	}
	doc := &document{key: key}
	s.docs[key] = doc
	return doc
}

// handleDidChange only keeps the buffer current. It never composes, never
// calls the read model, and never publishes: no keystroke reaches the
// diagnostics path.
func (s *Server) handleDidChange(msg *rpcMessage) {
	var params didChangeParams
	if json.Unmarshal(msg.Params, &params) != nil {
		return
	}
	key, ok := fileKeyOfURI(params.TextDocument.URI, s.windows)
	if !ok {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	doc := s.docs[key]
	if doc == nil {
		return
	}
	doc.version = params.TextDocument.Version
	// Full sync: the last whole-buffer change wins. A ranged change is an
	// incremental edit this server never asked for, so the text is dropped
	// rather than patched wrong.
	var text *string
	for i := range params.ContentChanges {
		if params.ContentChanges[i].Range != nil {
			text = nil
			continue
		}
		text = &params.ContentChanges[i].Text
	}
	s.setText(doc, text)
}

// handleDidSave re-reads every confining root's selected bundle. An
// unchanged bundle republishes only this file (and nothing at all when its
// set is byte-identical to the last one sent); a bundle that moved
// republishes everything it reaches.
func (s *Server) handleDidSave(ctx context.Context, msg *rpcMessage) {
	var params didSaveParams
	if json.Unmarshal(msg.Params, &params) != nil {
		return
	}
	uri := params.TextDocument.URI
	key, ok := fileKeyOfURI(uri, s.windows)
	if !ok {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.recordSave(key, &params)
	if s.recomposeFile(ctx, uri) {
		s.republishAll()
		return
	}
	s.publishFile(key, uri)
}

// recordSave applies a save's optional version and text to a tracked
// document.
func (s *Server) recordSave(key string, params *didSaveParams) {
	doc := s.docs[key]
	if doc == nil {
		return
	}
	if params.TextDocument.Version != nil {
		doc.version = *params.TextDocument.Version
	}
	if params.Text != nil {
		s.setText(doc, params.Text)
	}
}

// recomposeFile re-reads the bundle of every root that reaches uri and
// reports whether any of them moved.
func (s *Server) recomposeFile(ctx context.Context, uri string) bool {
	moved := false
	for _, root := range s.rootsFor(uri) {
		if s.compose(ctx, root, s.states[root.ID] == nil) {
			moved = true
		}
	}
	return moved
}

// handleDidClose stops tracking the buffer. Its diagnostics stay: they
// describe the bundle, not the buffer.
func (s *Server) handleDidClose(msg *rpcMessage) {
	var params didCloseParams
	if json.Unmarshal(msg.Params, &params) != nil {
		return
	}
	key, ok := fileKeyOfURI(params.TextDocument.URI, s.windows)
	if !ok {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if doc := s.docs[key]; doc != nil {
		s.setText(doc, nil)
		delete(s.docs, key)
	}
}
