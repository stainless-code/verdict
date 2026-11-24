package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stainless-code/verdict/internal/core/query"
)

// failingModel fails Detail or Diff with a non-cancellation error while armed.
type failingModel struct {
	ReadModel
	mu      sync.Mutex
	detail  bool
	diff    bool
	bundles bool
}

func (f *failingModel) failBundles(on bool) {
	f.mu.Lock()
	f.bundles = on
	f.mu.Unlock()
}

func (f *failingModel) Bundles(ctx context.Context, p string) (*query.View, error) {
	f.mu.Lock()
	fail := f.bundles
	f.mu.Unlock()
	if fail {
		return nil, errTransient
	}
	return f.ReadModel.Bundles(ctx, p)
}

var errTransient = errors.New("store busy for a moment")

func (f *failingModel) set(detail, diff bool) {
	f.mu.Lock()
	f.detail, f.diff = detail, diff
	f.mu.Unlock()
}

func (f *failingModel) Detail(ctx context.Context, r, b string) (*query.View, error) {
	f.mu.Lock()
	fail := f.detail
	f.mu.Unlock()
	if fail {
		return nil, errTransient
	}
	return f.ReadModel.Detail(ctx, r, b)
}

func (f *failingModel) Diff(ctx context.Context, r, b string) (*query.View, error) {
	f.mu.Lock()
	fail := f.diff
	f.mu.Unlock()
	if fail {
		return nil, errTransient
	}
	return f.ReadModel.Diff(ctx, r, b)
}

// TestTransientFailureKeepsLastGood pins item 2: a Detail or Diff read that
// fails for any reason but cancellation or a vanished bundle keeps the last
// good diagnostics, logs one Warning, and publishes nothing.
func TestTransientFailureKeepsLastGood(t *testing.T) {
	t.Parallel()
	t.Run("detail fails", func(t *testing.T) { t.Parallel(); checkKeepsLastGood(t, true, false) })
	t.Run("diff fails", func(t *testing.T) { t.Parallel(); checkKeepsLastGood(t, false, true) })
}

func checkKeepsLastGood(t *testing.T, detail, diff bool) {
	t.Helper()
	root := fixtureRoot(t)
	cfg := newConfig(root)
	failing := &failingModel{ReadModel: productionLayer(cfg, []servedRoot{newServedRoot(root, false)})}
	cfg.Layer = failing
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	mark := len(s.barrier(2, 0)) + 1
	// A rewrite moves the digest, so the diff is re-read too.
	writeBundle(t, root, "local-1", findingJSON(sigImp, "open", "important", "other.go", 4))
	failing.set(detail, diff)
	s.request(3, methodExecuteCommand, map[string]any{"command": CommandRefresh})
	before, resp := s.awaitResponse(3)
	frames := before[mark:]
	texts := strings.Join(logs(t, frames), "\n")
	if countMethod(frames, methodPublish) != 0 || countMethod(frames, methodLogMessage) != 1 ||
		!strings.Contains(texts, "keeping the last good diagnostics") || !strings.Contains(texts, errTransient.Error()) {
		t.Fatalf("frames: %d publishes, logs %q; want no publish and one warning", countMethod(frames, methodPublish), texts)
	}
	if !strings.Contains(string(resp.Result), `"findings":2`) {
		t.Fatalf("refresh reported %s, want the last good two findings", resp.Result)
	}
	failing.set(false, false)
	if got := refreshPublishes(t, s, 4); got != fileURI(root, "notes.txt")+" v- " {
		t.Fatalf("recovery published\n%s\nwant notes.txt cleared by the real rewrite", got)
	}
	if err := s.finish(5); err != nil {
		t.Fatal(err)
	}
}

// refreshPublishes runs verdict.refresh as request id and summarizes the
// publishes it sent.
func refreshPublishes(t *testing.T, s *session, id int) string {
	t.Helper()
	mark := len(s.snapshot())
	s.request(id, methodExecuteCommand, map[string]any{"command": CommandRefresh})
	before, _ := s.awaitResponse(id)
	return summarize(publishes(t, before[mark:]))
}

// TestCorruptStoreKeepsLastGood pins the same guarantee through the real
// read model: a findings.json caught mid-write (unparseable) refuses
// corrupt-store, and the published set stays.
func TestCorruptStoreKeepsLastGood(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	s := startSession(t, newConfig(root))
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	uri := fileURI(root, "notes.txt")
	openDoc(s, uri, 1, notesText)
	mark := len(s.barrier(2, 0)) + 1
	writeFile(t, root, ".verdict/state/local-1/findings.json", `[{"sig":`)
	s.notify(methodDidSave, map[string]any{"textDocument": map[string]any{"uri": uri}})
	frames := s.barrier(3, mark)
	texts := logs(t, frames)
	if countMethod(frames, methodPublish) != 0 || len(texts) != 1 || !strings.Contains(texts[0], "keeping the last good") ||
		!strings.Contains(texts[0], "corrupt-store") {
		t.Fatalf("a corrupt store published %d frames, logs %q", countMethod(frames, methodPublish), logs(t, frames))
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestGoneBundleClears pins the one failure that does clear: the bundle
// directory is gone, so reselection finds nothing and every set it
// published is cleared once.
func TestGoneBundleClears(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	s := startSession(t, newConfig(root))
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	mark := len(s.barrier(2, 0)) + 1
	if err := os.RemoveAll(filepath.Join(root, ".verdict", "state", "local-1")); err != nil {
		t.Fatal(err)
	}
	s.request(3, methodExecuteCommand, map[string]any{"command": CommandRefresh})
	before, _ := s.awaitResponse(3)
	want := fileURI(root, "notes.txt") + " v- \n" + fileURI(root, "other.go") + " v- "
	if got := summarize(publishes(t, before[mark:])); got != want {
		t.Fatalf("a gone bundle published\n%s\nwant both files cleared", got)
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestGonePinnedBundleClears pins the explicit-bundle path of the same rule:
// with --bundle the vanished bundle answers not-found and clears.
func TestGonePinnedBundleClears(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	cfg := newConfig(root)
	cfg.Bundle = "local-1"
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	mark := len(s.barrier(2, 0)) + 1
	if err := os.RemoveAll(filepath.Join(root, ".verdict", "state", "local-1")); err != nil {
		t.Fatal(err)
	}
	s.request(3, methodExecuteCommand, map[string]any{"command": CommandRefresh})
	before, _ := s.awaitResponse(3)
	frames := before[mark:]
	if countMethod(frames, methodPublish) != 2 || !strings.Contains(strings.Join(logs(t, frames), "\n"), "is gone") {
		t.Fatalf("a gone pinned bundle: %d publishes, logs %q", countMethod(frames, methodPublish), logs(t, frames))
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestFailedListingKeepsSelection pins the selection side of item 2: a
// bundle listing that fails keeps the current bundle and its diagnostics
// instead of reading as "no bundle".
func TestFailedListingKeepsSelection(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	cfg := newConfig(root)
	failing := &failingModel{ReadModel: productionLayer(cfg, []servedRoot{newServedRoot(root, false)})}
	cfg.Layer = failing
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	mark := len(s.barrier(2, 0)) + 1
	failing.failBundles(true)
	s.request(3, methodExecuteCommand, map[string]any{"command": CommandRefresh})
	before, resp := s.awaitResponse(3)
	texts := strings.Join(logs(t, before[mark:]), "\n")
	if countMethod(before[mark:], methodPublish) != 0 || !strings.Contains(texts, "keeping the current diagnostics") {
		t.Fatalf("a failed listing: %d publishes, logs %q", countMethod(before[mark:], methodPublish), texts)
	}
	if !strings.Contains(string(resp.Result), `"bundle":"local-1","findings":2`) {
		t.Fatalf("refresh after a failed listing = %s, want the kept selection", resp.Result)
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}
