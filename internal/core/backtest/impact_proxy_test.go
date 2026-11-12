package backtest

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	indexcore "github.com/stainless-code/verdict/internal/core/index"
)

// updateImpactProxy republishes the proxy table in docs/benchmark.md.
var updateImpactProxy = flag.Bool("update-impact-proxy", false, "republish the impact-proxy table in docs/benchmark.md")

// impactProxy is one measured ground-truth finding: the path the miner blamed,
// its language, whether the impact section names that path, and whether any row
// names a path the diff itself does not.
type impactProxy struct {
	Case     string
	Language string
	Path     string
	Named    bool
	Beyond   bool
}

// impactProxyDoc is the published block: a per-language tally of the findings
// the impact section names, pinned cell by cell like the corpus scorecard.
type impactProxyDoc struct {
	Language string
	Bugs     int
	Named    int
	Beyond   int
}

// The published table's markers and literals: docs/benchmark.md republishes
// the measured rows between them, and the test fails when the two differ.
const (
	impactProxyBegin     = "<!-- impact-proxy:begin -->"
	impactProxyEnd       = "<!-- impact-proxy:end -->"
	impactProxyHeader    = "| language | bugs | named | not named | beyond the diff |"
	impactProxySeparator = "| -------- | ---- | ----- | --------- | --------------- |"
	impactProxyRow       = "| %-8s | %-4d | %-5d | %-9d | %-15d |\n"
	benchmarkDocs        = "docs/benchmark.md"
)

// TestImpactProxy measures the recall delta over the backtest's own ground
// truth: every finding its fixtures and seeds blame is checked against the
// impact section the same diff produces, per language.
func TestImpactProxy(t *testing.T) {
	rows := measureImpactProxy(t)
	tally := tallyImpactProxy(rows)
	assertImpactProxyDocs(t, tally)
	for _, row := range tally {
		t.Logf("impact proxy: %s %d/%d named, %d beyond the diff", row.Language, row.Named, row.Bugs, row.Beyond)
	}
}

// measureImpactProxy runs every ground-truth case: the miner names the bugs,
// the index answers the same diff, and the section decides which paths it
// names.
func measureImpactProxy(t *testing.T) []impactProxy {
	t.Helper()
	var out []impactProxy
	for _, c := range impactProxyCases() {
		t.Run(c.name, func(t *testing.T) {
			out = append(out, measureSeed(t, c)...)
		})
	}
	return out
}

// measureSeed measures one fixture family: the miner names its bugs, and each
// candidate's diff is answered by an index built at its head.
func measureSeed(t *testing.T, c proxyCase) []impactProxy {
	t.Helper()
	seed := c.build(t)
	data := runMine(t, seed.r, seed.since)
	if len(data.Mine.Candidates) == 0 {
		t.Fatal("the miner produced no candidate; the proxy measures nothing")
	}
	var out []impactProxy
	for i := range data.Mine.Candidates {
		cand := &data.Mine.Candidates[i]
		assertSeedCandidate(t, cand, seed)
		out = append(out, measureCandidate(t, seed.r, cand)...)
	}
	return out
}

// assertSeedCandidate anchors the ground truth to the fixture: every blamed
// line names the fixture's own fix, and a fixture that pins its PR head is
// reconstructed at exactly that commit.
func assertSeedCandidate(t *testing.T, cand *Candidate, seed proxySeed) {
	t.Helper()
	for _, bug := range cand.Bugs {
		if bug.Fix != seed.fix {
			t.Errorf("mined bug %s:%d blames fix %s; want the fixture's fix %s", bug.Path, bug.Line, bug.Fix, seed.fix)
		}
	}
	if seed.head != "" && cand.Head != seed.head {
		t.Errorf("mined head = %s; want the fixture's PR head %s", cand.Head, seed.head)
	}
}

// TestImpactProxyCallerReach pins the two cross-package shapes: the hand-off
// names the caller file for a qualified package call and cannot for a method
// call, which is the corpus's method-caller gap showing up as lost reach rather
// than as a missed ground-truth path.
func TestImpactProxyCallerReach(t *testing.T) {
	for _, c := range []struct {
		name  string
		kind  crossPackageKind
		named bool
	}{
		{"package-call", crossPackageCall, true},
		{"method-call", crossPackageMethod, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			assertCallerReach(t, c.kind, c.named)
		})
	}
}

// assertCallerReach requires one cross-package shape to name (or not name) the
// caller file the diff never touches.
func assertCallerReach(t *testing.T, kind crossPackageKind, named bool) {
	t.Helper()
	r, since, _ := newCrossPackageRepo(t, kind)
	data := runMine(t, r, since)
	if len(data.Mine.Candidates) != 1 {
		t.Fatalf("candidates = %d; want 1", len(data.Mine.Candidates))
	}
	paths := impactPaths(impactFor(t, r, &data.Mine.Candidates[0]).Data.Blast)
	if got := slices.Contains(paths, "internal/api/api.go"); got != named {
		t.Errorf("caller file named = %v; want %v (paths: %v)", got, named, paths)
	}
}

// measureCandidate measures one mined candidate: its diff, the index built at
// its head, and one row per blamed path.
func measureCandidate(t *testing.T, r *repo, cand *Candidate) []impactProxy {
	t.Helper()
	impact := impactFor(t, r, cand)
	changed := impact.Data.Blast.Input.Paths
	named := impactPaths(impact.Data.Blast)
	var out []impactProxy
	for _, bug := range cand.Bugs {
		out = append(out, impactProxy{
			Case:     bug.Path,
			Language: impactLanguage(bug.Path),
			Path:     bug.Path,
			Named:    slices.Contains(named, bug.Path),
			Beyond:   slices.ContainsFunc(named, func(p string) bool { return !slices.Contains(changed, p) }),
		})
	}
	return out
}

// impactFor builds the candidate's head index and answers its diff, the same
// hand-off the fetch writes into a bundle's impact.json.
func impactFor(t *testing.T, r *repo, cand *Candidate) *indexcore.Impact {
	t.Helper()
	diff := r.git("diff", cand.Base, cand.Head)
	tree := strings.TrimSpace(r.git("rev-parse", cand.Head+"^{tree}"))
	rt := &core.Runtime{Commander: coreexec.Runner{MissingTool: true}, Env: userEnv(t, nil)}
	if _, ref := indexcore.Run(context.Background(), &indexcore.Request{
		Query: indexcore.QueryBuild, Runtime: rt, RepoRoot: r.dir, At: cand.Head,
	}); ref != nil {
		t.Fatalf("index build refused: %s: %s", ref.Code, ref.Message)
	}
	impact, ref := indexcore.ImpactOf(context.Background(), &indexcore.ImpactRequest{
		Runtime: rt, RepoRoot: r.dir, Index: tree, Diff: diff, Head: cand.Head,
	})
	if ref != nil {
		t.Fatalf("impact refused: %s: %s", ref.Code, ref.Message)
	}
	return impact
}

// impactPaths lists every path the section renders, in row order.
func impactPaths(b *indexcore.Blast) []string {
	var out []string
	for _, row := range b.Symbols {
		out = appendUnique(out, row.Path)
	}
	for _, row := range b.Callers {
		out = appendUnique(out, row.Path)
	}
	for _, row := range b.Tests {
		out = appendUnique(out, row.Path)
	}
	return out
}

// appendUnique appends one path the list does not carry yet.
func appendUnique(out []string, p string) []string {
	if slices.Contains(out, p) {
		return out
	}
	return append(out, p)
}

// impactLanguage classifies a blamed path by its extension, so the split names
// the languages the index reads and the files it cannot.
func impactLanguage(p string) string {
	switch ext := path.Ext(p); ext {
	case ".go":
		return "go"
	case ".ts", ".tsx", ".js":
		return "ts"
	case ".py":
		return "py"
	case ".txt":
		return "text"
	default:
		return strings.TrimPrefix(ext, ".")
	}
}

// tallyImpactProxy folds the measured findings into one row per language, in
// first-seen language order so the table is deterministic.
func tallyImpactProxy(rows []impactProxy) []impactProxyDoc {
	var out []impactProxyDoc
	for _, row := range rows {
		out = tallyRow(out, row)
	}
	return out
}

// tallyRow adds one measured finding to its language's row, opening the row on
// first sight so the table keeps the languages in fixture order.
func tallyRow(out []impactProxyDoc, row impactProxy) []impactProxyDoc {
	at := slices.IndexFunc(out, func(d impactProxyDoc) bool { return d.Language == row.Language })
	if at < 0 {
		out = append(out, impactProxyDoc{Language: row.Language})
		at = len(out) - 1
	}
	out[at].Bugs++
	if row.Named {
		out[at].Named++
	}
	if row.Beyond {
		out[at].Beyond++
	}
	return out
}

// proxySeed is one fixture family's starting point: the repository, the seed
// ref the miner starts at, the fix commit that later rewrites the blamed line,
// and the PR head the miner must reconstruct when the fixture pins one.
type proxySeed struct {
	r     *repo
	since string
	fix   string
	head  string
}

// proxyCase is one ground-truth fixture family: a name for the subtest and the
// builder that lays its seed out.
type proxyCase struct {
	build func(t *testing.T) proxySeed
	name  string
}

// impactProxyCases are the fixture families the proxy measures: the miner reads
// them like any other backtest seed, so the ground truth is the backtest's own,
// not a second hand-written list.
func impactProxyCases() []proxyCase {
	return []proxyCase{
		{name: "go-merged-fix", build: func(t *testing.T) proxySeed {
			t.Helper()
			b, fix := newMergedFixRepo(t)
			return proxySeed{r: b.r, since: b.base, fix: fix}
		}},
		{name: "go-revert", build: func(t *testing.T) proxySeed {
			t.Helper()
			b, fix := newRevertRepo(t)
			return proxySeed{r: b.r, since: b.base, fix: fix}
		}},
		{name: "go-package-call", build: func(t *testing.T) proxySeed {
			t.Helper()
			r, base, fix := newCrossPackageRepo(t, crossPackageCall)
			return proxySeed{r: r, since: base, fix: fix}
		}},
		{name: "go-method-call", build: func(t *testing.T) proxySeed {
			t.Helper()
			r, base, fix := newCrossPackageRepo(t, crossPackageMethod)
			return proxySeed{r: r, since: base, fix: fix}
		}},
		{name: "text-squash", build: func(t *testing.T) proxySeed {
			t.Helper()
			r, base, squash, fix := newSquashRepo(t)
			return proxySeed{r: r, since: base, fix: fix, head: squash}
		}},
		{name: "text-merged", build: func(t *testing.T) proxySeed {
			t.Helper()
			r, base, fix := newTextMergedRepo(t)
			return proxySeed{r: r, since: base, fix: fix}
		}},
		{name: "ts-save", build: func(t *testing.T) proxySeed {
			t.Helper()
			r, base, fix := newLangRepo(t, langTS)
			return proxySeed{r: r, since: base, fix: fix}
		}},
		{name: "py-save", build: func(t *testing.T) proxySeed {
			t.Helper()
			r, base, fix := newLangRepo(t, langPy)
			return proxySeed{r: r, since: base, fix: fix}
		}},
	}
}

// crossPackageKind selects the caller shape the cross-package fixture carries:
// a qualified package call the lexical index attributes, or a method call on a
// value it cannot.
type crossPackageKind int

const (
	crossPackageCall crossPackageKind = iota
	crossPackageMethod
)

// newCrossPackageRepo builds a merged PR that edits one declaration in
// internal/store with a caller in internal/api, plus a later fix that removes
// the blamed line so the miner blames it. The two shapes are the point of the
// pair: a qualified package function call is attributed, and a method call on a
// value is not.
func newCrossPackageRepo(t *testing.T, kind crossPackageKind) (*repo, string, string) {
	t.Helper()
	r := newRepo(t)
	r.write("go.mod", "module example.com/m\n\ngo 1.27\n")
	r.write("internal/store/store.go", crossPackageDecl(kind, false))
	r.write("internal/api/api.go", crossPackageCaller(kind))
	base := r.commit("feat: base")
	r.switchNew("feature")
	r.write("internal/store/store.go", crossPackageDecl(kind, true))
	r.commit("feat: announce the save")
	r.switchMain()
	r.merge("feature", "Merge pull request #3 from acme/feature")
	r.write("internal/store/store.go", crossPackageDecl(kind, false))
	fix := r.commit("fix: drop the announcement")
	return r, base, fix
}

// crossPackageDecl renders the declaring file for one cross-package shape; with
// bug set it carries the line the later fix removes.
func crossPackageDecl(kind crossPackageKind, bug bool) string {
	if kind == crossPackageMethod {
		body := "package store\n\ntype Store struct{}\n\nfunc (s *Store) Save(n int) {\n\t_ = n\n}\n"
		if bug {
			return "package store\n\ntype Store struct{}\n\nfunc (s *Store) Save(n int) {\n\t_ = n\n\tprintln(\"save\")\n}\n"
		}
		return body
	}
	if bug {
		return "package store\n\nfunc Save(n int) {\n\t_ = n\n\tprintln(\"save\")\n}\n"
	}
	return "package store\n\nfunc Save(n int) {\n\t_ = n\n}\n"
}

// crossPackageCaller renders the caller file for one cross-package shape.
func crossPackageCaller(kind crossPackageKind) string {
	if kind == crossPackageMethod {
		return "package api\n\nimport \"example.com/m/internal/store\"\n\nfunc Run(s *store.Store) {\n\ts.Save(1)\n}\n"
	}
	return "package api\n\nimport \"example.com/m/internal/store\"\n\nfunc Run() {\n\tstore.Save(1)\n}\n"
}

// newTextMergedRepo builds a merged PR that edits a file the index does not
// read, plus a later fix that rewrites the blamed line: a changed path with no
// declaration is the one ground-truth shape the hand-off cannot name.
func newTextMergedRepo(t *testing.T) (*repo, string, string) {
	t.Helper()
	r := newRepo(t)
	r.write("notes.txt", "one\n")
	base := r.commit("feat: base")
	r.switchNew("feature")
	r.write("notes.txt", "bug\n")
	r.commit("feat: introduce bug")
	r.switchMain()
	r.merge("feature", "Merge pull request #6 from acme/feature")
	r.write("notes.txt", "fixed\n")
	fix := r.commit("fix: repair the notes")
	return r, base, fix
}

// langKind selects the scanned-language fixture's source.
type langKind int

const (
	langTS langKind = iota
	langPy
)

// newLangRepo builds a merged PR whose blamed line sits in a scanned-language
// declaration, with a test beside it, and a later fix that rewrites the line.
func newLangRepo(t *testing.T, kind langKind) (*repo, string, string) {
	t.Helper()
	r := newRepo(t)
	files := langFiles(kind)
	for name, body := range files.base {
		r.write(name, body)
	}
	base := r.commit("feat: base")
	r.switchNew("feature")
	for name, body := range files.feature {
		r.write(name, body)
	}
	r.commit("feat: change the body")
	r.switchMain()
	r.merge("feature", "Merge pull request #4 from acme/feature")
	for name, body := range files.base {
		r.write(name, body)
	}
	fix := r.commit("fix: restore the body")
	return r, base, fix
}

// langBodies carries one scanned-language fixture: the base tree, the changed
// files, and the declaration the blame lands in.
type langBodies struct {
	base, feature map[string]string
}

// langFiles renders one scanned-language fixture.
func langFiles(kind langKind) langBodies {
	if kind == langPy {
		return langBodies{
			base: map[string]string{
				"lib/views.py":        "def route(value):\n    return value\n",
				"lib/use.py":          "from lib.views import route\n\n\ndef handle(value):\n    return route(value)\n",
				"tests/test_views.py": "from lib.views import route\n\n\ndef test_route():\n    assert route(1) == 1\n",
			},
			feature: map[string]string{
				"lib/views.py": "def route(value):\n    return value + 1\n",
			},
		}
	}
	return langBodies{
		base: map[string]string{
			"src/lib.ts":           "export function save(value: number): number {\n  return value\n}\n",
			"src/app/use.ts":       "import { save } from \"../lib\"\n\nexport function run(value: number): number {\n  return save(value)\n}\n",
			"src/app/save.spec.ts": "import { save } from \"../lib\"\n\ntest(\"save\", () => {\n  expect(save(1)).toBe(1)\n})\n",
		},
		feature: map[string]string{
			"src/lib.ts": "export function save(value: number): number {\n  return value + 1\n}\n",
		},
	}
}

// assertImpactProxyDocs fails when the table published in docs/benchmark.md is
// not the measured one, and under -update republishes it instead.
func assertImpactProxyDocs(t *testing.T, rows []impactProxyDoc) {
	t.Helper()
	p := benchmarkPath(t)
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", benchmarkDocs, err)
	}
	published := impactProxyBlock(t, string(body))
	measured := renderImpactProxy(rows)
	if published == measured {
		return
	}
	if *updateImpactProxy {
		republishImpactProxy(t, p, string(body), measured)
		return
	}
	for _, line := range impactProxyDiff(published, measured) {
		t.Errorf("%s: %s", benchmarkDocs, line)
	}
}

// republishImpactProxy rewrites the published table and logs the lines that
// moved, so a re-pin is reviewed like code.
func republishImpactProxy(t *testing.T, p, body, table string) {
	t.Helper()
	beg, end := impactProxySpan(t, body)
	updated := body[:beg+len(impactProxyBegin)] + table + body[end:]
	if err := os.WriteFile(p, []byte(updated), 0o644); err != nil {
		t.Fatalf("write %s: %v", benchmarkDocs, err)
	}
	for _, line := range impactProxyDiff(impactProxyBlock(t, body), table) {
		t.Logf("%s: %s", benchmarkDocs, line)
	}
}

// renderImpactProxy renders the measured rows as the published markdown block,
// carrying the blank line the markdown formatter keeps after the begin marker,
// so a republish is byte-identical to the document the formatter leaves.
func renderImpactProxy(rows []impactProxyDoc) string {
	var out strings.Builder
	out.WriteString("\n\n" + impactProxyHeader + "\n")
	out.WriteString(impactProxySeparator + "\n")
	for _, r := range rows {
		fmt.Fprintf(&out, impactProxyRow, r.Language, r.Bugs, r.Named, r.Bugs-r.Named, r.Beyond)
	}
	return out.String() + "\n"
}

// impactProxyBlock returns the published lines between the proxy markers.
func impactProxyBlock(t *testing.T, body string) string {
	t.Helper()
	beg, end := impactProxySpan(t, body)
	return body[beg+len(impactProxyBegin) : end]
}

// impactProxySpan returns the offsets of the two proxy markers.
func impactProxySpan(t *testing.T, body string) (int, int) {
	t.Helper()
	beg := strings.Index(body, impactProxyBegin)
	end := strings.Index(body, impactProxyEnd)
	if beg < 0 || end < beg {
		t.Fatalf("%s: wants %s and %s around the published table", benchmarkDocs, impactProxyBegin, impactProxyEnd)
	}
	return beg, end
}

// impactProxyDiff renders the lines two proxy tables disagree on, paired by
// position so a moved row is visible instead of canceling out.
func impactProxyDiff(published, measured string) []string {
	old, fresh := impactProxyLines(published), impactProxyLines(measured)
	var out []string
	for i := range max(len(old), len(fresh)) {
		before, after := lineOrEmpty(old, i), lineOrEmpty(fresh, i)
		switch {
		case before == after:
		case after == "":
			out = append(out, "- "+before)
		case before == "":
			out = append(out, "+ "+after)
		default:
			out = append(out, "- "+before, "+ "+after)
		}
	}
	return out
}

// impactProxyLines splits a rendered block, dropping the empty lines a
// trailing newline and the formatter's blank separator leave, so a blank line
// cannot shift every row beneath it into a false pair.
func impactProxyLines(body string) []string {
	var out []string
	for line := range strings.SplitSeq(body, "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// lineOrEmpty returns one line of a rendered block, or "" past its end.
func lineOrEmpty(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

// benchmarkPath resolves docs/benchmark.md from this package's test directory.
func benchmarkPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return filepath.Join(dir, benchmarkDocs)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("%s: no go.mod above the test directory", benchmarkDocs)
		}
		dir = parent
	}
}
