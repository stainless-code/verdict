package ingestcore

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
)

func fixedRuntime() *core.Runtime {
	return &core.Runtime{
		Now: func() time.Time {
			return time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
		},
	}
}

func isolateTestEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func setupBundle(t *testing.T, pr, headSHA string) (repoRoot, dir string) {
	t.Helper()
	isolateTestEnv(t)
	repoRoot = t.TempDir()
	dir = filepath.Join(repoRoot, ".verdict", "state", pr)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(`{"headRefOid":"`+headSHA+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(sampleDiff), 0o644); err != nil {
		t.Fatal(err)
	}
	return repoRoot, dir
}

func newReadReq(repoRoot, pr string) core.ReadRequest {
	return core.ReadRequest{
		Runtime:  fixedRuntime(),
		RepoRoot: repoRoot,
		Pr:       pr,
	}
}

func runIngestFile(t *testing.T, repoRoot, pr, sarifPath string) *Response {
	t.Helper()
	req := Request{ReadRequest: newReadReq(repoRoot, pr), SARIF: sarifPath}
	resp, ref := Run(context.Background(), &req)
	if ref != nil {
		t.Fatalf("unexpected refusal: %+v", ref)
	}
	return resp
}

func runIngestBytes(repoRoot, pr string, body []byte) (*Response, *core.RefusalError) {
	req := Request{
		ReadRequest: newReadReq(repoRoot, pr),
		SARIFData:   body,
		SARIFInline: true,
	}
	return Run(context.Background(), &req)
}

func loadStoreEntries(t *testing.T, dir string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "findings.json"))
	if err != nil {
		t.Fatalf("read findings.json: %v", err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("unmarshal findings.json: %v", err)
	}
	return entries
}

func markFirstFalsePositive(t *testing.T, dir string) {
	t.Helper()
	rawStore, err := os.ReadFile(filepath.Join(dir, "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jq.DecodeOrdered(rawStore)
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := doc.([]any)
	if !ok || len(arr) == 0 {
		t.Fatalf("unexpected decoded findings array: %T", doc)
	}
	first, ok := arr[0].(*jq.Obj)
	if !ok {
		t.Fatalf("unexpected first finding element: %T", arr[0])
	}
	first.Set("status", "false-positive")
	if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte(jq.PrettyOrdered(arr)), 0o644); err != nil {
		t.Fatal(err)
	}
}

const sampleDiff = "" +
	"diff --git a/pkg/auth.go b/pkg/auth.go\n" +
	"index 1111111..2222222 100644\n" +
	"--- a/pkg/auth.go\n" +
	"+++ b/pkg/auth.go\n" +
	"@@ -10,0 +11,4 @@ package auth\n" +
	"+func Verify() {\n" +
	"+  checkToken()\n" +
	"+}\n" +
	"diff --git a/vendor/gen.go b/vendor/gen.go\n" +
	"new file mode 100644\n" +
	"--- /dev/null\n" +
	"+++ b/vendor/gen.go\n" +
	"@@ -0,0 +1,2 @@\n" +
	"+package gen\n"

const testSARIFDoc = `{
  "version": "2.1.0",
  "runs": [
    {
      "tool": {
        "driver": {
          "name": "semgrep",
          "rules": [
            {
              "id": "go.jwt.none",
              "defaultConfiguration": {"level": "warning"},
              "properties": {"security-severity": 8.5}
            },
            {
              "id": "go.style.nit",
              "defaultConfiguration": {"level": "note"}
            }
          ]
        }
      },
      "results": [
        {
          "ruleId": "go.jwt.none",
          "message": {"text": "JWT signature skipped for @team"},
          "partialFingerprints": {"primaryLocationLineHash": "fp-jwt-1"},
          "locations": [{"physicalLocation": {"artifactLocation": {"uri": "pkg/auth.go"}, "region": {"startLine": 12, "snippet": {"text": "  checkToken()"}}}}]
        },
        {
          "ruleId": "go.jwt.none",
          "message": {"text": "JWT signature skipped for @team"},
          "partialFingerprints": {"primaryLocationLineHash": "fp-jwt-1"},
          "locations": [{"physicalLocation": {"artifactLocation": {"uri": "pkg/auth.go"}, "region": {"startLine": 12, "snippet": {"text": "  checkToken()"}}}}]
        },
        {
          "ruleId": "go.style.nit",
          "level": "warning",
          "message": {"text": "Out of diff context note"},
          "locations": [{"physicalLocation": {"artifactLocation": {"uri": "pkg/auth.go"}, "region": {"startLine": 99}}}]
        },
        {
          "ruleId": "go.vendor.rule",
          "level": "error",
          "message": {"text": "Generated vendor issue"},
          "locations": [{"physicalLocation": {"artifactLocation": {"uri": "vendor/gen.go"}, "region": {"startLine": 1}}}]
        },
        {
          "ruleId": "go.repo.policy",
          "level": "note",
          "message": {"text": "Missing SECURITY.md"}
        }
      ]
    }
  ]
}`

func assertInitialEntries(t *testing.T, entries []map[string]any) {
	t.Helper()
	if len(entries) != 2 {
		t.Fatalf("findings count = %d, want 2", len(entries))
	}
	assertEntry0(t, entries[0])
	assertEntry1(t, entries[1])
}

func assertEntry0(t *testing.T, e0 map[string]any) {
	t.Helper()
	assertEntryFields(t, e0, map[string]any{
		"source":       "tool",
		"tool":         "semgrep",
		"rule_id":      "go.jwt.none",
		"fingerprint":  "fp-jwt-1",
		"severity":     "critical",
		"anchored":     true,
		"inBounds":     true,
		"rigor":        "pointed",
		"rigor_source": "default",
		"quote":        "  checkToken()",
		"line":         float64(12),
		"path":         "pkg/auth.go",
	})
	body0, ok := e0["body"].(string)
	if !ok || !strings.Contains(body0, "@\u200bteam") {
		t.Fatalf("expected mention neutralization in body, got %q", e0["body"])
	}
}

func assertEntryFields(t *testing.T, got, want map[string]any) {
	t.Helper()
	for k, w := range want {
		if got[k] != w {
			t.Fatalf("entry[%q] = %v, want %v (full=%+v)", k, got[k], w, got)
		}
	}
}

func assertEntry1(t *testing.T, e1 map[string]any) {
	t.Helper()
	if e1["path"] != "vendor/gen.go" || e1["severity"] != "critical" || e1["quote"] != "package gen" {
		t.Fatalf("unexpected excluded entry: %+v", e1)
	}
	if e1["rigor_source"] == "verified" {
		t.Fatalf("tool finding must never get verified rigor: %+v", e1)
	}
	w, ok := e1["withheld"].(map[string]any)
	if !ok || w["source"] != "exclusion" || w["exclusion_id"] != "builtin:vendor" {
		t.Fatalf("expected exclusion withheld on the vendor entry, got %+v", e1)
	}
}

func TestRun_MappingAnchoringExclusionsAndIdempotency(t *testing.T) {
	repoRoot, dir := setupBundle(t, "42", "abc1234567890")
	mustWriteTestFile(t, filepath.Join(dir, "excluded.json"), `{"schema_version": "1", "excluded": [{"path": "vendor/gen.go", "rule_id": "builtin:vendor", "bytes": 120}]}`+"\n")
	sarifPath := filepath.Join(t.TempDir(), "scan.sarif")
	mustWriteTestFile(t, sarifPath, testSARIFDoc)

	resp := runIngestFile(t, repoRoot, "42", sarifPath)
	assertFirstIngestSummary(t, resp)
	assertInitialEntries(t, loadStoreEntries(t, dir))

	markFirstFalsePositive(t, dir)
	assertReingestIdempotent(t, repoRoot, dir, sarifPath)
	assertUpdatedSARIFInPlace(t, repoRoot, dir, sarifPath)
}

func mustWriteTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertReingestIdempotent(t *testing.T, repoRoot, dir, sarifPath string) {
	t.Helper()
	resp := runIngestFile(t, repoRoot, "42", sarifPath)
	if resp.Data.Added != 0 || resp.Data.Updated != 0 || resp.Data.Unchanged != 2 || resp.Data.Open != 1 {
		t.Fatalf("re-ingest not idempotent: %+v", resp.Data)
	}
	if len(resp.Data.Skipped) != 2 {
		t.Fatalf("re-ingest skips = %+v, want the same two typed skips", resp.Data.Skipped)
	}
	afterReingest := loadStoreEntries(t, dir)
	if len(afterReingest) != 2 || afterReingest[0]["status"] != "false-positive" {
		t.Fatalf("status or count drifted on re-ingest: len=%d status=%v", len(afterReingest), afterReingest[0]["status"])
	}
}

func assertUpdatedSARIFInPlace(t *testing.T, repoRoot, dir, sarifPath string) {
	t.Helper()
	mustWriteTestFile(t, sarifPath, strings.Replace(testSARIFDoc, "JWT signature skipped for @team", "JWT none algorithm accepted", 2))
	resp := runIngestFile(t, repoRoot, "42", sarifPath)
	if resp.Data.Added != 0 || resp.Data.Updated != 1 || resp.Data.Unchanged != 1 {
		t.Fatalf("unexpected counts on updated result: %+v", resp.Data)
	}
	afterUpdate := loadStoreEntries(t, dir)
	if len(afterUpdate) != 2 || afterUpdate[0]["body"] != "JWT none algorithm accepted" || afterUpdate[0]["status"] != "false-positive" {
		t.Fatalf("updated entry mismatch: len=%d body=%v status=%v", len(afterUpdate), afterUpdate[0]["body"], afterUpdate[0]["status"])
	}
}

func assertFirstIngestSummary(t *testing.T, resp *Response) {
	t.Helper()
	if resp.Data.Ingested != 2 || resp.Data.Added != 2 || resp.Data.Updated != 0 || resp.Data.Unchanged != 0 {
		t.Fatalf("unexpected counts after first ingest: %+v", resp.Data)
	}
	if resp.Data.Anchored != 2 || resp.Data.Withheld != 1 || resp.Data.Open != 2 {
		t.Fatalf("unexpected anchored/withheld/open: %+v", resp.Data)
	}
	if len(resp.Data.Tools) != 1 || resp.Data.Tools[0] != "semgrep" {
		t.Fatalf("tools = %v, want [semgrep]", resp.Data.Tools)
	}
	assertSkipRows(t, resp.Data.Skipped, []Skip{
		{Reason: SkipOutOfDiff, Tool: "semgrep", RuleID: "go.style.nit", Path: "pkg/auth.go", Line: 99},
		{Reason: SkipNoLocation, Tool: "semgrep", RuleID: "go.repo.policy"},
	})
}

func assertSkipRows(t *testing.T, got, want []Skip) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("skipped = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("skipped[%d] = %+v, want %+v (all=%+v)", i, got[i], want[i], got)
		}
	}
}

func TestRun_PreservesModelFindingOnVerdictSigMatchAndBackfillClampsVerified(t *testing.T) {
	repoRoot, dir := setupBundle(t, "43", "deadbeef1234")
	seed := `[
  {
    "sig": "modelsig111",
    "sig_version": "1",
    "kind": "inline",
    "path": "pkg/auth.go",
    "line": 11,
    "side": "RIGHT",
    "anchored": true,
    "body": "Existing model finding",
    "kb": [],
    "inBounds": true,
    "rigor": "walked",
    "rigor_source": "model",
    "severity": "important",
    "status": "open",
    "firstSeen": "deadbee",
    "lastSeen": "deadbee",
    "source": "model",
    "history": [{"at": "2026-04-15T10:00:00Z", "event": "reported"}]
  },
  {
    "sig": "toolsig222",
    "sig_version": "1",
    "kind": "inline",
    "path": "pkg/auth.go",
    "line": 12,
    "side": "RIGHT",
    "anchored": true,
    "body": "Legacy tool finding",
    "kb": [],
    "inBounds": true,
    "rigor": "said",
    "rigor_source": "verified",
    "severity": "important",
    "status": "open",
    "firstSeen": "deadbee",
    "lastSeen": "deadbee",
    "source": "tool",
    "tool": "gosec",
    "rule_id": "G101",
    "fingerprint": "fp-g101",
    "history": [{"at": "2026-04-15T10:00:00Z", "event": "reported"}]
  }
]
`
	if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	sarifPayload := `{
  "version": "2.1.0",
  "runs": [
    {
      "tool": {"driver": {"name": "verdict"}},
      "results": [
        {
          "ruleId": "verdict/important",
          "level": "warning",
          "message": {"text": "Existing model finding"},
          "partialFingerprints": {"verdict/sig": "modelsig111"},
          "locations": [{"physicalLocation": {"artifactLocation": {"uri": "pkg/auth.go"}, "region": {"startLine": 11}}}]
        }
      ]
    },
    {
      "tool": {"driver": {"name": "gosec"}},
      "results": [
        {
          "ruleId": "G101",
          "level": "warning",
          "message": {"text": "Legacy tool finding"},
          "partialFingerprints": {"id": "fp-g101"},
          "locations": [{"physicalLocation": {"artifactLocation": {"uri": "pkg/auth.go"}, "region": {"startLine": 12}}}]
        }
      ]
    }
  ]
}`
	resp, ref := runIngestBytes(repoRoot, "43", []byte(sarifPayload))
	if ref != nil {
		t.Fatalf("unexpected refusal: %+v", ref)
	}
	if resp.Data.Added != 0 || resp.Data.Unchanged < 1 {
		t.Fatalf("expected dedupe against verdict/sig and fp, got %+v", resp.Data)
	}
	assertPreservedModelAndClampedTool(t, dir)
}

func assertPreservedModelAndClampedTool(t *testing.T, dir string) {
	t.Helper()
	entries := loadStoreEntries(t, dir)
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0]["source"] != "model" || entries[0]["rigor"] != "walked" || entries[0]["rigor_source"] != "model" {
		t.Fatalf("model finding downgraded: %+v", entries[0])
	}
	if entries[1]["rigor_source"] != "default" {
		t.Fatalf("tool finding must clamp verified rigor_source to default, got %+v", entries[1])
	}

	obj := jq.NewObj("source", "tool", "rigor", "walked")
	recordcore.BackfillEntry(obj)
	if rs, _ := obj.Get("rigor_source"); rs != "default" {
		t.Fatalf("BackfillEntry(tool) rigor_source = %v, want default", rs)
	}
}

func TestRun_MissingSARIFAndBundleRefusals(t *testing.T) {
	validMinimal := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"t"}},"results":[]}]}`

	repoRoot, _ := setupBundle(t, "50", "abc1234")
	req := Request{ReadRequest: newReadReq(repoRoot, "50")}
	if _, ref := Run(context.Background(), &req); ref == nil || ref.Code != "missing-sarif" {
		t.Fatalf("got %+v, want missing-sarif", ref)
	}

	missingRoot := t.TempDir()
	if _, ref := runIngestBytes(missingRoot, "999", []byte(validMinimal)); ref == nil || ref.Code != "missing-bundle" {
		t.Fatalf("got %+v, want missing-bundle", ref)
	}
	if err := os.MkdirAll(filepath.Join(missingRoot, ".verdict", "state", "999"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ref := runIngestBytes(missingRoot, "999", []byte(validMinimal)); ref == nil || ref.Code != "missing-diff" {
		t.Fatalf("got %+v, want missing-diff", ref)
	}
}

func TestRun_OversizeRefusal(t *testing.T) {
	repoRoot, _ := setupBundle(t, "51", "abc1234")
	oversize := make([]byte, MaxSARIFBytes+1)
	copy(oversize, `{"version":"2.1.0","runs":[]}`)
	p := filepath.Join(t.TempDir(), "big.sarif")
	mustWriteTestFile(t, p, string(oversize))

	req := Request{ReadRequest: newReadReq(repoRoot, "51"), SARIF: p}
	if _, ref := Run(context.Background(), &req); ref == nil || ref.Code != "sarif-too-large" {
		t.Fatalf("got %+v, want sarif-too-large", ref)
	}
	if _, ref := runIngestBytes(repoRoot, "51", oversize); ref == nil || ref.Code != "sarif-too-large" {
		t.Fatalf("inline oversize got %+v, want sarif-too-large", ref)
	}
}

func TestRun_SymlinkRefusal(t *testing.T) {
	validMinimal := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"t"}},"results":[]}]}`
	repoRoot, _ := setupBundle(t, "51", "abc1234")
	tmp := t.TempDir()
	target := filepath.Join(tmp, "real.sarif")
	mustWriteTestFile(t, target, validMinimal)
	link := filepath.Join(tmp, "link.sarif")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	linkReq := Request{ReadRequest: newReadReq(repoRoot, "51"), SARIF: link}
	if _, ref := Run(context.Background(), &linkReq); ref == nil || ref.Code != "bad-sarif" {
		t.Fatalf("got %+v, want bad-sarif", ref)
	}
}

func TestRun_BadSARIFInputs(t *testing.T) {
	badDocs := []struct {
		name string
		body string
	}{
		{"wrong version", `{"version":"2.0.0","runs":[]}`},
		{"missing version", `{"runs":[]}`},
		{"missing runs", `{"version":"2.1.0"}`},
		{"runs not an array", `{"version":"2.1.0","runs":{}}`},
		{"results not an array", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"t"}},"results":{}}]}`},
		{"missing driver name", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":""}},"results":[]}]}`},
		{"rule index out of range", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"t"}},"results":[{"ruleIndex":3,"message":{"text":"hi"}}]}]}`},
		{"trailing json", `{"version":"2.1.0","runs":[]} {"extra":1}`},
		{"not an object", `[]`},
		{"truncated json", `{"version":"2.1.0","runs":[`},
		{"empty message", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"t"}},"results":[{"message":{"text":"   "}}]}]}`},
	}
	for _, tc := range badDocs {
		t.Run(tc.name, func(t *testing.T) {
			repoRoot, _ := setupBundle(t, "53", "abc1234")
			if _, ref := runIngestBytes(repoRoot, "53", []byte(tc.body)); ref == nil || ref.Code != "bad-sarif" {
				t.Fatalf("%s: got %+v, want bad-sarif", tc.name, ref)
			}
		})
	}
}

// TestRun_UnanchorableResultsAreTypedSkips proves one bad result cannot refuse
// a whole scan and cannot reach the store: every result the pinned diff cannot
// anchor comes back as a typed skip row, and no finding is stored at line 0.
func TestRun_UnanchorableResultsAreTypedSkips(t *testing.T) {
	repoRoot, dir := setupBundle(t, "54", "abc1234")
	payload := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"semgrep"}},"results":[
		{"ruleId":"r.traverse","message":{"text":"traverse"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"../etc/passwd"},"region":{"startLine":12}}}]},
		{"ruleId":"r.encoded","message":{"text":"encoded"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"a/%2e%2e/%2e%2e/etc/passwd"},"region":{"startLine":1}}}]},
		{"ruleId":"r.file","message":{"text":"file"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"file:///etc/passwd"},"region":{"startLine":1}}}]},
		{"ruleId":"r.absolute","message":{"text":"absolute"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"/etc/passwd"},"region":{"startLine":1}}}]},
		{"ruleId":"r.drive","message":{"text":"drive"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"C:/repo/a.go"},"region":{"startLine":1}}}]},
		{"ruleId":"r.noloc","message":{"text":"no location"}},
		{"ruleId":"r.zero","message":{"text":"zero"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"pkg/auth.go"},"region":{"startLine":0}}}]},
		{"ruleId":"r.negative","message":{"text":"negative"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"pkg/auth.go"},"region":{"startLine":-1}}}]},
		{"ruleId":"r.outside","message":{"text":"outside"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"pkg/auth.go"},"region":{"startLine":99}}}]},
		{"ruleId":"r.unknown","message":{"text":"unknown path"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"other.go"},"region":{"startLine":1}}}]}
	]}]}`
	resp := mustRunIngest(t, repoRoot, "54", payload)
	if resp.Data.Ingested != 0 || resp.Data.Added != 0 || resp.Data.Anchored != 0 || resp.Data.Open != 0 {
		t.Fatalf("unanchorable results reached the store: %+v", resp.Data)
	}
	assertSkipRows(t, resp.Data.Skipped, []Skip{
		{Reason: SkipPathEscapesRepo, Tool: "semgrep", RuleID: "r.traverse", Path: "../etc/passwd", Line: 12},
		{Reason: SkipPathEscapesRepo, Tool: "semgrep", RuleID: "r.encoded", Path: "a/%2e%2e/%2e%2e/etc/passwd", Line: 1},
		{Reason: SkipPathEscapesRepo, Tool: "semgrep", RuleID: "r.file", Path: "file:///etc/passwd", Line: 1},
		{Reason: SkipPathEscapesRepo, Tool: "semgrep", RuleID: "r.absolute", Path: "/etc/passwd", Line: 1},
		{Reason: SkipPathEscapesRepo, Tool: "semgrep", RuleID: "r.drive", Path: "C:/repo/a.go", Line: 1},
		{Reason: SkipNoLocation, Tool: "semgrep", RuleID: "r.noloc"},
		{Reason: SkipInvalidLine, Tool: "semgrep", RuleID: "r.zero", Path: "pkg/auth.go"},
		{Reason: SkipInvalidLine, Tool: "semgrep", RuleID: "r.negative", Path: "pkg/auth.go"},
		{Reason: SkipOutOfDiff, Tool: "semgrep", RuleID: "r.outside", Path: "pkg/auth.go", Line: 99},
		{Reason: SkipOutOfDiff, Tool: "semgrep", RuleID: "r.unknown", Path: "other.go", Line: 1},
	})
	if entries := loadStoreEntries(t, dir); len(entries) != 0 {
		t.Fatalf("skips reached the store: %+v", entries)
	}
}

// TestRun_SnippetVerificationGradesPointedAndSkipsStaleLocations proves the
// reported snippet is checked against the pinned diff: a matching one grades
// the finding pointed and records the diff line as its quote, and a stale one
// is skipped instead of being re-pointed at text the scanner never saw.
func TestRun_SnippetVerificationGradesPointedAndSkipsStaleLocations(t *testing.T) {
	repoRoot, dir := setupBundle(t, "55", "abc1234")
	payload := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"golangci-lint"}},"results":[
		{"ruleId":"govet.match","message":{"text":"Sorted snippet"},"partialFingerprints":{"id":"fp-match"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"pkg/auth.go"},"region":{"startLine":12,"snippet":{"text":"   checkToken()  "}}}}]},
		{"ruleId":"govet.stale","message":{"text":"Stale snippet"},"partialFingerprints":{"id":"fp-stale"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"pkg/auth.go"},"region":{"startLine":13,"snippet":{"text":"something else"}}}}]}
	]}]}`
	resp := mustRunIngest(t, repoRoot, "55", payload)
	if resp.Data.Added != 1 || resp.Data.Ingested != 1 {
		t.Fatalf("counts = %+v, want one anchored finding", resp.Data)
	}
	assertSkipRows(t, resp.Data.Skipped, []Skip{
		{Reason: SkipSnippetMismatch, Tool: "golangci-lint", RuleID: "govet.stale", Path: "pkg/auth.go", Line: 13},
	})
	entries := loadStoreEntries(t, dir)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	assertEntryFields(t, entries[0], map[string]any{
		"rigor":        "pointed",
		"rigor_source": "default",
		"quote":        "  checkToken()",
		"line":         float64(12),
	})
}

// TestRun_RepoRelativeURIFormsAnchor proves competitor-neutral URI handling:
// the path-only `file:` form and a Windows-separated path anchor on the same
// file as the plain POSIX path, while an RFC 8089 authority-bearing `file://`
// reference escapes the checkout and is skipped instead of read as a directory.
func TestRun_RepoRelativeURIFormsAnchor(t *testing.T) {
	repoRoot, _ := setupBundle(t, "56", "abc1234")
	payload := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"scanner"}},"results":[
		{"ruleId":"r.file","message":{"text":"file URI form"},"partialFingerprints":{"id":"fp-file"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"file:pkg/auth.go"},"region":{"startLine":12}}}]},
		{"ruleId":"r.windows","message":{"text":"windows form"},"partialFingerprints":{"id":"fp-win"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"pkg\\auth.go"},"region":{"startLine":13}}}]},
		{"ruleId":"r.authority","message":{"text":"authority form"},"partialFingerprints":{"id":"fp-localhost"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"file://localhost/etc/passwd"},"region":{"startLine":1}}}]}
	]}]}`
	resp := mustRunIngest(t, repoRoot, "56", payload)
	if resp.Data.Added != 2 || resp.Data.Anchored != 2 {
		t.Fatalf("URI forms did not anchor: %+v", resp.Data)
	}
	assertSkipRows(t, resp.Data.Skipped, []Skip{
		{Reason: SkipPathEscapesRepo, Tool: "scanner", RuleID: "r.authority", Path: "file://localhost/etc/passwd", Line: 1},
	})
}

// TestRun_DedupesRepeatedResultsWithinOneDocument proves the stable finding
// signature, not only the tool fingerprint, folds a scanner that reports the
// same site twice under two fingerprints.
func TestRun_DedupesRepeatedResultsWithinOneDocument(t *testing.T) {
	repoRoot, dir := setupBundle(t, "57", "abc1234")
	payload := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"scanner"}},"results":[
		{"ruleId":"r.dup","message":{"text":"Same site twice"},"partialFingerprints":{"id":"fp-a"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"pkg/auth.go"},"region":{"startLine":12}}}]},
		{"ruleId":"r.dup","message":{"text":"Same site twice"},"partialFingerprints":{"id":"fp-b"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"pkg/auth.go"},"region":{"startLine":12}}}]}
	]}]}`
	resp := mustRunIngest(t, repoRoot, "57", payload)
	if resp.Data.Ingested != 1 || resp.Data.Added != 1 {
		t.Fatalf("duplicate result was ingested twice: %+v", resp.Data)
	}
	if entries := loadStoreEntries(t, dir); len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
}

// TestRun_AmbiguousDiffPathSkips proves a path the diff names twice is not
// guessed at: the result is skipped with its own reason.
func TestRun_AmbiguousDiffPathSkips(t *testing.T) {
	repoRoot, _ := setupBundle(t, "58", "abc1234")
	dir := filepath.Join(repoRoot, ".verdict", "state", "58")
	ambiguous := "" +
		"diff --git a/dup.go b/dup.go\n--- a/dup.go\n+++ b/dup.go\n@@ -1,0 +1,1 @@\n+first\n" +
		"diff --git a/dup.go b/dup.go\n--- a/dup.go\n+++ b/dup.go\n@@ -5,0 +5,1 @@\n+second\n"
	mustWriteTestFile(t, filepath.Join(dir, "full.diff"), ambiguous)
	mustRemoveTestFileIfPresent(t, filepath.Join(dir, "hunks.tsv"))
	// The line sits outside both hunks on purpose: the path lookup runs first, so
	// an ambiguous file reports ambiguous-path, not out-of-diff.
	payload := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"scanner"}},"results":[
		{"ruleId":"r.dup","message":{"text":"ambiguous path"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"dup.go"},"region":{"startLine":99}}}]}
	]}]}`
	resp := mustRunIngest(t, repoRoot, "58", payload)
	assertSkipRows(t, resp.Data.Skipped, []Skip{
		{Reason: SkipAmbiguousPath, Tool: "scanner", RuleID: "r.dup", Path: "dup.go", Line: 99},
	})
}

// TestRun_UnsupportedSourceRefuses proves the format gate: `--from` names a
// format, and one this build cannot read refuses typed before any store write.
func TestRun_UnsupportedSourceRefuses(t *testing.T) {
	repoRoot, _ := setupBundle(t, "59", "abc1234")
	req := Request{
		ReadRequest: newReadReq(repoRoot, "59"),
		From:        "yaml",
		SARIFData:   []byte(`{"version":"2.1.0","runs":[]}`),
		SARIFInline: true,
	}
	if _, ref := Run(context.Background(), &req); ref == nil || ref.Code != "bad-from" {
		t.Fatalf("got %+v, want bad-from", ref)
	}
}

func mustRemoveTestFileIfPresent(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}

// TestRun_AnchorableDuplicateBeatsSkippedCopy proves a skipped copy never
// shadows the same scanner identity: batch dedupe only claims (tool,
// fingerprint) for results the pinned diff can place, so the anchorable copy
// still lands.
func TestRun_AnchorableDuplicateBeatsSkippedCopy(t *testing.T) {
	repoRoot, dir := setupBundle(t, "61", "abc1234")
	payload := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"scanner"}},"results":[
		{"ruleId":"r.dup","message":{"text":"Same identity twice"},"partialFingerprints":{"id":"fp-dup"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"pkg/auth.go"},"region":{"startLine":99}}}]},
		{"ruleId":"r.dup","message":{"text":"Same identity twice"},"partialFingerprints":{"id":"fp-dup"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"pkg/auth.go"},"region":{"startLine":12}}}]}
	]}]}`
	resp := mustRunIngest(t, repoRoot, "61", payload)
	if resp.Data.Added != 1 || resp.Data.Ingested != 1 || resp.Data.Anchored != 1 {
		t.Fatalf("anchorable duplicate did not land: %+v", resp.Data)
	}
	assertSkipRows(t, resp.Data.Skipped, []Skip{
		{Reason: SkipOutOfDiff, Tool: "scanner", RuleID: "r.dup", Path: "pkg/auth.go", Line: 99},
	})
	if entries := loadStoreEntries(t, dir); len(entries) != 1 || entries[0]["line"] != float64(12) {
		t.Fatalf("store = %+v, want the anchorable copy at line 12", entries)
	}
}

// TestRun_FileLevelResultOnExcludedPathSkips proves the parse-stage line gate
// runs before the exclusion control: a result with no usable line is skipped as
// invalid-line even on a path the trusted exclusion names, so the store never
// holds an unanchored finding at line 0.
func TestRun_FileLevelResultOnExcludedPathSkips(t *testing.T) {
	repoRoot, dir := setupBundle(t, "62", "abc1234")
	mustWriteTestFile(t, filepath.Join(dir, "excluded.json"),
		`{"schema_version": "1", "excluded": [{"path": "secret.go", "rule_id": "priv-1", "bytes": 120}]}`+"\n")
	payload := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"scanner"}},"results":[
		{"ruleId":"r.file-level","message":{"text":"File-level finding on an excluded path"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"secret.go"}}}]}
	]}]}`
	resp := mustRunIngest(t, repoRoot, "62", payload)
	if resp.Data.Withheld != 0 || resp.Data.Added != 0 || resp.Data.Open != 0 {
		t.Fatalf("file-level result was recorded: %+v", resp.Data)
	}
	assertSkipRows(t, resp.Data.Skipped, []Skip{
		{Reason: SkipInvalidLine, Tool: "scanner", RuleID: "r.file-level", Path: "secret.go"},
	})
	if entries := loadStoreEntries(t, dir); len(entries) != 0 {
		t.Fatalf("store = %+v, want nothing", entries)
	}
}

// TestRun_RelabeledEntryReindexesFingerprint proves an update that relabels a
// finding drops the stale (tool, fingerprint) index entry, so a later result
// carrying the old identity is added instead of folded into the relabeled one.
func TestRun_RelabeledEntryReindexesFingerprint(t *testing.T) {
	repoRoot, dir := setupBundle(t, "63", "abc1234")
	seed := `[
  {
    "sig": "seedsig111",
    "sig_version": "1",
    "kind": "inline",
    "path": "pkg/auth.go",
    "line": 11,
    "side": "RIGHT",
    "anchored": true,
    "body": "Seed finding",
    "kb": [],
    "inBounds": true,
    "rigor": "pointed",
    "rigor_source": "default",
    "severity": "important",
    "status": "open",
    "firstSeen": "abc1234",
    "lastSeen": "abc1234",
    "source": "tool",
    "tool": "gosec",
    "rule_id": "G101",
    "fingerprint": "fp-old",
    "history": [{"at": "2026-04-15T10:00:00Z", "event": "reported"}]
  }
]
`
	mustWriteTestFile(t, filepath.Join(dir, "findings.json"), seed)
	payload := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"semgrep"}},"results":[
		{"ruleId":"r.relabel","message":{"text":"Freshened finding"},"partialFingerprints":{"verdict/sig":"seedsig111","id":"fp-new"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"pkg/auth.go"},"region":{"startLine":12}}}]},
		{"ruleId":"G101","message":{"text":"Different finding"},"properties":{"tool":"gosec"},"partialFingerprints":{"id":"fp-old"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"pkg/auth.go"},"region":{"startLine":13}}}]}
	]}]}`
	resp := mustRunIngest(t, repoRoot, "63", payload)
	if resp.Data.Added != 1 || resp.Data.Updated != 1 || resp.Data.Unchanged != 0 {
		t.Fatalf("counts = %+v, want one update and one add", resp.Data)
	}
	entries := loadStoreEntries(t, dir)
	if len(entries) != 2 {
		t.Fatalf("store = %+v, want two findings", entries)
	}
	if entries[0]["body"] != "Freshened finding" || entries[0]["tool"] != "semgrep" ||
		entries[0]["fingerprint"] != "id=fp-new;verdict/sig=seedsig111" {
		t.Fatalf("relabeled entry = %+v", entries[0])
	}
	if entries[1]["tool"] != "gosec" || entries[1]["fingerprint"] != "fp-old" || entries[1]["body"] != "Different finding" {
		t.Fatalf("stale identity folded into the relabeled finding: %+v", entries[1])
	}
}

func TestRun_ExclusionsUnderSymlinkedAncestor(t *testing.T) {
	isolateTestEnv(t)
	realParent, linkParent, bundleDir := setupSymlinkedAncestorBundle(t)

	payload := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"semgrep"}},"results":[{"ruleId":"go.vendor.rule","level":"error","message":{"text":"Generated vendor issue"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"vendor/gen.go"},"region":{"startLine":1}}}]}]}]}`
	if resp := mustRunIngest(t, linkParent, "44", payload); resp.Data.Withheld != 1 {
		t.Fatalf("withheld = %d, want 1 under symlinked repoRoot ancestor", resp.Data.Withheld)
	}

	// A symlinked excluded.json inside the bundle must still be ignored by ReadConfined.
	for _, name := range []string{"excluded.json", "findings.json"} {
		mustRemoveTestFile(t, filepath.Join(bundleDir, name))
	}
	outsideExcl := filepath.Join(realParent, "outside-excluded.json")
	mustWriteTestFile(t, outsideExcl, `{"schema_version": "1", "excluded": [{"path": "vendor/gen.go", "rule_id": "builtin:vendor", "bytes": 120}]}`+"\n")
	if err := os.Symlink(outsideExcl, filepath.Join(bundleDir, "excluded.json")); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	if resp := mustRunIngest(t, linkParent, "44", payload); resp.Data.Withheld != 0 {
		t.Fatalf("withheld = %d, want 0 when excluded.json itself is a symlink", resp.Data.Withheld)
	}
}

func setupSymlinkedAncestorBundle(t *testing.T) (realParent, linkParent, bundleDir string) {
	t.Helper()
	var err error
	if realParent, err = filepath.EvalSymlinks(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	realRepo := filepath.Join(realParent, "repo")
	bundleDir = filepath.Join(realRepo, ".verdict", "state", "44")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteTestFile(t, filepath.Join(bundleDir, "PR.json"), "{\"headRefOid\":\"abc1234\"}\n")
	mustWriteTestFile(t, filepath.Join(bundleDir, "full.diff"), sampleDiff)
	mustWriteTestFile(t, filepath.Join(bundleDir, "excluded.json"), `{"schema_version": "1", "excluded": [{"path": "vendor/gen.go", "rule_id": "builtin:vendor", "bytes": 120}]}`+"\n")
	linkParent = filepath.Join(realParent, "link-parent")
	if err := os.Symlink(realRepo, linkParent); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	return realParent, linkParent, bundleDir
}

func mustRunIngest(t *testing.T, repoRoot, pr, payload string) *Response {
	t.Helper()
	resp, ref := runIngestBytes(repoRoot, pr, []byte(payload))
	if ref != nil {
		t.Fatalf("unexpected refusal: %+v", ref)
	}
	return resp
}

func mustRemoveTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}
