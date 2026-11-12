package indexcore

import (
	"context"
	"slices"
	"testing"
)

// writeTransitiveFixture writes a four-package import chain a ← b ← c ← d with
// a test in every package, c and d importing each other so the walk meets a
// cycle, and two tests in c so a cap's count is not the same number at every
// cap.
func writeTransitiveFixture(f *fixture) {
	f.write("go.mod", "module example.com/m\n\ngo 1.27\n")
	f.write("a/a.go", "package a\n\nfunc Target() {}\n")
	f.write("a/a_test.go", "package a\n\nimport \"testing\"\n\nfunc TestTarget(t *testing.T) {\n\tTarget()\n}\n")
	f.write("b/b.go", "package b\n\nimport \"example.com/m/a\"\n\nfunc UseA() {\n\ta.Target()\n}\n")
	f.write("b/b_test.go", "package b\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/a\"\n)\n\n"+
		"func TestUsesA(t *testing.T) {\n\ta.Target()\n}\n")
	f.write("c/c.go", "package c\n\nimport (\n\t\"example.com/m/b\"\n\t\"example.com/m/d\"\n)\n\n"+
		"func Deep() {\n\tb.UseA()\n\td.Deeper()\n}\n")
	f.write("c/c_test.go", "package c\n\nimport \"testing\"\n\nfunc TestDeep(t *testing.T) {\n\tDeep()\n}\n")
	f.write("c/more_test.go", "package c\n\nimport \"testing\"\n\nfunc TestMore(t *testing.T) {\n\tDeep()\n}\n")
	f.write("d/d.go", "package d\n\nimport \"example.com/m/c\"\n\nfunc Deeper() {\n\tc.Deep()\n}\n")
	f.write("d/d_test.go", "package d\n\nimport \"testing\"\n\nfunc TestDeeper(t *testing.T) {\n\tDeeper()\n}\n")
}

// TestTestsForTransitiveImportDepth proves the reverse import walk: a package's
// test is transitive-import at the hop count that reaches it, a direct import
// stays the direct mechanism, and the c↔d cycle terminates.
func TestTestsForTransitiveImportDepth(t *testing.T) {
	f := newFixture(t)
	writeTransitiveFixture(f)
	f.commit("one")
	f.build()
	assertTestRows(t, f.queryPath(QueryTestsFor, "a/a.go").Data.TestRows, []TestRow{
		{Path: "a/a_test.go", Via: viaSamePackage},
		{Path: "b/b_test.go", Via: viaReferences, Depth: 1},
		{Path: "c/c_test.go", Via: viaTransitiveImport, Depth: 2},
		{Path: "c/more_test.go", Via: viaTransitiveImport, Depth: 2},
		{Path: "d/d_test.go", Via: viaTransitiveImport, Depth: 3},
	})
}

// TestTestsForTransitiveScannedLanguages proves the walk reads each scanned
// language's own import data: a TypeScript and a Python test two package hops
// out are transitive candidates at depth 2.
func TestTestsForTransitiveScannedLanguages(t *testing.T) {
	f := newFixture(t)
	f.write("src/a.ts", "export function target(a: number) { return a; }\n")
	f.write("lib/b.ts", "import { target } from '../src/a.js';\n\nexport function use() {\n  return target(1);\n}\n")
	f.write("tests/deep.test.ts", "import { use } from '../lib/b.ts';\n\nuse();\n")
	f.write("pkg/mod.py", "def helper(x):\n    return x\n")
	f.write("pkg2/use.py", "from pkg.mod import helper\n\nhelper(1)\n")
	f.write("checks/test_deep.py", "from pkg2.use import helper\n\nhelper(2)\n")
	f.commit("one")
	f.build()
	assertTestRows(t, f.queryPath(QueryTestsFor, "src/a.ts").Data.TestRows, []TestRow{
		{Path: "tests/deep.test.ts", Via: viaTransitiveImport, Depth: 2},
	})
	assertTestRows(t, f.queryPath(QueryTestsFor, "pkg/mod.py").Data.TestRows, []TestRow{
		{Path: "checks/test_deep.py", Via: viaTransitiveImport, Depth: 2},
	})
}

// TestTestsForPackageImportIsOneHop proves a test whose own package imports the
// declaring package is a candidate at one hop even though its file imports only
// `testing`, and that a test naming the declaring package itself keeps the
// direct mechanism at that same hop.
func TestTestsForPackageImportIsOneHop(t *testing.T) {
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n\ngo 1.27\n")
	f.write("a/a.go", "package a\n\nfunc Target() {}\n")
	f.write("b/b.go", "package b\n\nimport \"example.com/m/a\"\n\nfunc Use() {\n\ta.Target()\n}\n")
	f.write("b/b_test.go", "package b\n\nimport \"testing\"\n\nfunc TestUse(t *testing.T) {\n\tUse()\n}\n")
	f.write("c/c_test.go", "package c\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/a\"\n)\n\n"+
		"func TestTarget(t *testing.T) {\n\ta.Target()\n}\n")
	f.commit("one")
	f.build()
	rows := []TestRow{
		{Path: "b/b_test.go", Via: viaTransitiveImport, Depth: 1},
		{Path: "c/c_test.go", Via: viaReferences, Depth: 1},
	}
	assertTestRows(t, f.queryPath(QueryTestsFor, "a/a.go").Data.TestRows, rows)
	assertDepthCap(t, f, "a/a.go", 1, rows, 0, "")
}

// TestTestsForDepthCapTruncates proves the depth cap bounds the rows and counts
// the candidates one hop past it instead of dropping them silently.
func TestTestsForDepthCapTruncates(t *testing.T) {
	f := newFixture(t)
	writeTransitiveFixture(f)
	f.commit("one")
	f.build()
	t.Run("two hops", func(t *testing.T) {
		assertDepthCap(t, f, "a/a.go", 2, []TestRow{
			{Path: "a/a_test.go", Via: viaSamePackage},
			{Path: "b/b_test.go", Via: viaReferences, Depth: 1},
			{Path: "c/c_test.go", Via: viaTransitiveImport, Depth: 2},
			{Path: "c/more_test.go", Via: viaTransitiveImport, Depth: 2},
		}, 1, "index: depth cap 2 reached (1 candidate test(s) one import hop beyond; raise --depth up to 8)")
	})
	t.Run("one hop keeps the immediate packages", func(t *testing.T) {
		assertDepthCap(t, f, "a/a.go", 1, []TestRow{
			{Path: "a/a_test.go", Via: viaSamePackage},
			{Path: "b/b_test.go", Via: viaReferences, Depth: 1},
		}, 2, "index: depth cap 1 reached (2 candidate test(s) one import hop beyond; raise --depth up to 8)")
	})
	t.Run("the c to d cycle terminates at the widest cap", func(t *testing.T) {
		assertDepthCap(t, f, "a/a.go", MaxDepth, []TestRow{
			{Path: "a/a_test.go", Via: viaSamePackage},
			{Path: "b/b_test.go", Via: viaReferences, Depth: 1},
			{Path: "c/c_test.go", Via: viaTransitiveImport, Depth: 2},
			{Path: "c/more_test.go", Via: viaTransitiveImport, Depth: 2},
			{Path: "d/d_test.go", Via: viaTransitiveImport, Depth: 3},
		}, 0, "")
	})
}

// assertDepthCap queries one path at one depth and asserts the rows it kept,
// the candidates it hid, and — when line is not empty — the human line naming
// that count.
func assertDepthCap(t *testing.T, f *fixture, p string, depth int, rows []TestRow, hid int, line string) {
	t.Helper()
	req := f.request(QueryTestsFor, nil)
	req.Path, req.Depth = p, depth
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index tests-for refused: %v", ref)
	}
	assertTestRows(t, resp.Data.TestRows, rows)
	if resp.Data.Depth != depth || resp.Data.DepthTruncated != hid {
		t.Errorf("depth = %d, depth_truncated = %d; want %d, %d",
			resp.Data.Depth, resp.Data.DepthTruncated, depth, hid)
	}
	if line != "" && !slices.Contains(resp.Human, line) {
		t.Errorf("human lines = %q; want one equal to %q", resp.Human, line)
	}
}

// TestTestsForSiblingTestKeepsTransitiveCandidate proves a candidate's hop
// count comes from its own imports, not its directory's shortest distance:
// adding a sibling that imports the target directly leaves another file's
// two-hop chain named at depth 2, in Go and in TypeScript, and a one-hop cap
// counts that file instead of dropping it.
func TestTestsForSiblingTestKeepsTransitiveCandidate(t *testing.T) {
	t.Run("go", func(t *testing.T) {
		f := newFixture(t)
		f.write("go.mod", "module example.com/m\n\ngo 1.27\n")
		f.write("a/a.go", "package a\n\nfunc Target() {}\n")
		f.write("b/b.go", "package b\n\nimport \"example.com/m/a\"\n\nfunc UseA() {\n\ta.Target()\n}\n")
		f.write("t/deep_test.go", "package t\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/b\"\n)\n\n"+
			"func TestDeep(t *testing.T) {\n\tb.UseA()\n}\n")
		f.commit("one")
		f.build()
		assertTestRows(t, f.queryPath(QueryTestsFor, "a/a.go").Data.TestRows, []TestRow{
			{Path: "t/deep_test.go", Via: viaTransitiveImport, Depth: 2},
		})
		f.write("t/direct_test.go", "package t\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/a\"\n)\n\n"+
			"func TestDirect(t *testing.T) {\n\ta.Target()\n}\n")
		f.commit("two")
		f.build()
		resp := f.queryPath(QueryTestsFor, "a/a.go")
		assertTestRows(t, resp.Data.TestRows, []TestRow{
			{Path: "t/deep_test.go", Via: viaTransitiveImport, Depth: 2},
			{Path: "t/direct_test.go", Via: viaReferences, Depth: 1},
		})
		if resp.Data.DepthTruncated != 0 {
			t.Errorf("depth_truncated = %d; want 0", resp.Data.DepthTruncated)
		}
		assertDepthCap(t, f, "a/a.go", 1, []TestRow{
			{Path: "t/direct_test.go", Via: viaReferences, Depth: 1},
		}, 1, "index: depth cap 1 reached (1 candidate test(s) one import hop beyond; raise --depth up to 8)")
	})
	t.Run("typescript", func(t *testing.T) {
		f := newFixture(t)
		f.write("src/a.ts", "export function target(a: number) { return a; }\n")
		f.write("lib/b.ts", "import { target } from '../src/a';\n\nexport function use() {\n  return target(1);\n}\n")
		f.write("tests/deep.test.ts", "import { use } from '../lib/b';\n\nuse();\n")
		f.commit("one")
		f.build()
		assertTestRows(t, f.queryPath(QueryTestsFor, "src/a.ts").Data.TestRows, []TestRow{
			{Path: "tests/deep.test.ts", Via: viaTransitiveImport, Depth: 2},
		})
		f.write("tests/sibling.test.ts", "import { target } from '../src/a';\n\ntarget(2);\n")
		f.commit("two")
		f.build()
		resp := f.queryPath(QueryTestsFor, "src/a.ts")
		assertTestRows(t, resp.Data.TestRows, []TestRow{
			{Path: "tests/deep.test.ts", Via: viaTransitiveImport, Depth: 2},
			{Path: "tests/sibling.test.ts", Via: viaReferences, Depth: 1},
		})
		if resp.Data.DepthTruncated != 0 {
			t.Errorf("depth_truncated = %d; want 0", resp.Data.DepthTruncated)
		}
		assertDepthCap(t, f, "src/a.ts", 1, []TestRow{
			{Path: "tests/sibling.test.ts", Via: viaReferences, Depth: 1},
		}, 1, "")
	})
}

// TestTestsForDepthRefusesOutOfRange proves the depth bound refuses through the
// service, where every transport passes, not only the CLI parser.
func TestTestsForDepthRefusesOutOfRange(t *testing.T) {
	f := newFixture(t)
	writeTransitiveFixture(f)
	f.commit("one")
	f.build()
	req := f.request(QueryTestsFor, nil)
	req.Path, req.Depth = "a/a.go", MaxDepth+1
	_, ref := Run(context.Background(), req)
	assertRefusal(t, ref, "bad-flags", "index tests-for wants --depth from 1 to 8")
}
