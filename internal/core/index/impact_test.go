package indexcore

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// impactDiff is the bundle diff every impact test hands over: it touches the
// line that declares a.A, so the changed declaration is A and nobody else.
const impactDiff = "diff --git a/a/ab.go b/a/ab.go\n" +
	"index 1111111..2222222 100644\n" +
	"--- a/a/ab.go\n" +
	"+++ b/a/ab.go\n" +
	"@@ -3 +3 @@\n" +
	"-func A() {}\n" +
	"+func A() { _ = 1 }\n"

// impactReq builds one impact hand-off against the fixture.
func (f *fixture) impactReq(index, head, diff string) *ImpactRequest {
	return &ImpactRequest{
		ReadRequest: core.ReadRequest{
			Runtime:  &core.Runtime{Commander: f.request(QueryBuild, nil).Commander, Env: envMap()},
			RepoRoot: f.dir,
		},
		Index: index, Head: head, Diff: diff,
	}
}

// impact runs one impact hand-off and requires success.
func (f *fixture) impact(index, head, diff string) *Impact {
	f.t.Helper()
	got, ref := ImpactOf(context.Background(), f.impactReq(index, head, diff))
	if ref != nil {
		f.t.Fatalf("impact refused: %s: %s", ref.Code, ref.Message)
	}
	return got
}

// impactRefuse runs one impact hand-off and returns its refusal.
func (f *fixture) impactRefuse(index, head, diff string) *core.RefusalError {
	f.t.Helper()
	_, ref := ImpactOf(context.Background(), f.impactReq(index, head, diff))
	if ref == nil {
		f.t.Fatal("impact answered; want a refusal")
	}
	return ref
}

// TestImpactNamedRefAnswers proves the hand-off rows: the changed declaration,
// its callers to the depth cap, and the candidate tests, all read from the
// index the operator named by ref.
func TestImpactNamedRefAnswers(t *testing.T) {
	f := newFixture(t)
	writeBlastFixture(f)
	f.commit("one")
	f.build()
	got := f.impact("HEAD", "", impactDiff)
	if got.Data.Kind != QueryBlastRadius {
		t.Errorf("kind = %q, want %q", got.Data.Kind, QueryBlastRadius)
	}
	if got.Index != "index:"+got.Data.Tree {
		t.Errorf("index label = %q, want the answered tree %q", got.Index, got.Data.Tree)
	}
	if !got.Data.Blast.Indexed {
		t.Error("impact answered unindexed; want the named tree")
	}
	assertSymbolRows(t, got.Data.Blast.Symbols, SymbolRow{Path: "a/ab.go", Name: "A", Kind: "func", Line: 3})
	wantCallers := []CallerRow{
		{Symbol: "example.com/m/a.A", Path: "a/a_test.go", Kind: "call", Resolution: "exact", Line: 6, Depth: 1},
		{Symbol: "example.com/m/a.A", Path: "a/top.go", Kind: "call", Resolution: "exact", Line: 4, Depth: 1},
		{Symbol: "example.com/m/a.A", Path: "b/b.go", Kind: "call", Resolution: "lexical", Line: 6, Depth: 1},
		{Symbol: "example.com/m/a.A", Path: "b/b_test.go", Kind: "call", Resolution: "lexical", Line: 10, Depth: 1},
		{Symbol: "example.com/m/a.A", Path: "c/c.go", Kind: "call", Resolution: "lexical", Line: 6, Depth: 2},
	}
	assertCallerRows(t, got.Data.Blast.Callers, wantCallers...)
	assertTestRows(t, got.Data.Blast.Tests, []TestRow{
		{Path: "a/a_test.go", Via: "same-package", Depth: 0},
		{Path: "b/b_test.go", Via: "references", Depth: 1},
		{Path: "c/c_test.go", Via: "transitive-import", Depth: 2},
	})
	if got.Data.Blast.Counts.Callers == nil || *got.Data.Blast.Counts.Callers != len(wantCallers) {
		t.Errorf("caller count = %v, want %d", got.Data.Blast.Counts.Callers, len(wantCallers))
	}
	if len(got.Warnings) != 0 {
		t.Errorf("warnings = %v, want none for a head-less hand-off", got.Warnings)
	}
}

// TestImpactDirFormAnswers proves the directory hand-off: a checkout the
// operator built an index in answers through its HEAD, with no revision
// argument.
func TestImpactDirFormAnswers(t *testing.T) {
	f := newFixture(t)
	writeBlastFixture(f)
	f.commit("one")
	f.build()
	got := f.impact(f.dir, "", impactDiff)
	if len(got.Data.Blast.Symbols) != 1 || got.Data.Blast.Symbols[0].Name != "A" {
		t.Errorf("symbols = %+v, want the one changed declaration", got.Data.Blast.Symbols)
	}
}

// TestImpactStaleNamesTheAnsweredTree proves a named index that answers for
// another tree warns and keeps its rows: the operator asked for it, so the
// bundle is never told the rows describe a tree they do not.
func TestImpactStaleNamesTheAnsweredTree(t *testing.T) {
	f := newFixture(t)
	writeBlastFixture(f)
	f.commit("one")
	f.build()
	named := f.run("", "rev-parse", "HEAD")
	f.write("a/ab.go", "package a\n\nfunc A() {}\n\nfunc B() {}\n\nfunc Later() {}\n")
	f.commit("two")
	head := f.run("", "rev-parse", "HEAD")
	headTree := f.run("", "rev-parse", "HEAD^{tree}")
	got := f.impact(named, head, impactDiff)
	if !got.Data.Stale {
		t.Error("impact answered fresh; want the stale flag for a tree that is not the bundle head")
	}
	want := []string{"» impact: the index answers for tree " + got.Data.Tree +
		", the bundle head is " + headTree + "; impact rows describe the named index"}
	if !slices.Equal(got.Warnings, want) {
		t.Errorf("warnings = %v, want %v", got.Warnings, want)
	}
}

// TestImpactUnresolvedHeadWarns proves a bundle head this clone cannot see
// warns instead of mislabelling the rows, and never refuses.
func TestImpactUnresolvedHeadWarns(t *testing.T) {
	f := newFixture(t)
	writeBlastFixture(f)
	f.commit("one")
	f.build()
	head := strings.Repeat("abc123", 6) + "abcd"
	got := f.impact("HEAD", head, impactDiff)
	want := []string{"» impact: the bundle head " + head + " is not in this clone; impact rows describe " +
		got.Data.Tree + ", not the reviewed head"}
	if !slices.Equal(got.Warnings, want) {
		t.Errorf("warnings = %v, want %v", got.Warnings, want)
	}
}

// TestImpactTruncatesEachList proves the row cap reaches every list and the
// hidden rows are counted rather than dropped silently.
func TestImpactTruncatesEachList(t *testing.T) {
	f := newFixture(t)
	writeBlastFixture(f)
	f.commit("one")
	f.build()
	req := f.impactReq("HEAD", "", impactDiff)
	req.Limit = 1
	got, ref := ImpactOf(context.Background(), req)
	if ref != nil {
		t.Fatalf("impact refused: %s: %s", ref.Code, ref.Message)
	}
	if len(got.Data.Blast.Symbols) != 1 {
		t.Errorf("symbol rows = %d, want 1", len(got.Data.Blast.Symbols))
	}
	if len(got.Data.Blast.Callers) != 1 {
		t.Errorf("caller rows = %d, want 1", len(got.Data.Blast.Callers))
	}
	if got.Data.Blast.Truncated.Callers == nil || *got.Data.Blast.Truncated.Callers != 4 {
		t.Errorf("caller truncation = %v, want 4 hidden of 5", got.Data.Blast.Truncated.Callers)
	}
	if got.Data.Blast.Truncated.Tests == nil || *got.Data.Blast.Truncated.Tests != 2 {
		t.Errorf("test truncation = %v, want 2 hidden of 3", got.Data.Blast.Truncated.Tests)
	}
}

// TestImpactRefusals pins every way the hand-off refuses: an empty name, a
// directory with no store, a revision this clone cannot resolve, a tree the
// store never built, and a corrupt tree file.
func TestImpactRefusals(t *testing.T) {
	f := newFixture(t)
	writeBlastFixture(f)
	f.commit("one")
	f.build()
	built := f.impact("HEAD", "", impactDiff)

	bare := filepath.Join(t.TempDir(), "bare")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	assertRefusal(t, f.impactRefuse("  ", "", impactDiff), codeBadFlags, impactNoIndex)
	assertRefusal(t, f.impactRefuse(bare, "", impactDiff), codeBadFlags,
		"impact: "+bare+" carries no index store (no .verdict/index directory)")
	if ref := f.impactRefuse("nosuchref", "", impactDiff); ref.Code != "bad-at" ||
		!strings.HasPrefix(ref.Message, "cannot resolve nosuchref^{tree}: ") {
		t.Errorf("unresolvable ref refusal = %q: %q, want bad-at naming the revision spec", ref.Code, ref.Message)
	}

	f.write("a/ab.go", "package a\n\nfunc A() {}\n\nfunc B() {}\n\n// later\n")
	f.commit("two")
	assertRefusal(t, f.impactRefuse("HEAD", "", impactDiff), codeMissingIndex,
		"no index for tree "+f.run("", "rev-parse", "HEAD^{tree}")+"; run verdict index build")

	if err := os.WriteFile(filepath.Join(f.dir, ".verdict", "index", "trees", built.Data.Tree+".json"),
		[]byte("{truncated"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertRefusal(t, f.impactRefuse("HEAD~1", "", impactDiff), codeIndexCorrupt,
		"tree "+built.Data.Tree+".json is truncated or invalid")
}
