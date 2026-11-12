package indexcore

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// blastFixture writes the known call graph: A and B in package a, Top calling
// both from a file that also carries an unresolved Top method call, a cycle in
// its own file, and callers at depth 1, 2, and 3.
func writeBlastFixture(f *fixture) {
	f.write("go.mod", "module example.com/m\n\ngo 1.27\n")
	f.write("a/ab.go", "package a\n\nfunc A() {}\n\nfunc B() {}\n")
	f.write("a/top.go", "package a\n\nfunc Top() {\n\tA()\n\tB()\n}\n")
	f.write("a/use.go", "package a\n\ntype holder struct{}\n\nfunc Use() {\n\tvar h holder\n\th.Top()\n}\n")
	f.write("a/cycle.go", "package a\n\nfunc Ping() {\n\tPong()\n}\n\nfunc Pong() {\n\tPing()\n}\n")
	f.write("a/a_test.go", "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tA()\n}\n")
	f.write("b/b.go", "package b\n\nimport \"example.com/m/a\"\n\nfunc UseA() {\n\ta.A()\n}\n")
	f.write("b/b_test.go", "package b\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/a\"\n)\n\nfunc TestUsesA(t *testing.T) {\n\ta.A()\n}\n")
	f.write("c/c.go", "package c\n\nimport \"example.com/m/b\"\n\nfunc Deep() {\n\tb.UseA()\n}\n")
	f.write("d/d.go", "package d\n\nimport \"example.com/m/c\"\n\nfunc Deeper() {\n\tc.Deep()\n}\n")
	f.write(".github/CODEOWNERS", "*       @everyone\n/a/*    @a-owner\na/*.go  @go-owner\n")
}

// blastRun executes one blast-radius query against the fixture.
func blastRun(t *testing.T, f *fixture, mutate func(*Request)) *Response {
	t.Helper()
	req := f.request(QueryBlastRadius, nil)
	mutate(req)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index blast-radius refused: %v", ref)
	}
	return resp
}

// blastRefuse executes one blast-radius query and returns its refusal.
func blastRefuse(t *testing.T, f *fixture, mutate func(*Request)) *core.RefusalError {
	t.Helper()
	req := f.request(QueryBlastRadius, nil)
	mutate(req)
	_, ref := Run(context.Background(), req)
	if ref == nil {
		t.Fatal("index blast-radius answered; want a refusal")
	}
	return ref
}

// assertSymbolRows compares changed declarations against literals, in order.
func assertSymbolRows(t *testing.T, got []SymbolRow, want ...SymbolRow) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("symbol rows = %+v; want %+v", got, want)
	}
}

// assertCallerRows compares caller rows against literals, in order.
func assertCallerRows(t *testing.T, got []CallerRow, want ...CallerRow) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("caller rows = %+v; want %+v", got, want)
	}
}

// assertPartnerRows compares co-change rows against literals, in order.
func assertPartnerRows(t *testing.T, got []BlastPartnerRow, want ...BlastPartnerRow) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("partner rows = %+v; want %+v", got, want)
	}
}

// TestBlastPathsWalksDepthTwo proves the --paths input: every declaration in
// the file is a changed symbol, callers at depth 1 and 2 appear, and the known
// depth-3 caller never does.
func TestBlastPathsWalksDepthTwo(t *testing.T) {
	f := newFixture(t)
	writeBlastFixture(f)
	f.commit("one")
	f.build()
	resp := blastRun(t, f, func(r *Request) { r.Paths = []string{"a/ab.go"} })
	blast := resp.Data.Blast
	assertSymbolRows(t, blast.Symbols,
		SymbolRow{Path: "a/ab.go", Name: "A", Kind: "func", Line: 3},
		SymbolRow{Path: "a/ab.go", Name: "B", Kind: "func", Line: 5},
	)
	assertCallerRows(t, blast.Callers,
		CallerRow{Symbol: "example.com/m/a.A", Path: "a/a_test.go", Kind: "call", Resolution: "exact", Line: 6, Depth: 1},
		CallerRow{Symbol: "example.com/m/a.A", Path: "a/top.go", Kind: "call", Resolution: "exact", Line: 4, Depth: 1},
		CallerRow{Symbol: "example.com/m/a.B", Path: "a/top.go", Kind: "call", Resolution: "exact", Line: 5, Depth: 1},
		CallerRow{Symbol: "example.com/m/a.A", Path: "b/b.go", Kind: "call", Resolution: "lexical", Line: 6, Depth: 1},
		CallerRow{Symbol: "example.com/m/a.A", Path: "b/b_test.go", Kind: "call", Resolution: "lexical", Line: 10, Depth: 1},
		CallerRow{Symbol: "example.com/m/b.UseA", Path: "c/c.go", Kind: "call", Resolution: "lexical", Line: 6, Depth: 2},
	)
	if got := *blast.Counts.Callers; got != 6 {
		t.Errorf("callers count = %d; want 6", got)
	}
	if got := *blast.Counts.Unresolved; got != 1 {
		t.Errorf("unresolved = %d; want 1", got)
	}
	assertTestRows(t, blast.Tests, []TestRow{
		{Path: "a/a_test.go", Via: viaSamePackage},
		{Path: "b/b_test.go", Via: viaReferences},
	})
	if blast.Indexed != true || resp.Data.Tree == "" {
		t.Fatalf("indexed = %t, tree %q; want the built HEAD tree", blast.Indexed, resp.Data.Tree)
	}
	if len(blast.Owners) != 1 || blast.Owners[0].Path != "a/ab.go" ||
		blast.Owners[0].OwnersSource != ".github/CODEOWNERS" ||
		!slices.Equal(blast.Owners[0].Owners, []string{"@go-owner"}) {
		t.Errorf("owners = %+v; want @go-owner from .github/CODEOWNERS", blast.Owners)
	}
}

// TestBlastCycleTerminates proves a two-node call cycle answers once per edge
// instead of growing a row chain.
func TestBlastCycleTerminates(t *testing.T) {
	f := newFixture(t)
	writeBlastFixture(f)
	f.commit("one")
	f.build()
	resp := blastRun(t, f, func(r *Request) { r.Paths = []string{"a/cycle.go"} })
	assertCallerRows(t, resp.Data.Blast.Callers,
		CallerRow{Symbol: "example.com/m/a.Pong", Path: "a/cycle.go", Kind: "call", Resolution: "exact", Line: 4, Depth: 1},
		CallerRow{Symbol: "example.com/m/a.Ping", Path: "a/cycle.go", Kind: "call", Resolution: "exact", Line: 8, Depth: 1},
	)
}

// TestBlastTruncatesEveryList proves the row limit truncates each list and
// reports the hidden rows instead of dropping them silently.
func TestBlastTruncatesEveryList(t *testing.T) {
	f := newFixture(t)
	writeBlastFixture(f)
	f.commit("one")
	f.build()
	resp := blastRun(t, f, func(r *Request) {
		r.Paths = []string{"a/ab.go"}
		r.Limit = 2
	})
	blast := resp.Data.Blast
	if len(blast.Symbols) != 2 || *blast.Truncated.ChangedSymbols != 0 {
		t.Errorf("symbols = %d rows, truncated %d; want 2 and 0",
			len(blast.Symbols), *blast.Truncated.ChangedSymbols)
	}
	if len(blast.Callers) != 2 || *blast.Counts.Callers != 6 || *blast.Truncated.Callers != 4 {
		t.Errorf("callers = %d rows, count %d, truncated %d; want 2, 6, 4",
			len(blast.Callers), *blast.Counts.Callers, *blast.Truncated.Callers)
	}
	if len(blast.Tests) != 2 || *blast.Truncated.Tests != 0 {
		t.Errorf("tests = %d rows, truncated %d; want 2 and 0",
			len(blast.Tests), *blast.Truncated.Tests)
	}
}

// TestBlastPRReadsBundleDiff proves --pr takes the changed paths and lines from
// the fetched bundle's full.diff and reports the bundle head.
func TestBlastPRReadsBundleDiff(t *testing.T) {
	f := newFixture(t)
	writeBlastFixture(f)
	f.commit("one")
	base := f.run("", "rev-parse", "HEAD")
	f.write("a/ab.go", "package a\n\nfunc A() {\n\t_ = 1\n}\n\nfunc B() {}\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "two")
	head := f.run("", "rev-parse", "HEAD")
	diff := f.run("", "diff", base, head, "--", "a/ab.go")
	f.write(".verdict/state/7/full.diff", diff+"\n")
	f.write(".verdict/state/7/PR.json",
		fmt.Sprintf(`{"state":"OPEN","headRefOid":%q,"baseRefOid":%q,"author":{"login":"t"}}`, head, base))
	f.build()
	resp := blastRun(t, f, func(r *Request) { r.Pr = "7" })
	blast := resp.Data.Blast
	if blast.Input.Pr != "7" || blast.Input.Head != head {
		t.Errorf("input = %+v; want pr 7 and head %s", blast.Input, head)
	}
	if !slices.Equal(blast.Input.Paths, []string{"a/ab.go"}) {
		t.Errorf("input paths = %v; want the bundle diff's one file", blast.Input.Paths)
	}
	assertSymbolRows(t, blast.Symbols, SymbolRow{Path: "a/ab.go", Name: "A", Kind: "func", Line: 3})
	if got := *blast.Counts.Callers; got != 5 {
		t.Errorf("callers = %d; want the five within depth 2", got)
	}
}

// TestBlastPRFallsBackToHEAD proves a bundle head the store does not hold is
// named once and the query answers from HEAD instead of refusing.
func TestBlastPRFallsBackToHEAD(t *testing.T) {
	f := newFixture(t)
	writeBlastFixture(f)
	f.commit("one")
	base := f.run("", "rev-parse", "HEAD")
	f.write("a/ab.go", "package a\n\nfunc A() {\n\t_ = 1\n}\n\nfunc B() {}\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "two")
	head := f.run("", "rev-parse", "HEAD")
	f.write(".verdict/state/7/full.diff", "diff --git a/a/ab.go b/a/ab.go\n--- a/a/ab.go\n+++ b/a/ab.go\n@@ -3 +3,3 @@\n-func A() {}\n+func A() {\n+\t_ = 1\n+}\n")
	f.write(".verdict/state/7/PR.json",
		fmt.Sprintf(`{"state":"OPEN","headRefOid":%q,"baseRefOid":%q,"author":{"login":"t"}}`, base, base))
	f.build()
	resp := blastRun(t, f, func(r *Request) { r.Pr = "7" })
	want := "» index: the PR head " + base + " is not indexed here; querying HEAD"
	if !slices.Contains(resp.Warnings, want) {
		t.Errorf("warnings = %q; want %q", resp.Warnings, want)
	}
	if !resp.Data.Blast.Indexed || resp.Data.Tree != f.run("", "rev-parse", head+"^{tree}") {
		t.Errorf("tree = %q; want HEAD's tree %s", resp.Data.Tree, f.run("", "rev-parse", head+"^{tree}"))
	}
}

// TestBlastDeletionAnchorsStayAboveTheRemovedLine proves a deletion marks the
// declaration it emptied, never the one that follows: deleting A's last body
// line must not name B.
func TestBlastDeletionAnchorsStayAboveTheRemovedLine(t *testing.T) {
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n\ngo 1.27\n")
	f.write("a/x.go", "package a\n\nfunc A() {\n\t_ = 1\n}\nfunc B() {}\n")
	f.commit("one")
	base := f.run("", "rev-parse", "HEAD")
	f.write("a/x.go", "package a\n\nfunc A() {\n}\nfunc B() {}\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "two")
	head := f.run("", "rev-parse", "HEAD")
	diff := f.run("", "diff", base, head, "--", "a/x.go")
	f.write(".verdict/state/7/full.diff", diff+"\n")
	f.write(".verdict/state/7/PR.json",
		fmt.Sprintf(`{"state":"OPEN","headRefOid":%q,"baseRefOid":%q,"author":{"login":"t"}}`, head, base))
	f.build()
	resp := blastRun(t, f, func(r *Request) { r.Pr = "7" })
	assertSymbolRows(t, resp.Data.Blast.Symbols,
		SymbolRow{Path: "a/x.go", Name: "A", Kind: "func", Line: 3},
	)
}

// TestBlastRenameOnlySectionNamesEveryDeclaration proves a hunk-less section
// reads as a whole-file change and warns, so a rename is never mistaken for a
// file that changed nothing.
func TestBlastRenameOnlySectionNamesEveryDeclaration(t *testing.T) {
	f := newFixture(t)
	writeBlastFixture(f)
	f.commit("one")
	base := f.run("", "rev-parse", "HEAD")
	f.git("mv", "a/ab.go", "a/renamed.go")
	f.git("commit", "-qm", "rename")
	head := f.run("", "rev-parse", "HEAD")
	diff := f.run("", "diff", "-M", base, head)
	f.write(".verdict/state/7/full.diff", diff+"\n")
	f.write(".verdict/state/7/PR.json",
		fmt.Sprintf(`{"state":"OPEN","headRefOid":%q,"baseRefOid":%q,"author":{"login":"t"}}`, head, base))
	f.build()
	resp := blastRun(t, f, func(r *Request) { r.Pr = "7" })
	assertSymbolRows(t, resp.Data.Blast.Symbols,
		SymbolRow{Path: "a/renamed.go", Name: "A", Kind: "func", Line: 3},
		SymbolRow{Path: "a/renamed.go", Name: "B", Kind: "func", Line: 5},
	)
	want := "» index: a/renamed.go changed with no line hunks; " +
		"every declaration in the new path is a changed symbol"
	if !slices.Contains(resp.Warnings, want) {
		t.Errorf("warnings = %q; want %q", resp.Warnings, want)
	}
}

// TestBlastNullCountsWithoutIndex proves an absent index reads the symbol,
// caller, and test counts as null (never 0) while the git-measured lists still
// answer.
//
//nolint:revive // the count matrix reads as one block
func TestBlastNullCountsWithoutIndex(t *testing.T) {
	f := newFixture(t)
	writeBlastFixture(f)
	f.commit("one")
	resp := blastRun(t, f, func(r *Request) { r.Paths = []string{"a/ab.go"} })
	blast := resp.Data.Blast
	if blast.Indexed {
		t.Fatal("indexed = true; want no index")
	}
	for name, count := range map[string]*int{
		"changed_symbols": blast.Counts.ChangedSymbols,
		"callers":         blast.Counts.Callers,
		"unresolved":      blast.Counts.Unresolved,
		"tests":           blast.Counts.Tests,
	} {
		if count != nil {
			t.Errorf("%s = %d; want null without an index", name, *count)
		}
	}
	for name, truncated := range map[string]*int{
		"changed_symbols": blast.Truncated.ChangedSymbols,
		"callers":         blast.Truncated.Callers,
		"tests":           blast.Truncated.Tests,
	} {
		if truncated != nil {
			t.Errorf("%s truncated = %d; want null without an index", name, *truncated)
		}
	}
	if len(blast.Symbols) != 0 || len(blast.Callers) != 0 || len(blast.Tests) != 0 {
		t.Errorf("rows = %d/%d/%d; want empty without an index",
			len(blast.Symbols), len(blast.Callers), len(blast.Tests))
	}
	if blast.Counts.Owners != 1 || len(blast.Owners) != 1 {
		t.Errorf("owners = %d rows; want the git-measured path", len(blast.Owners))
	}
}

// TestBlastCoChangePartners proves the reused co-change window and its bound:
// the default minimum count keeps both partners, and a higher one drops the
// rarer partner.
func TestBlastCoChangePartners(t *testing.T) {
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n\ngo 1.27\n")
	f.write("a/target.go", "package a\n\nfunc Target() {}\n")
	f.write("b/one.txt", "one\n")
	f.write("c/two.txt", "two\n")
	f.commit("one")
	for _, step := range []struct{ goBody, partner, body string }{
		{"package a\n\nfunc Target() {}\n\nfunc Second() {}\n", "b/one.txt", "two\n"},
		{"package a\n\nfunc Target() {}\n\nfunc Second() {}\n\nfunc Third() {}\n", "b/one.txt", "three\n"},
		{"package a\n\nfunc Target() {}\n\nfunc Second() {}\n\nfunc Third() {}\n\nfunc Fourth() {}\n", "c/two.txt", "changed\n"},
	} {
		f.write("a/target.go", step.goBody)
		f.write(step.partner, step.body)
		f.commit("step")
	}
	f.build()
	def := blastRun(t, f, func(r *Request) { r.Paths = []string{"a/target.go"} })
	assertPartnerRows(t, def.Data.Blast.Partners,
		BlastPartnerRow{Target: "a/target.go", Path: "b/one.txt", Commits: 3, Jaccard: 0.75},
		BlastPartnerRow{Target: "a/target.go", Path: "c/two.txt", Commits: 2, Jaccard: 0.5},
	)
	rare := blastRun(t, f, func(r *Request) {
		r.Paths = []string{"a/target.go"}
		r.MinCount = 3
	})
	assertPartnerRows(t, rare.Data.Blast.Partners,
		BlastPartnerRow{Target: "a/target.go", Path: "b/one.txt", Commits: 3, Jaccard: 0.75},
	)
}

// TestBlastRefusals pins the query's input refusals.
func TestBlastRefusals(t *testing.T) {
	f := newFixture(t)
	writeBlastFixture(f)
	f.commit("one")
	cases := []struct {
		name string
		mut  func(*Request)
		code string
		part string
	}{
		{
			name: "no input", mut: func(*Request) {},
			code: "bad-flags", part: "--pr <n> or --paths <path>",
		},
		{
			name: "both inputs", mut: func(r *Request) { r.Pr = "7"; r.Paths = []string{"a/ab.go"} },
			code: "bad-flags", part: "not both",
		},
		{
			name: "unknown path", mut: func(r *Request) { r.Paths = []string{"nope.go"} },
			code: "bad-flags", part: "nope.go is not a file at ",
		},
		{
			name: "missing bundle", mut: func(r *Request) { r.Pr = "99" },
			code: "missing-bundle", part: ".verdict/state/99",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := blastRefuse(t, f, tc.mut)
			if ref.Code != tc.code || !strings.Contains(ref.Message, tc.part) {
				t.Fatalf("refusal = %q: %q; want %s naming %q", ref.Code, ref.Message, tc.code, tc.part)
			}
		})
	}
}

// TestBlastWindowBounds pins the published bounds of --commits, --min-count,
// and --limit.
func TestBlastWindowBounds(t *testing.T) {
	f := newFixture(t)
	writeBlastFixture(f)
	f.commit("one")
	cases := []struct {
		name string
		mut  func(*Request)
		part string
	}{
		{"commits zero", func(r *Request) { r.Paths = []string{"a/ab.go"}; r.Commits = -1 }, "--commits from 1 to 100000"},
		{"min-count zero", func(r *Request) { r.Paths = []string{"a/ab.go"}; r.MinCount = -1 }, "--min-count from 1 to 100000"},
		{"limit over", func(r *Request) { r.Paths = []string{"a/ab.go"}; r.Limit = 201 }, "--limit from 1 to 200"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := blastRefuse(t, f, tc.mut)
			if ref.Code != "bad-flags" || !strings.Contains(ref.Message, tc.part) {
				t.Fatalf("refusal = %q: %q; want bad-flags naming %q", ref.Code, ref.Message, tc.part)
			}
		})
	}
}
