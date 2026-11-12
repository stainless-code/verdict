package packtest

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stainless-code/verdict/internal/core"
	packcore "github.com/stainless-code/verdict/internal/core/pack"
)

const testDiff = "diff --git a/src/a.go b/src/a.go\n--- a/src/a.go\n+++ b/src/a.go\n@@ -1 +1 @@\n-old\n+new\n"

const validFinding = `[{"path":"src/a.go","line":1,"body":"This finding points to the changed line.","rigor":"walked","kb":["rule-a"]}]`

type caseFixture struct {
	name     string
	findings string
	expect   string
	want     string
	reason   string
	tool     string
}

// TestPackTestCases checks each case expectation against a fixture pack.
func TestPackTestCases(t *testing.T) {
	tests := []caseFixture{
		{name: "passing case", findings: validFinding, expect: `{"rules":["rule-a"]}`, want: "PASS"},
		{name: "unselected expected rule", findings: validFinding, expect: `{"rules":["rule-b"]}`, want: "FAIL", reason: "expected rule is not selected: rule-b"},
		{name: "unknown expected rule", findings: validFinding, expect: `{"rules":["absent"]}`, want: "FAIL", reason: "unknown rule id absent"},
		{name: "unknown cited rule", findings: strings.ReplaceAll(validFinding, "rule-a", "absent"), expect: `{"rules":[]}`, want: "FAIL", reason: "finding 1 cites unknown rule id absent"},
		{name: "unanchored finding", findings: strings.Replace(validFinding, `"line":1`, `"line":99`, 1), expect: `{"rules":["rule-a"]}`, want: "FAIL", reason: "finding anchor is outside the diff"},
		{name: "not rules hit", findings: validFinding, expect: `{"rules":[],"not_rules":["rule-a"]}`, want: "FAIL", reason: "not_rules hit: rule-a"},
		{name: "missing coverage", findings: `[]`, expect: `{"rules":["rule-a"]}`, want: "FAIL", reason: "missing finding coverage for rule rule-a"},
		{name: "invalid finding schema", findings: `[{"path":4}]`, expect: `{"rules":[]}`, want: "FAIL", reason: "invalid finding:"},
		{name: "invalid not_rules shape", findings: validFinding, expect: `{"rules":[],"not_rules":null}`, want: "FAIL", reason: "not_rules must be an array"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { checkPackTestCase(t, &tt) })
	}
}

func checkPackTestCase(t *testing.T, tt *caseFixture) {
	t.Helper()
	p := testPack(t, tt.findings, tt.expect, tt.tool)
	resp, ref := runTest(t, p, "2026-01-01T00:00:00Z")
	if ref != nil {
		t.Fatalf("Run() refusal = %s: %s", ref.Code, ref.Message)
	}
	got := resp.Data.Cases[0]
	if got.Status != tt.want {
		t.Fatalf("status = %q, want %q: %+v", got.Status, tt.want, got)
	}
	if tt.reason != "" && !strings.Contains(got.Reason, tt.reason) {
		t.Fatalf("reason = %q, want substring %q", got.Reason, tt.reason)
	}
}

// TestPackTestSkipsIncompleteAndMissingTool checks both skip causes.
func TestPackTestSkipsIncompleteAndMissingTool(t *testing.T) {
	t.Run("incomplete", func(t *testing.T) {
		files := testPackFiles("", `{"rules":[]}`, "")
		delete(files, "evals/case/findings.json")
		p := loadTestPack(t, fstest.MapFS(files))
		resp, ref := runTest(t, p, "")
		if ref != nil || resp.Data.Cases[0].Status != "SKIP" || !strings.Contains(resp.Data.Cases[0].Reason, "missing findings.json") {
			t.Fatalf("Run() = %+v, %v; want incomplete SKIP", resp, ref)
		}
	})
	t.Run("missing required tool", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("PATH", dir)
		p := testPack(t, validFinding, `{"rules":[]}`, "packtest-command-not-present")
		resp, ref := runTest(t, p, "")
		if ref != nil || resp.Data.Cases[0].Status != "SKIP" || !strings.Contains(resp.Data.Cases[0].Reason, "packtest-command-not-present") {
			t.Fatalf("Run() = %+v, %v; want missing-tool SKIP", resp, ref)
		}
	})
}

// TestPackTestRefusesNoCasesAndOversize checks no-case and size refusals.
func TestPackTestRefusesNoCasesAndOversize(t *testing.T) {
	t.Run("no cases", testPackTestNoCases)
	t.Run("oversize file", testPackTestOversizeFile)
	t.Run("too many cases", testPackTestTooManyCases)
}

func testPackTestNoCases(t *testing.T) {
	p := loadTestPack(t, fstest.MapFS{
		"pack.json":       {Data: []byte(testManifest(``))},
		"lenses/test.md":  {Data: []byte("lens body\n")},
		"rules/rule-a.md": {Data: []byte("rule body\n")},
		"rules/rule-b.md": {Data: []byte("other rule body\n")},
	})
	if _, ref := runTest(t, p, ""); ref == nil || ref.Code != "pack-test-cases" {
		t.Fatalf("Run() refusal = %v; want pack-test-cases", ref)
	}
}

func testPackTestOversizeFile(t *testing.T) {
	files := testPackFiles(validFinding, `{"rules":["rule-a"]}`, "")
	files["evals/case/diff.patch"] = &fstest.MapFile{Data: []byte(strings.Repeat("x", 64*1024+1))}
	if _, ref := packcore.Load(fstest.MapFS(files), "."); ref == nil || ref.Code != "pack-too-large" {
		t.Fatalf("Load() refusal = %v; want pack-too-large", ref)
	}
}

func testPackTestTooManyCases(t *testing.T) {
	files := testPackFiles("", `{"rules":[]}`, "")
	for i := range 129 {
		name := fmt.Sprintf("evals/case-%03d/diff.patch", i)
		files[name] = &fstest.MapFile{Data: []byte(testDiff)}
	}
	p := loadTestPack(t, fstest.MapFS(files))
	if _, ref := runTest(t, p, ""); ref == nil || ref.Code != "pack-too-large" {
		t.Fatalf("Run() refusal = %v; want pack-too-large", ref)
	}
}

// TestPackTestSortsReversedDirectoryListings pins deterministic case order.
func TestPackTestSortsReversedDirectoryListings(t *testing.T) {
	files := testPackFiles(validFinding, `{"rules":["rule-a"]}`, "")
	files["evals/zeta/diff.patch"] = &fstest.MapFile{Data: []byte(testDiff)}
	files["evals/zeta/findings.json"] = &fstest.MapFile{Data: []byte(validFinding)}
	files["evals/zeta/expect.json"] = &fstest.MapFile{Data: []byte(`{"rules":["rule-a"]}`)}
	files["evals/alpha/diff.patch"] = &fstest.MapFile{Data: []byte(testDiff)}
	files["evals/alpha/findings.json"] = &fstest.MapFile{Data: []byte(validFinding)}
	files["evals/alpha/expect.json"] = &fstest.MapFile{Data: []byte(`{"rules":["rule-a"]}`)}
	p := loadTestPack(t, reverseFS{fstest.MapFS(files)})
	resp, ref := runTest(t, p, "")
	if ref != nil {
		t.Fatal(ref)
	}
	got := []string{resp.Data.Cases[0].Case, resp.Data.Cases[1].Case}
	if !slices.Equal(got, []string{"alpha", "case"}) || len(resp.Data.Cases) != 3 || resp.Data.Cases[2].Case != "zeta" {
		t.Fatalf("case order = %v, want [alpha case zeta]", got)
	}
}

func testPack(t *testing.T, findings, expect, tool string) *packcore.Pack {
	t.Helper()
	return loadTestPack(t, fstest.MapFS(testPackFiles(findings, expect, tool)))
}

func testPackFiles(findings, expect, tool string) map[string]*fstest.MapFile {
	files := map[string]*fstest.MapFile{
		"pack.json":                {Data: []byte(testManifest(tool))},
		"lenses/test.md":           {Data: []byte("lens body\n")},
		"rules/rule-a.md":          {Data: []byte("rule body\n")},
		"rules/rule-b.md":          {Data: []byte("other rule body\n")},
		"evals/case/diff.patch":    {Data: []byte(testDiff)},
		"evals/case/findings.json": {Data: []byte(findings)},
		"evals/case/expect.json":   {Data: []byte(expect)},
	}
	return files
}

func testManifest(tool string) string {
	toolField := ""
	if tool != "" {
		toolField = fmt.Sprintf(`,"tools":[%q]`, tool)
	}
	return `{"schema_version":"1","name":"acme/test","version":"1.0.0","severity":{"s":"important"},` +
		`"lenses":[{"id":"test"}],"rules":[{"id":"rule-a","severity":"s","globs":["src/*.go"]` + toolField +
		`},{"id":"rule-b","severity":"s","globs":["other/*.go"]}]}`
}

func loadTestPack(t *testing.T, files fs.FS) *packcore.Pack {
	t.Helper()
	p, ref := packcore.Load(files, ".")
	if ref != nil || p == nil {
		t.Fatalf("Load() = %v, %v", p, ref)
	}
	return p
}

func runTest(t *testing.T, p *packcore.Pack, at string) (*TestResponse, *core.RefusalError) {
	t.Helper()
	set, ref := packcore.Merge([]packcore.Origin{{Layer: packcore.LayerBuiltin, Source: "fixture", Pack: p}})
	if ref != nil {
		return nil, ref
	}
	return Run(context.Background(), p, set, at)
}

type reverseFS struct{ fstest.MapFS }

func (f reverseFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := f.MapFS.ReadDir(name)
	slices.Reverse(entries)
	return entries, err
}
