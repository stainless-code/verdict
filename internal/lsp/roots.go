package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// clientRootPlan resolves the served roots at initialize. With --root the
// operator's roots stand and any client folder is ignored (and said so):
// adopt is false. Without it the client's workspaceFolders are served, else
// its rootUri, else its rootPath, else the checkout the process started in.
// Every client root passes the same confinement as a document URI and must
// sit inside a git checkout; one that does not is dropped with a warning. It
// walks the filesystem, so it runs before stateMu is taken.
func (s *Server) clientRootPlan(params *initializeParams) (rootPlan, bool) {
	candidates := clientRootCandidates(params)
	if !s.workspace {
		if len(candidates) > 0 {
			s.queueLogf(msgInfo, "verdict: --root is set, so the client's workspace folders are ignored")
		}
		return rootPlan{}, false
	}
	paths := s.acceptedRoots(candidates)
	if len(candidates) == 0 && s.cfg.FallbackRoot != "" {
		paths = []string{s.cfg.FallbackRoot}
	}
	return s.planRoots(paths), true
}

// acceptedRoots keeps the candidates that pass confinement and the checkout
// guard, in order.
func (s *Server) acceptedRoots(candidates []rootCandidate) []string {
	var paths []string
	for _, c := range candidates {
		if p, ok := s.acceptClientRoot(c); ok {
			paths = append(paths, p)
		}
	}
	return paths
}

// warnRoots logs a root set that will publish nothing, and a pinned
// --bundle no served root carries. It runs on the read loop (the only writer
// of the root set) without stateMu: resolving the bundle touches the disk.
func (s *Server) warnRoots() {
	if len(s.roots) == 0 {
		s.queueLogf(msgWarning, "verdict: no workspace root inside a git checkout; nothing will be published "+
			"until the client adds a workspace folder in a checkout")
		return
	}
	if s.cfg.Bundle != "" && s.resolveAnywhere(s.cfg.Bundle) != nil {
		s.queueLogf(msgWarning, "verdict: %s", missingBundleMessage(s.cfg.Bundle))
	}
}

// rootCandidate is one client-supplied root: a URI or, from the deprecated
// rootPath, an OS path.
type rootCandidate struct {
	uri  string
	path string
}

func (c rootCandidate) String() string {
	if c.uri != "" {
		return c.uri
	}
	return c.path
}

// clientRootCandidates lists the client's roots in LSP precedence order.
func clientRootCandidates(params *initializeParams) []rootCandidate {
	var out []rootCandidate
	switch {
	case len(params.WorkspaceFolders) > 0:
		for _, f := range params.WorkspaceFolders {
			out = append(out, rootCandidate{uri: f.URI})
		}
	case params.RootURI != nil && *params.RootURI != "":
		out = append(out, rootCandidate{uri: *params.RootURI})
	case params.RootPath != nil && *params.RootPath != "":
		out = append(out, rootCandidate{path: *params.RootPath})
	}
	return out
}

// acceptClientRoot validates one client root: a clean absolute file URI (or
// absolute path) naming an existing directory inside a git checkout.
func (s *Server) acceptClientRoot(c rootCandidate) (string, bool) {
	p, err := clientRootPath(c, s.windows)
	if err != nil {
		s.queueLogf(msgWarning, "verdict: workspace root %s dropped: %v", quoteLog(c.String()), err)
		return "", false
	}
	if reason := s.boundedCheckout(p); reason != "" {
		s.queueLogf(msgWarning, "verdict: workspace root %s dropped: %s", p, reason)
		return "", false
	}
	return p, true
}

func clientRootPath(c rootCandidate, windows bool) (string, error) {
	if c.uri != "" {
		return osPathFromURI(c.uri, windows)
	}
	if !cleanText(c.path) || !filepath.IsAbs(c.path) {
		return "", errUnsupportedURI
	}
	return filepath.Clean(c.path), nil
}

// boundedCheckout runs the checkout guard through the bounded resolver: a
// root whose probe misses the budget (a hung volume) is dropped rather than
// stalling the session.
func (s *Server) boundedCheckout(dir string) string {
	problem, _, timedOut := s.resolver.run("root\x00"+dir, func() (string, bool) {
		return checkoutProblem(dir), true
	})
	if timedOut {
		return "its checkout could not be probed within " + s.lim.resolve.String()
	}
	return problem
}

// CheckoutProblem names why dir cannot be served, or returns "" when it is an
// existing directory inside a git checkout. The CLI refuses an explicit
// --root with it; the server drops a client root with it. Symlinks are
// resolved first, so a root that is (or sits under) a link into a tree
// outside any checkout is refused rather than served through the link.
func CheckoutProblem(dir string) string {
	return checkoutProblem(dir)
}

func checkoutProblem(dir string) string {
	physical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "it is not an existing directory"
	}
	info, err := os.Stat(physical)
	if err != nil || !info.IsDir() {
		return "it is not an existing directory"
	}
	inside, err := readcommon.InsideCheckout(physical)
	if err != nil {
		return "its checkout could not be probed: " + err.Error()
	}
	if inside {
		return ""
	}
	if lexical, lerr := readcommon.InsideCheckout(dir); lerr == nil && lexical {
		// The spelling sits in a checkout but a link leads out of it.
		return "it resolves to " + physical + ", which is not inside a git checkout"
	}
	return "it is not inside a git checkout"
}

// rootPlan is a root set ready to install: the served roots with their
// physical spellings resolved, and how many the --max-roots bound dropped.
type rootPlan struct {
	roots   []servedRoot
	dropped int
}

// planRoots resolves a root set. It walks the filesystem, so it runs before
// stateMu is taken.
func (s *Server) planRoots(paths []string) rootPlan {
	roots := s.rootsOf(paths)
	return rootPlan{roots: roots, dropped: distinctRoots(paths, s.windows) - len(roots)}
}

// installRoots replaces the served roots and rebuilds the read model. It
// does no I/O; the caller holds stateMu.
func (s *Server) installRoots(plan rootPlan) {
	if plan.dropped > 0 {
		s.queueLogf(msgWarning, "verdict: %d workspace root(s) beyond --max-roots %d dropped", plan.dropped, s.cfg.MaxRoots)
	}
	kept := map[string]bool{}
	for i := range plan.roots {
		kept[plan.roots[i].ID] = true
	}
	for id := range s.states {
		if !kept[id] {
			delete(s.states, id)
		}
	}
	s.setRoots(plan.roots)
}

func distinctRoots(paths []string, windows bool) int {
	seen := map[string]bool{}
	for _, p := range paths {
		seen[newServedRoot(osRoot(p), windows).ID] = true
	}
	return len(seen)
}

// handleDidChangeRoots queues a workspace-folder change and returns at
// once: validating added folders and resolving their spellings walk the
// filesystem, so none of it runs on the read loop. One background worker
// applies queued changes strictly in arrival order (see applyRootChange).
// Documents opened meanwhile are re-keyed when the change lands, so the end
// state is the one in-order processing gives. It is ignored under --root,
// which never advertised the capability.
func (s *Server) handleDidChangeRoots(ctx context.Context, msg *rpcMessage) {
	if !s.workspace {
		return
	}
	var params didChangeRootsParams
	if json.Unmarshal(msg.Params, &params) != nil {
		return
	}
	s.rootMu.Lock()
	defer s.rootMu.Unlock()
	s.rootQueue = append(s.rootQueue, params)
	if s.rootWorking {
		return
	}
	s.rootWorking = s.goBackground(func() { s.drainRootChanges(ctx) })
}

// drainRootChanges applies queued folder changes one at a time, in order,
// until the queue is empty.
func (s *Server) drainRootChanges(ctx context.Context) {
	for {
		s.rootMu.Lock()
		if len(s.rootQueue) == 0 || ctx.Err() != nil {
			s.rootQueue, s.rootWorking = nil, false
			s.rootMu.Unlock()
			return
		}
		next := s.rootQueue[0]
		s.rootQueue = s.rootQueue[1:]
		s.rootMu.Unlock()
		s.applyRootChange(ctx, &next)
	}
}

// applyRootChange validates and resolves the new root set (bounded walks,
// no lock held), installs it under stateMu (removed roots clear at once
// unless another root still reaches the file; open documents re-key), and
// then composes the added roots and moves the watcher to the rebuilt read
// model, behind any composition in flight.
func (s *Server) applyRootChange(ctx context.Context, params *didChangeRootsParams) {
	s.stateMu.Lock()
	current := s.rootsSnapshot()
	s.stateMu.Unlock()
	plan := s.planRoots(s.nextRootPaths(current, params))
	s.stateMu.Lock()
	s.installRoots(plan)
	s.rekeyDocs()
	added := newRoots(current, s.roots)
	s.republishAll()
	s.stateMu.Unlock()
	s.flushLogs()
	s.settleRoots(ctx, added)
}

// newRoots lists the roots in next that before did not serve.
func newRoots(before, next []servedRoot) []servedRoot {
	had := map[string]bool{}
	for i := range before {
		had[before[i].ID] = true
	}
	var out []servedRoot
	for i := range next {
		if !had[next[i].ID] {
			out = append(out, next[i])
		}
	}
	return out
}

// settleRoots finishes a root change in the background: the watcher moves
// to the rebuilt read model, then the added roots are composed (after any
// composition in flight) and everything republishes. The watcher starts
// first so a bundle write lands on one side or the other: after the
// watcher's baseline it is reported as a change, before it the compose
// reads it.
func (s *Server) settleRoots(ctx context.Context, added []servedRoot) {
	s.stopPump()
	s.callMu.Lock()
	s.startPump(ctx)
	s.callMu.Unlock()
	s.composeMu.Lock()
	defer s.composeMu.Unlock()
	for i := range added {
		s.compose(ctx, &added[i], true)
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.republishAll()
}

// nextRootPaths applies one change event to the current root paths,
// validating the added folders (a filesystem walk, run without stateMu).
func (s *Server) nextRootPaths(current []servedRoot, params *didChangeRootsParams) []string {
	removed := s.folderIDs(params.Event.Removed)
	var paths []string
	for i := range current {
		if !removed[current[i].ID] {
			paths = append(paths, current[i].Path)
		}
	}
	return append(paths, s.acceptedRoots(folderCandidates(params.Event.Added))...)
}

// folderIDs maps workspace folders onto root identities.
func (s *Server) folderIDs(folders []workspaceFolder) map[string]bool {
	ids := map[string]bool{}
	for _, f := range folders {
		if p, err := osPathFromURI(f.URI, s.windows); err == nil {
			ids[newServedRoot(osRoot(p), s.windows).ID] = true
		}
	}
	return ids
}

func folderCandidates(folders []workspaceFolder) []rootCandidate {
	out := make([]rootCandidate, 0, len(folders))
	for _, f := range folders {
		out = append(out, rootCandidate{uri: f.URI})
	}
	return out
}

// quoteLog renders client text for a log line without letting it break the
// line.
func quoteLog(s string) string {
	return fmt.Sprintf("%q", s)
}
