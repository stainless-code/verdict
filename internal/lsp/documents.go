package lsp

import (
	"context"
	"encoding/json"
)

// docRef is how the server sees one client URI: its file key, its physical
// spelling, and every served root that confines it (nested roots can each
// confine the same file).
type docRef struct {
	key      string
	physical string
	roots    []servedRoot
}

// resolveURI maps one client URI. A root confines it lexically, or, when the
// client spells the file through another path to the same tree (a symlinked
// directory such as /var and /private/var, or a Windows 8.3 short name), by
// the physical spellings of both: the document path is resolved once here
// (lstat only, never opened). The key is the first confining root's key for
// the file, so every spelling of one file shares one publish; a URI under no
// served root keys by its own cleaned path, so a root added later finds it.
// ok is false for a URI that is not a local or UNC file URI at all.
func (s *Server) resolveURI(uri string) (docRef, bool) {
	host, p, err := absFromURI(uri, s.windows)
	if err != nil {
		return docRef{}, false
	}
	ref := docRef{key: pathIdentity(host, p, s.windows), physical: s.physicalOfURI(uri)}
	first := true
	for i := range s.roots {
		root := &s.roots[i]
		rel, ok := s.relOf(root, uri, ref.physical)
		if !ok {
			continue
		}
		if first {
			ref.key, first = root.fileKey(rel), false
		}
		ref.roots = append(ref.roots, *root)
	}
	return ref, true
}

// relOf maps a URI under one root: lexically first, then through the
// physical spellings of the document and the root.
func (s *Server) relOf(root *servedRoot, uri, physical string) (string, bool) {
	if rel, err := root.relFromURI(uri); err == nil {
		return rel, true
	}
	if rel, err := root.relFromPhysical(physical); err == nil {
		return rel, true
	}
	return "", false
}

// physicalOfURI resolves a local file URI's physical slash spelling, or ""
// when it cannot (a UNC host, or nothing on the path exists).
func (s *Server) physicalOfURI(uri string) string {
	host, p, err := absFromURI(uri, s.windows)
	if err != nil || host != "" {
		return ""
	}
	phys, ok := physicalPath(p, s.windows, s.cfg.eval)
	if !ok {
		return ""
	}
	return phys
}

// handleDidOpen tracks the buffer and publishes its file's merged set. Every
// open file document is tracked, under a served root or not, so a root a
// later workspace-folder change adds finds its text and version; past
// maxDocs open documents a new one is refused with a log line rather than
// evicting another.
func (s *Server) handleDidOpen(ctx context.Context, msg *rpcMessage) {
	var params didOpenParams
	if json.Unmarshal(msg.Params, &params) != nil {
		return
	}
	item := params.TextDocument
	s.stateMu.Lock()
	ref, ok := s.resolveURI(item.URI)
	var doc *document
	if ok {
		doc = s.admitDoc(ref.key, item.URI)
	}
	if doc == nil {
		s.stateMu.Unlock()
		return
	}
	doc.uri, doc.version, doc.physical = item.URI, item.Version, ref.physical
	s.uriKeys[item.URI] = ref.key
	s.setText(doc, &item.Text)
	unread := s.unreadRoots(ref.roots)
	s.stateMu.Unlock()
	if len(unread) > 0 {
		s.composeNow(ctx, unread, true)
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if len(ref.roots) > 0 {
		s.publishFile(ref.key, item.URI)
	}
}

// unreadRoots keeps the roots never composed yet. The caller holds stateMu.
func (s *Server) unreadRoots(roots []servedRoot) []servedRoot {
	var out []servedRoot
	for i := range roots {
		if s.states[roots[i].ID] == nil {
			out = append(out, roots[i])
		}
	}
	return out
}

// composeNow recomposes roots on the read loop when no composition is in
// flight; otherwise it hands them to deferCompose, so a slow refresh never
// stalls the read loop (and with it $/cancelRequest and exit). It reports
// whether any root moved; a deferred recompose republishes on its own.
func (s *Server) composeNow(ctx context.Context, roots []servedRoot, reselect bool) bool {
	if !s.composeMu.TryLock() {
		s.deferCompose(ctx, roots)
		return false
	}
	defer s.composeMu.Unlock()
	moved := false
	for i := range roots {
		if s.compose(ctx, &roots[i], reselect || s.stateOf(roots[i].ID) == nil) {
			moved = true
		}
	}
	return moved
}

// stateOf reads one root's state under stateMu.
func (s *Server) stateOf(id string) *bundleState {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.states[id]
}

// rekeyDocs re-derives every open document's key after the roots changed,
// so a document opened under no root (or under another root's spelling)
// joins the file key a new root publishes. The caller holds stateMu.
func (s *Server) rekeyDocs() {
	docs := s.docs
	s.docs = map[string]*document{}
	for _, doc := range docs {
		ref, ok := s.resolveURI(doc.uri)
		if !ok {
			continue
		}
		doc.key = ref.key
		s.docs[ref.key] = doc
	}
	for uri := range s.uriKeys {
		if ref, ok := s.resolveURI(uri); ok {
			s.uriKeys[uri] = ref.key
		}
	}
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
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	doc := s.docs[s.uriKeys[params.TextDocument.URI]]
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
	s.stateMu.Lock()
	ref, ok := s.resolveURI(uri)
	if key, open := s.uriKeys[uri]; open {
		ref.key = key
	}
	if ok {
		s.recordSave(ref.key, &params)
	}
	s.stateMu.Unlock()
	if !ok || len(ref.roots) == 0 {
		return
	}
	moved := s.composeNow(ctx, ref.roots, false)
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if moved {
		s.republishAll()
		return
	}
	s.publishFile(ref.key, uri)
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

// handleDidClose stops tracking the buffer. Its diagnostics stay: they
// describe the bundle, not the buffer.
func (s *Server) handleDidClose(msg *rpcMessage) {
	var params didCloseParams
	if json.Unmarshal(msg.Params, &params) != nil {
		return
	}
	uri := params.TextDocument.URI
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	key, open := s.uriKeys[uri]
	if !open {
		return
	}
	delete(s.uriKeys, uri)
	if doc := s.docs[key]; doc != nil && doc.uri == uri {
		s.setText(doc, nil)
		delete(s.docs, key)
	}
}
