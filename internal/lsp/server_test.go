package lsp

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestInitializeAdvertisesOnlyWhatShips pins the capability object literally:
// full sync with save text, push diagnostics, and verdict.refresh. No hover,
// code action, or workspace-folder capability is claimed before it exists.
func TestInitializeAdvertisesOnlyWhatShips(t *testing.T) {
	t.Parallel()
	s := startSession(t, newConfig(fixtureRoot(t)))
	resp := s.initialize(1, nil)
	want := `{"capabilities":{"positionEncoding":"utf-16","textDocumentSync":{"openClose":true,"change":1,` +
		`"save":{"includeText":true}},"executeCommandProvider":{"commands":["verdict.refresh"]}},` +
		`"serverInfo":{"name":"verdict","version":"1.2.3"}}`
	if string(resp.Result) != want {
		t.Fatalf("initialize result =\n%s\nwant\n%s", resp.Result, want)
	}
	if err := s.finish(2); err != nil {
		t.Fatalf("Serve = %v, want nil after shutdown+exit", err)
	}
}

// TestLifecycleGates pins the lifecycle table: a request before initialize
// answers -32002, a notification before it is dropped, a second initialize
// is invalid, an unknown request answers -32601, unknown and `$/`
// notifications are silent, and every request after shutdown is invalid.
func TestLifecycleGates(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	s := startSession(t, newConfig(root))
	s.request(1, methodExecuteCommand, map[string]any{"command": CommandRefresh})
	_, early := s.awaitResponse(1)
	wantError(t, "a request before initialize", &early, rpcNotInitialized)
	openDoc(s, fileURI(root, "notes.txt"), 1, notesText)
	s.initialize(2, nil)
	s.request(3, methodInitialize, map[string]any{})
	_, again := s.awaitResponse(3)
	wantError(t, "a second initialize", &again, rpcInvalidRequest)
	mark := len(s.snapshot())
	s.notify("$/setTrace", map[string]any{"value": "verbose"})
	s.notify("$/unknownThing", nil)
	s.notify("workspace/didChangeConfiguration", map[string]any{})
	if extra := s.barrier(4, mark); len(extra) != 0 {
		t.Fatalf("ignored notifications wrote %d frames: %s", len(extra), extra[0].raw)
	}
	s.request(5, "textDocument/hover", map[string]any{})
	_, hover := s.awaitResponse(5)
	wantError(t, "hover (never advertised)", &hover, rpcMethodNotFound)
	s.request(6, methodShutdown, nil)
	s.awaitResponse(6)
	s.request(7, methodExecuteCommand, map[string]any{"command": CommandRefresh})
	_, late := s.awaitResponse(7)
	wantError(t, "a request after shutdown", &late, rpcInvalidRequest)
	s.notify(methodExit, nil)
	if err := s.wait(); err != nil {
		t.Fatalf("Serve = %v", err)
	}
	// The didOpen before initialize was dropped, and this session never sent
	// initialized, so nothing at all was published.
	if n := countMethod(s.snapshot(), methodPublish); n != 0 {
		t.Fatalf("a pre-initialize didOpen published %d frames", n)
	}
}

// TestExitWithoutShutdownIsAbnormal pins the LSP exit contract.
func TestExitWithoutShutdownIsAbnormal(t *testing.T) {
	t.Parallel()
	s := startSession(t, newConfig(fixtureRoot(t)))
	s.initialize(1, nil)
	s.notify(methodExit, nil)
	if err := s.wait(); !errors.Is(err, ErrExitWithoutShutdown) {
		t.Fatalf("Serve = %v, want ErrExitWithoutShutdown", err)
	}
}

// TestStdinEOFIsCleanExit pins that a host closing stdin ends the session
// cleanly.
func TestStdinEOFIsCleanExit(t *testing.T) {
	t.Parallel()
	s := startSession(t, newConfig(fixtureRoot(t)))
	s.initialize(1, nil)
	s.closeInput()
	if err := s.wait(); err != nil {
		t.Fatalf("Serve on EOF = %v, want nil", err)
	}
}

// TestMalformedFramesAnswerAndStop pins the body-level errors (the session
// continues) and the header-level one (one error frame, then the session
// stops with a transport error).
func TestMalformedFramesAnswerAndStop(t *testing.T) {
	t.Parallel()
	s := startSession(t, newConfig(fixtureRoot(t)))
	s.sendRaw(`{"jsonrpc":"2.0","id":1,`)
	s.sendRaw(`[1,2]`)
	s.sendRaw(`{"jsonrpc":"1.0","id":2,"method":"initialize"}`)
	s.initialize(3, nil)
	frames := s.snapshot()
	if len(frames) != 4 {
		t.Fatalf("frames = %d, want 4", len(frames))
	}
	wantError(t, "a truncated JSON body", &frames[0], rpcParseError)
	wantError(t, "an array body", &frames[1], rpcParseError)
	wantError(t, "a jsonrpc 1.0 request", &frames[2], rpcInvalidRequest)
	if string(frames[2].ID) != "2" {
		t.Errorf("the invalid request's error carries id %s, want 2", frames[2].ID)
	}
	s.sendBytes([]byte("Content-Length: nope\r\n\r\n"))
	err := s.wait()
	var te *transportError
	if !errors.As(err, &te) || !errors.Is(err, errFrameHeader) {
		t.Fatalf("Serve = %v, want a header transport error", err)
	}
	last := s.snapshot()
	if len(last) != 5 || string(last[4].ID) != "null" {
		t.Fatalf("header failure frames = %d, last %s", len(last), last[len(last)-1].raw)
	}
	wantError(t, "a malformed header", &last[4], rpcParseError)
}

// TestInitializedPublishesEveryFindingPath pins the initial publish: every
// path carrying an open finding gets its set, rendered with the root's URI,
// without a version (no document is open yet).
func TestInitializedPublishesEveryFindingPath(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	s := startSession(t, newConfig(root))
	s.initialize(1, nil)
	mark := len(s.snapshot())
	s.notify(methodInitialized, map[string]any{})
	got := summarize(publishes(t, s.barrier(2, mark)))
	want := fileURI(root, "notes.txt") + " v- " + sigCrit + "\n" + fileURI(root, "other.go") + " v- " + sigImp
	if got != want {
		t.Fatalf("initial publishes =\n%s\nwant\n%s", got, want)
	}
	d := publishes(t, s.snapshot())[0].Diagnostics[0]
	if d.Severity != severityError || d.Source != "verdict" || d.Range.Start.Line != 1 || d.Data.Bundle != "local-1" ||
		d.Message != "Finding c1111111 body with enough substance. · rigor walked" {
		t.Errorf("diagnostic = %+v", d)
	}
	if err := s.finish(3); err != nil {
		t.Fatal(err)
	}
}

// TestDidOpenPublishesVersionedSet pins didOpen: the opened URI gets its set
// with the document version and the range tightened by the client's text.
func TestDidOpenPublishesVersionedSet(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	s := startSession(t, newConfig(root))
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	mark := len(s.barrier(2, 0)) + 1
	uri := fileURI(root, "notes.txt")
	openDoc(s, uri, 7, notesText)
	got := publishes(t, s.barrier(3, mark))
	if len(got) != 1 || got[0].URI != uri || got[0].Version == nil || *got[0].Version != 7 {
		t.Fatalf("didOpen publishes = %+v", got)
	}
	d := got[0].Diagnostics[0]
	if d.Range.End != (position{Line: 1, Character: 8}) || d.Data.SpanMatches == nil || !*d.Data.SpanMatches {
		t.Fatalf("didOpen diagnostic range %+v span_matches %v", d.Range, d.Data.SpanMatches)
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestDidChangeNeverPublishes is the keystroke invariant: any number of
// didChange notifications, even ones that move the text off the reviewed
// span, write zero frames. The fake read model also proves no read happens.
func TestDidChangeNeverPublishes(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	cfg := newConfig(root)
	counting := &countingModel{ReadModel: productionLayer(cfg, []servedRoot{newServedRoot(root, false)})}
	cfg.Layer = counting
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	uri := fileURI(root, "notes.txt")
	openDoc(s, uri, 1, notesText)
	mark := len(s.barrier(2, 0)) + 1
	reads := counting.reads()
	for v := 2; v <= 6; v++ {
		s.notify(methodDidChange, map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": v},
			"contentChanges": []any{map[string]any{"text": strings.Repeat("edited\n", v)}},
		})
	}
	if extra := s.barrier(3, mark); len(extra) != 0 {
		t.Fatalf("didChange wrote %d frames; first: %s", len(extra), extra[0].raw)
	}
	if got := counting.reads(); got != reads {
		t.Fatalf("didChange made %d read-model calls", got-reads)
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestUnchangedSavePublishesNothing proves the dedup by frame count: a save
// with the same text and version over an unchanged bundle writes no frame,
// while a save after a didChange (new version) republishes once.
func TestUnchangedSavePublishesNothing(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	s := startSession(t, newConfig(root))
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	uri := fileURI(root, "notes.txt")
	openDoc(s, uri, 1, notesText)
	mark := len(s.barrier(2, 0)) + 1
	for range 3 {
		s.notify(methodDidSave, map[string]any{"textDocument": map[string]any{"uri": uri}, "text": notesText})
	}
	if extra := s.barrier(3, mark); len(extra) != 0 {
		t.Fatalf("unchanged saves wrote %d frames; first: %s", len(extra), extra[0].raw)
	}
	mark = len(s.snapshot())
	s.notify(methodDidChange, map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": 2},
		"contentChanges": []any{map[string]any{"text": "top\nnew line changed\nbottom\n"}},
	})
	s.notify(methodDidSave, map[string]any{"textDocument": map[string]any{"uri": uri}})
	got := publishes(t, s.barrier(4, mark))
	if len(got) != 1 || *got[0].Version != 2 || *got[0].Diagnostics[0].Data.SpanMatches {
		t.Fatalf("save after change = %+v, want one version-2 set that reports drift", got)
	}
	if err := s.finish(5); err != nil {
		t.Fatal(err)
	}
}

// TestSaveAfterBundleRewriteRepublishes pins the save trigger against an
// out-of-band record: with the watcher off, the save re-reads the bundle,
// the moved digest republishes the whole root, and a finding that turned
// false-positive clears its path.
func TestSaveAfterBundleRewriteRepublishes(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	s := startSession(t, newConfig(root))
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	uri := fileURI(root, "notes.txt")
	openDoc(s, uri, 1, notesText)
	mark := len(s.barrier(2, 0)) + 1
	writeBundle(t, root, "local-1",
		findingJSON(sigCrit, "false-positive", "critical", "notes.txt", 2),
		findingJSON(sigImp, "open", "important", "other.go", 4),
		findingJSON(sigInfo, "open", "informational", "other.go", 1),
	)
	s.notify(methodDidSave, map[string]any{"textDocument": map[string]any{"uri": uri}})
	got := publishes(t, s.barrier(3, mark))
	if len(got) != 2 {
		t.Fatalf("publishes after rewrite = %d, want 2: %+v", len(got), got)
	}
	if got[0].URI != uri || len(got[0].Diagnostics) != 0 {
		t.Errorf("notes.txt was not cleared: %+v", got[0])
	}
	if got[1].URI != fileURI(root, "other.go") || sigsOf(&got[1]) != sigInfo+","+sigImp {
		t.Errorf("other.go set = %s %s", got[1].URI, sigsOf(&got[1]))
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestRefreshCommandRecomposes pins verdict.refresh: it answers one row per
// root and republishes, and an unknown command refuses with the registered
// code and hint.
func TestRefreshCommandRecomposes(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	s := startSession(t, newConfig(root))
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	mark := len(s.barrier(2, 0)) + 1
	writeBundle(t, root, "local-1", findingJSON(sigImp, "open", "important", "other.go", 4))
	s.request(3, methodExecuteCommand, map[string]any{"command": CommandRefresh})
	before, resp := s.awaitResponse(3)
	want := `{"at":"2026-11-02T12:00:00Z","roots":[{"root":"` + newServedRoot(root, false).ID +
		`","bundle":"local-1","findings":1,"truncated":false}],"published":1}`
	if string(resp.Result) != want {
		t.Fatalf("refresh result = %s, want %s", resp.Result, want)
	}
	got := publishes(t, before[mark:])
	if len(got) != 1 || got[0].URI != fileURI(root, "notes.txt") || len(got[0].Diagnostics) != 0 {
		t.Fatalf("refresh publishes = %+v, want notes.txt cleared", got)
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestUnknownCommandRefusesBadParams pins the refusal for a command this
// build does not advertise: InvalidParams with the registered code, the
// advertised set, and the hint, byte for byte.
func TestUnknownCommandRefusesBadParams(t *testing.T) {
	t.Parallel()
	s := startSession(t, newConfig(fixtureRoot(t)))
	s.initialize(1, nil)
	s.request(2, methodExecuteCommand, map[string]any{"command": "verdict.reviewFile"})
	_, bad := s.awaitResponse(2)
	want := `{"jsonrpc":"2.0","id":2,"error":{"code":-32602,"message":"bad-params: unknown command ` +
		`\"verdict.reviewFile\"; this server advertises verdict.refresh","data":{"code":"bad-params",` +
		`"message":"unknown command \"verdict.reviewFile\"; this server advertises verdict.refresh",` +
		`"hint":"try: send one of the commands initialize advertised in executeCommandProvider.commands ` +
		`(this build: verdict.refresh)"}}}`
	if bad.raw != want {
		t.Fatalf("unknown command answered\n%s\nwant\n%s", bad.raw, want)
	}
	if err := s.finish(3); err != nil {
		t.Fatal(err)
	}
}

// TestCancelRequestAnswersCancelled pins $/cancelRequest: a refresh blocked
// in the read model answers -32800 once its id is canceled, and the
// published state is left as it was.
func TestCancelRequestAnswersCancelled(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	cfg := newConfig(root)
	blocking := &blockingModel{
		ReadModel: productionLayer(cfg, []servedRoot{newServedRoot(root, false)}),
		entered:   make(chan struct{}, 1),
		release:   make(chan struct{}),
	}
	cfg.Layer = blocking
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	mark := len(s.barrier(2, 0)) + 1
	blocking.arm()
	s.request(3, methodExecuteCommand, map[string]any{"command": CommandRefresh})
	testenv.Until(t, func() bool {
		select {
		case <-blocking.entered:
			return true
		default:
			return false
		}
	})
	s.notify(methodCancel, map[string]any{"id": 3})
	before, resp := s.awaitResponse(3)
	if resp.Error == nil || resp.Error.Code != rpcRequestCancelled {
		t.Fatalf("canceled refresh answered %s", resp.raw)
	}
	if extra := before[mark:]; len(extra) != 0 {
		t.Fatalf("a canceled refresh wrote %d frames first: %s", len(extra), extra[0].raw)
	}
	s.notify(methodCancel, map[string]any{"id": 99})
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestNoBundlePublishesNothingAndSaysSo pins the empty root: the session
// starts, publishes nothing, and logs the remedy once.
func TestNoBundlePublishesNothingAndSaysSo(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := startSession(t, newConfig(root))
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	openDoc(s, fileURI(root, "a.go"), 1, "package a\n")
	frames := s.barrier(2, 1)
	if countMethod(frames, methodPublish) != 0 || countMethod(frames, methodLogMessage) != 1 {
		t.Fatalf("empty root frames = %+v", frames)
	}
	if !strings.Contains(frames[0].raw, "no review bundle under") || !strings.Contains(frames[0].raw, "verdict.refresh") {
		t.Fatalf("log = %s", frames[0].raw)
	}
	if err := s.finish(3); err != nil {
		t.Fatal(err)
	}
}

// TestOutsideRootDocumentIsIgnored pins confinement at the protocol edge: a
// document under no served root is never tracked and never published, and
// a store path that leaves the root is dropped with one warning.
func TestOutsideRootDocumentIsIgnored(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBundle(t, root, "local-1",
		findingJSON(sigCrit, "open", "critical", "../outside.txt", 1),
		findingJSON(sigImp, "open", "important", "notes.txt", 2),
	)
	s := startSession(t, newConfig(root))
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	frames := s.barrier(2, 1)
	if countMethod(frames, methodLogMessage) != 1 || !strings.Contains(frames[0].raw, "1 finding(s)") {
		t.Fatalf("expected one dropped-path warning, got %+v", frames)
	}
	got := publishes(t, frames)
	if len(got) != 1 || got[0].URI != fileURI(root, "notes.txt") {
		t.Fatalf("publishes = %+v", got)
	}
	mark := len(s.snapshot())
	openDoc(s, "file:///elsewhere/notes.txt", 1, notesText)
	openDoc(s, fileURI(root, "../outside.txt"), 1, "x\n")
	if extra := s.barrier(3, mark); len(extra) != 0 {
		t.Fatalf("outside documents wrote %d frames", len(extra))
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// countingModel counts read-model calls.
type countingModel struct {
	ReadModel
	mu   sync.Mutex
	n    int
	subs int
}

func (c *countingModel) Changes(ctx context.Context) <-chan query.Change {
	c.mu.Lock()
	c.subs++
	c.mu.Unlock()
	return c.ReadModel.Changes(ctx)
}

func (c *countingModel) subscriptions() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.subs
}

func (c *countingModel) bump() {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
}

func (c *countingModel) reads() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func (c *countingModel) Bundles(ctx context.Context, p string) (*query.View, error) {
	c.bump()
	return c.ReadModel.Bundles(ctx, p)
}

func (c *countingModel) Detail(ctx context.Context, r, b string) (*query.View, error) {
	c.bump()
	return c.ReadModel.Detail(ctx, r, b)
}

func (c *countingModel) Diff(ctx context.Context, r, b string) (*query.View, error) {
	c.bump()
	return c.ReadModel.Diff(ctx, r, b)
}

// blockingModel blocks Bundles, once armed, until the caller's ctx ends or
// release closes.
type blockingModel struct {
	ReadModel
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	armed   bool
}

func (b *blockingModel) arm() {
	b.mu.Lock()
	b.armed = true
	b.mu.Unlock()
}

func (b *blockingModel) Bundles(ctx context.Context, p string) (*query.View, error) {
	b.mu.Lock()
	armed := b.armed
	b.mu.Unlock()
	if !armed {
		return b.ReadModel.Bundles(ctx, p)
	}
	b.entered <- struct{}{}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.release:
		return b.ReadModel.Bundles(ctx, p)
	}
}

var _ io.Writer = (*lockedWriter)(nil)

// TestShowDeferredOption pins initializationOptions.showDeferred: a
// deferred finding is hidden by default and publishes as a Hint on request;
// an initialization option this build does not read changes nothing.
func TestShowDeferredOption(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBundle(t, root, "local-1",
		findingJSON(sigCrit, "deferred", "critical", "notes.txt", 2),
		findingJSON(sigImp, "open", "important", "other.go", 4),
	)
	other := fileURI(root, "other.go") + " v- " + sigImp
	if got := initialPublishes(t, root, map[string]any{"allowRun": true}); got != other {
		t.Fatalf("default posture published\n%s", got)
	}
	shown := fileURI(root, "notes.txt") + " v- " + sigCrit + "\n" + other
	if got := initialPublishes(t, root, map[string]any{"showDeferred": true}); got != shown {
		t.Fatalf("showDeferred published\n%s", got)
	}
	d := deferredDiagnostic(t, root)
	if d.Severity != severityHint || !d.Data.Deferred {
		t.Fatalf("deferred diagnostic = severity %d deferred %v, want a deferred Hint", d.Severity, d.Data.Deferred)
	}
}

// initialPublishes runs one session to its initial publish and summarizes it.
func initialPublishes(t *testing.T, root string, initOptions map[string]any) string {
	t.Helper()
	s := startSession(t, newConfig(root))
	s.initialize(1, initOptions)
	s.notify(methodInitialized, map[string]any{})
	got := summarize(publishes(t, s.barrier(2, 1)))
	if err := s.finish(3); err != nil {
		t.Fatal(err)
	}
	return got
}

// deferredDiagnostic returns the notes.txt diagnostic under showDeferred.
func deferredDiagnostic(t *testing.T, root string) Diagnostic {
	t.Helper()
	s := startSession(t, newConfig(root))
	s.initialize(1, map[string]any{"showDeferred": true})
	s.notify(methodInitialized, map[string]any{})
	got := publishes(t, s.barrier(2, 1))
	if err := s.finish(3); err != nil {
		t.Fatal(err)
	}
	return got[0].Diagnostics[0]
}
