package indexcore

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// The three mechanisms a candidate test can reach a source file through.
const (
	viaSamePackage   = "same-package"
	viaReferences    = "references"
	viaImportsModule = "imports-module"
)

// testPlan is the resolved tests-for input: the queried path, its language, the
// declaring package directories, and one resolution target per declared symbol.
type testPlan struct {
	path    string
	dirs    []string
	lang    language
	targets []target
}

// testsFor executes `index tests-for`: candidate tests to read, never a
// coverage claim.
func (st *state) testsFor(ctx context.Context) (*Response, *core.RefusalError) {
	limit, ref := st.rowLimit(QueryTestsFor)
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
	var target *Target
	if plan.path != "" {
		target, ref = st.ownersAt(ctx, st.sourceRoot(), entries, plan.path)
		if ref != nil {
			return nil, ref
		}
	}
	rows := collectTestRows(t, plan)
	return st.testsForResponse(t, commit, stale, plan, target, rows, limit), nil
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

// collectTestRows walks the tree's test files once, labeling each candidate
// with the strongest mechanism that reaches the target.
func collectTestRows(t *treeIndex, plan *testPlan) []TestRow {
	dirs, importPaths := testMechanisms(t, plan)
	var rows []TestRow
	for i := range t.Files {
		f := &t.Files[i]
		if !isCandidateFile(f, plan.path) {
			continue
		}
		via := testVia(plan, dirs, importPaths, f)
		if via == "" {
			continue
		}
		rows = append(rows, TestRow{Path: f.Path, Via: via})
	}
	slices.SortFunc(rows, func(a, b TestRow) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		return strings.Compare(a.Via, b.Via)
	})
	return rows
}

// testMechanisms indexes the plan's package directories and import paths.
func testMechanisms(t *treeIndex, plan *testPlan) (map[string]bool, map[string]bool) {
	dirs := make(map[string]bool, len(plan.dirs))
	importPaths := make(map[string]bool, len(plan.dirs))
	for _, dir := range plan.dirs {
		dirs[dir] = true
		importPaths[importPathOf(t.Module, dir)] = true
	}
	return dirs, importPaths
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
// row: a readable test file in its language that is not the queried file
// itself.
func isCandidateFile(f *treeFile, queried string) bool {
	return testPath(fileLanguage(f), f.Path) && !f.Oversize && f.Error == "" && f.Path != queried
}

// testVia classifies one test file: same package directory, a recorded
// reference to a declared symbol, or an import of the declaring package.
func testVia(plan *testPlan, dirs, importPaths map[string]bool, f *treeFile) string {
	switch {
	case samePackageTest(plan, dirs, f):
		return viaSamePackage
	case referencesTargets(plan.targets, f), importsTargetPath(plan, f):
		return viaReferences
	case importsAny(f, importPaths):
		return viaImportsModule
	default:
		return ""
	}
}

// samePackageTest reports whether one candidate is a test in a declaring
// directory and in the queried declaration's language family: a `.py` test says
// nothing about the Go file beside it, while a JavaScript test can cover a
// TypeScript declaration.
func samePackageTest(plan *testPlan, dirs map[string]bool, f *treeFile) bool {
	return dirs[path.Dir(f.Path)] && plan.lang.sameFamily(fileLanguage(f))
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

// importsAny reports whether the file imports one of the plan's packages.
func importsAny(f *treeFile, importPaths map[string]bool) bool {
	for _, imp := range f.Imports {
		if importPaths[imp.Path] {
			return true
		}
	}
	return false
}

// testsForResponse renders the candidate list, truncating rows with a count.
func (st *state) testsForResponse(
	t *treeIndex, commit string, stale bool, plan *testPlan, target *Target, rows []TestRow, limit int,
) *Response {
	cov := coverageOf(t)
	all := rows
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
		st.testsForHuman(t, query, all, rows, truncated)
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
			Target: target,
		},
	}
}

// testsForHuman renders the candidate header, one row per test, and the
// truncation count.
func (st *state) testsForHuman(t *treeIndex, query string, all, rows []TestRow, truncated int) {
	st.line(fmt.Sprintf("index: %d candidate test(s) for %s at %s (%s, %s, %s)",
		len(all), query, t.Tree, viaCount(all, viaSamePackage),
		viaCount(all, viaReferences), viaCount(all, viaImportsModule)))
	for _, row := range rows {
		st.line(fmt.Sprintf("%s (%s)", quotePath(row.Path), row.Via))
	}
	if truncated > 0 {
		st.line(fmt.Sprintf("index: showing %d of %d candidate tests (%d not shown; raise --limit up to %d)",
			len(rows), len(all), truncated, MaxLimit))
	}
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
