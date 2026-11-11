package indexcore

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/testenv"
)

// writeGraphFixture writes the three-package candidate-test fixture: a target
// package with a same-package test, a package that references the symbol
// through an import edge, a package that only imports the target's package,
// and a non-test package.
func writeGraphFixture(f *fixture) {
	f.write("go.mod", "module example.com/m\n\ngo 1.27\n")
	f.write("a/a.go", "package a\n\nfunc Target() {}\n")
	f.write("a/other.go", "package a\n\nfunc Other() {}\n")
	f.write("a/a_test.go", "package a\n\nimport \"testing\"\n\nfunc TestTarget(t *testing.T) {\n\tTarget()\n}\n")
	f.write("b/b_test.go", "package b\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/a\"\n)\n\nfunc TestUses(t *testing.T) {\n\ta.Target()\n}\n")
	f.write("c/c.go", "package c\n\nfunc Local() {}\n")
	f.write("c/c_test.go", "package c\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/a\"\n)\n\nfunc TestOther(t *testing.T) {\n\ta.Other()\n}\n")
}

// queryPath runs one index query that names a path.
func (f *fixture) queryPath(query, p string) *Response {
	f.t.Helper()
	req := f.request(query, nil)
	req.Path = p
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		f.t.Fatalf("index %s %s refused: %v", query, p, ref)
	}
	return resp
}

// querySymbol runs the fixture's tests-for --symbol query.
func (f *fixture) querySymbol(symbol string) *Response {
	f.t.Helper()
	req := f.request(QueryTestsFor, nil)
	req.Symbol = symbol
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		f.t.Fatalf("index tests-for --symbol %s refused: %v", symbol, ref)
	}
	return resp
}

// bodyTree returns HEAD's tree id for refusal messages.
func (f *fixture) bodyTree() string { return f.run("", "rev-parse", "HEAD^{tree}") }

// emptyCommit adds one commit that changes nothing.
func (f *fixture) emptyCommit(msg string) {
	f.t.Helper()
	f.git("commit", "--allow-empty", "-qm", msg)
	f.day++
}

// assertTestRows compares candidate-test rows against literals, in order.
func assertTestRows(t *testing.T, got, want []TestRow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("test rows = %+v; want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("test row %d = %+v; want %+v", i, got[i], want[i])
		}
	}
}

// assertCoChangeRows compares partner rows against literals, including order.
func assertCoChangeRows(t *testing.T, got, want []CoChangeRow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("co-change rows = %+v; want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("co-change row %d = %+v; want %+v", i, got[i], want[i])
		}
	}
}

// assertOwners compares one target against literal owners and source.
func assertOwners(t *testing.T, got *Target, owners []string, source string) {
	t.Helper()
	if got == nil {
		t.Fatalf("target = nil; want owners %q from %s", owners, source)
	}
	if !slices.Equal(got.Owners, owners) || got.OwnersSource != source {
		t.Fatalf("owners = %q from %q; want %q from %q", got.Owners, got.OwnersSource, owners, source)
	}
}

// TestTestsForCandidates proves the three via kinds on a literal fixture and
// that a non-test file never appears as a candidate.
func TestTestsForCandidates(t *testing.T) {
	f := newFixture(t)
	writeGraphFixture(f)
	f.commit("one")
	f.build()
	want := []TestRow{
		{Path: "a/a_test.go", Via: viaSamePackage},
		{Path: "b/b_test.go", Via: viaReferences},
		{Path: "c/c_test.go", Via: viaImportsModule},
	}
	resp := f.queryPath(QueryTestsFor, "a/a.go")
	assertTestRows(t, resp.Data.TestRows, want)
	if resp.Data.Tests != 3 || resp.Data.Truncated != 0 || resp.Data.Limit != DefaultLimit {
		t.Errorf("tests data = %d tests, truncated %d, limit %d; want 3, 0, 50",
			resp.Data.Tests, resp.Data.Truncated, resp.Data.Limit)
	}
	for _, row := range resp.Data.TestRows {
		if !strings.HasSuffix(row.Path, "_test.go") {
			t.Errorf("non-test file %q appeared as a candidate", row.Path)
		}
	}
}

// TestTestsForSymbol proves the symbol form resolves the declaring package and
// answers the same candidates as the path form.
func TestTestsForSymbol(t *testing.T) {
	f := newFixture(t)
	writeGraphFixture(f)
	f.commit("one")
	f.build()
	resp := f.querySymbol("a.Target")
	want := []TestRow{
		{Path: "a/a_test.go", Via: viaSamePackage},
		{Path: "b/b_test.go", Via: viaReferences},
		{Path: "c/c_test.go", Via: viaImportsModule},
	}
	assertTestRows(t, resp.Data.TestRows, want)
	if resp.Data.Path != "" || resp.Data.Symbol != "a.Target" {
		t.Errorf("query = path %q, symbol %q; want the symbol form", resp.Data.Path, resp.Data.Symbol)
	}
	if resp.Data.Target != nil {
		t.Errorf("symbol query target = %+v; want none", resp.Data.Target)
	}
}

// TestTestsForRefusals proves the input shapes refuse instead of answering
// with an empty list.
func TestTestsForRefusals(t *testing.T) {
	f := newFixture(t)
	writeGraphFixture(f)
	f.commit("one")
	f.build()
	tree := f.bodyTree()
	cases := []struct {
		name, path, symbol, code, message string
	}{
		{
			name: "no input", code: "bad-flags",
			message: "index tests-for wants one path or --symbol pkg.Name",
		},
		{
			name: "both inputs", path: "a/a.go", symbol: "a.Target", code: "bad-flags",
			message: "index tests-for wants one path or --symbol pkg.Name",
		},
		{
			name: "missing file", path: "a/nope.go", code: "bad-flags",
			message: "index tests-for: a/nope.go is not an indexed Go file at " + tree,
		},
		{
			name: "bare symbol", symbol: "Target", code: "bad-flags",
			message: "index tests-for wants a qualified --symbol (pkg.Name)",
		},
		{
			name: "unknown declaration", symbol: "a.Nope", code: "bad-flags",
			message: "index tests-for: no declaration of a.Nope",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := f.request(QueryTestsFor, nil)
			req.Path, req.Symbol = tc.path, tc.symbol
			_, ref := Run(context.Background(), req)
			assertRefusal(t, ref, tc.code, tc.message)
		})
	}
}

// TestTestsForTruncates proves candidates truncate with a count, never
// silently.
func TestTestsForTruncates(t *testing.T) {
	f := newFixture(t)
	writeGraphFixture(f)
	f.commit("one")
	f.build()
	req := f.request(QueryTestsFor, nil)
	req.Path, req.Limit = "a/a.go", 2
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index tests-for refused: %v", ref)
	}
	if resp.Data.Tests != 3 || resp.Data.Truncated != 1 || resp.Data.Limit != 2 {
		t.Fatalf("truncated data = tests %d, truncated %d, limit %d; want 3, 1, 2",
			resp.Data.Tests, resp.Data.Truncated, resp.Data.Limit)
	}
	want := "index: showing 2 of 3 candidate tests (1 not shown; raise --limit up to 200)"
	if !slices.Contains(resp.Human, want) {
		t.Errorf("human lines = %q; want one equal to %q", resp.Human, want)
	}
}

// codeownersFixture writes the overlapping-rule fixture: the last matching
// rule wins, and b/b.go matches only the global rule.
func codeownersFixture(f *fixture, body string) {
	f.write("go.mod", "module example.com/m\n")
	f.write("a/a.go", "package a\n\nfunc Target() {}\n")
	f.write("b/b.go", "package b\n\nfunc Local() {}\n")
	f.write(".github/CODEOWNERS", body)
}

// TestOwnersLastMatchWins proves the last matching rule owns the path, across
// overlapping anchored and segment patterns.
func TestOwnersLastMatchWins(t *testing.T) {
	f := newFixture(t)
	codeownersFixture(f, "# global\n*       @everyone\n/a/*    @a-owner\na/*.go  @go-owner\n")
	f.commit("one")
	got := f.queryPath(QueryOwners, "a/a.go").Data.Target
	assertOwners(t, got, []string{"@go-owner"}, ".github/CODEOWNERS")
	other := f.queryPath(QueryOwners, "b/b.go").Data.Target
	assertOwners(t, other, []string{"@everyone"}, ".github/CODEOWNERS")
}

// TestOwnersLocationPrecedence proves GitHub's search order: .github/CODEOWNERS
// wins over the root file, which wins over docs/CODEOWNERS.
func TestOwnersLocationPrecedence(t *testing.T) {
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n")
	f.write("a/a.go", "package a\n\nfunc Target() {}\n")
	f.write(".github/CODEOWNERS", "* @github\n")
	f.write("CODEOWNERS", "* @root\n")
	f.write("docs/CODEOWNERS", "* @docs\n")
	f.commit("one")
	assertOwners(t, f.queryPath(QueryOwners, "a/a.go").Data.Target, []string{"@github"}, ".github/CODEOWNERS")
	if err := os.Remove(filepath.Join(f.dir, ".github", "CODEOWNERS")); err != nil {
		t.Fatal(err)
	}
	f.commit("drop github")
	assertOwners(t, f.queryPath(QueryOwners, "a/a.go").Data.Target, []string{"@root"}, "CODEOWNERS")
	if err := os.Remove(filepath.Join(f.dir, "CODEOWNERS")); err != nil {
		t.Fatal(err)
	}
	f.commit("drop root")
	assertOwners(t, f.queryPath(QueryOwners, "a/a.go").Data.Target, []string{"@docs"}, "docs/CODEOWNERS")
}

// TestOwnersAbsentFileIsNull proves an absent CODEOWNERS file reads as no
// owners, never as an owner named nobody.
func TestOwnersAbsentFileIsNull(t *testing.T) {
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n")
	f.write("a/a.go", "package a\n\nfunc Target() {}\n")
	f.commit("one")
	resp := f.queryPath(QueryOwners, "a/a.go")
	got := resp.Data.Target
	if got == nil || got.Owners != nil || got.OwnersSource != "" {
		t.Fatalf("target = %+v; want no owners and no source", got)
	}
	wire := resp.Data.Wire().MarshalCompact()
	if !strings.Contains(wire, `"owners":null`) || !strings.Contains(wire, `"owners_source":null`) {
		t.Errorf("wire = %s; want null owners and owners_source", wire)
	}
}

// TestOwnersNoMatchingRule proves a present file with no matching rule names
// its source and answers no owners.
func TestOwnersNoMatchingRule(t *testing.T) {
	f := newFixture(t)
	codeownersFixture(f, "docs/* @docs\n")
	f.commit("one")
	got := f.queryPath(QueryOwners, "a/a.go").Data.Target
	if got == nil || got.Owners != nil || got.OwnersSource != ".github/CODEOWNERS" {
		t.Fatalf("target = %+v; want no owners from .github/CODEOWNERS", got)
	}
}

// TestOwnersReadFromTreeNotWorktree proves the worktree copy is never read: an
// uncommitted edit does not change the answer.
func TestOwnersReadFromTreeNotWorktree(t *testing.T) {
	f := newFixture(t)
	codeownersFixture(f, "* @committed\n")
	f.commit("one")
	path := filepath.Join(f.dir, ".github", "CODEOWNERS")
	if err := os.WriteFile(path, []byte("* @worktree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertOwners(t, f.queryPath(QueryOwners, "a/a.go").Data.Target, []string{"@committed"}, ".github/CODEOWNERS")
}

// TestOwnersUnsupportedPatternWarns proves a rule shape the matcher cannot
// evaluate is counted in one warning and never silently changes ownership.
func TestOwnersUnsupportedPatternWarns(t *testing.T) {
	f := newFixture(t)
	codeownersFixture(f, "* @everyone\n!a/* @blocked\n")
	f.commit("one")
	resp := f.queryPath(QueryOwners, "a/a.go")
	assertOwners(t, resp.Data.Target, []string{"@everyone"}, ".github/CODEOWNERS")
	want := "» index: CODEOWNERS has 1 rule line(s) the index does not evaluate " +
		"(negation, escape, or character class); those lines match nothing"
	if !slices.Contains(resp.Warnings, want) {
		t.Errorf("warnings = %q; want one equal to %q", resp.Warnings, want)
	}
}

// TestOwnerGlobSemantics pins the documented pattern subset: anchors, segment
// patterns, `**`, `?`, and directory patterns.
//
//nolint:revive // one table row pins one pattern literal
func TestOwnerGlobSemantics(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"*", "a/b.go", true},
		{"*.go", "a/b.go", true},
		{"*.go", "a/b.md", false},
		{"a/*.go", "a/x.go", true},
		{"a/*.go", "a/sub/x.go", false},
		{"/a/*", "a/x.go", true},
		{"/a/*", "b/a/x.go", false},
		{"a/**", "a/sub/x.go", true},
		{"a/**/x.go", "a/x.go", true},
		{"a/**/x.go", "a/b/c/x.go", true},
		{"a/**/x.go", "b/x.go", false},
		{"docs/", "docs/a.go", true},
		{"docs/", "x/docs/a.go", true},
		{"docs/", "docsx/a.go", false},
		{"a?.go", "ab.go", true},
		{"a?.go", "abc.go", false},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+" "+tc.path, func(t *testing.T) {
			re, ok := compileOwnerPattern(tc.pattern)
			if !ok {
				t.Fatalf("compileOwnerPattern(%q) refused", tc.pattern)
			}
			rules := []ownerRule{{re: re, owners: []string{"@x"}}}
			got := matchOwners(rules, tc.path) != nil
			if got != tc.want {
				t.Errorf("%q vs %q = %t; want %t", tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}

// TestOwnerGlobRefusals proves the documented-unsupported shapes never reach
// the matcher, so a rule using them cannot silently change ownership.
func TestOwnerGlobRefusals(t *testing.T) {
	for _, pattern := range []string{"!a/*", `a\*`, "a[bc].go"} {
		if _, ok := compileOwnerPattern(pattern); ok {
			t.Errorf("compileOwnerPattern(%q) accepted an unsupported shape", pattern)
		}
	}
}

// writeHistoryFixture writes one target file co-changed with b three times, c
// twice, and d once, over four commits dated one day apart.
func writeHistoryFixture(f *fixture) {
	f.write("go.mod", "module example.com/m\n")
	f.write("a/target.go", "package a\n\nfunc Target() {}\n")
	f.write("b/partner.go", "package b\n\nfunc B() {}\n")
	f.write("c/rare.go", "package c\n\nfunc C() {}\n")
	f.write("d/never.go", "package d\n\nfunc D() {}\n")
	f.commit("one")
	f.write("a/target.go", "package a\n\nfunc Target() {}\n\nfunc Second() {}\n")
	f.write("b/partner.go", "package b\n\nfunc B() {}\n\nfunc B2() {}\n")
	f.commit("two")
	f.write("a/target.go", "package a\n\nfunc Target() {}\n\nfunc Second() {}\n\nfunc Third() {}\n")
	f.write("c/rare.go", "package c\n\nfunc C() {}\n\nfunc C2() {}\n")
	f.commit("three")
	f.write("a/target.go", "package a\n\nfunc Target() {}\n\nfunc Second() {}\n\nfunc Third() {}\n\nfunc Fourth() {}\n")
	f.write("b/partner.go", "package b\n\nfunc B() {}\n\nfunc B2() {}\n\nfunc B3() {}\n")
	f.commit("four")
}

// TestCoChangedPartners proves literal partner counts and jaccard over a known
// history, and that the window is echoed with its empty commit counted.
func TestCoChangedPartners(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	f.emptyCommit("five")
	resp := f.queryPath(QueryCoChanged, "a/target.go")
	assertCoChangeRows(t, resp.Data.CoChangeRows, []CoChangeRow{
		{Path: "b/partner.go", Commits: 3, Jaccard: 0.75},
		{Path: "c/rare.go", Commits: 2, Jaccard: 0.5},
	})
	if resp.Data.Partners != 2 || resp.Data.TargetCommits != 4 {
		t.Errorf("counts = partners %d, target_commits %d; want 2, 4",
			resp.Data.Partners, resp.Data.TargetCommits)
	}
	window := resp.Data.Window
	if window.Commits != 5 || window.Last != f.run("", "rev-parse", "HEAD") {
		t.Errorf("window = %+v; want 5 commits ending at HEAD", window)
	}
	if window.First != f.run("", "rev-list", "--max-parents=0", "HEAD") {
		t.Errorf("window.first = %s; want the root commit", window.First)
	}
}

// TestCoChangedMinCountFilters proves a partner below the minimum count is
// absent from the rows and the partner count.
func TestCoChangedMinCountFilters(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	req := f.request(QueryCoChanged, nil)
	req.Path, req.MinCount = "a/target.go", 3
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index co-changed refused: %v", ref)
	}
	assertCoChangeRows(t, resp.Data.CoChangeRows, []CoChangeRow{
		{Path: "b/partner.go", Commits: 3, Jaccard: 0.75},
	})
	if resp.Data.Partners != 1 {
		t.Errorf("partners = %d; want 1", resp.Data.Partners)
	}
}

// TestCoChangedExcludesMerges proves a merge commit is not counted in the
// window and contributes no co-change.
func TestCoChangedExcludesMerges(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	f.git("switch", "-qc", "side")
	f.write("merge-only.go", "package m\n\nfunc Merged() {}\n")
	f.commit("side work")
	f.git("switch", "-q", "main")
	f.git("merge", "--no-ff", "-m", "merge side", "side")
	f.day++
	resp := f.queryPath(QueryCoChanged, "a/target.go")
	if resp.Data.Window.Commits != 5 {
		t.Fatalf("window commits = %d; want 5 with the merge excluded", resp.Data.Window.Commits)
	}
	assertCoChangeRows(t, resp.Data.CoChangeRows, []CoChangeRow{
		{Path: "b/partner.go", Commits: 3, Jaccard: 0.75},
		{Path: "c/rare.go", Commits: 2, Jaccard: 0.5},
	})
}

// TestCoChangedWindowRef proves --since <ref> bounds the window to the commits
// after that ref.
func TestCoChangedWindowRef(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	f.emptyCommit("five")
	base := f.run("", "rev-parse", "HEAD~3")
	req := f.request(QueryCoChanged, nil)
	req.Path, req.Since, req.MinCount = "a/target.go", base, 1
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index co-changed refused: %v", ref)
	}
	if resp.Data.Window.Commits != 3 {
		t.Fatalf("window commits = %d; want 3 since %s", resp.Data.Window.Commits, base)
	}
	assertCoChangeRows(t, resp.Data.CoChangeRows, []CoChangeRow{
		{Path: "b/partner.go", Commits: 1, Jaccard: 0.5},
		{Path: "c/rare.go", Commits: 1, Jaccard: 0.5},
	})
}

// TestCoChangedWindowDuration proves a --since duration counts back from HEAD's
// own committer date, not the wall clock.
func TestCoChangedWindowDuration(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	req := f.request(QueryCoChanged, nil)
	req.Path, req.Since, req.MinCount = "a/target.go", "2d", 1
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index co-changed refused: %v", ref)
	}
	if resp.Data.Window.Commits != 3 {
		t.Fatalf("window commits = %d; want the three commits from the 2d cutoff", resp.Data.Window.Commits)
	}
	assertCoChangeRows(t, resp.Data.CoChangeRows, []CoChangeRow{
		{Path: "b/partner.go", Commits: 2, Jaccard: 0.6667},
		{Path: "c/rare.go", Commits: 1, Jaccard: 0.3333},
	})
}

// TestCoChangedCacheIsDeterministic proves two runs write byte-identical caches
// and a corrupt cache is rebuilt rather than read as an empty window.
func TestCoChangedCacheIsDeterministic(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	first := f.queryPath(QueryCoChanged, "a/target.go")
	before := snapshotCoChange(t, f.dir)
	if err := os.RemoveAll(filepath.Join(f.dir, ".verdict", "index", "cochange")); err != nil {
		t.Fatal(err)
	}
	second := f.queryPath(QueryCoChanged, "a/target.go")
	after := snapshotCoChange(t, f.dir)
	if !maps.Equal(before, after) {
		t.Fatalf("cache differs across identical queries:\nfirst:  %q\nsecond: %q", before, after)
	}
	assertCoChangeRows(t, second.Data.CoChangeRows, first.Data.CoChangeRows)
	if len(before) != 1 {
		t.Fatalf("cache files = %d; want 1", len(before))
	}
	for name := range before {
		path := filepath.Join(f.dir, ".verdict", "index", "cochange", name)
		if err := os.WriteFile(path, []byte(`{"schema_version":"1",`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	third := f.queryPath(QueryCoChanged, "a/target.go")
	assertCoChangeRows(t, third.Data.CoChangeRows, []CoChangeRow{
		{Path: "b/partner.go", Commits: 3, Jaccard: 0.75},
		{Path: "c/rare.go", Commits: 2, Jaccard: 0.5},
	})
}

// snapshotCoChange reads every co-change cache file keyed by name.
func snapshotCoChange(t *testing.T, root string) map[string]string {
	t.Helper()
	dir := filepath.Join(root, ".verdict", "index", "cochange")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(raw)
	}
	return out
}

// TestCoChangedCacheKeepsTwo proves the cache keeps the two newest windows.
func TestCoChangedCacheKeepsTwo(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	var paths []string
	for i := range 3 {
		f.write("a/target.go", "package a\n\nfunc Target() {}\n\nfunc Round"+string(rune('A'+i))+"() {}\n")
		f.commit("round " + string(rune('A'+i)))
		f.queryPath(QueryCoChanged, "a/target.go")
		stampCache(t, f.dir, int64(1000+i))
		paths = append(paths, f.run("", "rev-parse", "HEAD"))
	}
	entries, err := os.ReadDir(filepath.Join(f.dir, ".verdict", "index", "cochange"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("cache files = %d; want 2", len(entries))
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	want := []string{paths[2] + "-2000.json", paths[1] + "-2000.json"}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("cache files = %v; want %v", got, want)
	}
}

// stampCache pins every cache file's mtime, so prune's order is fixture data
// rather than the filesystem's clock granularity.
func stampCache(t *testing.T, root string, sec int64) {
	t.Helper()
	dir := filepath.Join(root, ".verdict", "index", "cochange")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		stamp := time.Unix(sec, 0)
		if err := os.Chtimes(filepath.Join(dir, e.Name()), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCoChangedShallowWarns proves a depth-limited clone is warned about: its
// counts are true of the commits present only.
func TestCoChangedShallowWarns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the file:// clone transport is not portable on Windows")
	}
	f := newFixture(t)
	writeHistoryFixture(f)
	clone := shallowClone(t, f.dir)
	req := &Request{
		Query:    QueryCoChanged,
		Path:     "a/target.go",
		Runtime:  &core.Runtime{Commander: coreexec.Runner{}, Env: envMap()},
		RepoRoot: clone,
	}
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index co-changed refused: %v", ref)
	}
	literal := "» index: shallow clone: co-change counts see only the commits present; " +
		"run 'git fetch --unshallow' for the full history"
	if !slices.Contains(resp.Warnings, literal) {
		t.Errorf("warnings = %q; want one equal to %q", resp.Warnings, literal)
	}
}

// shallowClone clones src at depth 1 through the file transport, which honors
// the depth locally.
func shallowClone(t *testing.T, src string) string {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "shallow")
	cmd := exec.CommandContext(t.Context(), "git", "clone", "-q", "--depth", "1", "file://"+filepath.ToSlash(src), dest)
	cmd.Env = testenv.Base(nil)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone --depth 1: %v\n%s", err, out)
	}
	return dest
}

// TestCoChangedRefusals proves bad bounds and unknown paths refuse with one
// message each.
func TestCoChangedRefusals(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	cases := []struct {
		message    string
		name, path string
		code       string
		commits    int
		limit      int
		minCount   int
	}{
		{
			name: "missing path", code: "bad-flags",
			message: "index co-changed wants one repository path",
		},
		{
			name: "unknown path", path: "nope.go", code: "bad-flags",
			message: "index co-changed: nope.go is not a file at HEAD",
		},
		{
			name: "commits high", path: "a/target.go", commits: MaxCommitWindow + 1, code: "bad-flags",
			message: "index co-changed wants --commits from 1 to 100000",
		},
		{
			name: "limit high", path: "a/target.go", limit: MaxLimit + 1, code: "bad-flags",
			message: "index co-changed wants --limit from 1 to 200",
		},
		{
			name: "min count low", path: "a/target.go", minCount: -1, code: "bad-flags",
			message: "index co-changed wants --min-count from 1 to 100000",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := f.request(QueryCoChanged, nil)
			req.Path = tc.path
			req.Commits, req.Limit, req.MinCount = tc.commits, tc.limit, tc.minCount
			_, ref := Run(context.Background(), req)
			assertRefusal(t, ref, tc.code, tc.message)
		})
	}
}

// TestCoChangedSinceRefusal proves an unresolvable --since ref refuses as a
// bad revision instead of counting the whole history.
func TestCoChangedSinceRefusal(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	req := f.request(QueryCoChanged, nil)
	req.Path, req.Since = "a/target.go", "nosuchref"
	_, ref := Run(context.Background(), req)
	if ref == nil || ref.Code != "bad-at" {
		t.Fatalf("refusal = %v; want bad-at", ref)
	}
}
