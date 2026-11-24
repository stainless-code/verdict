package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// adoptClientRoots resolves the served roots at initialize. With --root the
// operator's roots stand and any client folder is ignored (and said so).
// Without it the client's workspaceFolders are served, else its rootUri,
// else its rootPath, else the checkout the process started in. Every
// client root passes the same confinement as a document URI and must sit
// inside a git checkout; one that does not is dropped with a warning. The
// caller holds stateMu.
func (s *Server) adoptClientRoots(params *initializeParams) {
	candidates := clientRootCandidates(params)
	if !s.workspace {
		if len(candidates) > 0 {
			s.queueLogf(msgInfo, "verdict: --root is set, so the client's workspace folders are ignored")
		}
		return
	}
	paths := s.acceptedRoots(candidates)
	if len(candidates) == 0 && s.cfg.FallbackRoot != "" {
		paths = []string{s.cfg.FallbackRoot}
	}
	s.installRoots(paths)
	s.warnRoots()
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
// --bundle no served root carries.
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
	if reason := checkoutProblem(p); reason != "" {
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

// installRoots replaces the served roots and rebuilds the read model. The
// caller holds stateMu; a root beyond MaxRoots is dropped with a warning.
func (s *Server) installRoots(paths []string) {
	roots := s.rootsOf(paths)
	if dropped := distinctRoots(paths, s.windows) - len(roots); dropped > 0 {
		s.queueLogf(msgWarning, "verdict: %d workspace root(s) beyond --max-roots %d dropped", dropped, s.cfg.MaxRoots)
	}
	kept := map[string]bool{}
	for i := range roots {
		kept[roots[i].ID] = true
	}
	for id := range s.states {
		if !kept[id] {
			delete(s.states, id)
		}
	}
	s.setRoots(roots)
}

func distinctRoots(paths []string, windows bool) int {
	seen := map[string]bool{}
	for _, p := range paths {
		seen[newServedRoot(osRoot(p), windows).ID] = true
	}
	return len(seen)
}

// handleDidChangeRoots applies a workspace-folder change: removed roots
// stop contributing (their diagnostics clear unless another root still
// reaches the file), added roots are validated and composed, and the read
// model and its watcher are rebuilt over the new set. It is ignored under
// --root, which never advertised the capability.
func (s *Server) handleDidChangeRoots(ctx context.Context, msg *rpcMessage) {
	if !s.workspace {
		return
	}
	var params didChangeRootsParams
	if json.Unmarshal(msg.Params, &params) != nil {
		return
	}
	s.stopPump()
	s.stateMu.Lock()
	paths := s.nextRootPaths(&params)
	before := map[string]bool{}
	for i := range s.roots {
		before[s.roots[i].ID] = true
	}
	s.installRoots(paths)
	for i := range s.roots {
		if !before[s.roots[i].ID] {
			s.compose(ctx, &s.roots[i], true)
		}
	}
	s.republishAll()
	s.stateMu.Unlock()
	s.flushLogs()
	s.callMu.Lock()
	defer s.callMu.Unlock()
	s.startPump(ctx)
}

// nextRootPaths applies one change event to the current root paths.
func (s *Server) nextRootPaths(params *didChangeRootsParams) []string {
	removed := s.folderIDs(params.Event.Removed)
	var paths []string
	for i := range s.roots {
		if !removed[s.roots[i].ID] {
			paths = append(paths, s.roots[i].Path)
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
