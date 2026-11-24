package lsp

import (
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/testenv"
)

// gatedEval is a resolver that, while armed, blocks forever on paths
// containing needle (a hung volume) until the test releases it at cleanup.
type gatedEval struct {
	release chan struct{}
	needle  string
	once    sync.Once
	armed   atomic.Bool
	blocked atomic.Int64
}

func newGatedEval(t *testing.T, needle string) *gatedEval {
	t.Helper()
	g := &gatedEval{release: make(chan struct{}), needle: needle}
	t.Cleanup(g.unblock)
	return g
}

// unblock lets every blocked walk finish (the volume answers).
func (g *gatedEval) unblock() {
	g.once.Do(func() { close(g.release) })
}

func (g *gatedEval) eval(p string) (string, error) {
	if g.armed.Load() && strings.Contains(p, g.needle) {
		g.blocked.Add(1)
		<-g.release
	}
	return evalOS(p)
}

// TestHungVolumeNeverStallsKeystrokes pins item 1's keystroke property: with
// a path walk blocked forever, a didOpen of that file waits at most the
// resolve budget, and the didChange, $/cancelRequest, request, shutdown, and
// exit behind it are all handled; the slow path is logged once.
func TestHungVolumeNeverStallsKeystrokes(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	gate := newGatedEval(t, "hang.txt")
	gate.armed.Store(true)
	cfg := newConfig(root)
	cfg.eval = gate.eval
	cfg.lim.resolve = 20 * time.Millisecond
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	mark := len(s.barrier(2, 0)) + 1
	uri := fileURI(root, "hang.txt")
	openDoc(s, uri, 1, "x\n")
	s.notify(methodDidChange, map[string]any{
		"textDocument": map[string]any{"uri": uri, "version": 2}, "contentChanges": []any{map[string]any{"text": "y\n"}},
	})
	s.notify(methodCancel, map[string]any{"id": 99})
	openDoc(s, uri, 3, "z\n")
	texts := logs(t, s.barrier(3, mark))
	if len(texts) != 1 || !strings.Contains(texts[0], "took longer than 20ms") || gate.blocked.Load() == 0 {
		t.Fatalf("hung walk: logs %q, blocked walks %d; want one slow-path line", texts, gate.blocked.Load())
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestMissedResolutionKeepsTheGoodOne pins item 5: a later didOpen whose
// path walk misses (here, times out) keeps the spelling an earlier open
// resolved.
func TestMissedResolutionKeepsTheGoodOne(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	gate := newGatedEval(t, "notes.txt")
	cfg := newConfig(root)
	cfg.eval = gate.eval
	cfg.lim.resolve = 20 * time.Millisecond
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	plain := fileURI(root, "notes.txt")
	openDoc(s, plain, 1, notesText)
	s.barrier(2, 0)
	gate.armed.Store(true)
	openDoc(s, "file://localhost"+strings.TrimPrefix(plain, "file://"), 2, notesText)
	s.barrier(3, 0)
	s.server.stateMu.Lock()
	phys := s.server.docs[s.server.uriKeys[plain]].physical
	s.server.stateMu.Unlock()
	if phys == "" {
		t.Fatal("a missed resolution overwrote the spelling the first open resolved")
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestSaveResolvesAMissedSpelling pins item 6: a document whose spelling did
// not resolve at open (its walk hung) is resolved again on save and, on a
// hit, joins its root: the root is served through a link, the client opens
// the file by its real path, and only the save brings its findings.
func TestSaveResolvesAMissedSpelling(t *testing.T) {
	t.Parallel()
	target := fixtureRoot(t)
	link := filepath.Join(t.TempDir(), "link")
	symlink(t, target, link)
	physical, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	gate := newGatedEval(t, "notes.txt")
	cfg := newConfig(link)
	cfg.eval = gate.eval
	cfg.lim.resolve = 20 * time.Millisecond
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	mark := len(s.barrier(2, 0)) + 1
	opened := fileURI(physical, "notes.txt")
	gate.armed.Store(true)
	openDoc(s, opened, 1, notesText)
	if got := lastPublishFor(t, s.barrier(3, mark), opened); got != nil {
		t.Fatalf("an unresolved outside spelling published %+v", got)
	}
	gate.armed.Store(false)
	gate.unblock()
	testenv.Until(t, func() bool { return resolverIdle(s.server) })
	mark = len(s.snapshot())
	s.notify(methodDidSave, map[string]any{"textDocument": map[string]any{"uri": opened}})
	if got := lastPublishFor(t, s.barrier(4, mark), opened); got == nil || sigsOf(got) != sigCrit {
		t.Fatalf("the save did not resolve the spelling into its root: %+v", got)
	}
	if err := s.finish(5); err != nil {
		t.Fatal(err)
	}
}

// resolverIdle reports no walk in flight (the hung one has been released).
func resolverIdle(s *Server) bool {
	s.resolver.mu.Lock()
	defer s.resolver.mu.Unlock()
	return s.resolver.running == 0
}

// TestRootChangeRestartsTheWatcher pins item 9: after a folder change the
// watcher runs over the rebuilt read model, so an out-of-band change to the
// added root's bundle republishes without any client message.
func TestRootChangeRestartsTheWatcher(t *testing.T) {
	t.Parallel()
	first, later := fixtureRoot(t), fixtureRoot(t)
	cfg := newConfig()
	cfg.NoWatch = false
	cfg.Interval, cfg.Sweep = 10*time.Millisecond, 20*time.Millisecond
	s, _ := workspaceSession(t, map[string]any{"workspaceFolders": []any{folder(first)}}, cfg)
	s.notify(methodInitialized, map[string]any{})
	s.barrier(2, 0)
	s.notify(methodDidChangeRoots, map[string]any{"event": map[string]any{"added": []any{folder(later)}}})
	notes := fileURI(later, "notes.txt")
	testenv.Until(t, func() bool { return hasLine(summarize(publishes(t, s.snapshot())), notes+" v- "+sigCrit) })
	// The watcher takes its baseline asynchronously, so each poll writes a
	// fresh store (a new rationale each time) until the watcher reports one:
	// the test asserts that it does, not which write it catches first.
	writes := 0
	testenv.Until(t, func() bool {
		writes++
		rewriteTriaged(t, later, writes)
		got := lastPublishFor(t, s.snapshot(), notes)
		return got != nil && len(got.Diagnostics) == 0
	})
	if err := s.finish(3); err != nil {
		t.Fatal(err)
	}
}

// rewriteTriaged rewrites root's bundle with notes.txt's finding triaged
// won't-fix, varying the rationale by n so every write is a content change.
func rewriteTriaged(t *testing.T, root string, n int) {
	t.Helper()
	triaged := strings.Replace(findingJSON(sigCrit, "wont-fix", "critical", "notes.txt", 2),
		`"rationale":""`, `"rationale":"triaged in pass `+strconv.Itoa(n)+`"`, 1)
	writeBundle(t, root, "local-1", triaged, findingJSON(sigImp, "open", "important", "other.go", 4))
}
