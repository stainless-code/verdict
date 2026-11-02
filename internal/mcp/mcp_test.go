package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	learncore "github.com/stainless-code/verdict/internal/core/learn"
	triagecore "github.com/stainless-code/verdict/internal/core/triage"
	"github.com/stainless-code/verdict/internal/jq"
)

// fixedNow is the deterministic clock every test injects.
var fixedNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// testConfig builds a hermetic transport config rooted at the current cwd.
func testConfig() Config {
	return Config{
		Version: "1.2.3",
		Env:     map[string]string{"HOME": "/nonexistent"},
		Now:     func() time.Time { return fixedNow },
		Stderr:  &bytes.Buffer{},
	}
}

// fixture writes a minimal hermetic PR bundle and changes into its root.
func fixture(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	writeFile(t, ".review/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFile(t, ".review/5101/findings.json", `[]`)
}

// writeFile writes one fixture file, creating parents.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// frame is one decoded JSON-RPC response.
type frame struct { //nolint:govet // field order mirrors the decoded wire frame
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

// serve runs one scripted session and returns the decoded stdout frames.
func serve(t *testing.T, cfg *Config, lines ...string) map[string]frame {
	t.Helper()
	var out bytes.Buffer
	input := strings.NewReader(strings.Join(lines, "\n") + "\n")
	if err := New(cfg).Serve(context.Background(), input, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	return decodeFrames(t, out.Bytes())
}

// decodeFrames proves stdout holds protocol frames only.
//
//nolint:revive // frame decoding asserts one line of protocol output
func decodeFrames(t *testing.T, raw []byte) map[string]frame {
	t.Helper()
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return map[string]frame{}
	}
	out := map[string]frame{}
	for i, line := range strings.Split(text, "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatalf("stdout line %d is not JSON: %q", i+1, line)
		}
		var f frame
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatalf("stdout line %d: %v", i+1, err)
		}
		if f.JSONRPC != jsonRPCVersion {
			t.Errorf("line %d jsonrpc = %q", i+1, f.JSONRPC)
		}
		out[string(f.ID)] = f
	}
	return out
}

// TestToolSchemas proves every tool's schema is generated from its request
// struct, is valid JSON Schema subset, and hides adapter-owned fields.
//
//nolint:revive,gocognit,cyclop // the matrix reads as one assertion block
func TestToolSchemas(t *testing.T) {
	t.Parallel()
	tools := allTools(false)
	if len(tools) != 18 {
		t.Fatalf("tools = %d, want 18", len(tools))
	}
	wantNames := []string{
		"adopt", "calibrate", "check-tree", "doctor", "fetch", "fetch-local", "fix",
		"init", "learn", "next", "post", "ready", "record", "report", "show",
		"trend", "triage", "verify",
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.name)
	}
	if !reflect.DeepEqual(names, wantNames) {
		t.Errorf("tool names = %v, want %v", names, wantNames)
	}
	for _, tool := range tools {
		schema := inputSchema(tool)
		raw, err := json.Marshal(schema)
		if err != nil {
			t.Fatalf("%s: %v", tool.name, err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("%s schema: %v", tool.name, err)
		}
		checkSchema(t, tool.name, decoded)
		props, ok := decoded[schemaProperties].(map[string]any)
		if !ok {
			t.Fatalf("%s properties missing", tool.name)
		}
		for _, banned := range []string{"runtime", "effective", "warnings", "human", "diag", "root", "forces"} {
			if _, ok := props[banned]; ok {
				t.Errorf("%s exposes internal field %q", tool.name, banned)
			}
		}
		required, ok := decoded[schemaRequired].([]any)
		if !ok {
			t.Fatalf("%s required missing", tool.name)
		}
		for _, entry := range required {
			name, ok := entry.(string)
			if !ok {
				t.Fatalf("%s required entry is not a string", tool.name)
			}
			if _, ok := props[name]; !ok {
				t.Errorf("%s requires %q but has no such property", tool.name, name)
			}
		}
	}
}

// checkSchema walks the small JSON Schema subset the reflector emits.
//
//nolint:revive // the schema walk is one branch per JSON type
func checkSchema(t *testing.T, tool string, node map[string]any) {
	t.Helper()
	typ, ok := node[schemaType].(string)
	if !ok {
		t.Fatalf("%s has no schema type", tool)
	}
	switch typ {
	case typeObject:
		props, ok := node[schemaProperties].(map[string]any)
		if !ok {
			t.Fatalf("%s object without properties", tool)
		}
		if node[schemaAdditionalProperties] != false {
			t.Errorf("%s object is not closed", tool)
		}
		for name, raw := range props {
			child, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("%s.%s property is not an object", tool, name)
			}
			checkSchema(t, tool+"."+name, child)
		}
	case typeArray:
		items, ok := node[schemaItems].(map[string]any)
		if !ok {
			t.Fatalf("%s array without items", tool)
		}
		checkSchema(t, tool+"[]", items)
	case typeString, typeInteger, typeNumber, typeBoolean:
	default:
		t.Fatalf("%s has unknown schema type %q", tool, typ)
	}
}

// TestDataToolCall proves the shared data-response adapter (reflection over
// the response's Data field) reaches a service beyond the report family.
func TestDataToolCall(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFile(t, ".review/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	writeFile(t, ".review/5101/findings.json", `[{"kind":"inline","path":"notes.txt",`+
		`"side":"RIGHT","line":2,"anchored":true,"body":"Usable body.","suggestion":null,`+
		`"inBounds":true,"rigor":"walked","severity":"critical",`+
		`"sig":"c111111111111111111111111111111111111111","sig_version":"1",`+
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]`)
	cfg := testConfig()
	frames := serve(t, &cfg,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"triage","arguments":{"repo":"acme/app","pr":"5101","sig":"c1111111","status":"addressed","force":true,"dryRun":true}}}`,
	)
	var result callToolResult
	if err := json.Unmarshal(frames["1"].Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("triage refused: %s", result.Content[0].Text)
	}
	var data map[string]any
	if err := json.Unmarshal(result.StructuredContent, &data); err != nil {
		t.Fatal(err)
	}
	if data["status"] != "addressed" || data["dry_run"] != true {
		t.Errorf("triage data = %v", data)
	}
}

// postCommander scripts post's reads (live SHA, authorship, empty threads)
// and fails the test on any reviews POST.
type postCommander struct {
	t     *testing.T
	calls []string
}

// Run replays one canned read; dry-run must never reach the reviews POST.
func (c *postCommander) Run(_ context.Context, cmd core.Command) (core.Result, error) {
	joined := strings.Join(cmd.Argv, " ")
	c.calls = append(c.calls, joined)
	switch {
	case strings.Contains(joined, "/reviews"):
		c.t.Errorf("post dry-run reached the reviews POST: %q", cmd.Argv)
		return core.Result{}, errors.New("unexpected reviews POST")
	case strings.Contains(joined, "graphql"):
		return core.Result{Stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}`}, nil
	case strings.Contains(joined, "pulls/7"):
		return core.Result{Stdout: strings.Repeat("a", 40) + "\n"}, nil
	case strings.Contains(joined, "api user"):
		return core.Result{Stdout: "someone-else\n"}, nil
	default:
		c.t.Errorf("unexpected post argv: %q", cmd.Argv)
		return core.Result{}, errors.New("unexpected argv")
	}
}

// postFixture writes the bundle post's dry run needs.
func postFixture(t *testing.T) {
	t.Helper()
	writeFile(t, ".review/7/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"author":{"login":"someone-else"}}`)
	writeFile(t, ".review/7/full.diff",
		"diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n"+
			"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n")
	writeFile(t, ".review/7/review.json",
		`{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,"comments":[]}`)
	writeFile(t, ".review/7/findings.json", `[]`)
}

// TestPostTool proves the post tool is registered from core.PostRequest, a
// scripted dry run returns the envelope data without posting, and a typed
// refusal keeps isError/code.
//
//nolint:revive,cyclop,gocognit // the success/refusal/artifact matrix reads as one assertion block
func TestPostTool(t *testing.T) {
	t.Chdir(t.TempDir())
	postFixture(t)
	cmdr := &postCommander{t: t}
	cfg := testConfig()
	cfg.Commanders = map[string]core.Commander{"post": cmdr}
	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"post","arguments":{"repo":"acme/app","pr":"7","flags":{"approve":"false","reviewFile":".review/7/review.json","dryRun":true}}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"post","arguments":{"repo":"acme/app","pr":"7","flags":{"approve":"yes","dryRun":true}}}}`,
	}
	frames := serve(t, &cfg, lines...)
	var list toolsListResult
	if err := json.Unmarshal(frames["1"].Result, &list); err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	post := false
	for _, tool := range list.Tools {
		if tool.Name == "post" {
			post = true
			if ro, ok := tool.Annotations[annotationReadOnly].(bool); !ok || ro {
				t.Error("post readOnlyHint is not false")
			}
		}
	}
	if !post {
		t.Error("tools/list misses post")
	}
	var ok callToolResult
	if err := json.Unmarshal(frames["2"].Result, &ok); err != nil {
		t.Fatalf("post result: %v", err)
	}
	if ok.IsError {
		t.Fatalf("post dry-run refused: %s", ok.Content[0].Text)
	}
	if string(ok.StructuredContent) != ok.Content[0].Text {
		t.Error("post text and structuredContent diverged")
	}
	var data map[string]any
	if err := json.Unmarshal(ok.StructuredContent, &data); err != nil {
		t.Fatalf("post structuredContent: %v", err)
	}
	if data["dry_run"] != true || data["event"] != "COMMENT" || data["payload_hash"] == "" {
		t.Errorf("post dry-run data = %v", data)
	}
	if _, err := os.Stat(".review/7/post.json"); !os.IsNotExist(err) {
		t.Error("post dry-run wrote post.json")
	}
	if _, err := os.Stat(".review/7/findings.dryrun.json"); err != nil {
		t.Errorf("post dry-run did not stage findings.dryrun.json: %v", err)
	}
	var refused callToolResult
	if err := json.Unmarshal(frames["3"].Result, &refused); err != nil {
		t.Fatalf("post refusal result: %v", err)
	}
	if !refused.IsError {
		t.Error("bad approve did not set isError")
	}
	var refusal map[string]string
	if err := json.Unmarshal(refused.StructuredContent, &refusal); err != nil {
		t.Fatalf("post refusal structuredContent: %v", err)
	}
	if refusal["code"] != "bad-approve" {
		t.Errorf("post refusal code = %q", refusal["code"])
	}
}

// TestReadOnlyPolicy proves --read-only filters the tool list and makes a
// write tool an invalid-params error.
//
//nolint:revive // the policy matrix reads as one assertion block
func TestReadOnlyPolicy(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.ReadOnly = true
	readOnly := allTools(true)
	if len(readOnly) == 0 {
		t.Fatal("no read-only tools")
	}
	for _, tool := range readOnly {
		if !tool.readOnly {
			t.Errorf("%s leaked into --read-only", tool.name)
		}
	}
	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"record","arguments":{"repo":"a/b","pr":"1"}}}`,
	}
	frames := serve(t, &cfg, lines...)
	var list toolsListResult
	if err := json.Unmarshal(frames["1"].Result, &list); err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "record" || tool.Name == "post" {
			t.Errorf("read-only list exposed %q", tool.Name)
		}
		readOnly, ok := tool.Annotations[annotationReadOnly].(bool)
		if !ok || !readOnly {
			t.Errorf("%s readOnlyHint = false", tool.Name)
		}
	}
	if frames["2"].Error == nil || frames["2"].Error.Code != codeInvalidParams {
		t.Fatalf("record in read-only = %+v, want -32602", frames["2"].Error)
	}
}

// TestServeLifecycle drives initialize -> tools/list -> tools/call
// success -> refusal -> ping -> malformed frames in one session.
//
//nolint:revive,gocognit,cyclop // the lifecycle matrix reads as one assertion block
func TestServeLifecycle(t *testing.T) {
	fixture(t)
	cfg := testConfig()
	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"report","arguments":{"repo":"acme/app","pr":"5101"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"report","arguments":{"repo":"acme/app","pr":"9999"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"no/such/method"}`,
		`not json at all`,
	}
	frames := serve(t, &cfg, lines...)
	var init initializeResult
	if err := json.Unmarshal(frames["1"].Result, &init); err != nil {
		t.Fatal(err)
	}
	if init.ProtocolVersion != latestProtocol {
		t.Errorf("protocol = %q", init.ProtocolVersion)
	}
	if !init.ServerInfo.Experimental || init.ServerInfo.Name != serverName {
		t.Errorf("serverInfo = %+v", init.ServerInfo)
	}
	var list toolsListResult
	if err := json.Unmarshal(frames["2"].Result, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 18 {
		t.Errorf("tools = %d", len(list.Tools))
	}
	// Success.
	var ok callToolResult
	if err := json.Unmarshal(frames["3"].Result, &ok); err != nil {
		t.Fatalf("success result: %v", err)
	}
	if ok.IsError {
		t.Errorf("report isError = true: %s", ok.Content[0].Text)
	}
	var data map[string]any
	if err := json.Unmarshal(ok.StructuredContent, &data); err != nil {
		t.Fatalf("structuredContent: %v", err)
	}
	if data["generated_at"] != "2026-11-02T12:00:00Z" {
		t.Errorf("generated_at = %v (clock not injected)", data["generated_at"])
	}
	if string(ok.StructuredContent) != ok.Content[0].Text {
		t.Error("text and structuredContent diverged")
	}
	// Refusal.
	var refused callToolResult
	if err := json.Unmarshal(frames["4"].Result, &refused); err != nil {
		t.Fatalf("refusal result: %v", err)
	}
	if !refused.IsError {
		t.Error("missing store did not set isError")
	}
	var refusal map[string]string
	if err := json.Unmarshal(refused.StructuredContent, &refusal); err != nil {
		t.Fatalf("refusal structuredContent: %v", err)
	}
	if refusal["code"] != "missing-findings" {
		t.Errorf("refusal code = %q", refusal["code"])
	}
	if refusal["hint"] == "" || !strings.Contains(refused.Content[0].Text, refusal["hint"]) {
		t.Errorf("refusal hint = %q", refusal["hint"])
	}
	// Ping, unknown tool, unknown method, parse error.
	if string(frames["5"].Result) != "{}" {
		t.Errorf("ping result = %s", frames["5"].Result)
	}
	if frames["6"].Error == nil || frames["6"].Error.Code != codeInvalidParams {
		t.Errorf("unknown tool = %+v", frames["6"].Error)
	}
	if frames["7"].Error == nil || frames["7"].Error.Code != codeMethodNotFound {
		t.Errorf("unknown method = %+v", frames["7"].Error)
	}
	if frames["null"].Error == nil || frames["null"].Error.Code != codeParse {
		t.Errorf("malformed frame = %+v", frames["null"].Error)
	}
}

// TestCallValidation proves usage-shaped argument errors are -32602, not
// typed refusals.
func TestCallValidation(t *testing.T) {
	fixture(t)
	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"report","arguments":{"repo":"a/b","pr":"1","bogus":true}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"report","arguments":{"repo":"a/b","pr":7}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"report","arguments":[]}}`,
	}
	cfg := testConfig()
	frames := serve(t, &cfg, lines...)
	for id, f := range frames {
		if f.Error == nil || f.Error.Code != codeInvalidParams {
			t.Errorf("id %s = %+v, want -32602", id, f.Error)
		}
	}
}

// TestNegotiate proves the revision fallback and supported echo.
func TestNegotiate(t *testing.T) {
	t.Parallel()
	for _, version := range supportedProtocols {
		if got := negotiate(version); got != version {
			t.Errorf("negotiate(%q) = %q", version, got)
		}
	}
	if got := negotiate("1999-01-01"); got != latestProtocol {
		t.Errorf("unsupported = %q, want %q", got, latestProtocol)
	}
}

// TestArgsDecode proves the reflector decodes nested flags and initialism
// names into the concrete core request type.
//
//nolint:revive,cyclop // the decode matrix reads as one assertion block
func TestArgsDecode(t *testing.T) {
	t.Parallel()
	report := findTool(t, "report")
	raw, err := decodeArgs(report, json.RawMessage(`{"repo":"a/b","pr":"1","flags":{"status":"open","cost":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	req, ok := raw.(*core.ReportRequest)
	if !ok {
		t.Fatalf("decoded type = %T", raw)
	}
	if req.Flags.Status != "open" || !req.Flags.Cost {
		t.Fatalf("flags = %+v", req.Flags)
	}
	triage := findTool(t, "triage")
	decoded, err := decodeArgs(triage, json.RawMessage(`{"repo":"a/b","pr":"1","sig":"abc","status":"addressed","dryRun":true}`))
	if err != nil {
		t.Fatal(err)
	}
	triageReq, ok := decoded.(*triagecore.Request)
	if !ok {
		t.Fatalf("decoded type = %T", decoded)
	}
	if !triageReq.DryRun || triageReq.Sig != "abc" {
		t.Fatalf("triage = %+v", triageReq)
	}
	learn := findTool(t, "learn")
	decoded, err = decodeArgs(learn, json.RawMessage(`{"id":"x","rule":"r","scope":"repo"}`))
	if err != nil {
		t.Fatal(err)
	}
	learnReq, ok := decoded.(*learncore.Request)
	if !ok {
		t.Fatalf("decoded type = %T", decoded)
	}
	if learnReq.ID != "x" || learnReq.Rule != "r" {
		t.Fatalf("learn = %+v", learnReq)
	}
}

// findTool returns one registered tool.
func findTool(t *testing.T, name string) *tool {
	t.Helper()
	for _, tool := range allTools(false) {
		if tool.name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not registered", name)
	return nil
}

// TestEmptyInputExits proves stdin closure ends the session cleanly.
func TestEmptyInputExits(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	cfg := testConfig()
	if err := New(&cfg).Serve(context.Background(), strings.NewReader(""), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q", out.String())
	}
}

// TestCancellation proves the cancel notification reaches the call's
// context.
func TestCancellation(t *testing.T) {
	cfg := testConfig()
	server := New(&cfg)
	server.tools = append(server.tools, &tool{
		name:    "wait",
		request: reflect.TypeFor[struct{}](),
		invoke: func(ctx context.Context, _ any) (*jq.Obj, *core.RefusalError) {
			<-ctx.Done()
			return nil, &core.RefusalError{Code: "canceled", Message: "canceled by host"}
		},
	})
	server.byName["wait"] = server.tools[len(server.tools)-1]
	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wait","arguments":{}}}`,
		`{"jsonrpc":"2.0","method":"` + methodCancelled + `","params":{"requestId":1,"reason":"user"}}`,
	}
	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	frames := decodeFrames(t, out.Bytes())
	var got callToolResult
	if err := json.Unmarshal(frames["1"].Result, &got); err != nil {
		t.Fatal(err)
	}
	if !got.IsError || !strings.Contains(got.Content[0].Text, "canceled") {
		t.Fatalf("canceled result = %+v", got)
	}
}

// TestSchemaNamesSorted keeps the tool list stable for clients.
func TestSchemaNamesSorted(t *testing.T) {
	t.Parallel()
	names := make([]string, 0)
	for _, tool := range allTools(false) {
		names = append(names, tool.name)
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("tool names are not sorted: %v", names)
	}
}
