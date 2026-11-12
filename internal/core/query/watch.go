package query

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stainless-code/verdict/internal/overrides"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	watchDebounce    = 250 * time.Millisecond
	chunksDirectory  = "chunks"
	changeBufferSize = 8
)

// MaxEventBundles bounds the names a client receives in one event.
const MaxEventBundles = 100

// Change is one root's bundle invalidation batch.
type Change struct {
	At        time.Time
	Root      string
	Via       string
	Changed   []string
	Removed   []string
	Truncated bool
}

type dirMark struct {
	mtime int64
	ok    bool
}

type bundleWatch struct {
	finger string
	stamp  dirMark
	chunks dirMark
}

type rootWatch struct {
	bundles   map[string]bundleWatch
	contents  string
	state     dirMark
	overrides dirMark
}

type watchState struct {
	layer *Layer
	roots map[string]rootWatch
	mu    sync.Mutex
}

func newWatchState(layer *Layer) *watchState {
	w := &watchState{layer: layer, roots: map[string]rootWatch{}}
	for _, root := range layer.roots {
		w.roots[root.ID] = w.primeRoot(root)
	}
	return w
}

func (w *watchState) primeRoot(root Root) rootWatch {
	stateDir := readcommon.StateDir(root.Path)
	state := rootWatch{state: w.statMark(stateDir), bundles: map[string]bundleWatch{}}
	if names, err := w.bundleNames(root, stateDir); err == nil {
		for _, name := range names {
			state.bundles[name] = w.primeBundle(root, name)
		}
	}
	overridesDir := filepath.Join(root.Path, filepath.FromSlash(overrides.Dir))
	state.overrides = w.statMark(overridesDir)
	state.contents = w.directoryFingerprint(overridesDir, false)
	return state
}

func (w *watchState) primeBundle(root Root, name string) bundleWatch {
	dir := filepath.Join(readcommon.StateDir(root.Path), name)
	chunkDir := filepath.Join(dir, chunksDirectory)
	return bundleWatch{
		stamp:  w.statMark(dir),
		chunks: w.statMark(chunkDir),
		finger: w.directoryFingerprint(dir, true),
	}
}

func (w *watchState) statMark(path string) dirMark {
	info, err := w.layer.stat(path)
	if err != nil {
		return dirMark{}
	}
	return dirMark{mtime: info.ModTime().UnixNano(), ok: true}
}

func (w *watchState) bundleNames(root Root, dir string) ([]string, error) {
	entries, err := w.layer.readDir(dir)
	if err != nil {
		if errorsIsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names, _ := w.layer.capBundles(bundleNames(root, entries))
	slices.Sort(names)
	return names, nil
}

func errorsIsNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

func (w *watchState) Tick() []Change {
	w.mu.Lock()
	defer w.mu.Unlock()
	var changes []Change
	for _, root := range w.layer.roots {
		changes = append(changes, w.tickRoot(root)...)
	}
	return changes
}

func (w *watchState) tickRoot(root Root) []Change {
	old := w.roots[root.ID]
	stateDir := readcommon.StateDir(root.Path)
	newState, removed := w.refreshBundles(root, stateDir, old)
	changed := make([]string, 0)
	for _, name := range sortedBundleKeys(newState.bundles) {
		prior, existed := old.bundles[name]
		current, differs := w.tickBundle(root, stateDir, name, prior, existed)
		newState.bundles[name] = current
		if differs {
			changed = append(changed, name)
		}
	}
	overridesDir := filepath.Join(root.Path, filepath.FromSlash(overrides.Dir))
	newState.overrides = w.statMark(overridesDir)
	if newState.overrides != old.overrides {
		changed = append(changed, sortedBundleKeys(newState.bundles)...)
	}
	w.roots[root.ID] = newState
	return w.emit(root, "tick", changed, removed)
}

func (w *watchState) refreshBundles(root Root, stateDir string, old rootWatch) (rootWatch, []string) {
	stateStamp := w.statMark(stateDir)
	newState := old
	newState.state = stateStamp
	if stateStamp == old.state {
		return newState, nil
	}
	names, err := w.bundleNames(root, stateDir)
	if err != nil {
		newState.state = old.state
		return newState, nil
	}
	newState.bundles = keepBundleSet(old.bundles, names)
	return newState, removedBundles(old.bundles, names)
}

func removedBundles(old map[string]bundleWatch, names []string) []string {
	removed := make([]string, 0)
	for name := range old {
		if !slices.Contains(names, name) {
			removed = append(removed, name)
		}
	}
	return removed
}

func (w *watchState) tickBundle(root Root, stateDir, name string, prior bundleWatch, existed bool) (bundleWatch, bool) {
	current := w.statBundle(root, name, !existed || prior.chunks.ok)
	if !prior.chunks.ok && current.stamp != prior.stamp {
		current.chunks = w.chunkMarkIfPresent(filepath.Join(stateDir, name))
	}
	differs := !existed || current.stamp != prior.stamp || current.chunks != prior.chunks
	current.finger = prior.finger
	return current, differs
}

func (w *watchState) chunkMarkIfPresent(bundleDir string) dirMark {
	entries, err := w.layer.readDir(bundleDir)
	if err != nil {
		return dirMark{}
	}
	for _, entry := range entries {
		if entry.Name() == chunksDirectory && entry.IsDir() {
			return w.statMark(filepath.Join(bundleDir, chunksDirectory))
		}
	}
	return dirMark{}
}

func (w *watchState) statBundle(root Root, name string, checkChunks bool) bundleWatch {
	dir := filepath.Join(readcommon.StateDir(root.Path), name)
	current := bundleWatch{stamp: w.statMark(dir)}
	if checkChunks {
		current.chunks = w.statMark(filepath.Join(dir, chunksDirectory))
	}
	return current
}

func (w *watchState) Sweep() []Change {
	w.mu.Lock()
	defer w.mu.Unlock()
	var changes []Change
	for _, root := range w.layer.roots {
		changes = append(changes, w.sweepRoot(root)...)
	}
	return changes
}

func (w *watchState) sweepRoot(root Root) []Change {
	old := w.roots[root.ID]
	stateDir := readcommon.StateDir(root.Path)
	names, err := w.bundleNames(root, stateDir)
	if err != nil {
		return nil
	}
	newBundles, changed := w.fingerprintBundles(root, names, old.bundles)
	removed := removedBundles(old.bundles, names)
	overridesDir := filepath.Join(root.Path, filepath.FromSlash(overrides.Dir))
	overridesFinger := w.directoryFingerprint(overridesDir, false)
	if overridesFinger != old.contents {
		changed = append(changed, names...)
	}
	w.roots[root.ID] = rootWatch{
		state: w.statMark(stateDir), overrides: w.statMark(overridesDir),
		contents: overridesFinger, bundles: newBundles,
	}
	return w.emit(root, "sweep", changed, removed)
}

func (w *watchState) fingerprintBundles(root Root, names []string, old map[string]bundleWatch) (map[string]bundleWatch, []string) {
	newBundles := make(map[string]bundleWatch, len(names))
	changed := make([]string, 0)
	for _, name := range names {
		current := w.primeBundle(root, name)
		prior, existed := old[name]
		if !existed || current.finger != prior.finger {
			changed = append(changed, name)
		}
		newBundles[name] = current
	}
	return newBundles, changed
}

func (w *watchState) directoryFingerprint(dir string, chunks bool) string {
	var out strings.Builder
	info, err := w.layer.stat(dir)
	if err != nil {
		appendStatFailure(&out, err)
		return out.String()
	}
	fingerprintRow(&out, ".", info)
	entries, err := w.layer.readDir(dir)
	if err != nil {
		out.WriteString("!read\n")
		return out.String()
	}
	slices.SortFunc(entries, func(left, right os.DirEntry) int {
		return strings.Compare(left.Name(), right.Name())
	})
	for _, entry := range entries {
		w.appendFingerprintEntry(&out, dir, entry, chunks)
	}
	return out.String()
}

func appendStatFailure(out *strings.Builder, err error) {
	if !errorsIsNotExist(err) {
		out.WriteString("!stat\n")
	}
}

func (w *watchState) appendFingerprintEntry(out *strings.Builder, dir string, entry os.DirEntry, chunks bool) {
	name := entry.Name()
	path := filepath.Join(dir, name)
	child, err := w.layer.stat(path)
	if err != nil {
		fmt.Fprintf(out, "%s !stat\n", name)
		return
	}
	fingerprintRow(out, name, child)
	if chunks && name == chunksDirectory && child.IsDir() {
		out.WriteString(w.directoryFingerprint(path, false))
	}
}

func fingerprintRow(out *strings.Builder, name string, info os.FileInfo) {
	fmt.Fprintf(out, "%s\x00%d\x00%d\n", name, info.Size(), info.ModTime().UnixNano())
}

func (w *watchState) emit(root Root, via string, changed, removed []string) []Change {
	changed = uniqueSorted(changed)
	removed = uniqueSorted(removed)
	if len(changed) == 0 && len(removed) == 0 {
		return nil
	}
	if len(changed) > 0 || len(removed) > 0 {
		w.layer.evictBundles(root.ID, append(slices.Clone(changed), removed...))
	}
	truncated := len(changed) > MaxEventBundles
	if truncated {
		changed = changed[:MaxEventBundles]
	}
	if len(removed) > MaxEventBundles-len(changed) {
		removed = removed[:MaxEventBundles-len(changed)]
		truncated = true
	}
	return []Change{{At: w.layer.clock(), Root: root.ID, Via: via, Changed: changed, Removed: removed, Truncated: truncated}}
}

func keepBundleSet(old map[string]bundleWatch, names []string) map[string]bundleWatch {
	set := make(map[string]bundleWatch, len(names))
	for _, name := range names {
		set[name] = old[name]
	}
	return set
}

func sortedBundleKeys(values map[string]bundleWatch) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	slices.Sort(values)
	return slices.Compact(values)
}

func (l *Layer) evictBundles(root string, names []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key := range l.cache {
		if key.root == root && slices.Contains(names, key.bundle) {
			delete(l.cache, key)
		}
	}
}

// Changes returns invalidations until ctx is canceled.
// With watching disabled, the returned channel is already closed.
func (l *Layer) Changes(ctx context.Context) <-chan Change {
	out := make(chan Change, changeBufferSize)
	if l.noWatch {
		close(out)
		return out
	}
	go l.runChanges(ctx, out)
	return out
}

type changeBatch struct {
	timer   *time.Timer
	ready   <-chan time.Time
	pending []Change
}

func (b *changeBatch) add(changes []Change) {
	if len(changes) == 0 {
		return
	}
	b.pending = append(b.pending, changes...)
	if b.ready == nil {
		b.timer = time.NewTimer(watchDebounce)
		b.ready = b.timer.C
	}
}

func (b *changeBatch) flush(ctx context.Context, out chan<- Change) bool {
	for _, change := range b.pending {
		select {
		case out <- change:
		case <-ctx.Done():
			return false
		}
	}
	b.pending = nil
	b.timer = nil
	b.ready = nil
	return true
}

func (b *changeBatch) stop() {
	if b.timer != nil {
		b.timer.Stop()
	}
}

func (l *Layer) runChanges(ctx context.Context, out chan<- Change) {
	defer close(out)
	state := newWatchState(l)
	interval := time.NewTicker(l.interval)
	defer interval.Stop()
	sweep := time.NewTicker(l.sweep)
	defer sweep.Stop()
	batch := changeBatch{}
	defer batch.stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-interval.C:
			batch.add(state.Tick())
		case <-sweep.C:
			batch.add(state.Sweep())
		case <-batch.ready:
			if !batch.flush(ctx, out) {
				return
			}
		}
	}
}
