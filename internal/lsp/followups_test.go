package lsp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/testenv"
)

var errNeverResolved = errors.New("the volume never answered")

// queuedLogs lists the log lines a server has queued and not yet flushed
// (nothing flushes before initialize).
func queuedLogs(s *Server) []string {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	out := make([]string, 0, len(s.pendLogs))
	for _, l := range s.pendLogs {
		out = append(out, l.Message)
	}
	return out
}

// hungServer is a server whose every path walk blocks until the returned
// release runs, then resolves the path to itself. It runs on the real budget:
// inside a synctest bubble that budget is a fake clock, so a timeout costs no
// wall time and elapsed time is exact. The gate is made in the bubble, so a
// blocked walk counts as durably blocked.
func hungServer(cfg *Config) (*Server, func()) {
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	cfg.expire = nil
	cfg.eval = func(p string) (string, error) { <-gate; return p, nil }
	return New(cfg), release
}

// expectf fails the test, and carries on, when ok is false.
func expectf(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Errorf(format, args...)
	}
}

// TestWalkMissMessagesNameTheirCause pins item 2: a walk that ran past the
// budget says so, and a walk that was never started because every slot was
// held says that instead of blaming a budget it never used.
func TestWalkMissMessagesNameTheirCause(t *testing.T) {
	synctest.Test(t, walkMissMessages)
}

func walkMissMessages(t *testing.T) {
	s, release := hungServer(newConfig())
	defer release()
	for i := range maxResolvers + 1 {
		s.physicalOf(fmt.Sprintf("/vol/%d", i))
	}
	got := queuedLogs(s)
	if len(got) != maxResolvers+1 {
		t.Fatalf("%d lines queued, want %d: %q", len(got), maxResolvers+1, got)
	}
	expectf(t, strings.Contains(got[0], "took longer than 250ms"), "a hung walk logged %q, want the budget message", got[0])
	last := got[maxResolvers]
	expectf(t, !strings.Contains(last, "took longer"), "a walk with no free slot blamed the budget: %q", last)
	expectf(t, strings.Contains(last, "/vol/8 was skipped: all 8 filesystem walks are still outstanding"),
		"a walk with no free slot logged %q, want the no-slot message", last)
}

// TestSlowBookkeepingIsPruned pins item 3: a path that resolves is forgotten,
// and a volume that keeps missing on new paths never grows the table past its
// bound.
func TestSlowBookkeepingIsPruned(t *testing.T) {
	synctest.Test(t, slowBookkeeping)
}

func slowBookkeeping(t *testing.T) {
	s, release := hungServer(newConfig())
	defer release()
	s.physicalOf("/vol/slow")
	expectf(t, len(s.slow) == 1, "slow table has %d entries after a miss, want 1", len(s.slow))
	release() // the volume answers
	synctest.Wait()
	phys, ok := s.physicalOf("/vol/slow")
	expectf(t, ok && phys == "/vol/slow", "the path did not resolve once the volume answered: %q %v", phys, ok)
	expectf(t, len(s.slow) == 0, "slow table keeps %d entries after the path resolved, want 0", len(s.slow))
	for i := range 3 * maxSlowPaths {
		s.noteMiss(fmt.Sprintf("/vol/%d", i), missBudget)
	}
	expectf(t, len(s.slow) <= maxSlowPaths, "slow table grew to %d entries, want at most %d", len(s.slow), maxSlowPaths)
}

// TestRootsResolveConcurrently pins item 4: N roots on a slow volume cost one
// round, not N, and every root keeps its input position. The walks answer
// only once all of them are in flight, so a sequential resolver spends a
// budget on each in turn and returns unresolved roots.
func TestRootsResolveConcurrently(t *testing.T) {
	synctest.Test(t, rootsResolveConcurrently)
}

func rootsResolveConcurrently(t *testing.T) {
	paths := []string{"/vol/a", "/vol/b", "/vol/c", "/vol/d"}
	var arrived atomic.Int64
	all := make(chan struct{})
	var closeAll sync.Once
	defer closeAll.Do(func() { close(all) }) // sequential resolution never meets the rendezvous
	cfg := newConfig()
	cfg.expire = nil
	cfg.eval = func(p string) (string, error) {
		if arrived.Add(1) == int64(len(paths)) {
			closeAll.Do(func() { close(all) })
		}
		<-all
		return p + "-physical", nil
	}
	s := New(cfg)
	start := time.Now()
	roots := s.rootsOf(paths)
	expectf(t, time.Since(start) == 0, "resolving %d roots took %s, want the walks to overlap", len(paths), time.Since(start))
	want := []string{"/vol/a /vol/a-physical", "/vol/b /vol/b-physical", "/vol/c /vol/c-physical", "/vol/d /vol/d-physical"}
	got := rootSpellings(roots)
	expectf(t, slices.Equal(got, want), "roots = %q, want %q", got, want)
}

func rootSpellings(roots []servedRoot) []string {
	out := make([]string, 0, len(roots))
	for i := range roots {
		out = append(out, roots[i].slash+" "+roots[i].physical)
	}
	return out
}

// TestHungRootsAreBoundedAndOrdered pins item 4's bounds: with every walk
// hung, no more than maxResolvers walks start, the batch costs one budget,
// and the log lines name each root in input order with the cause that fits:
// the budget for the walks that started, the full slot table for the rest.
func TestHungRootsAreBoundedAndOrdered(t *testing.T) {
	synctest.Test(t, hungRoots)
}

func hungRoots(t *testing.T) {
	var started atomic.Int64
	gate := make(chan struct{})
	defer close(gate)
	cfg := newConfig()
	cfg.expire = nil
	cfg.MaxRoots = MaxRoots
	cfg.eval = func(string) (string, error) { started.Add(1); <-gate; return "", errNeverResolved }
	s := New(cfg)
	var paths []string
	for i := range maxResolvers + 2 {
		paths = append(paths, fmt.Sprintf("/vol/%02d", i))
	}
	start := time.Now()
	roots := s.rootsOf(paths)
	expectf(t, time.Since(start) == resolveBudget, "a hung batch took %s, want one budget (%s)", time.Since(start), resolveBudget)
	expectf(t, started.Load() == maxResolvers, "%d walks started, want exactly the %d slots", started.Load(), maxResolvers)
	expectf(t, len(roots) == len(paths) && roots[0].slash == "/vol/00" && roots[len(roots)-1].slash == "/vol/09",
		"roots out of order or missing: %d roots", len(roots))
	checkMissLines(t, queuedLogs(s), len(paths))
}

// checkMissLines wants one line per path in input order: the budget message
// for the first maxResolvers, the no-slot message for the rest.
func checkMissLines(t *testing.T, lines []string, n int) {
	t.Helper()
	if len(lines) != n {
		t.Fatalf("%d lines queued, want %d: %q", len(lines), n, lines)
	}
	for i, line := range lines {
		kind, other := "took longer", "skipped"
		if i >= maxResolvers {
			kind, other = other, kind
		}
		if !strings.Contains(line, fmt.Sprintf("/vol/%02d", i)) || !strings.Contains(line, kind) || strings.Contains(line, other) {
			t.Errorf("line %d = %q, want it to name /vol/%02d with the %q message", i, line, i, kind)
		}
	}
}

// TestClientRootsAreVettedConcurrently pins item 4 for the checkout probes
// at initialize: N hung probes cost one budget and every drop warning names
// its root in candidate order.
func TestClientRootsAreVettedConcurrently(t *testing.T) {
	synctest.Test(t, clientRootsVetted)
}

func clientRootsVetted(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	cfg := newConfig()
	cfg.expire = nil
	cfg.checkout = func(string) string { <-gate; return "" }
	s := New(cfg)
	cands := []rootCandidate{{uri: "file:///vol/a"}, {uri: "file:///vol/b"}, {uri: "file:///vol/c"}}
	start := time.Now()
	kept := s.acceptedRoots(cands)
	expectf(t, time.Since(start) == resolveBudget, "three hung probes took %s, want one budget (%s)", time.Since(start), resolveBudget)
	expectf(t, len(kept) == 0, "hung roots were kept: %q", kept)
	got := queuedLogs(s)
	if len(got) != 3 {
		t.Fatalf("%d warnings, want 3: %q", len(got), got)
	}
	for i, want := range []string{"/vol/a", "/vol/b", "/vol/c"} {
		expectf(t, strings.Contains(got[i], "workspace root "+want+" dropped"), "warning %d = %q, want it to name %s", i, got[i], want)
	}
}

// firstLogBeforeFirstPublish reports the frame indexes of the first log and
// the first publish in frames (-1 when absent).
func firstLogBeforeFirstPublish(frames []wireFrame) (logAt, publishAt int) {
	logAt, publishAt = -1, -1
	for i := range frames {
		if frames[i].Method == methodLogMessage && logAt < 0 {
			logAt = i
		}
		if frames[i].Method == methodPublish && publishAt < 0 {
			publishAt = i
		}
	}
	return logAt, publishAt
}

// TestSlowWalkLogsBeforeThePublish pins item 3's prompt flush: once the
// session is up, the slow-walk line goes out when the walk misses, not after
// the compose and publish the notification goes on to do.
func TestSlowWalkLogsBeforeThePublish(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	gate := newGatedEval(t, "notes.txt")
	cfg := newConfig(root)
	cfg.eval, cfg.expire = gate.eval, gate.expire
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	mark := len(s.barrier(2, 0)) + 1
	gate.armed.Store(true)
	openDoc(s, fileURI(root, "notes.txt"), 1, notesText)
	logAt, publishAt := firstLogBeforeFirstPublish(s.barrier(3, mark))
	if logAt < 0 || publishAt < 0 || logAt > publishAt {
		t.Fatalf("slow-walk log at frame %d, publish at %d; want the log first", logAt, publishAt)
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestSlowRootWalkLogsBeforeTheRootsInstall pins the same flush for a folder
// change: the slow-walk line for an added root goes out when its walk misses,
// ahead of the republish the install triggers (the removed root's files
// clearing), not after it.
func TestSlowRootWalkLogsBeforeTheRootsInstall(t *testing.T) {
	t.Parallel()
	first, second := fixtureRoot(t), checkout(t)
	gate := newGatedEval(t, second)
	gate.armed.Store(true)
	cfg := newConfig()
	cfg.eval, cfg.expire = gate.eval, gate.expire
	s, _ := workspaceSession(t, map[string]any{"workspaceFolders": []any{folder(first)}}, cfg)
	s.notify(methodInitialized, map[string]any{})
	mark := len(s.barrier(2, 0)) + 1
	s.notify(methodDidChangeRoots, map[string]any{"event": map[string]any{
		"added": []any{folder(second)}, "removed": []any{folder(first)},
	}})
	cleared := fileURI(first, "notes.txt") + " v- "
	testenv.Until(t, func() bool { return hasLine(summarize(publishes(t, s.snapshot()[mark:])), cleared) })
	logAt, publishAt := firstLogBeforeFirstPublish(s.snapshot()[mark:])
	if logAt < 0 || logAt > publishAt {
		t.Fatalf("slow-walk log at frame %d, first publish at %d; want the log first", logAt, publishAt)
	}
	if err := s.finish(3); err != nil {
		t.Fatal(err)
	}
}

// gatedChanges is a read model whose watcher baseline (Changes) blocks, from
// the call after the first, until released or the subscription's context
// ends: a baseline walking a slow volume.
type gatedChanges struct {
	ReadModel
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int64
	once    sync.Once
}

func (g *gatedChanges) Changes(ctx context.Context) <-chan query.Change {
	if g.calls.Add(1) > 1 {
		g.once.Do(func() { close(g.entered) })
		select {
		case <-g.release:
		case <-ctx.Done():
		}
	}
	return g.ReadModel.Changes(ctx)
}

// TestSlowWatcherBaselineNeverStallsTheReadLoop pins the arming order's other
// half: the baseline snapshot runs holding no session lock, so a folder
// change whose watcher restart is stuck on a slow volume leaves the read
// loop free: a didChange, a $/cancelRequest, and a request behind it are all
// handled while the baseline is held.
func TestSlowWatcherBaselineNeverStallsTheReadLoop(t *testing.T) {
	t.Parallel()
	first, second := fixtureRoot(t), checkout(t)
	cfg := newConfig()
	cfg.NoWatch = false
	gated := &gatedChanges{
		ReadModel: productionLayer(cfg, []servedRoot{newServedRoot(first, false)}),
		entered:   make(chan struct{}), release: make(chan struct{}),
	}
	t.Cleanup(func() { close(gated.release) })
	cfg.Layer = gated
	s, _ := workspaceSession(t, map[string]any{"workspaceFolders": []any{folder(first)}}, cfg)
	s.notify(methodInitialized, map[string]any{})
	uri := fileURI(first, "notes.txt")
	openDoc(s, uri, 1, notesText)
	mark := len(s.barrier(2, 0)) + 1
	s.notify(methodDidChangeRoots, map[string]any{"event": map[string]any{"added": []any{folder(second)}}})
	<-gated.entered // the restarted watcher's baseline is now held
	s.notify(methodDidChange, map[string]any{
		"textDocument": map[string]any{"uri": uri, "version": 2}, "contentChanges": []any{map[string]any{"text": "y\n"}},
	})
	s.notify(methodCancel, map[string]any{"id": 99})
	s.barrier(3, mark) // answered only if the read loop is free
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestCancelDuringAHungResolveEndsTheSession pins the session context
// reaching the resolver: a folder change whose walk never answers (and whose
// budget never runs out here) must not hold Serve open after the session is
// canceled.
func TestCancelDuringAHungResolveEndsTheSession(t *testing.T) {
	t.Parallel()
	first, second := fixtureRoot(t), checkout(t)
	gate := newGatedEval(t, second)
	gate.armed.Store(true)
	cfg := newConfig() // its budget never runs out
	cfg.eval = gate.eval
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s := startSessionCtx(ctx, t, cfg)
	s.request(1, methodInitialize, map[string]any{
		"capabilities": map[string]any{}, "workspaceFolders": []any{folder(first)},
	})
	s.awaitResponse(1)
	s.notify(methodInitialized, map[string]any{})
	s.barrier(2, 0)
	s.notify(methodDidChangeRoots, map[string]any{"event": map[string]any{"added": []any{folder(second)}}})
	testenv.Until(t, func() bool { return gate.blocked.Load() > 0 })
	cancel()
	// wait fails the test if Serve does not return: it is stuck on the walk.
	if err := s.wait(); err != nil {
		t.Fatalf("Serve = %v after cancel, want nil (the cancel is reported by Canceled)", err)
	}
}
