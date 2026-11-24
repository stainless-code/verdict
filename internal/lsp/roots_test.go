package lsp

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/testenv"
)

const workspaceCaps = `"workspace":{"workspaceFolders":{"supported":true,"changeNotifications":true}}`

func folder(root string) map[string]any {
	return map[string]any{"uri": newServedRootURI(root), "name": "w"}
}

func newServedRootURI(root string) string {
	return strings.TrimSuffix(fileURI(root, "x"), "/x")
}

// workspaceSession starts a session with no --root and initializes it with
// the given client params.
func workspaceSession(t *testing.T, params map[string]any, cfgs ...*Config) (*session, wireFrame) {
	t.Helper()
	cfg := newConfig()
	if len(cfgs) > 0 {
		cfg = cfgs[0]
	}
	s := startSession(t, cfg)
	params["capabilities"] = map[string]any{}
	s.request(1, methodInitialize, params)
	_, resp := s.awaitResponse(1)
	return s, resp
}

// TestWorkspaceFoldersAreServed pins item 3's default: without --root the
// client's workspace folders are the roots, the workspace capability is
// advertised, and a folder outside any git checkout is dropped with a
// warning instead of being served.
func TestWorkspaceFoldersAreServed(t *testing.T) {
	t.Parallel()
	good := fixtureRoot(t)
	bare := t.TempDir()
	writeBundle(t, bare, "local-1", findingJSON(sigCrit, "open", "critical", "x.go", 1))
	s, resp := workspaceSession(t, map[string]any{
		"workspaceFolders": []any{folder(good), folder(bare)},
		"rootUri":          newServedRootURI(bare),
	})
	if !strings.Contains(string(resp.Result), workspaceCaps) {
		t.Fatalf("workspace mode did not advertise folder support: %s", resp.Result)
	}
	s.notify(methodInitialized, map[string]any{})
	frames := s.barrier(2, 0)
	texts := strings.Join(logs(t, frames), "\n")
	if !strings.Contains(texts, "workspace root "+bare+" dropped: it is not inside a git checkout") {
		t.Fatalf("the non-checkout folder was not reported dropped: %q", texts)
	}
	want := fileURI(good, "notes.txt") + " v- " + sigCrit + "\n" + fileURI(good, "other.go") + " v- " + sigImp
	if got := summarize(publishes(t, frames)); got != want {
		t.Fatalf("workspace publishes =\n%s\nwant only the checkout's", got)
	}
	if err := s.finish(3); err != nil {
		t.Fatal(err)
	}
}

// TestWorkspaceRootPrecedence pins rootUri, then rootPath, when the client
// sends no workspaceFolders.
func TestWorkspaceRootPrecedence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		params func(root string) map[string]any
		name   string
	}{
		{func(root string) map[string]any { return map[string]any{"rootUri": newServedRootURI(root)} }, "rootUri"},
		{func(root string) map[string]any { return map[string]any{"rootPath": root} }, "rootPath"},
	} {
		root := fixtureRoot(t)
		s, _ := workspaceSession(t, tc.params(root))
		s.notify(methodInitialized, map[string]any{})
		if got := len(publishes(t, s.barrier(2, 0))); got != 2 {
			t.Fatalf("%s: %d publishes, want the root's 2", tc.name, got)
		}
		if err := s.finish(3); err != nil {
			t.Fatal(err)
		}
	}
}

// TestExplicitRootIgnoresClientFolders pins that --root is authoritative:
// no workspace capability, client folders ignored and said so.
func TestExplicitRootIgnoresClientFolders(t *testing.T) {
	t.Parallel()
	root, other := fixtureRoot(t), fixtureRoot(t)
	s := startSession(t, newConfig(root))
	s.request(1, methodInitialize, map[string]any{
		"capabilities":     map[string]any{},
		"workspaceFolders": []any{folder(other)},
	})
	_, resp := s.awaitResponse(1)
	if strings.Contains(string(resp.Result), `"workspace"`) {
		t.Fatalf("--root advertised workspace folders: %s", resp.Result)
	}
	s.notify(methodInitialized, map[string]any{})
	frames := s.barrier(2, 0)
	if !strings.Contains(strings.Join(logs(t, frames), "\n"), "--root is set, so the client's workspace folders are ignored") {
		t.Fatalf("no ignore notice: %q", logs(t, frames))
	}
	for _, p := range publishes(t, frames) {
		if !strings.HasPrefix(p.URI, newServedRootURI(root)+"/") {
			t.Fatalf("a client folder was served: %s", p.URI)
		}
	}
	if err := s.finish(3); err != nil {
		t.Fatal(err)
	}
}

// TestWorkspaceFallbackAndEmpty pins the no-roots cases: a client that names
// no root gets the checkout the server started in; with none, nothing is
// served and a warning says why.
func TestWorkspaceFallbackAndEmpty(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	cfg := newConfig()
	cfg.FallbackRoot = root
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	if got := len(publishes(t, s.barrier(2, 0))); got != 2 {
		t.Fatalf("fallback root published %d, want 2", got)
	}
	if err := s.finish(3); err != nil {
		t.Fatal(err)
	}
	empty := startSession(t, newConfig())
	empty.initialize(1, nil)
	empty.notify(methodInitialized, map[string]any{})
	frames := empty.barrier(2, 0)
	if countMethod(frames, methodPublish) != 0 ||
		!strings.Contains(strings.Join(logs(t, frames), "\n"), "no workspace root inside a git checkout") {
		t.Fatalf("no-root session frames: %+v", logs(t, frames))
	}
	if err := empty.finish(3); err != nil {
		t.Fatal(err)
	}
}

// TestDidChangeWorkspaceFolders pins folder changes: a removed root's
// diagnostics clear, an added root is validated, composed, and published,
// and the change is ignored under --root.
func TestDidChangeWorkspaceFolders(t *testing.T) {
	t.Parallel()
	first, second := fixtureRoot(t), checkout(t)
	writeBundle(t, second, "local-7", findingJSON(sigInfo, "open", "informational", "b.go", 1))
	s, _ := workspaceSession(t, map[string]any{"workspaceFolders": []any{folder(first)}})
	s.notify(methodInitialized, map[string]any{})
	mark := len(s.barrier(2, 0)) + 1
	s.notify(methodDidChangeRoots, map[string]any{"event": map[string]any{
		"added": []any{folder(second), folder(t.TempDir())}, "removed": []any{folder(first)},
	}})
	// The removed root clears at once (in order, before the barrier); the
	// added root is composed in the background and publishes after it.
	frames := s.barrier(3, mark)
	cleared := fileURI(first, "notes.txt") + " v- \n" + fileURI(first, "other.go") + " v- "
	if got := publishes(t, frames); summarize(got) != cleared {
		t.Fatalf("folder change cleared\n%s\nwant\n%s", summarize(got), cleared)
	}
	if !strings.Contains(strings.Join(logs(t, frames), "\n"), "not inside a git checkout") {
		t.Fatalf("the added non-checkout folder was not reported: %q", logs(t, frames))
	}
	added := fileURI(second, "b.go") + " v- " + sigInfo
	testenv.Until(t, func() bool { return strings.Contains(summarize(publishes(t, s.snapshot())), added) })
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}

	explicit := startSession(t, newConfig(first))
	explicit.initialize(1, nil)
	explicit.notify(methodInitialized, map[string]any{})
	mark = len(explicit.barrier(2, 0)) + 1
	raw, err := json.Marshal(map[string]any{"event": map[string]any{"removed": []any{folder(first)}}})
	if err != nil {
		t.Fatal(err)
	}
	explicit.sendRaw(`{"jsonrpc":"2.0","method":"workspace/didChangeWorkspaceFolders","params":` + string(raw) + `}`)
	if extra := explicit.barrier(3, mark); len(extra) != 0 {
		t.Fatalf("--root honored a folder change: %d frames", len(extra))
	}
	if err := explicit.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestClientRootConfinement pins the confinement a client root passes: a
// non-file URI, a remote host, a relative rootPath, and a missing directory
// are each dropped with a warning.
func TestClientRootConfinement(t *testing.T) {
	t.Parallel()
	folders := []any{
		map[string]any{"uri": "https://example.com/repo", "name": "a"},
		map[string]any{"uri": "file:///nonexistent/verdict-lsp-root", "name": "c"},
	}
	if runtime.GOOS != "windows" {
		// A remote authority is refused outright off Windows; on Windows it
		// is a UNC path, and probing one would reach for the network.
		folders = append(folders, map[string]any{"uri": "file://server/share/repo", "name": "b"})
	}
	s, _ := workspaceSession(t, map[string]any{"workspaceFolders": folders})
	s.notify(methodInitialized, map[string]any{})
	texts := logs(t, s.barrier(2, 0))
	if len(texts) != len(folders)+1 || !strings.Contains(texts[len(folders)], "no workspace root inside a git checkout") {
		t.Fatalf("confinement logs = %q, want one drop per folder and the empty-roots warning", texts)
	}
	if err := s.finish(3); err != nil {
		t.Fatal(err)
	}
}

// TestDocumentOpenedBeforeItsRoot pins that every open document is tracked,
// served or not: a document opened outside every root, whose root a later
// workspace-folder change adds, publishes with its version and its text's
// tightened range instead of the untracked whole-line form.
func TestDocumentOpenedBeforeItsRoot(t *testing.T) {
	t.Parallel()
	first, later := fixtureRoot(t), fixtureRoot(t)
	s, _ := workspaceSession(t, map[string]any{"workspaceFolders": []any{folder(first)}})
	s.notify(methodInitialized, map[string]any{})
	uri := fileURI(later, "notes.txt")
	openDoc(s, uri, 5, notesText)
	mark := len(s.barrier(2, 0)) + 1
	s.notify(methodDidChangeRoots, map[string]any{"event": map[string]any{"added": []any{folder(later)}}})
	var got *publishParams
	testenv.Until(t, func() bool {
		got = lastPublishFor(t, s.snapshot()[mark:], uri)
		return got != nil
	})
	if got == nil || got.Version == nil || *got.Version != 5 || sigsOf(got) != sigCrit ||
		got.Diagnostics[0].Range.End != (position{Line: 1, Character: 8}) {
		t.Fatalf("the earlier-opened document published %+v, want version 5 with a tightened range", got)
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// lastPublishFor returns the last publish for uri among frames, or nil.
func lastPublishFor(t *testing.T, frames []wireFrame, uri string) *publishParams {
	t.Helper()
	var got *publishParams
	for _, p := range publishes(t, frames) {
		if p.URI == uri {
			got = &p
		}
	}
	return got
}
