package mcp

// The dashboard query tools and verdict:// resources compose through the
// shared query layer: these tests pin the tool results, the resource
// addresses, the root confinement, and the read-only proof. Fixtures use
// literal sigs and model names, never values rebuilt from the code.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core/query"
)

const (
	querySigOpen = "c111111111111111111111111111111111111111"
	querySigDone = "d444444444444444444444444444444444444444"
)

// queryFixture writes one PR bundle and one local baseline bundle with an
// open finding, a cost record, and a post receipt under the temp root.
func queryFixture(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	headA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	pr := `{"state":"OPEN","headRefOid":"` + headA + `","baseRefOid":"` + headB + `",` +
		`"author":{"login":"teammate"},"url":"https://github.com/acme/app/pull/1234"}`
	diff := "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
		"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"
	writeFile(t, ".verdict/state/1234/PR.json", pr)
	writeFile(t, ".verdict/state/1234/full.diff", diff)
	writeFile(t, ".verdict/state/1234/post.json",
		`{"schema_version":"1","review":{"id":7},"head":"`+headA+`","model":"alpha",`+
			`"posted_at":"2026-11-12T12:00:00Z","confidence":null,"security_summary":null}`)
	writeFile(t, ".verdict/state/1234/findings.json", "["+queryFinding(querySigOpen, "open", "critical")+
		","+queryFinding(querySigDone, "addressed", "important")+"]")
	writeFile(t, ".verdict/state/1234/cost.json",
		`[{"models":[{"name":"alpha","input_tokens":400,"output_tokens":100}]}]`)
	writeFile(t, ".verdict/state/local-1/PR.json",
		`{"state":"OPEN","headRefOid":"`+headB+`","baseRefOid":"`+headA+`","author":{"login":"teammate"}}`)
	writeFile(t, ".verdict/state/local-1/full.diff", diff)
	writeFile(t, ".verdict/state/local-1/findings.json", "["+queryFinding(querySigOpen, "open", "critical")+"]")
	writeFile(t, ".verdict/state/local-1/cost.json",
		`[{"models":[{"name":"alpha","input_tokens":10,"output_tokens":2}]}]`)
}

func queryFinding(sig, status, severity string) string {
	return fmt.Sprintf(`{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,`+
		`"anchored":true,"body":"Usable body here with enough substance.","suggestion":null,`+
		`"inBounds":true,"rigor":"walked","severity":%q,"sig":%q,"sig_version":"1",`+
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":%q,"rationale":"","history":[]}`,
		severity, sig, status)
}

func queryCall(t *testing.T, cfg *Config, name, arguments string) callToolResult {
	t.Helper()
	frames := serve(t, cfg, fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, name, arguments))
	if frames["1"].Error != nil {
		t.Fatalf("%s call error = %+v", name, frames["1"].Error)
	}
	var result callToolResult
	if err := json.Unmarshal(frames["1"].Result, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func queryData(t *testing.T, result callToolResult) map[string]any {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool refused: %s", result.Content[0].Text)
	}
	if string(result.StructuredContent) != result.Content[0].Text {
		t.Error("tool text and structuredContent diverged")
	}
	var data map[string]any
	if err := json.Unmarshal(result.StructuredContent, &data); err != nil {
		t.Fatal(err)
	}
	return data
}

// rowField reads one typed field out of a query row, failing the test when
// the row carries no such field.
func rowField[T any](t *testing.T, row any, key string) T {
	t.Helper()
	obj, ok := row.(map[string]any)
	if !ok {
		t.Fatalf("row is not an object: %v", row)
	}
	value, ok := obj[key].(T)
	if !ok {
		t.Fatalf("row %s is not a %T: %v", key, *new(T), obj[key])
	}
	return value
}

//
//nolint:revive // the listing matrix reads as one assertion block
func TestQueryToolsListBundles(t *testing.T) {
	queryFixture(t)
	cfg := testConfig()
	data := queryData(t, queryCall(t, &cfg, "query-bundles", `{}`))
	rows, ok := data["rows"].([]any)
	if !ok {
		t.Fatalf("bundles rows missing: %v", data)
	}
	if len(rows) != 2 {
		t.Fatalf("bundles rows = %d, want 2", len(rows))
	}
	bundles := map[string]bool{}
	for _, row := range rows {
		bundles[rowField[string](t, row, "bundle")] = true
	}
	if !bundles["1234"] || !bundles["local-1"] {
		t.Errorf("bundles rows = %v, want 1234 and local-1", bundles)
	}
	patterned := queryData(t, queryCall(t, &cfg, "query-bundles", `{"pattern":"1234"}`))
	prows, ok := patterned["rows"].([]any)
	if !ok {
		t.Fatalf("patterned rows missing: %v", patterned)
	}
	if len(prows) != 1 || rowField[string](t, prows[0], "bundle") != "1234" {
		t.Errorf("patterned rows = %v, want only 1234", patterned["rows"])
	}
}

func TestQueryToolsOpenFindings(t *testing.T) {
	queryFixture(t)
	cfg := testConfig()
	data := queryData(t, queryCall(t, &cfg, "query-findings", `{"bundle":"1234"}`))
	report, ok := data["report"].(map[string]any)
	if !ok {
		t.Fatalf("detail report missing: %v", data)
	}
	items, ok := report["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("detail items missing: %v", report)
	}
	sigs := map[string]bool{}
	for _, item := range items {
		sigs[rowField[string](t, item, "sig")] = true
	}
	if !sigs[querySigOpen] {
		t.Errorf("detail items = %v, want the open finding %s", sigs, querySigOpen)
	}
}

//
//nolint:revive // the by-matrix reads as one assertion block
func TestQueryToolsCostByModel(t *testing.T) {
	queryFixture(t)
	cfg := testConfig()
	for _, args := range []string{`{"by":"model"}`, `{}`} {
		data := queryData(t, queryCall(t, &cfg, "query-cost", args))
		cost, ok := data["cost"].(map[string]any)
		if !ok {
			t.Fatalf("cost missing: %v", data)
		}
		if cost["by"] != "model" {
			t.Errorf("cost.by = %v, want model", cost["by"])
		}
		rows, ok := cost["rows"].([]any)
		if !ok || len(rows) == 0 {
			t.Fatalf("cost rows missing: %v", cost)
		}
		if rowField[string](t, rows[0], "dimension") != "alpha" {
			t.Errorf("cost rows = %v, want the alpha model bucket", cost["rows"])
		}
	}
	data := queryData(t, queryCall(t, &cfg, "query-cost", `{"by":"repo"}`))
	cost, ok := data["cost"].(map[string]any)
	if !ok {
		t.Fatalf("cost missing: %v", data)
	}
	if cost["by"] != "repo" {
		t.Errorf("cost.by = %v, want repo", cost["by"])
	}
}

// TestQueryVocabularyDrivesRouting pins that the by-vocabulary the query
// layer owns is the vocabulary the MCP surface serves: every declared cost
// and precision dimension is a valid resource URI, and the query-cost
// schema enum is exactly the layer's cost vocabulary.
//
//nolint:gocognit,revive,cyclop // the vocabulary matrix reads as one assertion block
func TestQueryVocabularyDrivesRouting(t *testing.T) {
	t.Parallel()
	for _, dim := range query.CostDimensions() {
		q, ok := parseQueryURI("verdict://cost/" + dim)
		if !ok || q.by != dim {
			t.Errorf("cost URI for %q = %+v, %v; want the dimension %q", dim, q, ok, dim)
		}
	}
	for _, dim := range query.PrecisionDimensions() {
		q, ok := parseQueryURI("verdict://precision/" + dim)
		if !ok || q.by != dim {
			t.Errorf("precision URI for %q = %+v, %v; want the dimension %q", dim, q, ok, dim)
		}
	}
	if q, ok := parseQueryURI("verdict://cost"); !ok || q.by != query.DefaultCostBy {
		t.Errorf("bare cost URI = %+v, %v; want the default %q", q, ok, query.DefaultCostBy)
	}
	if got := query.CostDimensions(); !slices.Contains(got, query.DefaultCostBy) {
		t.Errorf("cost vocabulary = %v, want it to contain the default %q", got, query.DefaultCostBy)
	}
	if _, ok := parseQueryURI("verdict://cost/bogus"); ok {
		t.Error("cost URI for an undeclared dimension parsed; want resource-not-found")
	}
	if _, ok := parseQueryURI("verdict://precision/bogus"); ok {
		t.Error("precision URI for an undeclared dimension parsed; want resource-not-found")
	}
	dims := query.CostDimensions()
	dims[0] = "mutated"
	if got := query.CostDimensions(); got[0] == "mutated" {
		t.Errorf("cost vocabulary followed a mutated slice: %v", got)
	}
	props, ok := inputSchema(findTool(t, "query-cost"))[schemaProperties].(map[string]any)
	if !ok {
		t.Fatal("query-cost schema has no properties")
	}
	by, ok := props["by"].(map[string]any)
	if !ok {
		t.Fatalf("query-cost by property = %T, want an object", props["by"])
	}
	enum, ok := by[schemaEnum].([]any)
	if !ok {
		t.Fatalf("query-cost by enum = %T, want a list", by[schemaEnum])
	}
	got := make([]string, 0, len(enum))
	for _, value := range enum {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("query-cost by enum value = %T, want a string", value)
		}
		got = append(got, text)
	}
	if want := query.CostDimensions(); !slices.Equal(got, want) {
		t.Errorf("query-cost by enum = %v, want the layer vocabulary %v", got, want)
	}
}

func TestQueryToolsFindingDetailAndCopy(t *testing.T) {
	queryFixture(t)
	cfg := testConfig()
	args := fmt.Sprintf(`{"bundle":"1234","sig":%q}`, querySigOpen)
	data := queryData(t, queryCall(t, &cfg, "query-finding", args))
	item, ok := data["item"].(map[string]any)
	if !ok || item["sig"] != querySigOpen {
		t.Errorf("finding item = %v, want sig %s", data["item"], querySigOpen)
	}
	copyData := queryData(t, queryCall(t, &cfg, "query-copy", args))
	if copyData["sig"] != querySigOpen {
		t.Errorf("copy sig = %v, want %s", copyData["sig"], querySigOpen)
	}
	triage, ok := copyData["triage"].(string)
	if !ok || !strings.Contains(triage, querySigOpen) {
		t.Errorf("copy triage = %v, want the runnable command naming %s", copyData["triage"], querySigOpen)
	}
}

func TestQueryToolsHealth(t *testing.T) {
	queryFixture(t)
	cfg := testConfig()
	data := queryData(t, queryCall(t, &cfg, "query-health", `{}`))
	roots, ok := data["roots"].([]any)
	if !ok || len(roots) != 1 {
		t.Errorf("health roots = %v, want one served root", data["roots"])
	}
	if data["version"] != "1.2.3" {
		t.Errorf("health version = %v, want the build version", data["version"])
	}
}

//nolint:gocognit,revive,cyclop // the refusal matrix reads as one assertion block
func TestQueryToolsRefusals(t *testing.T) {
	queryFixture(t)
	cfg := testConfig()
	missing := queryCall(t, &cfg, "query-findings", `{"bundle":"no-such-bundle"}`)
	if !missing.IsError {
		t.Fatal("missing bundle did not set isError")
	}
	var refusal map[string]any
	if err := json.Unmarshal(missing.StructuredContent, &refusal); err != nil {
		t.Fatal(err)
	}
	if refusal["code"] != "not-found" {
		t.Errorf("missing bundle code = %v, want not-found", refusal["code"])
	}
	unknown := queryCall(t, &cfg, "query-finding",
		fmt.Sprintf(`{"bundle":"1234","sig":%q}`, "00000000"))
	if !unknown.IsError {
		t.Fatal("unknown sig did not set isError")
	}
	if err := json.Unmarshal(unknown.StructuredContent, &refusal); err != nil {
		t.Fatal(err)
	}
	if refusal["code"] != "unknown-sig" {
		t.Errorf("unknown sig code = %v, want unknown-sig", refusal["code"])
	}
	for _, pattern := range []string{"../x", "/abs", "a//b"} {
		bad := queryCall(t, &cfg, "query-bundles", fmt.Sprintf(`{"pattern":%q}`, pattern))
		if !bad.IsError {
			t.Fatalf("pattern %q did not set isError", pattern)
		}
		if err := json.Unmarshal(bad.StructuredContent, &refusal); err != nil {
			t.Fatal(err)
		}
		if refusal["code"] != "bad-glob" {
			t.Errorf("pattern %q code = %v, want bad-glob", pattern, refusal["code"])
		}
	}
	// `[` is a valid literal glob (only `*` is special), so it lists zero
	// bundles instead of refusing: no bundle is named `[`.
	bracket := queryCall(t, &cfg, "query-bundles", `{"pattern":"["}`)
	if bracket.IsError {
		t.Fatalf("[ refused: %s", bracket.Content[0].Text)
	}
	data := queryData(t, bracket)
	if rows, ok := data["rows"].([]any); !ok || len(rows) != 0 {
		t.Errorf("[ rows = %v, want zero rows", data["rows"])
	}
}

//
//nolint:revive // the template matrix reads as one assertion block
func TestQueryTemplatesList(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	frames := serve(t, &cfg, `{"jsonrpc":"2.0","id":1,"method":"resources/templates/list"}`)
	if frames["1"].Error != nil {
		t.Fatalf("templates/list error = %+v", frames["1"].Error)
	}
	var result struct {
		ResourceTemplates []struct {
			URITemplate string `json:"uriTemplate"`
			Name        string `json:"name"`
			MIMEType    string `json:"mimeType"`
		} `json:"resourceTemplates"`
	}
	if err := json.Unmarshal(frames["1"].Result, &result); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"verdict://findings/{bundle}",
		"verdict://finding/{bundle}/{sig}",
		"verdict://compare/{bundle}/{baseline}",
		"verdict://cost/{by}",
		"verdict://precision/{by}",
	}
	if len(result.ResourceTemplates) != len(want) {
		t.Fatalf("templates = %d, want %d", len(result.ResourceTemplates), len(want))
	}
	for i, template := range result.ResourceTemplates {
		if template.URITemplate != want[i] {
			t.Errorf("template %d = %q, want %q", i, template.URITemplate, want[i])
		}
		if template.MIMEType != "application/json" {
			t.Errorf("template %d mimeType = %q, want application/json", i, template.MIMEType)
		}
	}
}

func TestQueryResourcesRead(t *testing.T) {
	queryFixture(t)
	uris := []string{
		"verdict://roots",
		"verdict://bundles",
		"verdict://health",
		"verdict://cost",
		"verdict://cost/repo",
		"verdict://findings/1234",
		"verdict://finding/1234/" + querySigOpen,
		"verdict://compare/1234/local-1",
		"verdict://precision/category",
	}
	lines := make([]string, 0, len(uris))
	for i, uri := range uris {
		lines = append(lines, fmt.Sprintf(
			`{"jsonrpc":"2.0","id":%d,"method":"resources/read","params":{"uri":%q}}`, i+1, uri))
	}
	cfg := testConfig()
	frames := serve(t, &cfg, lines...)
	for i, uri := range uris {
		checkQueryResourceRead(t, uri, frames[strconv.Itoa(i+1)])
	}
}

func checkQueryResourceRead(t *testing.T, uri string, got frame) {
	t.Helper()
	if got.Error != nil {
		t.Fatalf("resources/read %s error = %+v", uri, got.Error)
	}
	var result resourceReadResult
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("%s contents = %d, want one", uri, len(result.Contents))
	}
	content := result.Contents[0]
	if content.URI != uri {
		t.Errorf("%s content uri = %q", uri, content.URI)
	}
	if content.MIMEType != "application/json" {
		t.Errorf("%s mimeType = %q, want application/json", uri, content.MIMEType)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(content.Text), &data); err != nil {
		t.Fatalf("%s text is not JSON: %v", uri, err)
	}
	if _, ok := data["at"]; !ok {
		t.Errorf("%s text misses the envelope clock", uri)
	}
}

func TestQueryResourcesRefuseBadURIs(t *testing.T) {
	queryFixture(t)
	uris := []string{
		"file:///etc/passwd",
		"verdict://findings/..",
		"verdict://findings/%2e%2e",
		"verdict://findings/../1234",
		"verdict://unknown",
		"verdict://finding/1234/nothex",
		"verdict://cost/bogus",
		"verdict://precision/bogus",
		"verdict://findings/no-such-bundle",
		"verdict://finding/1234/00000000",
		"verdict://",
	}
	lines := make([]string, 0, len(uris))
	for i, uri := range uris {
		lines = append(lines, fmt.Sprintf(
			`{"jsonrpc":"2.0","id":%d,"method":"resources/read","params":{"uri":%q}}`, i+1, uri))
	}
	cfg := testConfig()
	frames := serve(t, &cfg, lines...)
	for i, uri := range uris {
		checkQueryRefusal(t, uri, frames[strconv.Itoa(i+1)])
	}
}

// checkQueryRefusal pins the dashboard resource error codes: a URI the
// grammar does not admit and a bundle the layer does not hold are
// resource-not-found (-32002), while an addressable-but-missing finding is
// invalid params (-32602) naming the refusal code in error data. The URI
// appears in every message.
func checkQueryRefusal(t *testing.T, uri string, got frame) {
	t.Helper()
	if got.Error == nil {
		t.Fatalf("resources/read %q answered without an error", uri)
	}
	wantCode := codeResourceNotFound
	if uri == "verdict://finding/1234/00000000" {
		wantCode = codeInvalidParams
	}
	if got.Error.Code != wantCode {
		t.Errorf("resources/read %q code = %d, want %d", uri, got.Error.Code, wantCode)
	}
	if !strings.Contains(got.Error.Message, uri) && wantCode == codeResourceNotFound {
		t.Errorf("resources/read %q message = %q, want the URI", uri, got.Error.Message)
	}
	if wantCode == codeInvalidParams {
		checkRefusalHint(t, uri, got)
	}
}

// checkRefusalHint pins that a -32602 resource refusal carries the refusal
// payload's hint alongside its code, exactly as a tool refusal does.
func checkRefusalHint(t *testing.T, uri string, got frame) {
	t.Helper()
	data, ok := got.Error.Data.(map[string]any)
	if !ok {
		t.Fatalf("resources/read %q error data = %T, want the refusal payload", uri, got.Error.Data)
	}
	hint, ok := data["hint"].(string)
	if !ok || hint == "" {
		t.Errorf("resources/read %q error data hint = %v, want a non-empty hint", uri, data["hint"])
	}
	if data["code"] != "unknown-sig" {
		t.Errorf("resources/read %q error data code = %v, want unknown-sig", uri, data["code"])
	}
}

// snapshotFiles records every file's size and mtime under dir, so a test
// can prove a read changed nothing on disk.
//
//nolint:revive // the snapshot walk is one flat directory loop
func snapshotFiles(t *testing.T, dir string) map[string][2]int64 {
	t.Helper()
	snap := map[string][2]int64{}
	err := filepath.WalkDir(dir, func(p string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, serr := entry.Info()
		if serr != nil {
			return serr
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		snap[rel] = [2]int64{info.Size(), info.ModTime().UnixNano()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

//
//nolint:revive // the read-only proof reads as one assertion block
func TestQueryToolsAreReadOnly(t *testing.T) {
	queryFixture(t)
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotFiles(t, dir)
	cfg := testConfig()
	args := fmt.Sprintf(`{"bundle":"1234","sig":%q}`, querySigOpen)
	for _, call := range [][2]string{
		{"query-bundles", `{}`},
		{"query-findings", `{"bundle":"1234"}`},
		{"query-finding", args},
		{"query-copy", args},
		{"query-cost", `{"by":"model"}`},
		{"query-health", `{}`},
	} {
		result := queryCall(t, &cfg, call[0], call[1])
		if result.IsError {
			t.Fatalf("%s refused: %s", call[0], result.Content[0].Text)
		}
	}
	uris := []string{
		"verdict://roots",
		"verdict://bundles",
		"verdict://health",
		"verdict://cost",
		"verdict://cost/repo",
		"verdict://findings/1234",
		"verdict://finding/1234/" + querySigOpen,
		"verdict://compare/1234/local-1",
		"verdict://precision/category",
	}
	lines := make([]string, 0, len(uris))
	for i, uri := range uris {
		lines = append(lines, fmt.Sprintf(
			`{"jsonrpc":"2.0","id":%d,"method":"resources/read","params":{"uri":%q}}`, i+1, uri))
	}
	frames := serve(t, &cfg, lines...)
	for i, uri := range uris {
		if got := frames[strconv.Itoa(i+1)]; got.Error != nil {
			t.Fatalf("resources/read %s error = %+v", uri, got.Error)
		}
	}
	after := snapshotFiles(t, dir)
	if len(before) != len(after) {
		t.Fatalf("file count = %d, want %d: the query tools wrote something", len(after), len(before))
	}
	for rel := range before {
		if before[rel] != after[rel] {
			t.Errorf("%s changed across the query tools", rel)
		}
	}
}

func TestQueryConfinesTraversal(t *testing.T) {
	queryFixture(t)
	cfg := testConfig()
	refused := queryCall(t, &cfg, "query-findings", `{"bundle":"../outside"}`)
	if !refused.IsError {
		t.Fatal("traversal bundle did not set isError")
	}
	var refusal map[string]any
	if err := json.Unmarshal(refused.StructuredContent, &refusal); err != nil {
		t.Fatal(err)
	}
	if refusal["code"] != "bad-flags" {
		t.Errorf("traversal code = %v, want bad-flags (the bundle-name guard)", refusal["code"])
	}
}

//
//nolint:revive // the surface matrix reads as one assertion block
func TestQueryToolsServeReadOnly(t *testing.T) {
	queryFixture(t)
	cfg := testConfig()
	cfg.ReadOnly = true
	frames := serve(t, &cfg, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var list toolsListResult
	if err := json.Unmarshal(frames["1"].Result, &list); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"query-bundles", "query-findings", "query-finding", "query-copy", "query-cost", "query-health"} {
		found := false
		for _, tool := range list.Tools {
			if tool.Name == want {
				found = true
				if !tool.Annotations.ReadOnlyHint {
					t.Errorf("%s readOnlyHint is not true in --read-only", want)
				}
			}
		}
		if !found {
			t.Errorf("--read-only list misses %q", want)
		}
	}
	data := queryData(t, queryCall(t, &cfg, "query-health", `{}`))
	if _, ok := data["roots"]; !ok {
		t.Errorf("read-only health misses roots: %v", data)
	}
}

// TestQueryServesRepoNotInstallRoot pins that the dashboard reads serve the
// repository the server runs in, not Config.Root: an installed binary's
// root is its install directory, which here carries a decoy bundle the
// query surface must never list.
func TestQueryServesRepoNotInstallRoot(t *testing.T) {
	install := t.TempDir()
	writeFile(t, filepath.Join(install, "package.json"), `{"version":"1.2.3"}`)
	writeFile(t, filepath.Join(install, ".verdict/state/9999/findings.json"), "[]")
	queryFixture(t)
	cfg := testConfig()
	cfg.Root = install
	data := queryData(t, queryCall(t, &cfg, "query-bundles", `{}`))
	rows, ok := data["rows"].([]any)
	if !ok {
		t.Fatalf("bundles rows missing: %v", data)
	}
	var got []string
	for _, row := range rows {
		got = append(got, rowField[string](t, row, "bundle"))
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"1234", "local-1"}) {
		t.Errorf("query-bundles = %v, want the repo's [1234 local-1]", got)
	}
	findings := queryData(t, queryCall(t, &cfg, "query-findings", `{"bundle":"1234"}`))
	if _, ok := findings["report"].(map[string]any); !ok {
		t.Errorf("query-findings 1234 = %v, want the repo bundle's report", findings)
	}
	frames := serve(t, &cfg,
		`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"verdict://findings/1234"}}`)
	checkQueryResourceRead(t, "verdict://findings/1234", frames["1"])
}
