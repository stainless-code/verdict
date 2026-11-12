package indexcore

import (
	"context"
	"errors"
	"io/fs"
	"os"
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
		{Path: "tests/extra.spec.ts", Via: viaReferences, Depth: 1},
		{Path: "util.test.ts", Via: viaSamePackage},
	})
	assertTestRows(t, f.queryPath(QueryTestsFor, "pkg/mod.py").Data.TestRows, []TestRow{
		{Path: "pkg/test_mod.py", Via: viaSamePackage},
		{Path: "tests/test_use.py", Via: viaReferences, Depth: 1},
	})
}

// TestSharedBlobParsesPerLanguage proves two paths holding identical bytes in
// two languages are each parsed in their own language, that the store caches
// neither under the shared blob, and that those parses are reported as parses
// rather than as cache reuse — the shared paths are read again on every build.
func TestSharedBlobParsesPerLanguage(t *testing.T) {
	f := newFixture(t)
	f.write("same.ts", "shared(1)\n")
	f.write("same.py", "shared(1)\n")
	f.write("other.ts", "other(1)\n")
	f.commit("one")
	assertBuildCounts(t, f, []buildCount{{3, 0}, {2, 1}})
	tree, ref := readTree(storePath(f.dir), f.bodyTree())
	if ref != nil {
		t.Fatalf("readTree refused: %v", ref)
	}
	want := []struct{ path, lang, call string }{
		{"other.ts", "typescript", "other"},
		{"same.py", "python", "shared"},
		{"same.ts", "typescript", "shared"},
	}
	if len(tree.Files) != len(want) {
		t.Fatalf("files = %+v; want two", tree.Files)
	}
	for i := range want {
		assertScannedFile(t, &tree.Files[i], want[i].path, want[i].lang, want[i].call)
	}
	if n := storeFiles(t, f.dir, "shards"); n != 1 {
		t.Errorf("shards = %d; want 1, for the one blob only a single language claims", n)
	}
	const sharedBlob = "2cf812485aef6b0f59dd7407debb4c6cd3476357"
	if _, err := os.Stat(shardPath(storePath(f.dir), sharedBlob)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("shard for the blob two languages share: err = %v; want it never written", err)
	}
}

// buildCount is one build's expected parse and reuse counts.
type buildCount struct{ parsed, reused int }

// assertBuildCounts rebuilds the fixture once per expectation and compares the
// reported counts.
func assertBuildCounts(t *testing.T, f *fixture, want []buildCount) {
	t.Helper()
	for i, w := range want {
		resp := f.build()
		if resp.Data.Parsed != w.parsed || resp.Data.Reused != w.reused {
			t.Errorf("build %d parsed/reused = %d/%d; want %d/%d",
				i+1, resp.Data.Parsed, resp.Data.Reused, w.parsed, w.reused)
		}
	}
}

// assertScannedFile compares one stored file against its literal path and
// language, and proves the shared blob was parsed as that language's own call
// site.
func assertScannedFile(t *testing.T, got *treeFile, path, lang, call string) {
	t.Helper()
	if got.Path != path || got.Language != lang {
		t.Errorf("file = %s/%s; want %s/%s", got.Path, got.Language, path, lang)
	}
	if refs := got.Refs; len(refs) != 1 || refs[0].Name != call {
		t.Errorf("%s refs = %+v; want one call of %s", got.Path, refs, call)
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

// TestImportEdgeStaysInsideOneLanguage proves the language-family gate holds
// for every mechanism a candidate can arrive by, not only the same-package arm:
// a test file importing the queried one across a language family is never a
// candidate, in either directory, and blast-radius inherits the answer.
func TestImportEdgeStaysInsideOneLanguage(t *testing.T) {
	const goTarget = "package pkg\n\nfunc Target() {}\n"
	const tsTarget = "export function Target() {\n  return 1;\n}\n"
	cases := []struct {
		name, target, targetBody, test, testBody string
		want                                     []TestRow
	}{
		{
			name:   "go target, same directory javascript test",
			target: "pkg/app.go", targetBody: goTarget,
			test: "pkg/app.test.js", testBody: "import \"./app\";\nexport const x = 1;\n",
		},
		{
			name:   "go target, same directory typescript test",
			target: "pkg/app.go", targetBody: goTarget,
			test: "pkg/app.test.ts", testBody: "import \"./app\";\nexport const x = 1;\n",
		},
		{
			name:   "go target, same directory python test",
			target: "pkg/app.go", targetBody: goTarget,
			test: "pkg/test_app.py", testBody: "from .app import Target\n",
		},
		{
			name:   "go target, other directory javascript test",
			target: "pkg/app.go", targetBody: goTarget,
			test: "checks/app.test.js", testBody: "import \"../pkg/app\";\nexport const x = 1;\n",
		},
		{
			name:   "go target, other directory typescript test",
			target: "pkg/app.go", targetBody: goTarget,
			test: "checks/app.test.ts", testBody: "import \"../pkg/app\";\nexport const x = 1;\n",
		},
		{
			name:   "go target, other directory python test",
			target: "pkg/app.go", targetBody: goTarget,
			test: "checks/test_app.py", testBody: "from pkg import app\n",
		},
		{
			name:   "go target, same directory go test",
			target: "pkg/app.go", targetBody: goTarget,
			test:     "pkg/app_test.go",
			testBody: "package pkg\n\nimport \"testing\"\n\nfunc TestTarget(t *testing.T) {\n\tTarget()\n}\n",
			want:     []TestRow{{Path: "pkg/app_test.go", Via: viaSamePackage}},
		},
		{
			name:   "typescript target, other directory typescript test",
			target: "web/app.ts", targetBody: tsTarget,
			test: "checks/app.test.ts", testBody: "import \"../web/app\";\nexport const x = 1;\n",
			want: []TestRow{{Path: "checks/app.test.ts", Via: viaReferences, Depth: 1}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.write("go.mod", "module example.com/m\n\ngo 1.27\n")
			f.write(tc.target, tc.targetBody)
			f.write(tc.test, tc.testBody)
			f.commit("one")
			f.build()
			assertTestRows(t, f.queryPath(QueryTestsFor, tc.target).Data.TestRows, tc.want)
			resp := blastRun(t, f, func(r *Request) { r.Paths = []string{tc.target} })
			assertTestRows(t, resp.Data.Blast.Tests, tc.want)
		})
	}
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

// TestTestsForPythonSubmoduleImport proves `from lib import mod`, which names
// the module and the submodule in one statement, is an import of `lib/mod.py`,
// and that the same statement naming another submodule is only an import of the
// declaring package: a candidate test, never a reference to this file.
func TestTestsForPythonSubmoduleImport(t *testing.T) {
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n\ngo 1.27\n")
	f.write("lib/mod.py", "def helper(x):\n    return x\n")
	f.write("lib/other.py", "def helper(x):\n    return x\n")
	f.write("tests/test_mod.py", "from lib import mod\n\nmod.helper(1)\n")
	f.write("tests/test_other.py", "from lib import other\n\nother.helper(1)\n")
	f.commit("one")
	f.build()
	assertTestRows(t, f.queryPath(QueryTestsFor, "lib/mod.py").Data.TestRows, []TestRow{
		{Path: "tests/test_mod.py", Via: viaReferences, Depth: 1},
		{Path: "tests/test_other.py", Via: viaImportsModule, Depth: 1},
	})
}

// TestRenameAcrossLanguagesReparses proves a shard cached for one language is
// not reused for another: renaming a file from `.py` to `.ts` keeps its bytes,
// so only the recorded language can tell the build to read it again.
func TestRenameAcrossLanguagesReparses(t *testing.T) {
	const src = "function target(a) { return a; }\n"
	f := newFixture(t)
	f.write("same.py", src)
	f.commit("one")
	f.build()
	f.git("mv", "same.py", "same.ts")
	f.commit("rename")
	resp := f.build()
	if resp.Data.Parsed != 1 || resp.Data.Reused != 0 {
		t.Errorf("parsed/reused = %d/%d; want 1/0 for the renamed file", resp.Data.Parsed, resp.Data.Reused)
	}
	tree, ref := readTree(storePath(f.dir), f.bodyTree())
	if ref != nil {
		t.Fatalf("readTree refused: %v", ref)
	}
	if len(tree.Files) != 1 || tree.Files[0].Language != "typescript" {
		t.Fatalf("files = %+v; want one typescript file", tree.Files)
	}
	assertDecls(t, tree.Files[0].Decls, []decl{{Name: "target", Kind: "func", Line: 1}})
}
