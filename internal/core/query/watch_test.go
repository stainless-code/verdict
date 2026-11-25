package query

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/overrides"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/testenv"
)

type watchFileInfo struct {
	os.FileInfo
	modTime time.Time
}

func (f watchFileInfo) ModTime() time.Time { return f.modTime }

// TestTickSeesInPlaceWrite checks a bundle-directory mtime change.
func TestTickSeesInPlaceWrite(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{".verdict/state/1234/post.json": `{"status":"before"}`})
	bundleDir := filepath.Join(root, ".verdict", "state", "1234")
	marks := map[string]time.Time{}
	var mu sync.Mutex
	statCalls := 0
	stat := func(path string) (os.FileInfo, error) {
		mu.Lock()
		statCalls++
		marked := marks[path]
		mu.Unlock()
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if marked.IsZero() {
			return info, nil
		}
		return watchFileInfo{FileInfo: info, modTime: marked}, nil
	}
	base := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	marks[bundleDir] = base
	layer := New(Snapshot{Roots: []Root{{Path: root}}, Runtime: &core.Runtime{Now: fixedNow}}, Options{
		MaxBundles: 10, Stat: stat, ReadDir: os.ReadDir, Clock: fixedNow,
	})
	state := newWatchState(t.Context(), layer)
	statCalls = 0
	marks[bundleDir] = base.Add(time.Second)
	changes := state.Tick()
	if len(changes) != 1 || !slices.Equal(changes[0].Changed, []string{"1234"}) || changes[0].Via != "tick" {
		t.Fatalf("Tick() = %#v, want one tick invalidation for 1234", changes)
	}
	if statCalls != 3 {
		t.Fatalf("tick stat calls = %d, want 2 + N = 3", statCalls)
	}
}

// TestTickStatCostIncludesChunkDirectories checks the per-tick stat budget.
func TestTickStatCostIncludesChunkDirectories(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".verdict/state/1234/post.json":               `{}`,
		".verdict/state/local-1/post.json":            `{}`,
		".verdict/state/local-1/chunks/one.cost.json": `{}`,
	})
	statCalls := 0
	stat := func(path string) (os.FileInfo, error) {
		statCalls++
		return os.Stat(path)
	}
	layer := New(Snapshot{Roots: []Root{{Path: root}}}, Options{
		MaxBundles: 10, Stat: stat, ReadDir: os.ReadDir,
	})
	state := newWatchState(t.Context(), layer)
	statCalls = 0
	if changes := state.Tick(); len(changes) != 0 {
		t.Fatalf("Tick() = %#v, want no changes", changes)
	}
	if statCalls != 5 {
		t.Fatalf("tick stat calls = %d, want 2 + N + K = 5", statCalls)
	}
}

// TestSweepCatchesInPlaceWrite checks entry fingerprints for in-place edits.
func TestSweepCatchesInPlaceWrite(t *testing.T) {
	root, _ := fixture(t)
	layer := New(Snapshot{Roots: []Root{{Path: root}}, Runtime: &core.Runtime{Now: fixedNow}}, Options{
		MaxBundles: 10, Stat: os.Stat, ReadDir: os.ReadDir, Clock: fixedNow,
	})
	state := newWatchState(t.Context(), layer)
	path := filepath.Join(root, ".verdict", "state", "1234", "post.json")
	if err := os.WriteFile(path, []byte(`{"edited":"with a different byte count"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	changes := state.Sweep()
	if len(changes) != 1 || changes[0].Via != "sweep" || !slices.Equal(changes[0].Changed, []string{"1234"}) {
		t.Fatalf("Sweep() = %#v, want one sweep invalidation for 1234", changes)
	}
}

// TestWatchShutdownBounded checks cancellation closes the change stream.
func TestWatchShutdownBounded(t *testing.T) {
	root, _ := fixture(t)
	layer := New(Snapshot{Roots: []Root{{Path: root}}}, Options{Interval: 100 * time.Millisecond, Sweep: time.Second})
	ctx, cancel := context.WithCancel(t.Context())
	changes := layer.Changes(ctx)
	cancel()
	testenv.Until(t, func() bool {
		select {
		case _, open := <-changes:
			return !open
		default:
			return false
		}
	})
}

// TestChangesSnapshotsBeforeReturning pins the arming order: the watcher's
// baseline is taken before Changes returns, so a bundle written once it has
// returned is reported (a caller that composed before the write would
// otherwise never learn of it, the baseline on the goroutine absorbing it),
// and reported once. The first stat, the baseline's, is held until the
// test releases it: Changes returning while it is held means the baseline
// is still to be taken.
func TestChangesSnapshotsBeforeReturning(t *testing.T) {
	root, _ := fixture(t)
	synctest.Test(t, func(t *testing.T) {
		g := newBaselineGate(filepath.Join(root, ".verdict", "state"))
		defer g.open() // a failed run must not leave the baseline held
		layer := New(Snapshot{Roots: []Root{{Path: root}}, Runtime: &core.Runtime{Now: fixedNow}}, Options{
			MaxBundles: 10, Stat: g.stat, ReadDir: os.ReadDir, Clock: fixedNow,
			Interval: time.Second, Sweep: time.Hour,
		})
		g.armed.Store(true)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var changes <-chan Change
		returned := make(chan struct{})
		go func() {
			changes = layer.Changes(ctx)
			close(returned)
		}()
		<-g.started
		synctest.Wait()
		if isClosed(returned) {
			t.Fatal("Changes returned before the baseline snapshot was taken")
		}
		g.open()
		<-returned
		writeFiles(t, root, map[string]string{".verdict/state/9999/post.json": `{}`})
		g.bumped.Store(true)
		wantOneChange(t, changes)
	})
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// wantOneChange wants the next change to be the tick that names bundle 9999,
// and no other for ten more ticks.
func wantOneChange(t *testing.T, changes <-chan Change) {
	t.Helper()
	got := <-changes
	if !slices.Equal(got.Changed, []string{"9999"}) || got.Via != "tick" {
		t.Fatalf("change after Changes returned = %#v, want one tick invalidation for 9999", got)
	}
	select {
	case extra := <-changes:
		t.Fatalf("the write was reported twice: %#v", extra)
	case <-time.After(10 * time.Second):
	}
}

// baselineGate is a stat that holds the first call after arming (the
// baseline's) until opened, and, once bumped, moves the state directory's
// mtime an hour so a write registers whatever the filesystem's clock
// resolution. Its channels belong to the bubble that makes it, so a held
// stat counts as durably blocked.
type baselineGate struct {
	open     func()
	started  chan struct{}
	gate     chan struct{}
	stateDir string
	first    sync.Once
	armed    atomic.Bool
	bumped   atomic.Bool
}

func newBaselineGate(stateDir string) *baselineGate {
	g := &baselineGate{stateDir: stateDir, started: make(chan struct{}), gate: make(chan struct{})}
	g.open = sync.OnceFunc(func() { close(g.gate) })
	return g
}

func (g *baselineGate) stat(path string) (os.FileInfo, error) {
	if g.armed.Load() {
		g.first.Do(func() {
			close(g.started)
			<-g.gate
		})
	}
	info, err := os.Stat(path)
	if err != nil || path != g.stateDir || !g.bumped.Load() {
		return info, err
	}
	return watchFileInfo{FileInfo: info, modTime: info.ModTime().Add(time.Hour)}, nil
}

// TestChangesBaselineHonorsCancellation pins that the baseline walk stops
// when its context ends: a root change or shutdown that cancels while the
// snapshot is on a slow volume must not wait out the rest of the walk, and no
// watcher starts for a canceled subscription.
func TestChangesBaselineHonorsCancellation(t *testing.T) {
	root, _ := fixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var armed atomic.Bool
	var calls atomic.Int64
	stat := func(path string) (os.FileInfo, error) {
		if armed.Load() && calls.Add(1) == 1 {
			cancel()
		}
		return os.Stat(path)
	}
	layer := New(Snapshot{Roots: []Root{{Path: root}}}, Options{MaxBundles: 10, Stat: stat, ReadDir: os.ReadDir})
	armed.Store(true)
	changes := layer.Changes(ctx)
	if _, open := <-changes; open {
		t.Fatal("a canceled subscription delivered a change or stayed open")
	}
	// The first stat canceled the walk. A full baseline of this fixture (the
	// state directory, two bundles with their fingerprints, the overrides
	// directory) makes well over this many calls.
	if n := calls.Load(); n > 3 {
		t.Fatalf("the baseline walk made %d stat calls after cancellation, want it to stop", n)
	}
}

// TestChangesBaselineStopsBeforeTheOverridesWalk: a root with no bundles still
// stops at cancellation instead of walking .verdict/overrides.
func TestChangesBaselineStopsBeforeTheOverridesWalk(t *testing.T) {
	root := rootWithOverridesOnly(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var armed atomic.Bool
	var calls atomic.Int64
	stat := func(path string) (os.FileInfo, error) {
		if armed.Load() && calls.Add(1) == 1 {
			cancel()
		}
		return os.Stat(path)
	}
	layer := New(Snapshot{Roots: []Root{{Path: root}}}, Options{MaxBundles: 10, Stat: stat, ReadDir: os.ReadDir})
	armed.Store(true)
	if _, open := <-layer.Changes(ctx); open {
		t.Fatal("a canceled subscription delivered a change or stayed open")
	}
	if n := calls.Load(); n > 1 {
		t.Fatalf("the baseline walk made %d stat calls after cancellation, want only the state directory's", n)
	}
}

// rootWithOverridesOnly is a repo root with an empty state directory and three
// override files, so the overrides walk is the baseline's only real work.
func rootWithOverridesOnly(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	ovr := filepath.Join(root, filepath.FromSlash(overrides.Dir))
	for _, dir := range []string{readcommon.StateDir(root), ovr} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"a.json", "b.json", "c.json"} {
		if err := os.WriteFile(filepath.Join(ovr, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
