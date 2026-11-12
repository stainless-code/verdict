package query

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
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
	state := newWatchState(layer)
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
	state := newWatchState(layer)
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
	state := newWatchState(layer)
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
