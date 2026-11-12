package indexcore

import (
	"context"
	"testing"
)

// writeScannedFixture writes one repository holding a TypeScript, a
// JavaScript, and a Python slice, plus files the index must not scan.
func writeScannedFixture(f *fixture) {
	f.write("go.mod", "module example.com/m\n\ngo 1.27\n")
	f.write("util.ts", "export function target(a: number) { return a; }\n")
	f.write("app.ts", "import { target } from './util';\nconst v = target(1);\nobj.target(2);\n")
	f.write("app.js", "const fs = require('fs');\nfs.readFile(1);\n")
	f.write("pkg/mod.py", "def helper(x):\n    return x\n")
	f.write("pkg/use.py", "from .mod import helper\n\nhelper(1)\n")
	f.write("other/pkg.ts", "export function use() {\n  return target();\n}\n")
	f.write("NOTES.md", "target()\n")
}

// TestBuildIndexesScannedLanguages proves the scanned file set, the per-file
// language, and the language breakdown a build reports.
func TestBuildIndexesScannedLanguages(t *testing.T) {
	f := newFixture(t)
	writeScannedFixture(f)
	f.commit("one")
	resp := f.build()
	want := []LangRow{
		{Language: "javascript", Files: 1},
		{Language: "python", Files: 2},
		{Language: "typescript", Files: 3},
	}
	if len(resp.Data.Languages) != len(want) {
		t.Fatalf("languages = %+v; want %+v", resp.Data.Languages, want)
	}
	for i := range want {
		if resp.Data.Languages[i] != want[i] {
			t.Errorf("language %d = %+v; want %+v", i, resp.Data.Languages[i], want[i])
		}
	}
	if resp.Data.Files != 6 || resp.Data.Skipped != 0 {
		t.Errorf("files = %d, skipped = %d; want 6, 0", resp.Data.Files, resp.Data.Skipped)
	}
}

// TestCallersTypeScriptRows proves a TypeScript call and attribute site in the
// declaring directory are lexical rows, and that the same name elsewhere is
// not attributed.
func TestCallersTypeScriptRows(t *testing.T) {
	f := newFixture(t)
	writeScannedFixture(f)
	f.commit("one")
	f.build()
	req := f.request(QueryCallers, nil)
	req.Symbol = "target"
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index callers refused: %v", ref)
	}
	assertRows(t, resp.Data.Rows, []Row{
		{Path: "app.ts", Line: 2, Kind: KindCall, Resolution: "lexical", Language: "typescript"},
		{Path: "app.ts", Line: 3, Kind: KindMethod, Resolution: "lexical", Language: "typescript"},
	})
}

// TestCallersPythonRows proves a Python call in the declared module's package
// is a lexical row with its attribute sites.
func TestCallersPythonRows(t *testing.T) {
	f := newFixture(t)
	writeScannedFixture(f)
	f.commit("one")
	f.build()
	req := f.request(QueryCallers, nil)
	req.Symbol = "pkg.helper"
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index callers refused: %v", ref)
	}
	assertRows(t, resp.Data.Rows, []Row{
		{Path: "pkg/use.py", Line: 3, Kind: KindCall, Resolution: "lexical", Language: "python"},
	})
}

// TestTestsForScannedLanguages proves the candidate test set for a TypeScript
// and a Python source file: a test in the same directory, a test elsewhere that
// imports the file, and a test that imports nothing of the sort.
func TestTestsForScannedLanguages(t *testing.T) {
	f := newFixture(t)
	f.write("util.ts", "export function target(a: number) { return a; }\n")
	f.write("util.test.ts", "import { target } from './util';\n\ntarget(1);\n")
	f.write("tests/extra.spec.ts", "import { target } from '../util';\n\ntarget(2);\n")
	f.write("tests/unrelated.spec.ts", "import { other } from '../nowhere';\n\nother(3);\n")
	f.write("pkg/mod.py", "def helper(x):\n    return x\n")
	f.write("pkg/test_mod.py", "from .mod import helper\n\nhelper(1)\n")
	f.write("tests/test_use.py", "from pkg.mod import helper\n\nhelper(2)\n")
	f.commit("one")
	f.build()
	assertTestRows(t, f.queryPath(QueryTestsFor, "util.ts").Data.TestRows, []TestRow{
		{Path: "tests/extra.spec.ts", Via: viaReferences},
		{Path: "util.test.ts", Via: viaSamePackage},
	})
	assertTestRows(t, f.queryPath(QueryTestsFor, "pkg/mod.py").Data.TestRows, []TestRow{
		{Path: "pkg/test_mod.py", Via: viaSamePackage},
		{Path: "tests/test_use.py", Via: viaReferences},
	})
}

// TestSharedBlobParsesPerLanguage proves two paths holding identical bytes in
// two languages are each parsed in their own language, and that the store
// caches neither under the shared blob.
func TestSharedBlobParsesPerLanguage(t *testing.T) {
	f := newFixture(t)
	f.write("same.ts", "shared(1)\n")
	f.write("same.py", "shared(1)\n")
	f.commit("one")
	f.build()
	tree, ref := readTree(storePath(f.dir), f.bodyTree())
	if ref != nil {
		t.Fatalf("readTree refused: %v", ref)
	}
	want := []struct{ path, lang string }{
		{"same.py", "python"},
		{"same.ts", "typescript"},
	}
	if len(tree.Files) != len(want) {
		t.Fatalf("files = %+v; want two", tree.Files)
	}
	for i := range want {
		assertScannedFile(t, &tree.Files[i], want[i].path, want[i].lang)
	}
	if n := storeFiles(t, f.dir, "shards"); n != 0 {
		t.Errorf("shards = %d; want 0 for a blob two languages share", n)
	}
}

// assertScannedFile compares one stored file against its literal path and
// language, and proves the shared blob was parsed as that language's own call
// site.
func assertScannedFile(t *testing.T, got *treeFile, path, lang string) {
	t.Helper()
	if got.Path != path || got.Language != lang {
		t.Errorf("file = %s/%s; want %s/%s", got.Path, got.Language, path, lang)
	}
	if refs := got.Refs; len(refs) != 1 || refs[0].Name != "shared" {
		t.Errorf("%s refs = %+v; want one call of shared", got.Path, refs)
	}
}

// TestScannedFileRefusal proves a query naming an unscanned path refuses with
// the shipped message.
func TestScannedFileRefusal(t *testing.T) {
	f := newFixture(t)
	writeScannedFixture(f)
	f.commit("one")
	f.build()
	req := f.request(QueryTestsFor, nil)
	req.Path = "NOTES.md"
	_, ref := Run(context.Background(), req)
	assertRefusal(t, ref, "bad-flags", "index tests-for: NOTES.md is not an indexed source file at "+f.bodyTree())
}

// TestTestPathNamesEveryLanguageRule pins the test-file naming rule of every
// scanned language.
func TestTestPathNamesEveryLanguageRule(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"a/a_test.go", true},
		{"a/a.go", false},
		{"web/util.test.ts", true},
		{"web/util.spec.js", true},
		{"web/__tests__/util.js", true},
		{"web/util.ts", false},
		{"pkg/test_mod.py", true},
		{"pkg/mod_test.py", true},
		{"pkg/conftest.py", true},
		{"pkg/tests/helper.py", true},
		{"pkg/mod.py", false},
		{"docs/readme.md", false},
	}
	for _, c := range cases {
		if got := testPath(indexedLanguage(c.path), c.path); got != c.want {
			t.Errorf("testPath(%q) = %v; want %v", c.path, got, c.want)
		}
	}
}

// writeMixedLanguageFixture writes one Go and one Python package in a single
// directory, each declaring Target and each holding its own test and its own
// call site.
func writeMixedLanguageFixture(f *fixture) {
	f.write("go.mod", "module example.com/m\n\ngo 1.27\n")
	f.write("pkg/main.go", "package pkg\n\nfunc Target() {}\n")
	f.write("pkg/main_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestTarget(t *testing.T) {\n\tTarget()\n}\n")
	f.write("pkg/mod.py", "def Target():\n    return 1\n")
	f.write("pkg/test_mod.py", "from .mod import Target\n\nTarget()\n")
}

// TestCandidatesStayInsideOneLanguage proves a mixed directory does not cross
// languages: the Python test is not a candidate for the Go file beside it, nor
// the Go test for the Python file.
func TestCandidatesStayInsideOneLanguage(t *testing.T) {
	f := newFixture(t)
	writeMixedLanguageFixture(f)
	f.commit("one")
	f.build()
	assertTestRows(t, f.queryPath(QueryTestsFor, "pkg/main.go").Data.TestRows, []TestRow{
		{Path: "pkg/main_test.go", Via: viaSamePackage},
	})
	assertTestRows(t, f.queryPath(QueryTestsFor, "pkg/mod.py").Data.TestRows, []TestRow{
		{Path: "pkg/test_mod.py", Via: viaSamePackage},
	})
}

// TestCallersStayInsideOneLanguage proves a call in one language is not a
// caller of another language's declaration that happens to share a directory.
func TestCallersStayInsideOneLanguage(t *testing.T) {
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n\ngo 1.27\n")
	f.write("pkg/main.go", "package pkg\n\nfunc Target() {}\n")
	f.write("pkg/job.py", "def run():\n    Target()\n")
	f.write("pkg2/mod.py", "def Target():\n    return 1\n")
	f.commit("one")
	f.build()
	req := f.request(QueryCallers, nil)
	req.Symbol = "pkg.Target"
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index callers refused: %v", ref)
	}
	assertRows(t, resp.Data.Rows, nil)
}
