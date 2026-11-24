package lsp

import (
	"cmp"
	"encoding/json"
	"slices"
	"strings"
	"testing"
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
func workspaceSession(t *testing.T, params map[string]any) (*session, wireFrame) {
	t.Helper()
	s := startSession(t, newConfig())
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
	frames := s.barrier(3, mark)
	want := fileURI(first, "notes.txt") + " v- \n" + fileURI(first, "other.go") + " v- \n" +
		fileURI(second, "b.go") + " v- " + sigInfo
	got := publishes(t, frames)
	sortPublishes(got)
	if summarize(got) != sortedLines(want) {
		t.Fatalf("folder change published\n%s\nwant\n%s", summarize(got), sortedLines(want))
	}
	if !strings.Contains(strings.Join(logs(t, frames), "\n"), "not inside a git checkout") {
		t.Fatalf("the added non-checkout folder was not reported: %q", logs(t, frames))
	}
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

func sortPublishes(ps []publishParams) {
	slices.SortFunc(ps, func(a, b publishParams) int { return cmp.Compare(a.URI, b.URI) })
}

func sortedLines(s string) string {
	lines := strings.Split(s, "\n")
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

// TestClientRootConfinement pins the confinement a client root passes: a
// non-file URI, a remote host, a relative rootPath, and a missing directory
// are each dropped with a warning.
func TestClientRootConfinement(t *testing.T) {
	t.Parallel()
	s, _ := workspaceSession(t, map[string]any{"workspaceFolders": []any{
		map[string]any{"uri": "https://example.com/repo", "name": "a"},
		map[string]any{"uri": "file://server/share/repo", "name": "b"},
		map[string]any{"uri": "file:///nonexistent/verdict-lsp-root", "name": "c"},
	}})
	s.notify(methodInitialized, map[string]any{})
	texts := logs(t, s.barrier(2, 0))
	if len(texts) != 4 || !strings.Contains(texts[3], "no workspace root inside a git checkout") {
		t.Fatalf("confinement logs = %q, want three drops and the empty-roots warning", texts)
	}
	if err := s.finish(3); err != nil {
		t.Fatal(err)
	}
}
