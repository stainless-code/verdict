package lsp

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
)

// docRef is how the server sees one client URI: its file key, its physical
// spelling, and every served root that confines it (nested roots can each
// confine the same file).
type docRef struct {
	key      string
	physical string
	roots    []servedRoot
}

// refFor maps one client URI whose physical spelling the caller already
// resolved (outside any lock; see physicalOfURI). A root confines it
// lexically, or, when the client spells the file through another path to the
// same tree (a symlinked directory such as /var and /private/var, or a
// Windows 8.3 short name), by the physical spellings of both. The key is the
// first confining root's key for the file, so every spelling of one file
// shares one publish; a URI under no served root keys by its own cleaned
// path, so a root added later finds it. ok is false for a URI that is not a
// local or UNC file URI at all. It does no I/O; the caller holds stateMu.
func (s *Server) refFor(uri, physical string) (docRef, bool) {
	host, p, err := absFromURI(uri, s.windows)
	if err != nil {
		return docRef{}, false
	}
	ref := docRef{key: pathIdentity(host, p, s.windows), physical: physical}
	first := true
	for i := range s.roots {
		root := &s.roots[i]
		rel, ok := s.relOf(root, uri, physical)
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
// when it cannot (a UNC host, or nothing on the path exists). It walks the
// filesystem (lstat only), so it never runs under stateMu: a hung volume
// must not stall the publishes and keystrokes that lock guards. It reads
// only fields fixed at construction.
func (s *Server) physicalOfURI(uri string) string {
	host, p, err := absFromURI(uri, s.windows)
	if err != nil || host != "" {
		return ""
	}
	phys, _ := s.physicalOf(p)
	return phys
}

// handleDidOpen tracks the buffer and publishes its file's merged set. Every
// open file document is tracked, under a served root or not, so a root a
// later workspace-folder change adds finds its text and version; past
// maxDocs open documents a new one is refused with a log line rather than
// evicting another. The path is resolved before stateMu is taken, through
// the bounded resolver, so a hung volume delays this notification by at most
// the resolve budget; the apply then reads the roots as they are under the
// lock.
func (s *Server) handleDidOpen(ctx context.Context, msg *rpcMessage) {
	var params didOpenParams
	if json.Unmarshal(msg.Params, &params) != nil {
		return
	}
	item := params.TextDocument
	physical := s.physicalOfURI(item.URI)
	defer s.flushLogs()
	s.stateMu.Lock()
	ref, ok := s.refFor(item.URI, physical)
	var doc *document
	if ok {
		doc = s.admitDoc(ref.key, item.URI)
	}
	if doc == nil {
		s.stateMu.Unlock()
		return
	}
	if physical != "" || doc.physical == "" {
		// A miss (a slow walk, or a file that does not exist yet) never
		// overwrites a spelling an earlier open resolved.
		doc.physical = physical
	}
	buf := doc.spelling(item.URI)
	buf.version = item.Version
	s.setText(buf, &item.Text)
	s.touch(doc, item.URI)
	s.uriKeys[item.URI] = ref.key
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
// joins the file key a new root publishes. It uses each document's stored
// physical spelling, so it does no I/O; the caller holds stateMu. Two
// documents that now share a key (one file the old roots keyed apart) merge
// into one that keeps every open spelling's version; the most recently
// changed spelling becomes the publish spelling and keeps its text, the
// others release theirs. The line saying so is queued, never written under
// the lock.
func (s *Server) rekeyDocs() {
	docs := s.docs
	s.docs = map[string]*document{}
	merged := 0
	for _, key := range slices.Sorted(maps.Keys(docs)) {
		if s.rekeyDoc(docs[key]) {
			merged++
		}
	}
	for key, doc := range s.docs {
		for uri := range doc.bufs {
			s.uriKeys[uri] = key
		}
	}
	if merged > 0 {
		s.queueLogf(msgInfo, "verdict: %d open document(s) now name a file another open document names; "+
			"each file publishes once", merged)
	}
}

// rekeyDoc files one document under its current key, merging it into a
// document already there or dropping it when it no longer keys at all. It
// reports a merge. The caller holds stateMu.
func (s *Server) rekeyDoc(doc *document) bool {
	ref, ok := s.refFor(doc.uri, doc.physical)
	if !ok {
		s.forgetDoc(doc)
		return false
	}
	doc.key = ref.key
	kept := s.docs[ref.key]
	if kept == nil {
		s.docs[ref.key] = doc
		return false
	}
	s.mergeDocs(kept, doc)
	return true
}

// mergeDocs folds other into kept: every spelling (with its own version)
// joins kept, the most recently changed spelling becomes the publish
// spelling and keeps its text, and every other spelling's text is released.
func (s *Server) mergeDocs(kept, other *document) {
	for uri, b := range other.bufs {
		if kept.bufs[uri] == nil {
			kept.spelling(uri)
			kept.bufs[uri] = b
			continue
		}
		s.setText(b, nil)
	}
	if kept.physical == "" {
		kept.physical = other.physical
	}
	kept.uri = kept.latest()
	for uri, b := range kept.bufs {
		if uri != kept.uri {
			s.setText(b, nil)
		}
	}
}

// forgetDoc drops a document the server can no longer key: its text is
// released and none of its spellings stay mapped. The caller holds stateMu.
func (s *Server) forgetDoc(doc *document) {
	s.dropText(doc)
	for uri := range doc.bufs {
		delete(s.uriKeys, uri)
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

// handleDidChange only keeps the changed spelling's buffer current. It never
// composes, never calls the read model, never touches the filesystem, and
// never publishes: no keystroke reaches the diagnostics path.
func (s *Server) handleDidChange(msg *rpcMessage) {
	var params didChangeParams
	if json.Unmarshal(msg.Params, &params) != nil {
		return
	}
	uri := params.TextDocument.URI
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	doc := s.docs[s.uriKeys[uri]]
	if doc == nil || doc.bufs[uri] == nil {
		return
	}
	buf := doc.bufs[uri]
	buf.version = params.TextDocument.Version
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
	s.setText(buf, text)
	s.touch(doc, uri)
}

// handleDidSave re-reads every confining root's selected bundle. An
// unchanged bundle republishes only this file (and nothing at all when its
// set is byte-identical to the last one sent); a bundle that moved
// republishes everything it reaches. An open document reuses the physical
// spelling resolved when it opened; one whose spelling never resolved (a
// slow walk, or a file opened before it existed) is resolved again here,
// through the bounded resolver and before stateMu is taken, and joins its
// root's key on a hit.
func (s *Server) handleDidSave(ctx context.Context, msg *rpcMessage) {
	var params didSaveParams
	if json.Unmarshal(msg.Params, &params) != nil {
		return
	}
	uri := params.TextDocument.URI
	physical, open := s.openPhysical(uri)
	if physical == "" {
		physical = s.physicalOfURI(uri)
	}
	defer s.flushLogs()
	s.stateMu.Lock()
	ref, ok := s.refFor(uri, physical)
	if open {
		ref.key = s.adoptPhysical(uri, physical)
	}
	if ok {
		s.recordSave(ref.key, uri, &params)
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

// openPhysical returns the stored physical spelling of an open document.
func (s *Server) openPhysical(uri string) (string, bool) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	key, open := s.uriKeys[uri]
	if !open {
		return "", false
	}
	if doc := s.docs[key]; doc != nil {
		return doc.physical, true
	}
	return "", false
}

// adoptPhysical stores a newly resolved physical spelling on an open
// document that had none and re-files it under the key that spelling
// gives, returning the document's key. The caller holds stateMu.
func (s *Server) adoptPhysical(uri, physical string) string {
	key := s.uriKeys[uri]
	doc := s.docs[key]
	if doc == nil || physical == "" || doc.physical != "" {
		return key
	}
	doc.physical = physical
	delete(s.docs, key)
	s.rekeyDoc(doc)
	for spelled := range doc.bufs {
		s.uriKeys[spelled] = doc.key
	}
	return s.uriKeys[uri]
}

// recordSave applies a save's optional version and text to the saved
// spelling's buffer.
func (s *Server) recordSave(key, uri string, params *didSaveParams) {
	doc := s.docs[key]
	if doc == nil || doc.bufs[uri] == nil {
		return
	}
	buf := doc.bufs[uri]
	if params.TextDocument.Version != nil {
		buf.version = *params.TextDocument.Version
	}
	if params.Text != nil {
		s.setText(buf, params.Text)
	}
	s.touch(doc, uri)
}

// handleDidClose stops tracking one spelling of the buffer. The document
// stays while another spelling of the same file is still open: it then
// publishes under the remaining spelling changed most recently, with that
// spelling's own version and text (never the closed one's). It goes when the
// last spelling closes. Its diagnostics stay: they describe the bundle, not
// the buffer.
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
	doc := s.docs[key]
	if doc == nil || doc.bufs[uri] == nil {
		return
	}
	s.setText(doc.bufs[uri], nil)
	delete(doc.bufs, uri)
	if len(doc.bufs) > 0 {
		doc.uri = doc.latest()
		return
	}
	delete(s.docs, key)
}
