package lsp

import (
	"context"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/testenv"
)

// hangingEval is a resolver that blocks on paths containing needle until
// released, counting every call: a hung volume, observable.
type hangingEval struct {
	entered chan struct{}
	release chan struct{}
	needle  string
	calls   atomic.Int64
}

func (h *hangingEval) eval(p string) (string, error) {
	h.calls.Add(1)
	if h.needle != "" && strings.Contains(p, h.needle) {
		h.entered <- struct{}{}
		<-h.release
	}
	return evalOS(p)
}

// TestPathResolutionNeverHoldsStateMu pins item 1: a didOpen whose path
// resolution hangs (a hung volume) holds no session lock while it waits, so
// publishes, keystrokes, and the watch pump are not stalled behind it.
func TestPathResolutionNeverHoldsStateMu(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	hang := &hangingEval{entered: make(chan struct{}, 1), release: make(chan struct{}), needle: "hang.txt"}
	cfg := newConfig(root)
	cfg.eval = hang.eval
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	s.barrier(2, 0)
	openDoc(s, fileURI(root, "hang.txt"), 1, "x\n")
	testenv.Until(t, func() bool {
		select {
		case <-hang.entered:
			return true
		default:
			return false
		}
	})
	if !s.server.stateMu.TryLock() {
		close(hang.release)
		t.Fatal("stateMu is held while a document path resolves")
	}
	s.server.stateMu.Unlock()
	close(hang.release)
	if err := s.finish(3); err != nil {
		t.Fatal(err)
	}
}

// TestSaveReusesTheOpenResolution pins item 6: a save of an open document
// reuses the physical spelling resolved when it opened instead of walking
// the filesystem again.
func TestSaveReusesTheOpenResolution(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	spy := &hangingEval{}
	cfg := newConfig(root)
	cfg.eval = spy.eval
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	uri := fileURI(root, "notes.txt")
	openDoc(s, uri, 1, notesText)
	s.barrier(2, 0)
	before := spy.calls.Load()
	for range 3 {
		s.notify(methodDidSave, map[string]any{"textDocument": map[string]any{"uri": uri}})
	}
	s.barrier(3, 0)
	if after := spy.calls.Load(); after != before {
		t.Fatalf("three saves of an open document resolved its path %d more times", after-before)
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestRootChangeNeverStallsTheReadLoop pins item 2: a workspace-folder
// change that arrives while a composition is blocked is applied without
// waiting for it, so the $/cancelRequest behind it is read at once; the
// added root then publishes once the composition lets go.
func TestRootChangeNeverStallsTheReadLoop(t *testing.T) {
	t.Parallel()
	first, later := fixtureRoot(t), fixtureRoot(t)
	cfg := newConfig()
	blocking := &blockingModel{
		ReadModel: productionLayer(cfg, []servedRoot{newServedRoot(first, false), newServedRoot(later, false)}),
		entered:   make(chan struct{}, 4),
		release:   make(chan struct{}),
	}
	cfg.Layer = blocking
	s, _ := workspaceSession(t, map[string]any{"workspaceFolders": []any{folder(first)}}, cfg)
	s.notify(methodInitialized, map[string]any{})
	s.barrier(2, 0)
	blocking.arm()
	s.request(3, methodExecuteCommand, map[string]any{"command": CommandRefresh})
	testenv.Until(t, func() bool { return len(blocking.entered) > 0 })
	s.notify(methodDidChangeRoots, map[string]any{"event": map[string]any{"added": []any{folder(later)}}})
	s.notify(methodCancel, map[string]any{"id": 3})
	_, refresh := s.awaitResponse(3)
	wantError(t, "the refresh behind the folder change", &refresh, rpcRequestCancelled)
	close(blocking.release)
	added := fileURI(later, "notes.txt") + " v- " + sigCrit
	testenv.Until(t, func() bool { return strings.Contains(summarize(publishes(t, s.snapshot())), added) })
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestBackgroundGateClosesAtTeardown pins item 3 deterministically: once
// Serve has torn down, no background work can start, so nothing can Add to
// the group its final Wait already passed.
func TestBackgroundGateClosesAtTeardown(t *testing.T) {
	t.Parallel()
	s := startSession(t, newConfig(fixtureRoot(t)))
	s.initialize(1, nil)
	if err := s.finish(2); err != nil {
		t.Fatal(err)
	}
	if s.server.goBackground(func() {}) || s.server.goDrain(func() {}) {
		t.Fatal("background work started after teardown")
	}
	s.server.deferCompose(context.Background(), s.server.roots)
}

// TestCancelDuringSaveBurst is the -race stress for item 3: the plain Serve
// is canceled in the middle of a burst of saves (some meeting a busy
// composition and queuing background re-reads) while its read goroutine
// may still be dispatching; it must never panic or race.
func TestCancelDuringSaveBurst(t *testing.T) {
	t.Parallel()
	for range 20 {
		cancelDuringBurst(t)
	}
}

func cancelDuringBurst(t *testing.T) {
	t.Helper()
	root := fixtureRoot(t)
	cfg := newConfig(root)
	cfg.NoWatch = false
	cfg.Interval, cfg.Sweep = time.Millisecond, time.Millisecond
	server := New(cfg)
	inR, inW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { writeSaveBurst(inW, fileURI(root, "notes.txt"), cancel) })
	if err := server.Serve(ctx, inR, io.Discard); err != nil {
		t.Fatalf("Serve = %v", err)
	}
	cancel()
	_ = inW.Close()
	_ = inR.Close()
	wg.Wait()
}

// writeSaveBurst writes the handshake and 200 saves, canceling halfway.
func writeSaveBurst(w io.Writer, uri string, cancel func()) {
	frames := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"initialized","params":{}}`,
	}
	save := `{"jsonrpc":"2.0","method":"textDocument/didSave","params":{"textDocument":{"uri":"` + uri + `"}}}`
	for range 200 {
		frames = append(frames, save)
	}
	for i, f := range frames {
		if i == 100 {
			cancel()
		}
		if _, err := w.Write(encodeFrame([]byte(f))); err != nil {
			return
		}
	}
}

// TestRekeyMergeReleasesText pins item 4: when a root change makes two open
// documents name one file, the merged one's tracked text is released (the
// session total stays exact), every spelling still maps to the survivor,
// and one line says so.
func TestRekeyMergeReleasesText(t *testing.T) {
	t.Parallel()
	s := New(newConfig())
	a := &document{uri: "file:///w/a.go", key: "k1", uris: map[string]bool{"file:///w/a.go": true}}
	b := &document{uri: "file:///w/./a.go", key: "k2", uris: map[string]bool{"file:///w/./a.go": true}}
	text := "package a\n"
	s.setText(a, &text)
	s.setText(b, &text)
	s.docs = map[string]*document{"k1": a, "k2": b}
	s.uriKeys = map[string]string{a.uri: "k1", b.uri: "k2"}
	s.stateMu.Lock()
	s.rekeyDocs()
	s.stateMu.Unlock()
	if len(s.docs) != 1 || s.docBytes != len(text) {
		t.Fatalf("after the merge: %d documents, %d tracked bytes; want 1 and %d", len(s.docs), s.docBytes, len(text))
	}
	survivor := s.docs[s.uriKeys[a.uri]]
	if survivor == nil || s.uriKeys[a.uri] != s.uriKeys[b.uri] || !survivor.uris[a.uri] || !survivor.uris[b.uri] {
		t.Fatalf("spellings after the merge: uriKeys %v, survivor %+v", s.uriKeys, survivor)
	}
}

// TestCloseOneSpellingKeepsTheOther pins item 5: with one file open under two
// spellings, closing one leaves the document tracked, so a didChange for the
// other still lands and the next save publishes under the remaining
// spelling; closing the last one drops it.
func TestCloseOneSpellingKeepsTheOther(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	s := startSession(t, newConfig(root))
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	plain := fileURI(root, "notes.txt")
	local := "file://localhost" + strings.TrimPrefix(plain, "file://")
	openDoc(s, plain, 1, notesText)
	openDoc(s, local, 2, notesText)
	s.notify(methodDidClose, map[string]any{"textDocument": map[string]any{"uri": local}})
	s.notify(methodDidChange, map[string]any{
		"textDocument":   map[string]any{"uri": plain, "version": 7},
		"contentChanges": []any{map[string]any{"text": notesText}},
	})
	mark := len(s.barrier(2, 0)) + 1
	s.notify(methodDidSave, map[string]any{"textDocument": map[string]any{"uri": plain}})
	// The closed spelling was the one last published under, so the save
	// clears it and publishes under the spelling still open.
	want := local + " v- \n" + plain + " v7 " + sigCrit
	if got := summarize(publishes(t, s.barrier(3, mark))); got != want {
		t.Fatalf("after closing one spelling the save published\n%s\nwant\n%s", got, want)
	}
	s.notify(methodDidClose, map[string]any{"textDocument": map[string]any{"uri": plain}})
	s.barrier(4, 0)
	s.server.stateMu.Lock()
	open, bytes := len(s.server.docs), s.server.docBytes
	s.server.stateMu.Unlock()
	if open != 0 || bytes != 0 {
		t.Fatalf("after closing every spelling: %d documents, %d bytes tracked", open, bytes)
	}
	if err := s.finish(5); err != nil {
		t.Fatal(err)
	}
}

// TestUNCRootIsNeverProbed pins item 8: a UNC root's slash spelling has its
// host stripped, so resolving it would probe an unrelated local path; it is
// never resolved at all.
func TestUNCRootIsNeverProbed(t *testing.T) {
	t.Parallel()
	spy := &hangingEval{}
	s := New(newConfig())
	s.windows = true
	s.cfg.eval = spy.eval
	root := newServedRoot(`\\server\share\repo`, true)
	if got := s.physicalRoot(&root); got != "" || spy.calls.Load() != 0 {
		t.Fatalf("a UNC root resolved to %q after %d probes", got, spy.calls.Load())
	}
}
