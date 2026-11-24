package lsp

import (
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stainless-code/verdict/internal/testenv"
)

// TestJobQueueBoundsAndOrder pins the dispatcher: at most limit jobs run,
// capacity more wait, a job past both is refused, and waiting jobs start in
// submission order as slots free.
func TestJobQueueBoundsAndOrder(t *testing.T) {
	t.Parallel()
	q := jobQueue{limit: 16, capacity: 3}
	rec := &startLog{gate: make(chan struct{})}
	if accepted := rec.submitAll(&q, 20); accepted != 19 {
		t.Fatalf("accepted %d of 20 jobs, want 19 (16 running, 3 waiting, 1 refused)", accepted)
	}
	testenv.Until(t, func() bool { return rec.count() == 16 })
	if running, waiting := q.counts(); running != 16 || waiting != 3 {
		t.Fatalf("running %d waiting %d, want 16 and 3", running, waiting)
	}
	// Free one slot at a time: each release starts exactly the oldest
	// waiting job.
	rec.releaseOne(t)
	rec.releaseOne(t)
	rec.releaseOne(t)
	if got := rec.tail(3); got != "16,17,18" {
		t.Fatalf("waiting jobs started in order %s, want 16,17,18", got)
	}
	close(rec.gate)
	testenv.Until(t, func() bool {
		running, waiting := q.counts()
		return running == 0 && waiting == 0
	})
}

// startLog records the order jobs start in; each job then blocks on gate.
type startLog struct {
	gate    chan struct{}
	started []int
	mu      sync.Mutex
}

func (l *startLog) job(n int) func() {
	return func() {
		l.mu.Lock()
		l.started = append(l.started, n)
		l.mu.Unlock()
		<-l.gate
	}
}

// releaseOne unblocks one running job and waits for the next to start.
func (l *startLog) releaseOne(t *testing.T) {
	t.Helper()
	n := l.count()
	l.gate <- struct{}{}
	testenv.Until(t, func() bool { return l.count() == n+1 })
}

func (l *startLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.started)
}

// tail renders the last n started job numbers.
func (l *startLog) tail(n int) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	parts := make([]string, 0, n)
	for _, v := range l.started[len(l.started)-n:] {
		parts = append(parts, strconv.Itoa(v))
	}
	return strings.Join(parts, ",")
}

// submitAll submits jobs 0..n-1 and counts the accepted ones.
func (l *startLog) submitAll(q *jobQueue, n int) int {
	accepted := 0
	for i := range n {
		if q.submit(l.job(i)) {
			accepted++
		}
	}
	return accepted
}

// TestRequestPastTheQueueIsRefused pins the protocol edge of the same
// bound: with one slot and no queue, a second request answers
// ServerCancelled at once while the first is still running.
func TestRequestPastTheQueueIsRefused(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	cfg := newConfig(root)
	cfg.lim = limits{inflight: 1, queued: 1}
	blocking := &blockingModel{
		ReadModel: productionLayer(cfg, []servedRoot{newServedRoot(root, false)}),
		entered:   make(chan struct{}, 4),
		release:   make(chan struct{}),
	}
	cfg.Layer = blocking
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	s.barrier(2, 0)
	blocking.arm()
	for id := 3; id <= 5; id++ {
		s.request(id, methodExecuteCommand, map[string]any{"command": CommandRefresh})
	}
	_, busy := s.awaitResponse(5)
	wantError(t, "a request past the queue", &busy, rpcServerCancelled)
	close(blocking.release)
	for id := 3; id <= 4; id++ {
		if _, resp := s.awaitResponse(id); resp.Error != nil {
			t.Fatalf("queued request %d answered %s", id, resp.raw)
		}
	}
	if err := s.finish(6); err != nil {
		t.Fatal(err)
	}
}

// TestMaxDocsRefusesTheNextDocument pins the open-document bound: past it a
// new document is refused with a log line and gets no diagnostics, and a
// close frees the slot.
func TestMaxDocsRefusesTheNextDocument(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	cfg := newConfig(root)
	cfg.lim = limits{docs: 1}
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	mark := len(s.barrier(2, 0)) + 1
	openDoc(s, fileURI(root, "a.txt"), 1, "a\n")
	openDoc(s, fileURI(root, "notes.txt"), 1, notesText)
	frames := s.barrier(3, mark)
	if countMethod(frames, methodPublish) != 0 || len(logs(t, frames)) != 1 ||
		!strings.Contains(logs(t, frames)[0], "1 documents are open") {
		t.Fatalf("over the doc bound: %d publishes, logs %q", countMethod(frames, methodPublish), logs(t, frames))
	}
	mark = len(s.snapshot())
	s.notify(methodDidClose, map[string]any{"textDocument": map[string]any{"uri": fileURI(root, "a.txt")}})
	openDoc(s, fileURI(root, "notes.txt"), 1, notesText)
	if got := summarize(publishes(t, s.barrier(4, mark))); got != fileURI(root, "notes.txt")+" v1 "+sigCrit {
		t.Fatalf("after a close the document published %q", got)
	}
	if err := s.finish(5); err != nil {
		t.Fatal(err)
	}
}

// TestFindingsCapTruncatesByRank pins the per-bundle findings bound: past it
// only the highest-ranked findings publish, each marked truncated, with one
// warning, and refresh reports the cut.
func TestFindingsCapTruncatesByRank(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	cfg := newConfig(root)
	cfg.lim = limits{findings: 1}
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	frames := s.barrier(2, 0)
	got := publishes(t, frames)
	if summarize(got) != fileURI(root, "notes.txt")+" v- "+sigCrit || !got[0].Diagnostics[0].Data.Truncated {
		t.Fatalf("capped publishes = %s", summarize(got))
	}
	if texts := logs(t, frames); len(texts) != 1 || !strings.Contains(texts[0], "only the 1 highest-ranked are published") {
		t.Fatalf("cap logs = %q", texts)
	}
	s.request(3, methodExecuteCommand, map[string]any{"command": CommandRefresh})
	if _, resp := s.awaitResponse(3); !strings.Contains(string(resp.Result), `"findings":1,"truncated":true`) {
		t.Fatalf("refresh did not report the cut: %s", resp.Result)
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestPublishFitsTheFrameCap pins the frame bound: a set whose frame would
// exceed the cap is cut to the longest prefix that fits, every kept
// diagnostic marked truncated, with one warning.
func TestPublishFitsTheFrameCap(t *testing.T) {
	t.Parallel()
	root := bigBundle(t, 30)
	cfg := newConfig(root)
	cfg.lim = limits{frameBytes: 4096}
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	frames := s.barrier(2, 0)
	got := publishes(t, frames)
	if len(got) != 1 || len(got[0].Diagnostics) == 0 || len(got[0].Diagnostics) >= 30 || !got[0].Diagnostics[0].Data.Truncated {
		t.Fatalf("frame-capped publish = %s", summarize(got))
	}
	if n := largestPublish(frames); n > 4096 {
		t.Fatalf("a publish frame body is %d bytes, over the 4096 cap", n)
	}
	if texts := logs(t, frames); len(texts) != 1 || !strings.Contains(texts[0], "exceed the frame cap") {
		t.Fatalf("frame cap logs = %q", texts)
	}
	if n := refreshFrames(s, 3); n != 0 {
		t.Fatalf("an unchanged cut set republished or re-logged: %d frames", n)
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// bigBundle is a checkout whose bundle carries n findings on one file.
func bigBundle(t *testing.T, n int) string {
	t.Helper()
	root := checkout(t)
	findings := make([]string, 0, n)
	for i := range n {
		findings = append(findings, findingJSON(strings.Repeat(strconv.Itoa(i%10), 40), "open", "important", "big.go", i+1))
	}
	writeBundle(t, root, "local-1", findings...)
	return root
}

// refreshFrames runs verdict.refresh as request id and counts every frame it
// wrote before its answer.
func refreshFrames(s *session, id int) int {
	s.t.Helper()
	mark := len(s.snapshot())
	s.request(id, methodExecuteCommand, map[string]any{"command": CommandRefresh})
	before, _ := s.awaitResponse(id)
	return len(before[mark:])
}

func largestPublish(frames []wireFrame) int {
	n := 0
	for i := range frames {
		if frames[i].Method == methodPublish {
			n = max(n, len(frames[i].raw))
		}
	}
	return n
}

// TestDidSaveRecordsVersion pins item 6: a didSave that carries a version
// updates the document's, so the republish names it.
func TestDidSaveRecordsVersion(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	s := startSession(t, newConfig(root))
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	uri := fileURI(root, "notes.txt")
	openDoc(s, uri, 1, notesText)
	mark := len(s.barrier(2, 0)) + 1
	s.notify(methodDidSave, map[string]any{"textDocument": map[string]any{"uri": uri, "version": 9}, "text": notesText})
	if got := summarize(publishes(t, s.barrier(3, mark))); got != uri+" v9 "+sigCrit {
		t.Fatalf("a versioned save published %q, want version 9", got)
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestDocBytesRunningTotal pins item 10: the session text total is a running
// sum that open, change, and close keep exact, and a buffer past the
// session bound is tracked by version only.
func TestDocBytesRunningTotal(t *testing.T) {
	t.Parallel()
	s := New(newConfig("/r"))
	a, b := &document{key: "a"}, &document{key: "b"}
	five, three := "12345", "123"
	s.setText(a, &five)
	s.setText(b, &three)
	s.setText(a, &three)
	if s.docBytes != 6 || !a.hasText || a.text != "123" {
		t.Fatalf("docBytes = %d after open/open/change, want 6", s.docBytes)
	}
	s.setText(b, nil)
	if s.docBytes != 3 || b.hasText {
		t.Fatalf("docBytes = %d after dropping b, want 3", s.docBytes)
	}
	s.docBytes = maxDocBytesTotal - 2
	s.setText(b, &three)
	if b.hasText || s.docBytes != maxDocBytesTotal-2 {
		t.Fatalf("a buffer past the session bound was tracked (docBytes %d)", s.docBytes)
	}
}

// TestPlainLineDropsInvisibleRunes pins item 7 literal by literal.
func TestPlainLineDropsInvisibleRunes(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"a\u2028b":                 "ab",
		"a\u2029b":                 "ab",
		"a\u200bb\u200cc\u200dd":   "abcd",
		"a\u2060b":                 "ab",
		"\ufeffbom\ufeff":          "bom",
		"soft\u00adhyphen":         "softhyphen",
		"a\u202eb\u2066c":          "abc",
		"a\x00b\x1fc\x7fd":         "abcd",
		"a\u0085b":                 "ab",
		"tab\there":                "tab here",
		"  caf\u00e9 \U0001F600  ": "caf\u00e9 \U0001F600",
	} {
		if got := plainLine(in); got != want {
			t.Errorf("plainLine(%q) = %q, want %q", in, got, want)
		}
	}
}
