package lsp

import (
	"context"
	"encoding/json"
)

// rootFor maps one client URI onto the served root that confines it and the
// root-relative path. ok is false for a URI under no served root: such a
// document is tracked by nobody and never receives diagnostics.
func (s *Server) rootFor(uri string) (*servedRoot, string, bool) {
	for i := range s.roots {
		if rel, err := s.roots[i].relFromURI(uri); err == nil {
			return &s.roots[i], rel, true
		}
	}
	return nil, "", false
}

// handleDidOpen tracks the buffer and publishes its path's set.
func (s *Server) handleDidOpen(ctx context.Context, msg *rpcMessage) {
	var params didOpenParams
	if json.Unmarshal(msg.Params, &params) != nil {
		return
	}
	item := params.TextDocument
	root, rel, ok := s.rootFor(item.URI)
	if !ok {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	doc := &document{uri: item.URI, rootID: root.ID, key: root.pathKey(rel), version: item.Version}
	if s.trackedText(len(item.Text), item.URI) {
		doc.text, doc.hasText = item.Text, true
	}
	s.docs[item.URI] = doc
	s.docKeys[root.ID+"\x00"+doc.key] = item.URI
	if s.states[root.ID] == nil {
		s.compose(ctx, root, true)
	}
	s.publishKey(root, doc.key, rel)
}

// handleDidChange only keeps the buffer current. It never composes, never
// calls the read model, and never publishes: no keystroke reaches the
// diagnostics path.
func (s *Server) handleDidChange(msg *rpcMessage) {
	var params didChangeParams
	if json.Unmarshal(msg.Params, &params) != nil {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	doc := s.docs[params.TextDocument.URI]
	if doc == nil {
		return
	}
	doc.version = params.TextDocument.Version
	// Full sync: the last whole-buffer change wins. A ranged change is an
	// incremental edit this server never asked for, so the text is dropped
	// rather than patched wrong.
	doc.text, doc.hasText = "", false
	for _, change := range params.ContentChanges {
		if change.Range != nil {
			doc.text, doc.hasText = "", false
			continue
		}
		if s.trackedText(len(change.Text), doc.uri) {
			doc.text, doc.hasText = change.Text, true
		} else {
			doc.text, doc.hasText = "", false
		}
	}
}

// handleDidSave re-reads the selected bundle's cached detail. An unchanged
// bundle republishes only this document (and nothing at all when its set is
// byte-identical to the last one sent); a bundle that moved republishes the
// whole root.
func (s *Server) handleDidSave(ctx context.Context, msg *rpcMessage) {
	var params didSaveParams
	if json.Unmarshal(msg.Params, &params) != nil {
		return
	}
	uri := params.TextDocument.URI
	root, rel, ok := s.rootFor(uri)
	if !ok {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	doc := s.docs[uri]
	if doc != nil && params.Text != nil {
		if s.trackedText(len(*params.Text), uri) {
			doc.text, doc.hasText = *params.Text, true
		} else {
			doc.text, doc.hasText = "", false
		}
	}
	if s.compose(ctx, root, s.states[root.ID] == nil) {
		s.republishRoot(root)
		return
	}
	s.publishKey(root, root.pathKey(rel), rel)
}

// handleDidClose stops tracking the buffer. Its diagnostics stay: they
// describe the bundle, not the buffer.
func (s *Server) handleDidClose(msg *rpcMessage) {
	var params didCloseParams
	if json.Unmarshal(msg.Params, &params) != nil {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	uri := params.TextDocument.URI
	doc := s.docs[uri]
	if doc == nil {
		return
	}
	delete(s.docs, uri)
	k := doc.rootID + "\x00" + doc.key
	if s.docKeys[k] == uri {
		delete(s.docKeys, k)
	}
}
