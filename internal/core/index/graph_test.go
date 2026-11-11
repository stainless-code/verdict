package indexcore

import (
	"context"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
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
	codeownersFixture(f, "* @everyone\n!a/* @blocked\na[bc].go @bracket\n")
	f.commit("one")
	resp := f.queryPath(QueryOwners, "a/a.go")
	assertOwners(t, resp.Data.Target, []string{"@everyone"}, ".github/CODEOWNERS")
	want := "» index: CODEOWNERS has 2 rule line(s) the index does not evaluate " +
		"(negation, escape, or character class); those lines match nothing"
	if !slices.Contains(resp.Warnings, want) {
		t.Errorf("warnings = %q; want one equal to %q", resp.Warnings, want)
	}
}

// TestOwnersOwnerlessRuleClearsEarlierOwnership proves a rule that names nobody
// is kept and clears an earlier match: the path reads an empty owner list, not
// the previous rule's owners and not the no-rule null.
func TestOwnersOwnerlessRuleClearsEarlierOwnership(t *testing.T) {
	f := newFixture(t)
	codeownersFixture(f, "* @everyone\nspecial.go\n")
	f.write("special.go", "package m\n\nfunc Special() {}\n")
	f.commit("one")
	resp := f.queryPath(QueryOwners, "special.go")
	got := resp.Data.Target
	if got == nil || got.Owners == nil || len(got.Owners) != 0 || got.OwnersSource != ".github/CODEOWNERS" {
		t.Fatalf("target = %+v; want an empty owner list from .github/CODEOWNERS", got)
	}
	if wire := resp.Data.Wire().MarshalCompact(); !strings.Contains(wire, `"owners":[]`) {
		t.Errorf("wire = %s; want an empty owners array", wire)
	}
	want := "index: special.go has no owners: the last matching rule in .github/CODEOWNERS names none (at " +
		f.bodyTree() + ")"
	if !slices.Contains(resp.Human, want) {
		t.Errorf("human lines = %q; want one equal to %q", resp.Human, want)
	}
	assertOwners(t, f.queryPath(QueryOwners, "a/a.go").Data.Target, []string{"@everyone"}, ".github/CODEOWNERS")
}

// TestOwnersInlineCommentAndEscapedHash proves an inline `#` starts a comment
// after the owners, while `\#` in a pattern names a literal `#`.
func TestOwnersInlineCommentAndEscapedHash(t *testing.T) {
	f := newFixture(t)
	codeownersFixture(f, "*.go @a # contact @backup\n"+`a\#b.go @hash # comment`+"\n")
	f.write("a#b.go", "package m\n\nfunc Hash() {}\n")
	f.commit("one")
	resp := f.queryPath(QueryOwners, "a/a.go")
	assertOwners(t, resp.Data.Target, []string{"@a"}, ".github/CODEOWNERS")
	if len(resp.Warnings) != 0 {
		t.Errorf("warnings = %q; want none: an inline comment is not an unsupported pattern", resp.Warnings)
	}
	assertOwners(t, f.queryPath(QueryOwners, "a#b.go").Data.Target, []string{"@hash"}, ".github/CODEOWNERS")
}

// TestOwnersLeadingEscapedHashIsNotEvaluated proves GitHub's CODEOWNERS escape
// warning holds: `\#` does not turn a comment line into a rule, so it cannot
// take ownership from an earlier pattern.
func TestOwnersLeadingEscapedHashIsNotEvaluated(t *testing.T) {
	f := newFixture(t)
	codeownersFixture(f, "* @default\n"+`\#secret.go @special`+"\n")
	f.write("#secret.go", "package m\n\nfunc Secret() {}\n")
	f.commit("one")
	resp := f.queryPath(QueryOwners, "#secret.go")
	assertOwners(t, resp.Data.Target, []string{"@default"}, ".github/CODEOWNERS")
	want := "» index: CODEOWNERS has 1 rule line(s) the index does not evaluate " +
		"(negation, escape, or character class); those lines match nothing"
	if !slices.Contains(resp.Warnings, want) {
		t.Errorf("warnings = %q; want one equal to %q", resp.Warnings, want)
	}
}

// TestOwnersDirectoryRuleOwnsDescendants proves a directory named without a
// trailing slash owns every file below it, GitHub's directory-rule form.
func TestOwnersDirectoryRuleOwnsDescendants(t *testing.T) {
	f := newFixture(t)
	codeownersFixture(f, "* @default\n**/logs @logs\n")
	f.write("build/logs/error.log", "boom\n")
	f.commit("one")
	assertOwners(t, f.queryPath(QueryOwners, "build/logs/error.log").Data.Target, []string{"@logs"}, ".github/CODEOWNERS")
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
		{"a**b", "axxb", true},
		{"a**b", "a/x/b", false},
		{"**/x", "x", true},
		{"**/x", "a/b/x", true},
		{"x/**", "x/a/b.go", true},
		{"x/**", "y/x/a", false},
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "a/xb", false},
		{"a/**b", "a/xb", true},
		{"a/**b", "a/x/b", false},
		{"a**/b", "a/x/b", false},
		{"a**/b", "ax/b", true},
		{"a/**/x.go", "a/x.go", true},
		{"a/**/x.go", "a/b/c/x.go", true},
		{"a/**/x.go", "b/x.go", false},
		{"docs/", "docs/a.go", true},
		{"docs/", "x/docs/a.go", true},
		{"docs/", "docsx/a.go", false},
		{"logs", "build/logs/error.log", true},
		{"**/logs", "build/logs/error.log", true},
		{"/docs", "docs/a/b.md", true},
		{"logs", "build/logsx/error.log", false},
		{"café/*.go", "café/a.go", true},
		{"café/*.go", "cafe/a.go", false},
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

// TestOwnersAtOldTreeMarksStale proves `--at` answers the requested tree,
// publishes the staleness bit, and warns with both trees, like every other
// --at query.
func TestOwnersAtOldTreeMarksStale(t *testing.T) {
	f := newFixture(t)
	codeownersFixture(f, "* @old\n")
	f.commit("one")
	old := f.run("", "rev-parse", "HEAD^{tree}")
	f.write(".github/CODEOWNERS", "* @new\n")
	f.commit("two")
	head := f.run("", "rev-parse", "HEAD^{tree}")
	req := f.request(QueryOwners, nil)
	req.Path, req.At = "a/a.go", old
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index owners refused: %v", ref)
	}
	assertOwners(t, resp.Data.Target, []string{"@old"}, ".github/CODEOWNERS")
	if !resp.Data.Stale || resp.Data.Tree != old {
		t.Errorf("stale = %t, tree = %s; want true and %s", resp.Data.Stale, resp.Data.Tree, old)
	}
	want := "» index: built for " + old + ", HEAD is " + head
	if !slices.Contains(resp.Warnings, want) {
		t.Errorf("warnings = %q; want one equal to %q", resp.Warnings, want)
	}
	if wire := resp.Data.Wire().MarshalCompact(); !strings.Contains(wire, `"stale":true`) {
		t.Errorf("wire = %s; want the published stale bit", wire)
	}
}

// TestOwnersReadsTheFileWhole proves the CODEOWNERS blob is evaluated in full
// however small the Go parsing cap is: the rule below the cap still owns the
// path, so no cap-driven truncation can silently change an owner.
func TestOwnersReadsTheFileWhole(t *testing.T) {
	f := newFixture(t)
	codeownersFixture(f, "* @everyone\n"+strings.Repeat("docs/* @docs\n", 40)+"a/*.go @go-owner\n")
	f.commit("one")
	req := f.request(QueryOwners, &Limits{FileBytes: 8})
	req.Path = "a/a.go"
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index owners refused: %v", ref)
	}
	assertOwners(t, resp.Data.Target, []string{"@go-owner"}, ".github/CODEOWNERS")
}

// TestTestsForAtOldTreeMarksStale proves `--at` answers the requested tree and
// marks the envelope stale when it is not HEAD's.
func TestTestsForAtOldTreeMarksStale(t *testing.T) {
	f := newFixture(t)
	writeGraphFixture(f)
	f.commit("one")
	f.build()
	old := f.run("", "rev-parse", "HEAD^{tree}")
	f.write("c/c.go", "package c\n\nfunc Local() {}\n\nfunc Added() {}\n")
	f.commit("two")
	head := f.run("", "rev-parse", "HEAD^{tree}")
	req := f.request(QueryTestsFor, nil)
	req.Path, req.At = "a/a.go", old
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index tests-for refused: %v", ref)
	}
	assertTestRows(t, resp.Data.TestRows, []TestRow{
		{Path: "a/a_test.go", Via: viaSamePackage},
		{Path: "b/b_test.go", Via: viaReferences},
		{Path: "c/c_test.go", Via: viaImportsModule},
	})
	if !resp.Data.Stale || resp.Data.Tree != old {
		t.Errorf("stale = %t, tree = %s; want true and %s", resp.Data.Stale, resp.Data.Tree, old)
	}
	want := "» index: built for " + old + ", HEAD is " + head
	if !slices.Contains(resp.Warnings, want) {
		t.Errorf("warnings = %q; want one equal to %q", resp.Warnings, want)
	}
}

// TestOwnerGlobRefusals proves the documented-unsupported shapes never reach
// the matcher, so a rule using them cannot silently change ownership.
func TestOwnerGlobRefusals(t *testing.T) {
	for _, pattern := range []string{"!a/*", `a\*`, "a[bc].go", `\#secret.go`} {
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
	f.build()
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

// TestCoChangedIgnoresPreV2Cache proves a cache written before the schema bump
// is a miss: its partners are discarded and the window is rewritten at the
// current schema.
func TestCoChangedIgnoresPreV2Cache(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	f.build()
	head := f.run("", "rev-parse", "HEAD")
	dir := filepath.Join(f.dir, ".verdict", "index", "cochange")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, head+"-2000.json")
	stale := `{"schema_version":"1","head":"` + head + `","since":"","commits":2000,"changes":[` +
		`{"commit":"` + strings.Repeat("a", 40) + `","paths":["a/target.go","z/bogus.go"]},` +
		`{"commit":"` + strings.Repeat("b", 40) + `","paths":["a/target.go","z/bogus.go"]}]}`
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	resp := f.queryPath(QueryCoChanged, "a/target.go")
	assertCoChangeRows(t, resp.Data.CoChangeRows, []CoChangeRow{
		{Path: "b/partner.go", Commits: 3, Jaccard: 0.75},
		{Path: "c/rare.go", Commits: 2, Jaccard: 0.5},
	})
	rewritten, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rewritten), `"schema_version":"2"`) {
		t.Errorf("cache = %s; want the rewritten schema 2 window", rewritten)
	}
	if strings.Contains(string(rewritten), "z/bogus.go") {
		t.Errorf("cache = %s; want the stale partners gone", rewritten)
	}
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
	f.build()
	var paths []string
	for i := range 3 {
		f.write("a/target.go", "package a\n\nfunc Target() {}\n\nfunc Round"+string(rune('A'+i))+"() {}\n")
		f.commit("round " + string(rune('A'+i)))
		head := f.run("", "rev-parse", "HEAD")
		f.queryPath(QueryCoChanged, "a/target.go")
		stampCache(t, f.dir, head+"-2000.json", int64(1000+i))
		paths = append(paths, head)
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

// stampCache pins one cache file's mtime, so prune's order is fixture data
// rather than the filesystem's clock.
func stampCache(t *testing.T, root, name string, sec int64) {
	t.Helper()
	stamp := time.Unix(sec, 0)
	path := filepath.Join(root, ".verdict", "index", "cochange", name)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
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

// cochangeCacheDirName is the cache directory a fixture looks for, spelled as
// the published layout rather than read from the code under test.
const cochangeCacheDirName = ".verdict/index/cochange"

// TestCoChangedRenameListsBothPaths proves a rename enters the window as its
// old and new path, so the new name co-changes with the old one and with
// everything in that commit.
func TestCoChangedRenameListsBothPaths(t *testing.T) {
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n")
	f.write("old/target.go", "package old\n\nfunc Target() {}\n")
	f.write("partner.go", "package m\n\nfunc Partner() {}\n")
	f.commit("one")
	f.build()
	if err := os.MkdirAll(filepath.Join(f.dir, "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.git("mv", "old/target.go", "new/target.go")
	f.write("partner.go", "package m\n\nfunc Partner() {}\n\nfunc Partner2() {}\n")
	f.commit("rename and touch")
	req := f.request(QueryCoChanged, nil)
	req.Path, req.MinCount = "new/target.go", 1
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index co-changed refused: %v", ref)
	}
	assertCoChangeRows(t, resp.Data.CoChangeRows, []CoChangeRow{
		{Path: "old/target.go", Commits: 1, Jaccard: 0.5},
		{Path: "partner.go", Commits: 1, Jaccard: 0.5},
	})
	if resp.Data.TargetCommits != 1 {
		t.Errorf("target commits = %d; want 1", resp.Data.TargetCommits)
	}
	raw, err := os.ReadFile(filepath.Join(f.dir, cochangeCacheDirName, f.run("", "rev-parse", "HEAD")+"-2000.json"))
	if err != nil {
		t.Fatalf("reading the co-change cache: %v", err)
	}
	for _, want := range []string{`"old/target.go"`, `"new/target.go"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("cache = %s; want it to carry %s", raw, want)
		}
	}
}

// TestCoChangedReadsWithoutWriting proves a query on a checkout that carries no
// index store answers from git and leaves the workspace alone: no .verdict, one
// warning naming why the cache is absent.
func TestCoChangedReadsWithoutWriting(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	resp := f.queryPath(QueryCoChanged, "a/target.go")
	assertCoChangeRows(t, resp.Data.CoChangeRows, []CoChangeRow{
		{Path: "b/partner.go", Commits: 3, Jaccard: 0.75},
		{Path: "c/rare.go", Commits: 2, Jaccard: 0.5},
	})
	if _, err := os.Stat(filepath.Join(f.dir, ".verdict")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("query created %s (stat error %v); want a read to write nothing", filepath.Join(f.dir, ".verdict"), err)
	}
	want := "» index: co-change cache not written: the index store is absent (run verdict index build)"
	if len(resp.Warnings) != 1 || resp.Warnings[0] != want {
		t.Errorf("warnings = %q; want exactly [%q]", resp.Warnings, want)
	}
}

// TestCoChangedReadOnlyStoreStillAnswers proves an unwritable index store costs
// the answer nothing: the rows are computed and one warning names the cache.
func TestCoChangedReadOnlyStoreStillAnswers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a 0555 directory does not deny writes on Windows")
	}
	f := newFixture(t)
	writeHistoryFixture(f)
	f.build()
	indexDir := filepath.Join(f.dir, ".verdict", "index")
	if err := os.Chmod(indexDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(indexDir, 0o755); err != nil {
			t.Errorf("restoring the index store's mode: %v", err)
		}
	})
	resp := f.queryPath(QueryCoChanged, "a/target.go")
	assertCoChangeRows(t, resp.Data.CoChangeRows, []CoChangeRow{
		{Path: "b/partner.go", Commits: 3, Jaccard: 0.75},
		{Path: "c/rare.go", Commits: 2, Jaccard: 0.5},
	})
	if _, err := os.Stat(filepath.Join(f.dir, cochangeCacheDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a read-only store gained %s (stat error %v)", cochangeCacheDirName, err)
	}
	const prefix = "» index: co-change cache not written: "
	if len(resp.Warnings) != 1 || !strings.HasPrefix(resp.Warnings[0], prefix) {
		t.Errorf("warnings = %q; want exactly one starting %q", resp.Warnings, prefix)
	}
}

// seededDecoys fills dir with old JSON files a prune would delete if it
// followed a symlink, and returns dir.
func seededDecoys(t *testing.T, dir string, n int) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range n {
		path := filepath.Join(dir, "decoy-"+strconv.Itoa(i)+".json")
		if err := os.WriteFile(path, []byte(`{"decoy":`+strconv.Itoa(i)+`}`), 0o644); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(int64(i), 0)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// assertDecoysIntact reads every seeded decoy back byte for byte.
func assertDecoysIntact(t *testing.T, dir string, n int) {
	t.Helper()
	for i := range n {
		path := filepath.Join(dir, "decoy-"+strconv.Itoa(i)+".json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("decoy %d: %v", i, err)
		}
		want := `{"decoy":` + strconv.Itoa(i) + `}`
		if string(raw) != want {
			t.Errorf("decoy %d = %q; want %q", i, raw, want)
		}
	}
}

// assertSymlinkedCacheSkip proves the query still answered and named the link
// that kept the cache off disk.
func assertSymlinkedCacheSkip(t *testing.T, resp *Response, reason string) {
	t.Helper()
	assertCoChangeRows(t, resp.Data.CoChangeRows, []CoChangeRow{
		{Path: "b/partner.go", Commits: 3, Jaccard: 0.75},
		{Path: "c/rare.go", Commits: 2, Jaccard: 0.5},
	})
	want := "» index: co-change cache not written: " + reason
	if !slices.Contains(resp.Warnings, want) {
		t.Errorf("warnings = %q; want one equal to %q", resp.Warnings, want)
	}
}

// TestCoChangedSymlinkedCacheDirWritesNothing proves a symlinked cache
// directory earns no write and no prune: the rows still answer, every file in
// the linked directory survives, and one warning names the link.
func TestCoChangedSymlinkedCacheDirWritesNothing(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	f.build()
	outside := seededDecoys(t, t.TempDir(), 3)
	if err := os.Symlink(outside, filepath.Join(f.dir, cochangeCacheDirName)); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	resp := f.queryPath(QueryCoChanged, "a/target.go")
	assertSymlinkedCacheSkip(t, resp, "the co-change cache directory is a symlink")
	assertDecoysIntact(t, outside, 3)
}

// TestCoChangedSymlinkedStoreWritesNothing proves a symlinked `.verdict/index`
// earns no cache write or prune inside the directory it points at.
func TestCoChangedSymlinkedStoreWritesNothing(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	f.build()
	store := filepath.Join(t.TempDir(), "store")
	if err := os.Rename(filepath.Join(f.dir, ".verdict", "index"), store); err != nil {
		t.Fatal(err)
	}
	outside := seededDecoys(t, filepath.Join(store, "cochange"), 3)
	if err := os.Symlink(store, filepath.Join(f.dir, ".verdict", "index")); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	resp := f.queryPath(QueryCoChanged, "a/target.go")
	assertSymlinkedCacheSkip(t, resp, "the index store is a symlink")
	assertDecoysIntact(t, outside, 3)
}

// TestCoChangedSymlinkedCacheFileWritesNothing proves a symlinked cache file is
// left in place and the file it points at is never read as a window or
// overwritten.
func TestCoChangedSymlinkedCacheFileWritesNothing(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	f.build()
	cache := filepath.Join(f.dir, cochangeCacheDirName, f.run("", "rev-parse", "HEAD")+"-2000.json")
	f.queryPath(QueryCoChanged, "a/target.go")
	if err := os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "decoy.json")
	if err := os.WriteFile(outside, []byte("not a window"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, cache); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	resp := f.queryPath(QueryCoChanged, "a/target.go")
	assertSymlinkedCacheSkip(t, resp, "the co-change cache file is a symlink")
	info, err := os.Lstat(cache)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("cache path = %v (%v); want the symlink left in place", info, err)
	}
	if raw, err := os.ReadFile(outside); err != nil || string(raw) != "not a window" {
		t.Errorf("decoy = %q (%v); want it untouched", raw, err)
	}
}

// movingHeadCommander performs act once, when the query reaches an argv the
// move predicate names, so a ref that moves mid-query is visible in what the
// query reads next.
type movingHeadCommander struct {
	act   func()
	move  func(argv []string) bool
	moved bool
}

// Run implements core.Commander.
func (c *movingHeadCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if !c.moved && c.move(cmd.Argv) {
		c.moved = true
		c.act()
	}
	return coreexec.Runner{}.Run(ctx, cmd)
}

// movesOnLog triggers the move when the history read starts.
func movesOnLog(argv []string) bool { return slices.Contains(argv, "log") }

// movesOnTree triggers the move when a tree resolution starts.
func movesOnTree(argv []string) bool {
	return slices.ContainsFunc(argv, func(arg string) bool { return strings.HasSuffix(arg, "^{tree}") })
}

// TestCoChangedPinsTheResolvedCommit proves the window is read from the commit
// the query resolved: a HEAD that moves before git log runs cannot add its
// commits to the window or name the cache file.
func TestCoChangedPinsTheResolvedCommit(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	f.build()
	head := f.run("", "rev-parse", "HEAD")
	req := f.request(QueryCoChanged, nil)
	req.Path = "a/target.go"
	req.Commander = &movingHeadCommander{
		move: movesOnLog,
		act:  func() { f.emptyCommit("moved mid-query") },
	}
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index co-changed refused: %v", ref)
	}
	moved := f.run("", "rev-parse", "HEAD")
	if moved == head {
		t.Fatal("the commander did not move HEAD; the fixture proves nothing")
	}
	if resp.Data.Window.Last != head || resp.Data.Window.Commits != 4 {
		t.Errorf("window = %+v; want the four commits ending at %s", resp.Data.Window, head)
	}
	assertCoChangeRows(t, resp.Data.CoChangeRows, []CoChangeRow{
		{Path: "b/partner.go", Commits: 3, Jaccard: 0.75},
		{Path: "c/rare.go", Commits: 2, Jaccard: 0.5},
	})
	for _, name := range []string{head + "-2000.json", moved + "-2000.json"} {
		_, err := os.Stat(filepath.Join(f.dir, cochangeCacheDirName, name))
		want := name == head+"-2000.json"
		if got := err == nil; got != want {
			t.Errorf("cache %s exists = %t; want %t", name, got, want)
		}
	}
}

// TestCoChangedReadsThePinnedTree proves the tree the answer reads is the
// resolved commit's, not a HEAD re-read that may have moved: a CODEOWNERS rule
// landing after resolution cannot change this answer.
func TestCoChangedReadsThePinnedTree(t *testing.T) {
	f := newFixture(t)
	codeownersFixture(f, "* @old\n")
	f.commit("one")
	f.build()
	req := f.request(QueryCoChanged, nil)
	req.Path = "a/a.go"
	req.Commander = &movingHeadCommander{
		move: movesOnTree,
		act: func() {
			f.write(".github/CODEOWNERS", "* @new\n")
			f.commit("moved mid-query")
		},
	}
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index co-changed refused: %v", ref)
	}
	if f.run("", "show", "HEAD:.github/CODEOWNERS") != "* @new" {
		t.Fatal("the commander did not move HEAD's CODEOWNERS; the fixture proves nothing")
	}
	assertOwners(t, resp.Data.Target, []string{"@old"}, ".github/CODEOWNERS")
}

// TestCoChangedShallowCachesNothing proves a depth-limited window is never
// cached: the cache would survive `git fetch --unshallow` and keep answering
// with the truncated window. After unshallow the query recomputes the full
// history and caches it.
func TestCoChangedShallowCachesNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the file:// clone transport is not portable on Windows")
	}
	f := newFixture(t)
	writeHistoryFixture(f)
	clone := shallowClone(t, f.dir)
	if _, ref := Run(context.Background(), &Request{
		Query:    QueryBuild,
		Runtime:  &core.Runtime{Commander: coreexec.Runner{}, Env: envMap()},
		RepoRoot: clone,
	}); ref != nil {
		t.Fatalf("index build in the shallow clone refused: %v", ref)
	}
	shallow := queryClone(t, clone, "a/target.go", 1)
	if shallow.Data.Window.Commits != 1 {
		t.Fatalf("shallow window commits = %d; want 1", shallow.Data.Window.Commits)
	}
	if _, err := os.Stat(filepath.Join(clone, cochangeCacheDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a shallow query cached %s (stat error %v)", cochangeCacheDirName, err)
	}
	gitIn(t, clone, "fetch", "-q", "--unshallow")
	full := queryClone(t, clone, "a/target.go", 2)
	if full.Data.Window.Commits != 4 {
		t.Fatalf("window commits after unshallow = %d; want 4", full.Data.Window.Commits)
	}
	assertCoChangeRows(t, full.Data.CoChangeRows, []CoChangeRow{
		{Path: "b/partner.go", Commits: 3, Jaccard: 0.75},
		{Path: "c/rare.go", Commits: 2, Jaccard: 0.5},
	})
	entries, err := os.ReadDir(filepath.Join(clone, cochangeCacheDirName))
	if err != nil {
		t.Fatalf("the unshallowed query cached nothing: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("cache files = %d; want the one full window", len(entries))
	}
}

// TestCoChangedNonUTF8PathSkipsCache proves a window holding a path JSON cannot
// round-trip is answered from git's bytes and never cached, so no later hit can
// disagree with the first run.
func TestCoChangedNonUTF8PathSkipsCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a 0xff byte in a path is not representable on Windows")
	}
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n")
	f.write("partner.go", "package m\n\nfunc Partner() {}\n")
	f.commit("one")
	f.build()
	f.write("partner.go", "package m\n\nfunc Partner() {}\n\nfunc Partner2() {}\n")
	f.commitObject("bad\xffname.go", "package m\n\nfunc Bad() {}\n", "a path JSON cannot carry")
	req := f.request(QueryCoChanged, nil)
	req.Path, req.MinCount = "bad\xffname.go", 1
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index co-changed refused: %v", ref)
	}
	assertCoChangeRows(t, resp.Data.CoChangeRows, []CoChangeRow{
		{Path: "partner.go", Commits: 1, Jaccard: 0.5},
	})
	if _, err := os.Stat(filepath.Join(f.dir, cochangeCacheDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a non-UTF-8 window cached %s (stat error %v)", cochangeCacheDirName, err)
	}
	want := "» index: co-change cache not written: a path in the window is not valid UTF-8"
	if !slices.Contains(resp.Warnings, want) {
		t.Errorf("warnings = %q; want one equal to %q", resp.Warnings, want)
	}
}

// queryClone runs one co-changed query in a clone at the given minimum count.
func queryClone(t *testing.T, dir, p string, minCount int) *Response {
	t.Helper()
	req := &Request{
		Query: QueryCoChanged, Path: p, MinCount: minCount,
		Runtime:  &core.Runtime{Commander: coreexec.Runner{}, Env: envMap()},
		RepoRoot: dir,
	}
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index co-changed %s refused: %v", p, ref)
	}
	return resp
}

// gitIn runs one installed-git command in dir and fails on a non-zero exit.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = testenv.Base(nil)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestCoChangedTruncatesPartners proves partner rows truncate with a count,
// never silently.
func TestCoChangedTruncatesPartners(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	req := f.request(QueryCoChanged, nil)
	req.Path, req.Limit = "a/target.go", 1
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index co-changed refused: %v", ref)
	}
	if resp.Data.Partners != 2 || resp.Data.Truncated != 1 || resp.Data.Limit != 1 {
		t.Fatalf("truncated data = partners %d, truncated %d, limit %d; want 2, 1, 1",
			resp.Data.Partners, resp.Data.Truncated, resp.Data.Limit)
	}
	assertCoChangeRows(t, resp.Data.CoChangeRows, []CoChangeRow{
		{Path: "b/partner.go", Commits: 3, Jaccard: 0.75},
	})
	want := "index: showing 1 of 2 partners (1 not shown; raise --limit up to 200)"
	if !slices.Contains(resp.Human, want) {
		t.Errorf("human lines = %q; want one equal to %q", resp.Human, want)
	}
}

// TestCoChangedTieOrder proves partners with the same co-change count sort by
// path, so equal counts never order by map iteration.
func TestCoChangedTieOrder(t *testing.T) {
	f := newFixture(t)
	f.write("t/t.go", "package t\n\nfunc T() {}\n")
	f.write("z/z.go", "package z\n\nfunc Z() {}\n")
	f.write("m/m.go", "package m\n\nfunc M() {}\n")
	f.commit("one")
	f.write("t/t.go", "package t\n\nfunc T() {}\n\nfunc T2() {}\n")
	f.commit("two")
	req := f.request(QueryCoChanged, nil)
	req.Path, req.MinCount = "t/t.go", 1
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index co-changed refused: %v", ref)
	}
	assertCoChangeRows(t, resp.Data.CoChangeRows, []CoChangeRow{
		{Path: "m/m.go", Commits: 1, Jaccard: 0.5},
		{Path: "z/z.go", Commits: 1, Jaccard: 0.5},
	})
}

// TestCoChangedEmptyWindow proves `--since HEAD` reads an empty window as zero
// commits with no bounds, not as an error or a foreign history.
func TestCoChangedEmptyWindow(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	req := f.request(QueryCoChanged, nil)
	req.Path, req.Since = "a/target.go", f.run("", "rev-parse", "HEAD")
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index co-changed refused: %v", ref)
	}
	if resp.Data.Window != (Window{}) || resp.Data.TargetCommits != 0 || resp.Data.Partners != 0 {
		t.Fatalf("empty window = %+v, target commits %d, partners %d; want zeroes",
			resp.Data.Window, resp.Data.TargetCommits, resp.Data.Partners)
	}
	if len(resp.Data.CoChangeRows) != 0 {
		t.Errorf("rows = %+v; want none", resp.Data.CoChangeRows)
	}
	if wire := resp.Data.Wire().MarshalCompact(); !strings.Contains(wire, `"window":{"commits":0}`) {
		t.Errorf("wire = %s; want a four-key window carrying only its zero commits", wire)
	}
	want := `index: no co-changed partners of a/target.go across 0 commit(s) (target in 0)`
	if !slices.Contains(resp.Human, want) {
		t.Errorf("human lines = %q; want one equal to %q", resp.Human, want)
	}
}

// TestCoChangedTargetOutsideWindow proves a window that never touched the
// target answers zero target commits and no partners.
func TestCoChangedTargetOutsideWindow(t *testing.T) {
	f := newFixture(t)
	writeHistoryFixture(f)
	f.write("d/never.go", "package d\n\nfunc D() {}\n\nfunc D2() {}\n")
	f.commit("five")
	req := f.request(QueryCoChanged, nil)
	req.Path, req.Since, req.MinCount = "a/target.go", "HEAD~1", 1
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index co-changed refused: %v", ref)
	}
	if resp.Data.Window.Commits != 1 || resp.Data.TargetCommits != 0 || resp.Data.Partners != 0 {
		t.Fatalf("window = %+v, target commits %d, partners %d; want one commit touching neither",
			resp.Data.Window, resp.Data.TargetCommits, resp.Data.Partners)
	}
	assertCoChangeRows(t, resp.Data.CoChangeRows, nil)
}

// TestCoChangedNewlinePath proves a partner whose name carries a newline is one
// row with that literal name, not two rows or a broken record.
func TestCoChangedNewlinePath(t *testing.T) {
	f := newFixture(t)
	f.write("t/t.go", "package t\n\nfunc T() {}\n")
	f.commit("one")
	f.write("t/t.go", "package t\n\nfunc T() {}\n\nfunc T2() {}\n")
	f.commitObject("weird\nname.go", "package w\n\nfunc W() {}\n", "two")
	req := f.request(QueryCoChanged, nil)
	req.Path, req.MinCount = "t/t.go", 1
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index co-changed refused: %v", ref)
	}
	assertCoChangeRows(t, resp.Data.CoChangeRows, []CoChangeRow{
		{Path: "weird\nname.go", Commits: 1, Jaccard: 0.5},
	})
}
