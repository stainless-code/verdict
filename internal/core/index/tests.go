package indexcore

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// The four mechanisms a candidate test can reach a source file through.
const (
	viaSamePackage      = "same-package"
	viaReferences       = "references"
	viaImportsModule    = "imports-module"
	viaTransitiveImport = "transitive-import"
)

// minTransitiveDepth is the first hop count that reads as transitive: a test
// one hop out imports the declaring package itself, which the direct
// mechanisms already name.
const minTransitiveDepth = 2

// testsDepthCapped names the candidates the depth cap hid.
const testsDepthCapped = "index: depth cap %d reached (%d candidate test(s) one import hop beyond; raise --depth up to %d)"

// testPlan is the resolved tests-for input: the declaring language and the
// queried path, the package directories candidates can sit in, one resolution
// target per declared symbol, and the import-hop cap.
type testPlan struct {
	lang    language
	path    string
	dirs    []string
	targets []target
	depth   int
}

// testsFor executes `index tests-for`: candidate tests to read, never a
// coverage claim.
func (st *state) testsFor(ctx context.Context) (*Response, *core.RefusalError) {
	limit, ref := st.rowLimit(QueryTestsFor)
	if ref != nil {
		return nil, ref
	}
	depth, ref := st.depthLimit()
	if ref != nil {
		return nil, ref
	}
	if (st.req.Path == "") == (st.req.Symbol == "") {
		return nil, &core.RefusalError{
			Code: codeBadFlags, Message: "index tests-for wants one path or --symbol pkg.Name",
		}
	}
	plan, t, commit, stale, entries, ref := st.testTargets(ctx)
	if ref != nil {
		return nil, ref
	}
	plan.depth = depth
	var target *Target
	if plan.path != "" {
		target, ref = st.ownersAt(ctx, st.sourceRoot(), entries, plan.path)
		if ref != nil {
			return nil, ref
		}
	}
	return st.testsForResponse(t, commit, stale, plan, target, walkTestRows(t, plan, newImportGraph(t)), limit), nil
}

// testTargets loads the tree, its entries, and the candidate plan in one step.
func (st *state) testTargets(
	ctx context.Context,
) (*testPlan, *treeIndex, string, bool, []treeEntry, *core.RefusalError) {
	r := st.sourceRoot()
	st.trust = st.readTrust(ctx, r)
	tree, commit, ref := st.queryRef(ctx, r)
	if ref != nil {
		return nil, nil, "", false, nil, ref
	}
	t, ref := readTree(storePath(r), tree)
	if ref != nil {
		return nil, nil, "", false, nil, ref
	}
	stale := st.warnStale(ctx, r, tree)
	entries, ref := st.lsTree(ctx, r, tree)
	if ref != nil {
		return nil, nil, "", false, nil, ref
	}
	plan, ref := resolveTestPlan(packageMap(t), t, st.req.Path, st.req.Symbol)
	if ref != nil {
		return nil, nil, "", false, nil, ref
	}
	return plan, t, commit, stale, entries, nil
}

// resolveTestPlan resolves the path or symbol form to its candidate target.
func resolveTestPlan(
	pkgs map[string]*packageInfo, t *treeIndex, rawPath, symbol string,
) (*testPlan, *core.RefusalError) {
	if rawPath != "" {
		return pathTestPlan(pkgs, t, rawPath)
	}
	return symbolTestPlan(pkgs, symbol)
}

// pathTestPlan resolves a queried source file to its package and declarations.
func pathTestPlan(pkgs map[string]*packageInfo, t *treeIndex, rawPath string) (*testPlan, *core.RefusalError) {
	p := normalizePath(rawPath)
	f := findIndexedFile(t, p)
	if f == nil || f.Oversize || f.Error != "" {
		return nil, &core.RefusalError{
			Code:    codeBadFlags,
			Message: "index tests-for: " + p + " is not an indexed source file at " + t.Tree,
		}
	}
	pkg := pkgs[path.Dir(p)]
	if pkg == nil {
		return nil, &core.RefusalError{
			Code:    codeBadFlags,
			Message: "index tests-for: " + p + " is not an indexed source file at " + t.Tree,
		}
	}
	plan := &testPlan{path: p, dirs: []string{pkg.dir}, lang: fileLanguage(f)}
	for _, d := range f.Decls {
		plan.targets = append(plan.targets, target{pkg: pkg, name: d.Name, lang: plan.lang, exported: d.Exported})
	}
	return plan, nil
}

// symbolTestPlan resolves a qualified symbol to every package declaring it.
func symbolTestPlan(pkgs map[string]*packageInfo, symbol string) (*testPlan, *core.RefusalError) {
	symbol = strings.TrimSpace(symbol)
	qualifier, _ := splitSymbol(symbol)
	if qualifier == "" {
		return nil, &core.RefusalError{
			Code: codeBadFlags, Message: "index tests-for wants a qualified --symbol (pkg.Name)",
		}
	}
	targets, ref := resolveTargets(pkgs, symbol)
	if ref != nil {
		return nil, ref
	}
	if len(targets) == 0 {
		return nil, &core.RefusalError{
			Code: codeBadFlags, Message: "index tests-for: no declaration of " + symbol,
		}
	}
	var dirs []string
	for _, tgt := range targets {
		if !slices.Contains(dirs, tgt.pkg.dir) {
			dirs = append(dirs, tgt.pkg.dir)
		}
	}
	slices.Sort(dirs)
	return &testPlan{dirs: dirs, lang: targets[0].lang, targets: targets}, nil
}

// findIndexedFile finds one indexed file by its slash path.
func findIndexedFile(t *treeIndex, p string) *treeFile {
	if indexedLanguage(p) == "" {
		return nil
	}
	for i := range t.Files {
		if t.Files[i].Path == p {
			return &t.Files[i]
		}
	}
	return nil
}

// testWalk is one candidate walk: the rows, and the candidate files one import
// hop past the depth cap that it counted instead of naming.
type testWalk struct {
	rows   []TestRow
	hidden []string
}

// walkTestRows walks the tree's test files once, labeling each candidate with
// the strongest mechanism that reaches the target and the import hops its own
// imports took. The reverse walk runs one hop past the cap so a candidate's own
// chain is readable at the depth that is counted rather than named.
func walkTestRows(t *treeIndex, plan *testPlan, g *importGraph) testWalk {
	dirs, importPaths := testMechanisms(t, plan)
	depths := g.reverseDepth(planNodes(plan), plan.depth+1)
	var walk testWalk
	for i := range t.Files {
		if f := &t.Files[i]; isCandidateFile(plan, f) {
			walk.add(plan, dirs, importPaths, g, depths, f)
		}
	}
	slices.SortFunc(walk.rows, func(a, b TestRow) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		return strings.Compare(a.Via, b.Via)
	})
	return walk
}

// add records one candidate file: the row it is named on, the hidden path it is
// counted on when a transitive chain lands one hop past the cap, or nothing
// when no mechanism reaches it or its chain lands further out than that.
func (w *testWalk) add(plan *testPlan, dirs, importPaths map[string]bool, g *importGraph, depths map[graphNode]int, f *treeFile) {
	via, depth := testVia(plan, dirs, importPaths, g, depths, f)
	switch {
	case via == "":
	case via == viaTransitiveImport && depth > plan.depth:
		if depth == plan.depth+1 {
			w.hidden = append(w.hidden, f.Path)
		}
	default:
		w.rows = append(w.rows, TestRow{Path: f.Path, Via: via, Depth: depth})
	}
}

// planNodes names the plan's directories in its own language family, where the
// reverse walk starts.
func planNodes(plan *testPlan) []graphNode {
	nodes := make([]graphNode, 0, len(plan.dirs))
	for _, dir := range plan.dirs {
		nodes = append(nodes, graphNode{family: plan.lang.family(), dir: dir})
	}
	return nodes
}

// testMechanisms indexes the plan's package directories and import paths, each
// rendered in the plan's own language.
func testMechanisms(t *treeIndex, plan *testPlan) (map[string]bool, map[string]bool) {
	dirs := make(map[string]bool, len(plan.dirs))
	importPaths := make(map[string]bool, len(plan.dirs))
	for _, dir := range plan.dirs {
		dirs[dir] = true
		importPaths[packageImportPath(plan.lang, t.Module, dir)] = true
	}
	return dirs, importPaths
}

// packageImportPath renders one declaring directory's import path in the plan's
// language: a Go package path joined onto the module, or the directory itself
// for a scanned language, whose imports never carry a Go module path.
func packageImportPath(lang language, module, dir string) string {
	if lang.family() == langGo {
		return importPathOf(module, dir)
	}
	return dir
}

// importsTargetPath reports whether one scanned file imports the queried file
// itself, by a specifier the scanner resolves against its own directory. A
// Python `from lib import mod` names the module and the submodule it takes from
// it, so the imported name is joined on: that statement is how a test outside
// the module's directory imports the module.
func importsTargetPath(plan *testPlan, f *treeFile) bool {
	lang := fileLanguage(f)
	if plan.path == "" || lang == langGo {
		return false
	}
	want := trimSourceExt(plan.path)
	for _, imp := range f.Imports {
		if importNamesPath(lang, path.Dir(f.Path), imp, want) {
			return true
		}
	}
	return false
}

// importNamesPath reports whether one recorded import resolves to the wanted
// path: the module itself, or for Python the submodule a `from … import …`
// statement takes out of that module.
func importNamesPath(lang language, dir string, imp importEntry, want string) bool {
	module := scannedSpecifier(lang, dir, imp.Path)
	switch {
	case module == "":
		return false
	case trimSourceExt(module) == want:
		return true
	case lang.family() == langPython:
		return trimSourceExt(path.Join(module, imp.Name)) == want
	default:
		return false
	}
}

// scannedSpecifier resolves one module specifier to a repository path as the
// scanner can read it, or "" when the format offers no rule.
func scannedSpecifier(lang language, dir, spec string) string {
	switch lang.family() {
	case langTypeScript:
		if !strings.HasPrefix(spec, ".") {
			return ""
		}
		return path.Join(dir, spec)
	case langPython:
		return pythonSpecifier(dir, spec)
	default:
		return ""
	}
}

// pythonSpecifier resolves one Python import path to a repository path: a
// relative one against the importing file's package, an absolute one against
// the repository root.
func pythonSpecifier(dir, spec string) string {
	dots := 0
	for dots < len(spec) && spec[dots] == '.' {
		dots++
	}
	if dots == 0 {
		return strings.ReplaceAll(spec, ".", "/")
	}
	for i := 1; i < dots; i++ {
		dir = path.Dir(dir)
	}
	return path.Join(dir, strings.ReplaceAll(spec[dots:], ".", "/"))
}

// trimSourceExt drops one trailing source extension, and nothing else.
func trimSourceExt(p string) string {
	if ext := path.Ext(p); ext != "" && indexedLanguage(p) != "" {
		return strings.TrimSuffix(p, ext)
	}
	return p
}

// isCandidateFile reports whether one indexed file can be a candidate test
// row at all: a readable test file in the plan's language family that is not
// the queried file itself. Every mechanism that reaches a candidate passes
// here first, so no edge can list a test from another family.
func isCandidateFile(plan *testPlan, f *treeFile) bool {
	lang := fileLanguage(f)
	return testPath(lang, f.Path) && plan.lang.sameFamily(lang) &&
		!f.Oversize && f.Error == "" && f.Path != plan.path
}

// testVia classifies one test file and counts the import hops it took: same
// package directory, a recorded reference to a declared symbol, an import of
// the declaring package, or a package that reaches the declaring package
// through imports of its own.
func testVia(
	plan *testPlan, dirs, importPaths map[string]bool, g *importGraph, depths map[graphNode]int, f *treeFile,
) (string, int) {
	switch {
	case samePackageTest(dirs, f):
		return viaSamePackage, 0
	case referencesTargets(plan.targets, f), importsTargetPath(plan, f):
		return viaReferences, 1
	case importsAny(f, importPaths):
		return viaImportsModule, 1
	default:
		return transitiveVia(g, depths, f)
	}
}

// transitiveVia labels one file reached through its own package's imports, or
// nothing: a file one hop out imports the declaring package itself, and the
// direct mechanisms name that edge first.
func transitiveVia(g *importGraph, depths map[graphNode]int, f *treeFile) (string, int) {
	if depth := g.candidateDepth(depths, f); depth >= minTransitiveDepth {
		return viaTransitiveImport, depth
	}
	return "", 0
}

// samePackageTest reports whether one candidate is a test in a declaring
// directory. The language family is already settled by isCandidateFile: a `.py`
// test says nothing about the Go file beside it, while a JavaScript test can
// cover a TypeScript declaration.
func samePackageTest(dirs map[string]bool, f *treeFile) bool {
	return dirs[path.Dir(f.Path)]
}

// referencesTargets reports whether the file carries a reference the index can
// attribute to one of the plan's declarations.
func referencesTargets(targets []target, f *treeFile) bool {
	same := path.Dir(f.Path)
	for _, tgt := range targets {
		if referencesOne(tgt, same == tgt.pkg.dir, f) {
			return true
		}
	}
	return false
}

// referencesOne reports whether the file carries one attributable reference to
// a declaration.
func referencesOne(tgt target, samePackage bool, f *treeFile) bool {
	for _, r := range f.Refs {
		if r.Name != tgt.name {
			continue
		}
		row, matched := resolveRef(tgt, samePackage, f, r)
		if matched && row.Resolution != resolutionUnresolved {
			return true
		}
	}
	return false
}

// importsAny reports whether the file imports one of the plan's packages,
// resolving each specifier by its own language's rule: a Go import is the
// package path itself, while a scanned one names the package only once joined
// onto the importing file's directory.
func importsAny(f *treeFile, importPaths map[string]bool) bool {
	lang := fileLanguage(f)
	dir := path.Dir(f.Path)
	for _, imp := range f.Imports {
		if importPaths[importedPackage(lang, dir, imp)] {
			return true
		}
	}
	return false
}

// importedPackage resolves one import entry to the package path its own
// language names: a Go import path verbatim, or a scanned specifier joined onto
// the importing file's directory. A specifier naming a module inside the
// declaring package is not this mechanism: the file-level reference covers it.
func importedPackage(lang language, dir string, imp importEntry) string {
	if lang.family() == langGo {
		return imp.Path
	}
	return scannedSpecifier(lang, dir, imp.Path)
}

// testsForResponse renders the candidate list, truncating rows with a count.
func (st *state) testsForResponse(
	t *treeIndex, commit string, stale bool, plan *testPlan, target *Target, walk testWalk, limit int,
) *Response {
	cov := coverageOf(t)
	all, rows := walk.rows, walk.rows
	truncated := 0
	if len(rows) > limit {
		truncated = len(rows) - limit
		rows = rows[:limit]
	}
	query := st.req.Symbol
	if plan.path != "" {
		query = plan.path
	}
	if len(all) == 0 {
		st.line("index: no candidate tests for " + query + " at " + t.Tree)
	} else {
		st.testsForHuman(t, query, all, rows, truncated, walk, plan)
	}
	next := "read each candidate test before changing the code"
	if len(all) == 0 {
		next = "run verdict index build when the tree changes"
	}
	return &Response{
		At:   st.req.TimeUTC(),
		Next: next,
		Data: &Data{
			Kind: QueryTestsFor, Path: plan.path, Symbol: st.req.Symbol,
			Tree: t.Tree, Commit: commit, Stale: stale, Built: true,
			Files: cov.files, Skipped: cov.skipped,
			Tests: len(all), TestRows: rows, Truncated: truncated, Limit: limit,
			Depth: plan.depth, DepthTruncated: len(walk.hidden),
			Target: target,
		},
	}
}

// testsForHuman renders the candidate header, one row per test, and the
// truncation counts.
func (st *state) testsForHuman(t *treeIndex, query string, all, rows []TestRow, truncated int, walk testWalk, plan *testPlan) {
	st.line(fmt.Sprintf("index: %d candidate test(s) for %s at %s (%s, %s, %s, %s)",
		len(all), query, t.Tree, viaCount(all, viaSamePackage), viaCount(all, viaReferences),
		viaCount(all, viaImportsModule), viaCount(all, viaTransitiveImport)))
	for _, row := range rows {
		st.line(testRowText(row))
	}
	if truncated > 0 {
		st.line(fmt.Sprintf("index: showing %d of %d candidate tests (%d not shown; raise --limit up to %d)",
			len(rows), len(all), truncated, MaxLimit))
	}
	if len(walk.hidden) > 0 {
		st.line(fmt.Sprintf(testsDepthCapped, plan.depth, len(walk.hidden), MaxDepth))
	}
}

// testRowText renders one candidate, naming the import hops it took.
func testRowText(row TestRow) string {
	if row.Depth > 0 {
		return fmt.Sprintf("%s (%s, depth %d)", quotePath(row.Path), row.Via, row.Depth)
	}
	return fmt.Sprintf("%s (%s)", quotePath(row.Path), row.Via)
}

// viaCount counts the candidate rows one mechanism labeled.
func viaCount(rows []TestRow, via string) string {
	n := 0
	for _, row := range rows {
		if row.Via == via {
			n++
		}
	}
	return fmt.Sprintf("%s %d", via, n)
}

// rowLimit resolves one query's row limit and enforces the published bounds,
// so every transport — not only the CLI parser — holds them.
func (st *state) rowLimit(query string) (int, *core.RefusalError) {
	return boundedValue(query, "--limit", st.req.Limit, DefaultLimit, MaxLimit)
}

// depthLimit resolves the transitive walk's import-hop cap under the published
// bounds.
func (st *state) depthLimit() (int, *core.RefusalError) {
	return boundedValue(QueryTestsFor, "--depth", st.req.Depth, DefaultDepth, MaxDepth)
}

// boundedValue resolves one numeric flag: zero takes the default, and
// anything outside 1..max refuses with the query and flag named.
func boundedValue(query, flag string, v, def, bound int) (int, *core.RefusalError) {
	if v == 0 {
		return def, nil
	}
	if v < 1 || v > bound {
		return 0, &core.RefusalError{
			Code:    codeBadFlags,
			Message: fmt.Sprintf("index %s wants %s from 1 to %d", query, flag, bound),
		}
	}
	return v, nil
}
