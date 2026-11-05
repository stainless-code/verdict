package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	learncore "github.com/stainless-code/verdict/internal/core/learn"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/testenv"
)

var fixedNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func testConfig() Config {
	return Config{
		Version: "1.2.3",
		Env:     map[string]string{"HOME": "/nonexistent"},
		Now:     func() time.Time { return fixedNow },
		Stderr:  &bytes.Buffer{},
	}
}

func fixture(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	writeFile(t, ".review/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFile(t, ".review/5101/findings.json", `[]`)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type frame struct { //nolint:govet // field order mirrors the decoded wire frame
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

func serve(t *testing.T, cfg *Config, lines ...string) map[string]frame {
	t.Helper()
	var out bytes.Buffer
	input := strings.NewReader(strings.Join(lines, "\n") + "\n")
	if err := New(cfg).Serve(context.Background(), input, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	return decodeFrames(t, out.Bytes())
}

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
	tools := allTools(false, false)
	if len(tools) != 18 {
		t.Fatalf("tools = %d, want 18", len(tools))
	}
	wantNames := []string{
		"adopt", "calibrate", "check-tree", "doctor", "fetch", "fetch-local", "fix",
		"init", "learn", "next", "post", "ready", "record", "report", "show",
		"threads", "trend", "verify",
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
	cfg := testConfig()
	frames := serve(t, &cfg,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"learn","arguments":{"status":true}}}`,
	)
	var result callToolResult
	if err := json.Unmarshal(frames["1"].Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("learn refused: %s", result.Content[0].Text)
	}
	var data map[string]any
	if err := json.Unmarshal(result.StructuredContent, &data); err != nil {
		t.Fatal(err)
	}
	if rules, ok := data["rules"].([]any); !ok || len(rules) != 0 {
		t.Errorf("learn data = %v", data)
	}
}

type postCommander struct {
	t     *testing.T
	calls []string
}

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

func postFixture(t *testing.T) {
	t.Helper()
	writeFile(t, ".review/7/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"`+strings.Repeat("b", 40)+`","author":{"login":"someone-else"}}`)
	writeFile(t, ".review/7/full.diff",
		"diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n"+
			"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n")
	writeFile(t, ".review/7/review.json",
		`{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,"comments":[]}`)
	writeFile(t, ".review/7/findings.json", `[]`)
	req := &controls.Request{BundleDir: ".review/7", BaseSHA: strings.Repeat("b", 40), AllowHead: true}
	if _, ref := controls.Resolve(t.Context(), nil, req); ref != nil {
		t.Fatal(ref)
	}
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
			if ro := tool.Annotations.ReadOnlyHint; ro {
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

// TestReadOnlyPolicy pins what --read-only serves and what it hides.
//
//nolint:revive,cyclop // the policy matrix reads as one assertion block
func TestReadOnlyPolicy(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.ReadOnly = true
	readOnly := allTools(true, false)
	if len(readOnly) == 0 {
		t.Fatal("no read-only tools")
	}
	for _, tool := range readOnly {
		if !tool.readOnly && !tool.servedWhenReadOnly {
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
	kept := map[string]bool{}
	for _, tool := range list.Tools {
		switch tool.Name {
		case "post", "threads":
			t.Errorf("read-only list exposed %q", tool.Name)
		case "report", "record":
			kept[tool.Name] = true
		}
	}
	if !kept["report"] || !kept["record"] {
		t.Error("read-only list dropped report or record")
	}
	if frames["2"].Error != nil {
		t.Fatalf("record in read-only = %+v, want a tool result", frames["2"].Error)
	}
	var refused callToolResult
	if err := json.Unmarshal(frames["2"].Result, &refused); err != nil {
		t.Fatal(err)
	}
	if !resultIsBadFlags(t, refused) {
		t.Errorf("non-dry record in read-only = %+v, want an isError bad-flags refusal", refused)
	}
}

// TestReadOnlyRecordDryRun proves the read-only gate lets dry_run through:
// the refusal that follows names the bundle, not the read-only server.
func TestReadOnlyRecordDryRun(t *testing.T) {
	t.Parallel()
	readOnly := allTools(true, false)
	var record *tool
	for _, tool := range readOnly {
		if tool.name == "record" {
			record = tool
		}
	}
	if record == nil {
		t.Fatal("read-only record tool is missing")
	}
	request, err := record.decode([]byte(`{"repo":"acme/app","pr":"local-nope","dry_run":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ref := record.invoke(context.Background(), request); ref == nil || ref.Code == "bad-flags" {
		t.Fatalf("dry-run record refusal = %+v, want the bundle gate to speak first", ref)
	}
}

// resultIsBadFlags reports whether a tool result is the read-only refusal.
func resultIsBadFlags(t *testing.T, result callToolResult) bool {
	t.Helper()
	if !result.IsError {
		return false
	}
	return strings.Contains(string(result.StructuredContent), `"code":"bad-flags"`)
}

// TestReadOnlyReportSchema proves the read-only schema offers no writing arg.
func TestReadOnlyReportSchema(t *testing.T) {
	t.Parallel()
	report := toolNamed(t, readOnlyList(t), "report")
	if report.Annotations.ReadOnlyHint {
		t.Error("report readOnlyHint = true, but --output and --sarif write files")
	}
	props := schemaProps(t, report.InputSchema, argFlags)
	for _, arg := range []string{"output", "sarif"} {
		if _, offered := props[arg]; offered {
			t.Errorf("read-only report schema still offers %q", arg)
		}
	}
	if _, ok := props[argStatus]; !ok {
		t.Error("read-only report schema dropped the status filter")
	}
}

func readOnlyList(t *testing.T) toolsListResult {
	t.Helper()
	cfg := testConfig()
	cfg.ReadOnly = true
	frames := serve(t, &cfg, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var list toolsListResult
	if err := json.Unmarshal(frames["1"].Result, &list); err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	return list
}

func toolNamed(t *testing.T, list toolsListResult, name string) toolDescriptor {
	t.Helper()
	for _, tool := range list.Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tools/list has no %q", name)
	return toolDescriptor{}
}

// TestReadOnlyReportRefusesArtifactArgs proves --read-only refuses, not hides.
func TestReadOnlyReportRefusesArtifactArgs(t *testing.T) {
	fixture(t)
	const call = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` +
		`{"name":"report","arguments":{"repo":"acme/app","pr":"5101","flags":{"output":"out.md"}}}}`
	readOnly := testConfig()
	readOnly.ReadOnly = true
	frames := serve(t, &readOnly, call)
	if frames["1"].Error == nil || frames["1"].Error.Code != codeInvalidParams {
		t.Fatalf("report --output in read-only = %+v, want -32602", frames["1"].Error)
	}
	if msg := frames["1"].Error.Message; !strings.Contains(msg, `unknown argument "flags.output"`) {
		t.Errorf("report --output in read-only = %q, want the unknown-argument refusal", msg)
	}
	if _, err := os.Stat("out.md"); !os.IsNotExist(err) {
		t.Fatalf("read-only report wrote out.md: %v", err)
	}
	open := testConfig()
	frames = serve(t, &open, call)
	if frames["1"].Error != nil {
		t.Fatalf("report --output without --read-only = %+v", frames["1"].Error)
	}
	if _, err := os.Stat("out.md"); err != nil {
		t.Errorf("report --output without --read-only wrote no file: %v", err)
	}
}

func schemaProps(t *testing.T, schema map[string]any, key string) map[string]any {
	t.Helper()
	parent, ok := schema[schemaProperties].(map[string]any)
	if !ok {
		t.Fatal("schema has no properties")
	}
	child, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("schema has no %q object", key)
	}
	props, ok := child[schemaProperties].(map[string]any)
	if !ok {
		t.Fatalf("%q has no properties", key)
	}
	return props
}

// TestWriteToolsNeverClaimReadOnly holds every hint to its own description.

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
//nolint:revive // the decode matrix reads as one assertion block
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
	learn := findTool(t, "learn")
	decoded, err := decodeArgs(learn, json.RawMessage(`{"id":"x","rule":"r","scope":"repo"}`))
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

func findTool(t *testing.T, name string) *tool {
	t.Helper()
	for _, tool := range allTools(false, true) {
		if tool.name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not registered", name)
	return nil
}

// TestOperatorSurface proves the human-owned triage verb stays off the
// default and read-only surfaces and appears only with --operator. A call
// without the flag is a hidden-tool error naming --operator; with it, the
// call reaches the service (a typed refusal here is still a tool result, not
// -32602).
//
//nolint:revive // the surface matrix reads as one assertion block
func TestOperatorSurface(t *testing.T) {
	t.Parallel()
	if hasTriage(allTools(false, false)) {
		t.Fatal("triage leaked onto the default surface")
	}
	if hasTriage(allTools(true, false)) {
		t.Fatal("triage leaked onto the --read-only surface")
	}
	if hasTriage(allTools(true, true)) {
		t.Fatal("triage leaked when --read-only and --operator are combined")
	}
	if !hasTriage(allTools(false, true)) {
		t.Fatal("--operator does not expose triage")
	}
	cfg := testConfig()
	frames := serve(t, &cfg,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"triage","arguments":{"repo":"a/b","pr":"1","sig":"deadbeef","status":"wont-fix","rationale":"operator-only test rationale here","dryRun":true}}}`)
	if frames["1"].Error == nil || frames["1"].Error.Code != codeInvalidParams {
		t.Fatalf("triage call without --operator = %+v, want -32602", frames["1"].Error)
	}
	if msg := frames["1"].Error.Message; !strings.Contains(msg, "is hidden") || !strings.Contains(msg, "--operator") {
		t.Errorf("triage call without --operator = %q, want a hidden-tool error naming --operator", msg)
	}
	cfg.Operator = true
	frames = serve(t, &cfg,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"triage","arguments":{"repo":"a/b","pr":"1","sig":"deadbeef","status":"wont-fix","rationale":"operator-only test rationale here","dryRun":true}}}`)
	var list toolsListResult
	if err := json.Unmarshal(frames["1"].Result, &list); err != nil {
		t.Fatal(err)
	}
	if !listHasTriage(list.Tools) {
		t.Fatal("--operator tools/list misses triage")
	}
	if frames["2"].Error != nil {
		t.Fatalf("triage call with --operator = %+v, want a tool result", frames["2"].Error)
	}
}

func hasTriage(tools []*tool) bool {
	for _, tool := range tools {
		if tool.name == "triage" {
			return true
		}
	}
	return false
}

func listHasTriage(tools []toolDescriptor) bool {
	for _, tool := range tools {
		if tool.Name == "triage" {
			return true
		}
	}
	return false
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

// TestServeEndsOnContextCancelWithoutEOF proves a canceled Serve context (the
// binary's SIGINT/SIGTERM) ends a session whose stdin stays open: the
// in-flight call's context is canceled, its canceled refusal is written, and
// Serve returns without error and without waiting for the host to close its
// pipe. Under -race it also pins that the read loop's dispatch and Serve's
// wait for the handlers are ordered. The tool reports "running" through a
// file, not a channel: a real signal carries no happens-before edge from the
// dispatch, and a channel here would hand the race detector one.
func TestServeEndsOnContextCancelWithoutEOF(t *testing.T) {
	cfg := testConfig()
	server := New(&cfg)
	running := filepath.Join(t.TempDir(), "running")
	server.tools = append(server.tools, runningWaitTool(running))
	server.byName["wait"] = server.tools[len(server.tools)-1]
	stdin, host := io.Pipe()
	t.Cleanup(func() { host.Close() }) // unblocks the read loop Serve leaves behind
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, stdin, &out) }()
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wait","arguments":{}}}` + "\n"
	if _, err := host.Write([]byte(call)); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, running)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve = %v, want nil on a context shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve still running 5s after its context was canceled, with stdin open")
	}
	var got callToolResult
	if err := json.Unmarshal(decodeFrames(t, out.Bytes())["1"].Result, &got); err != nil {
		t.Fatal(err)
	}
	if !got.IsError || !strings.Contains(got.Content[0].Text, "canceled by shutdown") {
		t.Fatalf("in-flight call result = %+v, want the canceled refusal", got)
	}
}

func runningWaitTool(running string) *tool {
	return &tool{
		name:    "wait",
		request: reflect.TypeFor[struct{}](),
		invoke: func(ctx context.Context, _ any) (*jq.Obj, *core.RefusalError) {
			if err := os.WriteFile(running, nil, 0o600); err != nil {
				return nil, &core.RefusalError{Code: "internal", Message: err.Error()}
			}
			<-ctx.Done()
			return nil, &core.RefusalError{Code: "canceled", Message: "canceled by shutdown"}
		},
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	testenv.Until(t, func() bool {
		_, err := os.Stat(path)
		return err == nil
	})
}

// TestSchemaNamesSorted keeps the tool list stable for clients.
func TestSchemaNamesSorted(t *testing.T) {
	t.Parallel()
	names := make([]string, 0)
	for _, tool := range allTools(false, false) {
		names = append(names, tool.name)
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("tool names are not sorted: %v", names)
	}
}

// TestRefusalResultRetryAt proves a retryable refusal carries retry_at in
// both MCP representations, and a plain refusal stays unchanged.
func TestRefusalResultRetryAt(t *testing.T) {
	t.Parallel()
	res := refusalResult(&core.RefusalError{
		Code: "post-failed", Message: "post failed", RetryAt: "2026-11-02T12:00:05Z",
	})
	if !strings.Contains(res.Content[0].Text, "retry_at: 2026-11-02T12:00:05Z") {
		t.Errorf("text = %q, want retry_at", res.Content[0].Text)
	}
	var got map[string]string
	if err := json.Unmarshal(res.StructuredContent, &got); err != nil {
		t.Fatalf("structuredContent: %v", err)
	}
	if got["retry_at"] != "2026-11-02T12:00:05Z" {
		t.Errorf("structured retry_at = %q", got["retry_at"])
	}
	plain := refusalResult(&core.RefusalError{Code: "post-failed", Message: "post failed"})
	if strings.Contains(plain.Content[0].Text, "retry_at") {
		t.Errorf("plain text carries retry_at: %q", plain.Content[0].Text)
	}
	var plainMap map[string]any
	if err := json.Unmarshal(plain.StructuredContent, &plainMap); err != nil {
		t.Fatalf("plain structuredContent: %v", err)
	}
	if _, ok := plainMap["retry_at"]; ok {
		t.Error("plain structuredContent carries retry_at")
	}
}

type threadsCommander struct {
	t    *testing.T
	page string
}

func (c *threadsCommander) Run(_ context.Context, cmd core.Command) (core.Result, error) {
	if strings.Contains(strings.Join(cmd.Argv, " "), "api graphql") {
		return core.Result{Stdout: c.page}, nil
	}
	c.t.Errorf("unexpected threads argv: %q", cmd.Argv)
	return core.Result{}, errors.New("unexpected argv")
}

func (c *threadsCommander) LookPath(string) error { return nil }

// TestThreadsTool proves the MCP threads tool refreshes its bundle snapshot, is
// open-world, and returns the same data profile as the CLI.
//
//nolint:revive // the registry/refresh matrix reads as one assertion block
func TestThreadsTool(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFile(t, ".review/7/PR.json",
		`{"state":"OPEN","headRefOid":"`+strings.Repeat("a", 40)+`"}`)
	var registered *tool
	for _, candidate := range allTools(false, false) {
		if candidate.name == "threads" {
			registered = candidate
			break
		}
	}
	if registered == nil {
		t.Fatal("threads tool is not registered")
	}
	if registered.readOnly || !registered.openWorld || !registered.idempotent {
		t.Errorf("threads annotations = %+v", registered)
	}
	page := `{"data":{"repository":{"pullRequest":{"reviewThreads":{` +
		`"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[` +
		`{"id":"t1","isResolved":false,"isOutdated":false,"path":"a.go","line":2,"diffSide":"RIGHT",` +
		`"comments":{"totalCount":1,"nodes":[{"id":"c1","databaseId":9,` +
		`"author":{"__typename":"User","login":"ana"},"body":"nit","createdAt":"2026-11-01T00:00:00Z"}]}}]}}}}}`
	cmdr := &threadsCommander{t: t, page: page}
	req := &fetchcore.ThreadsRequest{}
	req.ReadRequest = core.ReadRequest{
		Runtime: &core.Runtime{Env: map[string]string{}, Now: func() time.Time { return fixedNow }, Commander: cmdr},
		Repo:    "o/r", Pr: "7",
	}
	data, ref := registered.invoke(context.Background(), req)
	if ref != nil {
		t.Fatalf("threads invoke refusal: %v", ref)
	}
	threadsVal, _ := data.Get("threads")
	if got, ok := jq.IntOf(threadsVal); !ok || got != 1 {
		t.Errorf("data.threads = %v", threadsVal)
	}
	if got, _ := data.Get("provider"); got != "github" {
		t.Errorf("data.provider = %v", got)
	}
	if _, err := os.Stat(".review/7/threads.json"); err != nil {
		t.Errorf("refresh wrote no feed: %v", err)
	}
}

func concurrentReport(id int) string {
	pr := "9999"
	if id%2 == 0 {
		pr = "5101"
	}
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"report","arguments":{"repo":"acme/app","pr":%q}}}`, id, pr)
}

// TestConcurrentCallsAreIsolated drives many tool calls in flight at once
// (Serve dispatches each on its own goroutine) and proves every response is
// whole, correctly routed by id, and identical to the same call made alone.
// Under -race it also covers the shared writer, the in-flight table, and the
// read-only config the tools share.
func TestConcurrentCallsAreIsolated(t *testing.T) {
	fixture(t)
	cfg := testConfig()
	alone := serve(t, &cfg, concurrentReport(0), concurrentReport(1))
	if bytes.Equal(alone["0"].Result, alone["1"].Result) {
		t.Fatal("baseline success and refusal are identical; the test would not notice a mixed-up response")
	}
	const first, calls = 100, 48
	lines := make([]string, 0, calls)
	for id := first; id < first+calls; id++ {
		lines = append(lines, concurrentReport(id))
	}
	frames := serve(t, &cfg, lines...)
	if len(frames) != calls {
		t.Fatalf("got %d response frames, want %d", len(frames), calls)
	}
	for id := first; id < first+calls; id++ {
		got := frames[strconv.Itoa(id)]
		if want := alone[strconv.Itoa(id%2)]; !bytes.Equal(got.Result, want.Result) {
			t.Errorf("id %d result differs from the same call alone:\n got  %s\n want %s", id, got.Result, want.Result)
		}
	}
}
