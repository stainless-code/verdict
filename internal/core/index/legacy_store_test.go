package indexcore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The three files a pre-language store was written from. They are literals,
// not a re-parse: the fixture has to describe what the old build wrote.
const (
	legacyGoMod = "module example.com/m\n\ngo 1.27\n"

	legacyTarget = "package a\n\nfunc Target() {}\n\nfunc Caller() {\n\tTarget()\n}\n"

	legacyTargetTest = "package a\n\nimport \"testing\"\n\nfunc TestTarget(t *testing.T) {\n\tTarget()\n}\n"

	legacyUser = "package b\n\nimport \"example.com/m/a\"\n\nfunc Uses() {\n\ta.Target()\n}\n"
)

// legacyTree is one tree document as the store wrote it before the language
// field existed: no "language" key anywhere, and a "1" store version beside it.
const legacyTree = `{
  "schema_version": "1",
  "tree": "%s",
  "module": "example.com/m",
  "files": [
    {
      "path": "a/a.go",
      "blob": "%s",
      "package": "a",
      "decls": [
        {"name": "Target", "kind": "func", "line": 3, "exported": true},
        {"name": "Caller", "kind": "func", "line": 5, "exported": true}
      ],
      "refs": [
        {"name": "Target", "kind": "call", "line": 6, "col": 2}
      ],
      "size": %d
    },
    {
      "path": "a/a_test.go",
      "blob": "%s",
      "package": "a",
      "imports": [{"path": "testing", "name": "testing"}],
      "decls": [
        {"name": "TestTarget", "kind": "func", "line": 5, "exported": true}
      ],
      "refs": [
        {"name": "Target", "kind": "call", "line": 6, "col": 2}
      ],
      "size": %d
    },
    {
      "path": "b/b.go",
      "blob": "%s",
      "package": "b",
      "imports": [{"path": "example.com/m/a", "name": "a"}],
      "decls": [
        {"name": "Uses", "kind": "func", "line": 5, "exported": true}
      ],
      "refs": [
        {"name": "Target", "kind": "call", "qualifier": "a", "line": 6, "col": 3, "selector": true}
      ],
      "size": %d
    }
  ]
}
`

// writeLegacyStore commits the fixture and writes the store a build from
// before the language field would have left behind.
func writeLegacyStore(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.write("go.mod", legacyGoMod)
	f.write("a/a.go", legacyTarget)
	f.write("a/a_test.go", legacyTargetTest)
	f.write("b/b.go", legacyUser)
	f.commit("one")
	tree := f.run("", "rev-parse", "HEAD^{tree}")
	store := filepath.Join(f.dir, ".verdict", "index")
	for _, dir := range []string{treesDirName, shardsDirName} {
		if err := os.MkdirAll(filepath.Join(store, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	doc := fmt.Sprintf(legacyTree,
		tree,
		f.run("", "rev-parse", "HEAD:a/a.go"), len(legacyTarget),
		f.run("", "rev-parse", "HEAD:a/a_test.go"), len(legacyTargetTest),
		f.run("", "rev-parse", "HEAD:b/b.go"), len(legacyUser),
	)
	if err := os.WriteFile(filepath.Join(store, treesDirName, tree+jsonSuffix), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, versionName), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

// TestLegacyTreeWithoutLanguageReadsAsGo proves a tree document written before
// the language field answers Go queries the way a Go tree does: exact rows
// inside the declaring package, the lexical row through the import, and the
// same-package candidate test.
func TestLegacyTreeWithoutLanguageReadsAsGo(t *testing.T) {
	f := writeLegacyStore(t)

	req := f.request(QueryCallers, nil)
	req.Symbol = "a.Target"
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index callers refused: %v", ref)
	}
	assertRows(t, resp.Data.Rows, []Row{
		{Path: "a/a.go", Line: 6, Kind: KindCall, Resolution: resolutionExact, Language: "go"},
		{Path: "a/a_test.go", Line: 6, Kind: KindCall, Resolution: resolutionExact, Language: "go"},
		{Path: "b/b.go", Line: 6, Kind: KindCall, Resolution: resolutionLexical, Language: "go"},
	})
	if resp.Data.Unresolved != 0 {
		t.Errorf("unresolved = %d; want 0", resp.Data.Unresolved)
	}

	tests := f.request(QueryTestsFor, nil)
	tests.Path = "a/a.go"
	resp, ref = Run(context.Background(), tests)
	if ref != nil {
		t.Fatalf("index tests-for refused: %v", ref)
	}
	if len(resp.Data.TestRows) != 1 || resp.Data.TestRows[0] != (TestRow{Path: "a/a_test.go", Via: viaSamePackage}) {
		t.Errorf("test rows = %+v; want one same-package a/a_test.go", resp.Data.TestRows)
	}
}

// TestStoreVersionMarksAnOlderStoreStale proves the version bump reaches the
// reader: a store at the previous version reads as unbuilt, so status names the
// rebuild instead of claiming a tree the build never wrote.
func TestStoreVersionMarksAnOlderStoreStale(t *testing.T) {
	f := writeLegacyStore(t)
	req := f.request(QueryStatus, nil)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index status refused: %v", ref)
	}
	if resp.Data.Built || !resp.Data.Stale {
		t.Errorf("status built = %t, stale = %t; want built false, stale true", resp.Data.Built, resp.Data.Stale)
	}
	if len(resp.Data.Trees) != 0 {
		t.Errorf("status trees = %+v; want none", resp.Data.Trees)
	}
	if resp.Next != "build it with verdict index build" {
		t.Errorf("next = %q; want the build instruction", resp.Next)
	}
}
