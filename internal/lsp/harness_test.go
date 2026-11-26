package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/testenv"
)

var fixedNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

const (
	sigCrit = "c111111111111111111111111111111111111111"
	sigImp  = "b222222222222222222222222222222222222222"
	sigInfo = "d333333333333333333333333333333333333333"
	headB   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	// notesText is the working tree the fixture diff reviewed.
	notesText = "top\nnew line\nbottom\n"
)

const fixtureDiff = "diff --git a/notes.txt b/notes.txt\n" +
	"index 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n" +
	"@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

func writeFile(t *testing.T, base, rel, content string) {
	t.Helper()
	full := filepath.Join(base, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// findingJSON renders one inline store record.
func findingJSON(sig, status, severity, path string, line int) string {
	return fmt.Sprintf(`{"kind":"inline","path":%q,"side":"RIGHT","line":%d,"anchored":true,`+
		`"body":"Finding %s body with enough substance.","suggestion":null,"inBounds":true,`+
		`"rigor":"walked","severity":%q,"sig":%q,"sig_version":"1","firstSeen":"aaaaaaa",`+
		`"lastSeen":"aaaaaaa","status":%q,"rationale":"","history":[]}`,
		path, line, sig[:8], severity, sig, status)
}

// writeBundle writes one local bundle with the fixture diff and the given
// findings.
func writeBundle(t *testing.T, root, name string, findings ...string) {
	t.Helper()
	dir := ".verdict/state/" + name + "/"
	writeFile(t, root, dir+"source.json",
		`{"schema_version":"1","kind":"range","repo":"acme/app","slug":"`+strings.TrimPrefix(name, "local-")+`",`+
			`"head_ref":"v2","base_sha":"`+headB+`","head_sha":"`+headB+`","dirty":false}`)
	writeFile(t, root, dir+"full.diff", fixtureDiff)
	writeFile(t, root, dir+"findings.json", "["+strings.Join(findings, ",")+"]")
}

// fixtureRoot is a repository with one local bundle: a critical finding on
// notes.txt:2 and an important one on other.go:4.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	root := checkout(t)
	writeBundle(t, root, "local-1",
		findingJSON(sigCrit, "open", "critical", "notes.txt", 2),
		findingJSON(sigImp, "open", "important", "other.go", 4),
	)
	return root
}

// checkout is an empty git checkout: the root guard looks for its .git
// entry, and the query layer's trust gate asks git about its index, which
// a bare .git directory cannot answer (it would fail closed).
func checkout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitInit(t, dir)
	return dir
}

// gitInit makes dir an empty git checkout.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", "init", "-q")
	cmd.Dir, cmd.Env, cmd.WaitDelay = dir, testenv.Base(nil), time.Minute
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

// logs returns the window/logMessage texts among frames.
func logs(t *testing.T, frames []wireFrame) []string {
	t.Helper()
	var out []string
	for i := range frames {
		if frames[i].Method != methodLogMessage {
			continue
		}
		var p logMessageParams
		if err := json.Unmarshal(frames[i].Params, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p.Message)
	}
	return out
}

// wireFrame is one decoded server frame.
type wireFrame struct {
	Error  *rpcError `json:"error"`
	Method string    `json:"method"`
	raw    string
	ID     json.RawMessage `json:"id"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

// session drives one Serve over pipes and collects every frame it writes.
type session struct {
	readErr error
	writes  chan []byte
	server  *Server
	t       *testing.T
	in      *io.PipeWriter
	done    chan error
	drained chan struct{}
	frames  []wireFrame
	mu      sync.Mutex
}

// newConfig is a session config whose path walks never run out of budget:
// a walk the test needs resolved resolves however slow the machine is, and
// a test that needs a timeout makes one with a gatedEval.
func newConfig(roots ...string) *Config {
	return &Config{
		Version: "1.2.3",
		Roots:   roots,
		Now:     func() time.Time { return fixedNow },
		Stderr:  io.Discard,
		NoWatch: true,
		expire:  func(string) <-chan struct{} { return nil },
	}
}

func startSession(t *testing.T, cfg *Config) *session {
	t.Helper()
	return startSessionCtx(context.Background(), t, cfg)
}

// startSessionCtx is startSession with the session's own context, so a test
// can cancel it the way a client that dies does.
func startSessionCtx(ctx context.Context, t *testing.T, cfg *Config) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	server := New(cfg)
	s := &session{
		t: t, in: inW, done: make(chan error, 1), drained: make(chan struct{}),
		server: server, writes: make(chan []byte, 4096),
	}
	go func() {
		err := server.Serve(ctx, inR, outW)
		_ = outW.Close()
		s.done <- err
	}()
	go s.collect(outR)
	go s.pump()
	t.Cleanup(func() {
		_ = inW.Close()
	})
	return s
}

// collect decodes every stdout frame into the log. It returns at stdout EOF,
// which Serve's closing of the pipe guarantees comes after the last frame,
// and closes drained so a wait can be sure the log is complete.
func (s *session) collect(r io.Reader) {
	defer close(s.drained)
	br := bufio.NewReader(r)
	for {
		body, err := readFrame(br)
		if errors.Is(err, io.EOF) {
			return
		}
		if err == nil {
			err = s.append(body)
		}
		if err != nil {
			s.fail(err)
			return
		}
	}
}

// append decodes one stdout body into the frame log.
func (s *session) append(body []byte) error {
	f := wireFrame{raw: string(body)}
	if err := json.Unmarshal(body, &f); err != nil {
		return fmt.Errorf("stdout frame is not JSON: %q", body)
	}
	s.mu.Lock()
	s.frames = append(s.frames, f)
	s.mu.Unlock()
	return nil
}

// fail records a stdout that is not pure frames; snapshot reports it.
func (s *session) fail(err error) {
	s.mu.Lock()
	s.readErr = err
	s.mu.Unlock()
}

// wantError fails unless f is an error response with code.
func wantError(t *testing.T, what string, f *wireFrame, code int) {
	t.Helper()
	if f.Error == nil || f.Error.Code != code {
		t.Fatalf("%s answered %s, want error %d", what, f.raw, code)
	}
}

func (s *session) sendRaw(body string) {
	s.sendBytes(encodeFrame([]byte(body)))
}

// sendBytes queues raw stdin bytes. A writer goroutine owns the pipe, so a
// server that stops reading (the bug a test hunts) fails the test's wait
// instead of blocking the test goroutine on the write.
func (s *session) sendBytes(raw []byte) {
	s.writes <- raw
}

// closeInput queues stdin EOF after every pending write.
func (s *session) closeInput() {
	s.writes <- nil
}

// pump writes queued stdin bytes in order; nil closes stdin.
func (s *session) pump() {
	for raw := range s.writes {
		if raw == nil {
			_ = s.in.Close()
			return
		}
		if _, err := s.in.Write(raw); err != nil {
			return
		}
	}
}

func (s *session) request(id int, method string, params any) {
	s.t.Helper()
	s.sendRaw(mustJSON(s.t, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}))
}

func (s *session) notify(method string, params any) {
	s.t.Helper()
	s.sendRaw(mustJSON(s.t, map[string]any{"jsonrpc": "2.0", "method": method, "params": params}))
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// snapshot returns a copy of every frame read so far.
func (s *session) snapshot() []wireFrame {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readErr != nil {
		s.t.Fatalf("stdout carried a non-frame: %v", s.readErr)
	}
	return append([]wireFrame(nil), s.frames...)
}

// awaitResponse waits for the response to id and returns every frame read
// before it (in order) and the response itself.
func (s *session) awaitResponse(id int) ([]wireFrame, wireFrame) {
	s.t.Helper()
	want := strconv.Itoa(id)
	var before []wireFrame
	var resp wireFrame
	testenv.Until(s.t, func() bool {
		frames := s.snapshot()
		for i, f := range frames {
			if string(f.ID) == want && f.Method == "" {
				before, resp = frames[:i], f
				return true
			}
		}
		return false
	})
	return before, resp
}

// barrier sends a request the server answers inline (an unknown method)
// and returns the frames written since mark, before that answer. Because
// notifications run inline in arrival order, a notification that published
// nothing leaves this slice empty: the proof is a frame count, not a timer.
func (s *session) barrier(id, mark int) []wireFrame {
	s.t.Helper()
	s.request(id, "verdict/barrier", nil)
	before, resp := s.awaitResponse(id)
	if resp.Error == nil || resp.Error.Code != rpcMethodNotFound {
		s.t.Fatalf("barrier %d answered %s", id, resp.raw)
	}
	return before[mark:]
}

func (s *session) initialize(id int, initOptions any) wireFrame {
	s.t.Helper()
	params := map[string]any{"processId": nil, "capabilities": map[string]any{}}
	if initOptions != nil {
		params["initializationOptions"] = initOptions
	}
	s.request(id, methodInitialize, params)
	_, resp := s.awaitResponse(id)
	return resp
}

// finish sends shutdown and exit and returns Serve's result.
func (s *session) finish(id int) error {
	s.t.Helper()
	s.request(id, methodShutdown, nil)
	_, resp := s.awaitResponse(id)
	if resp.Error != nil || string(resp.Result) != "null" {
		s.t.Fatalf("shutdown answered %s", resp.raw)
	}
	s.notify(methodExit, nil)
	return s.wait()
}

// wait returns Serve's result once Serve has returned and the collector has
// read every frame it wrote, so a snapshot taken afterwards is complete.
// Serve returning alone is not enough: the collector may still be decoding
// the last body.
func (s *session) wait() error {
	s.t.Helper()
	var err error
	testenv.Until(s.t, func() bool {
		select {
		case err = <-s.done:
			return true
		default:
			return false
		}
	})
	testenv.Until(s.t, func() bool {
		select {
		case <-s.drained:
			return true
		default:
			return false
		}
	})
	return err
}

// fileURI is the one way a test spells a file URI: the server's own
// rendering for this OS (`file:///C:/...` on Windows, forward slashes,
// percent-escaped), so a test URI and a published one compare as strings.
func fileURI(root, rel string) string {
	r := newServedRoot(root, runtime.GOOS == "windows")
	return r.uriFor(rel)
}

func openDoc(s *session, uri string, version int, text string) {
	s.notify(methodDidOpen, map[string]any{"textDocument": map[string]any{
		"uri": uri, "languageId": "plaintext", "version": version, "text": text,
	}})
}

// publishes decodes every publishDiagnostics frame in frames.
func publishes(t *testing.T, frames []wireFrame) []publishParams {
	t.Helper()
	var out []publishParams
	for i := range frames {
		if frames[i].Method != methodPublish {
			continue
		}
		var p publishParams
		if err := json.Unmarshal(frames[i].Params, &p); err != nil {
			t.Fatalf("publish params: %v", err)
		}
		out = append(out, p)
	}
	return out
}

func sigsOf(p *publishParams) string {
	var sigs []string
	for i := range p.Diagnostics {
		sigs = append(sigs, p.Diagnostics[i].Code)
	}
	return strings.Join(sigs, ",")
}

func countMethod(frames []wireFrame, method string) int {
	n := 0
	for _, f := range frames {
		if f.Method == method {
			n++
		}
	}
	return n
}

// summarize renders each publish as one `uri v<version> sig,sig` line, so a
// test compares a whole sequence against one literal.
func summarize(ps []publishParams) string {
	lines := make([]string, 0, len(ps))
	for i := range ps {
		version := "-"
		if ps[i].Version != nil {
			version = strconv.Itoa(*ps[i].Version)
		}
		lines = append(lines, ps[i].URI+" v"+version+" "+sigsOf(&ps[i]))
	}
	return strings.Join(lines, "\n")
}
