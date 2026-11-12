package serve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/schemacheck"
	"github.com/stainless-code/verdict/internal/schemagen"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestWatchFlags checks polling flag defaults and bounds.
func TestWatchFlags(t *testing.T) {
	var output bytes.Buffer
	runner := &cli.Runner{Cmd: "serve", Out: &output, Human: &output, Diag: &output, Effective: cli.Defaults()}
	opts, code, done := parseFlags(runner, []string{
		"--interval", "250ms", "--sweep", "2s", "--no-watch",
	})
	if done || code != 0 || opts.interval != 250*time.Millisecond || opts.sweep != 2*time.Second || !opts.noWatch {
		t.Fatalf("parseFlags() = %#v, %d, %v", opts, code, done)
	}
	for _, args := range [][]string{
		{"--interval", "50ms"},
		{"--sweep", "100ms"},
	} {
		output.Reset()
		runner := &cli.Runner{Cmd: "serve", Out: &output, Human: &output, Diag: &output, Effective: cli.Defaults()}
		_, code, done := parseFlags(runner, args)
		if !done || code != cli.ExitUsage {
			t.Errorf("parseFlags(%q) = %d, %v, want usage error", args, code, done)
		}
	}
}

// TestSSEEventIsOneEnvelope validates the compact event against its schema.
func TestSSEEventIsOneEnvelope(t *testing.T) {
	server, err := New(&Config{Layer: testLayer(t.TempDir(), nil), Token: testToken, Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	change := query.Change{
		At: fixedNow(), Root: "3f2a9c1b77de", Via: "tick",
		Changed: []string{"1234"}, Removed: []string{},
	}
	schema, err := generatedEnvelopeSchema()
	if err != nil {
		t.Fatal(err)
	}
	frame := eventFrame{id: 7, change: change}
	payload := eventPayload(t, server, &frame)
	validateEventPayload(t, schema, payload)
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
}

func (*deadlineRecorder) SetWriteDeadline(time.Time) error { return nil }

func eventPayload(t *testing.T, server *Server, frame *eventFrame) string {
	t.Helper()
	response := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	if !server.writeEvent(response, frame) {
		t.Fatal("writeEvent failed")
	}
	var payload string
	dataLines := 0
	for line := range strings.SplitSeq(response.Body.String(), "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			payload = data
			dataLines++
		}
	}
	if dataLines != 1 || strings.Contains(payload, "\n") {
		t.Fatalf("SSE data lines = %d, payload = %q", dataLines, payload)
	}
	return payload
}

func validateEventPayload(t *testing.T, schema any, payload string) {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if violations := schemacheck.Validate(schema, schema, value); len(violations) > 0 {
		t.Fatalf("SSE envelope violates generated schema: %v", violations)
	}
	if !strings.Contains(payload, `"via":"tick"`) {
		t.Fatalf("payload is not compact JSON: %s", payload)
	}
}

func generatedEnvelopeSchema() (any, error) {
	files, err := schemagen.Generate()
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if file.Name == "envelope.schema.json" {
			return schemacheck.Decode(file.Data)
		}
	}
	return nil, io.ErrUnexpectedEOF
}

// TestSSEHeartbeat checks heartbeat timing and event-route security guards.
func TestSSEHeartbeat(t *testing.T) {
	root := writeFixture(t, fixtureFiles())
	layer := testLayer(root, nil)
	server, err := New(&Config{
		Layer: layer, Port: testPort, Token: testToken, Heartbeat: time.Millisecond, Now: fixedNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	stream := httptest.NewServer(server.Handler())
	t.Cleanup(stream.Close)
	checkSSEGuards(t, stream)
	resp := openSSE(t, stream, time.Second)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	assertHeartbeat(t, resp)
}

type sseGuardCase struct {
	headers map[string]string
	name    string
	path    string
	host    string
	want    int
}

func checkSSEGuards(t *testing.T, stream *httptest.Server) {
	t.Helper()
	for _, tc := range []sseGuardCase{
		{name: "no token", path: "/v1/events", want: http.StatusUnauthorized},
		{name: "query token", path: "/v1/events?token=x", headers: map[string]string{"Authorization": "Bearer " + testToken}, want: http.StatusBadRequest},
		{name: "bad host", path: "/v1/events", host: "attacker.test:47123", headers: map[string]string{"Authorization": "Bearer " + testToken}, want: http.StatusForbidden},
		{name: "bad origin", path: "/v1/events", headers: map[string]string{"Authorization": "Bearer " + testToken, "Origin": "https://evil.test"}, want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) { assertGuardStatus(t, stream, tc) })
	}
}

func assertGuardStatus(t *testing.T, stream *httptest.Server, tc sseGuardCase) {
	t.Helper()
	host := tc.host
	if host == "" {
		host = testHost
	}
	resp := requestSSE(t, stream, tc.path, host, tc.headers, time.Second)
	_ = resp.Body.Close()
	if resp.StatusCode != tc.want {
		t.Errorf("status = %d, want %d", resp.StatusCode, tc.want)
	}
}

func openSSE(t *testing.T, stream *httptest.Server, timeout time.Duration) *http.Response {
	t.Helper()
	return requestSSE(t, stream, "/v1/events", testHost,
		map[string]string{"Authorization": "Bearer " + testToken}, timeout)
}

func requestSSE(t *testing.T, stream *httptest.Server, path, host string, headers map[string]string, timeout time.Duration) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stream.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := stream.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func assertHeartbeat(t *testing.T, resp *http.Response) {
	t.Helper()
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read heartbeat: %v", err)
		}
		if line == ": ping\n" {
			break
		}
	}
}

// TestNoWatchHasNoEventRoute checks watch health and route absence.
func TestNoWatchHasNoEventRoute(t *testing.T) {
	root := writeFixture(t, fixtureFiles())
	layer := query.New(query.Snapshot{
		Version: "1.2.3", Roots: []query.Root{{Path: root}},
	}, query.Options{NoWatch: true, MaxBundles: 10})
	server, err := New(&Config{Layer: layer, Port: testPort, Token: testToken, NoWatch: true, Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	stream := httptest.NewServer(server.Handler())
	t.Cleanup(stream.Close)
	assertNotFound(t, stream, "/v1/events")
	assertNoWatchHealth(t, stream)
}

func assertNotFound(t *testing.T, stream *httptest.Server, path string) {
	t.Helper()
	resp := authorizedRequest(t, stream, path)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET %s status = %d, want 404", path, resp.StatusCode)
	}
}

func assertNoWatchHealth(t *testing.T, stream *httptest.Server) {
	t.Helper()
	resp := authorizedRequest(t, stream, "/v1/health")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/health status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Data struct {
			Watch bool `json:"watch"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Watch {
		t.Error("health watch = true, want false")
	}
}

func authorizedRequest(t *testing.T, stream *httptest.Server, path string) *http.Response {
	t.Helper()
	return requestSSE(t, stream, path, testHost,
		map[string]string{"Authorization": "Bearer " + testToken}, time.Second)
}

// TestSSEOverflowTruncates checks the bounded per-client event buffer.
func TestSSEOverflowTruncates(t *testing.T) {
	broker := newEventBroker()
	_, events := broker.subscribe()
	first := query.Change{At: fixedNow(), Root: "root", Via: "tick", Changed: []string{"1"}}
	changed := make([]string, 101)
	for i := range changed {
		changed[i] = fmt.Sprintf("bundle-%d", i)
	}
	second := query.Change{At: fixedNow(), Root: "root", Via: "tick", Changed: changed}
	broker.publish(&first)
	broker.publish(&second)
	frame := <-events
	if frame.id != 2 || !frame.change.Truncated || len(frame.change.Changed) != 100 {
		t.Fatalf("overflow frame = %#v, want latest event marked truncated", frame)
	}
}

// TestServerShutdownBounded checks cancellation joins the watcher promptly.
func TestServerShutdownBounded(t *testing.T) {
	root := writeFixture(t, fixtureFiles())
	server, err := New(&Config{
		Root: root, Port: 0, Token: testToken, Interval: 100 * time.Millisecond,
		Sweep: time.Second, Now: fixedNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Listen(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	cancel()
	var serveErr error
	testenv.Until(t, func() bool {
		select {
		case serveErr = <-done:
			return true
		default:
			return false
		}
	})
	if serveErr != nil {
		t.Fatalf("Serve() after cancel = %v, want nil", serveErr)
	}
}

// TestShutdownStopsOpenEventsStream stops the stream during shutdown.
func TestShutdownStopsOpenEventsStream(t *testing.T) {
	root := writeFixture(t, fixtureFiles())
	server, err := New(&Config{Root: root, Port: 0, Token: testToken, Now: fixedNow})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server.drainTimeout = 100 * time.Millisecond
	if err := server.Listen(t.Context()); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	response := openLiveEventStream(t, server)
	t.Cleanup(func() { _ = response.Body.Close() })
	cancel()
	if serveErr := awaitServe(t, done); serveErr != nil {
		t.Fatalf("Serve() with an open event stream = %v, want nil", serveErr)
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatalf("read stopped event stream: %v", err)
	}
}

func openLiveEventStream(t *testing.T, server *Server) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(
		t.Context(), http.MethodGet, "http://"+server.Addr()+"/v1/events", nil,
	)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+testToken)
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		t.Fatalf("GET /v1/events: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/events = %d, want 200", response.StatusCode)
	}
	return response
}

func awaitServe(t *testing.T, done <-chan error) error {
	t.Helper()
	var serveErr error
	testenv.Until(t, func() bool {
		select {
		case serveErr = <-done:
			return true
		default:
			return false
		}
	})
	return serveErr
}
