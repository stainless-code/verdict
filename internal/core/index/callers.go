package indexcore

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// packageInfo groups one directory's indexed files.
type packageInfo struct {
	dir        string
	importPath string
	files      []*treeFile
}

// callerResults is the query's measured outcome.
type callerResults struct {
	rows       []Row
	unresolved int
}

// coverage counts the tree's indexed and skipped files.
type coverage struct {
	files   int
	skipped int
}

// callers executes `index callers`.
func (st *state) callers(ctx context.Context) (*Response, *core.RefusalError) {
	limit, ref := st.rowLimit(QueryCallers)
	if ref != nil {
		return nil, ref
	}
	root := st.sourceRoot()
	st.trust = st.readTrust(ctx, root)
	tree, commit, ref := st.queryRef(ctx, root)
	if ref != nil {
		return nil, ref
	}
	store := storePath(root)
	t, ref := readTree(store, tree)
	if ref != nil {
		return nil, ref
	}
	stale := st.warnStale(ctx, root, tree)
	pkgs := packageMap(t)
	targets, ref := resolveTargets(pkgs, st.req.Symbol)
	if ref != nil {
		return nil, ref
	}
	results := queryCallers(pkgs, targets)
	return st.callersResponse(t, commit, stale, results, limit), nil
}

// queryRef resolves the query's tree and commit. A raw tree SHA has no
// commit to report, which is not an error.
func (st *state) queryRef(ctx context.Context, root string) (string, string, *core.RefusalError) {
	at := st.req.At
	if at == "" {
		at = "HEAD"
	}
	return st.queryRefAt(ctx, root, at)
}

// queryRefAt resolves one explicit revision to its tree and commit.
func (st *state) queryRefAt(ctx context.Context, root, at string) (string, string, *core.RefusalError) {
	tree, ref := st.resolveTree(ctx, root, at)
	if ref != nil {
		return "", "", ref
	}
	commit, ref := st.resolveCommit(ctx, root, at)
	if ref != nil {
		if !shaRe.MatchString(at) {
			return "", "", ref
		}
		commit = ""
	}
	return tree, commit, nil
}

// warnStale marks a query for a tree that is not HEAD's.
func (st *state) warnStale(ctx context.Context, root, tree string) bool {
	head, ref := st.resolveTree(ctx, root, "HEAD")
	if ref != nil || head == tree {
		return false
	}
	st.warn(fmt.Sprintf("» index: built for %s, HEAD is %s", tree, head))
	return true
}

// packageMap groups a tree's files by directory and collects declarations.
func packageMap(t *treeIndex) map[string]*packageInfo {
	pkgs := make(map[string]*packageInfo)
	for i := range t.Files {
		addPackageFile(pkgs, t, &t.Files[i])
	}
	return pkgs
}

// addPackageFile adds one tree file to its directory's package.
func addPackageFile(pkgs map[string]*packageInfo, t *treeIndex, f *treeFile) {
	if f.Oversize || f.Error != "" {
		return
	}
	dir := path.Dir(f.Path)
	pkg := pkgs[dir]
	if pkg == nil {
		pkg = &packageInfo{dir: dir}
		pkg.importPath = importPathOf(t.Module, dir)
		pkgs[dir] = pkg
	}
	pkg.files = append(pkg.files, f)
}

// importPathOf renders one package directory's import path.
func importPathOf(module, dir string) string {
	switch {
	case module == "":
		return dir
	case dir == ".":
		return module
	default:
		return module + "/" + dir
	}
}

// target is one package declaring the queried symbol.
type target struct {
	pkg      *packageInfo
	name     string
	kind     string
	lang     language
	exported bool
}

// resolveTargets resolves a qualified or bare symbol to its declaring
// packages.
//
//nolint:revive // the query resolution matrix reads as one block
func resolveTargets(pkgs map[string]*packageInfo, symbol string) ([]target, *core.RefusalError) {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return nil, &core.RefusalError{Code: codeBadFlags, Message: "index callers wants one symbol"}
	}
	qualifier, name := splitSymbol(symbol)
	if name == "" {
		return nil, &core.RefusalError{Code: codeBadFlags, Message: "index callers wants a symbol name after the package qualifier"}
	}
	dirs := sortedDirs(pkgs)
	wanted := dirs
	if qualifier != "" {
		var ref *core.RefusalError
		wanted, ref = matchingPackages(pkgs, dirs, qualifier)
		if ref != nil {
			return nil, ref
		}
	}
	var targets []target
	for _, dir := range wanted {
		pkg := pkgs[dir]
		for _, f := range pkg.files {
			for _, d := range f.Decls {
				if d.Name == name {
					targets = append(targets, target{
						pkg: pkg, name: name, kind: d.Kind, lang: fileLanguage(f), exported: d.Exported,
					})
				}
			}
		}
	}
	return targets, nil
}

// splitSymbol cuts one symbol into its optional package qualifier and name.
func splitSymbol(symbol string) (string, string) {
	if qualifier, name, ok := strings.CutLast(symbol, "."); ok {
		return qualifier, name
	}
	return "", symbol
}

// sortedDirs returns the package directories in stable order.
func sortedDirs(pkgs map[string]*packageInfo) []string {
	dirs := make([]string, 0, len(pkgs))
	for dir := range pkgs {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	return dirs
}

// matchingPackages resolves a qualifier against directory names, import
// paths, and import-path suffixes, refusing when nothing or more than one
// directory matches.
func matchingPackages(pkgs map[string]*packageInfo, dirs []string, qualifier string) ([]string, *core.RefusalError) {
	var exact []string
	var byBase []string
	for _, dir := range dirs {
		pkg := pkgs[dir]
		switch {
		case dir == qualifier, pkg.importPath == qualifier:
			exact = append(exact, dir)
		case pkg.importPath != "" && strings.HasSuffix(pkg.importPath, "/"+qualifier):
			byBase = append(byBase, dir)
		}
	}
	if len(exact) > 0 {
		return exact, nil
	}
	if len(byBase) == 1 {
		return byBase, nil
	}
	if len(byBase) > 1 {
		return nil, &core.RefusalError{Code: codeBadFlags, Message: "package qualifier " + qualifier + " is ambiguous"}
	}
	return nil, &core.RefusalError{
		Code:    codeBadFlags,
		Message: "package qualifier " + qualifier + " matches no package directory or import path",
	}
}

// queryCallers walks every package once per target: exact matches inside the
// declaring package, lexical matches through an import edge elsewhere.
func queryCallers(pkgs map[string]*packageInfo, targets []target) callerResults {
	var out callerResults
	seen := map[Row]bool{}
	dirs := sortedDirs(pkgs)
	for _, tgt := range targets {
		for _, dir := range dirs {
			pkg := pkgs[dir]
			for _, f := range pkg.files {
				collectFileRefs(&out, seen, tgt, pkg.dir == tgt.pkg.dir, f)
			}
		}
	}
	sortRows(out.rows)
	return out
}

// collectFileRefs folds one file's matching references into the result set.
//
//nolint:revive // the query resolution matrix reads as one block
func collectFileRefs(out *callerResults, seen map[Row]bool, tgt target, same bool, f *treeFile) {
	for _, r := range f.Refs {
		if r.Name != tgt.name {
			continue
		}
		row, matched := resolveRef(tgt, same, f, r)
		if !matched {
			continue
		}
		if row.Resolution == resolutionUnresolved {
			out.unresolved++
			continue
		}
		row.Language = string(fileLanguage(f))
		if !seen[row] {
			seen[row] = true
			out.rows = append(out.rows, row)
		}
	}
}

// sortRows orders caller rows by path, line, and kind.
func sortRows(rows []Row) {
	slices.SortFunc(rows, func(a, b Row) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return strings.Compare(a.Kind, b.Kind)
	})
}

// resolveRef classifies one matching reference. It reports false when the
// reference cannot name the target at all; a true row with the unresolved
// resolution is a candidate that needs type information, counted and not
// rendered. A file in another language family shares nothing but a directory
// with the declaration, so it is not a candidate.
func resolveRef(tgt target, samePkg bool, f *treeFile, r ref) (Row, bool) {
	lang := fileLanguage(f)
	if !lang.sameFamily(tgt.lang) {
		return Row{}, false
	}
	if lang != langGo {
		return resolveScannedRef(lang, tgt, samePkg, f, r)
	}
	return resolveGoRef(tgt, samePkg, f, r)
}

// resolveGoRef attributes one reference to a Go declaration: a bare name only
// inside the declaring package, a qualified name by import path, and a
// same-package method or unresolved qualifier as a counted candidate.
func resolveGoRef(tgt target, samePkg bool, f *treeFile, r ref) (Row, bool) {
	if !r.Selector {
		return resolveBareRef(samePkg, f, r)
	}
	if r.Qualifier == "" {
		if samePkg && (r.Unresolved || r.Kind == KindMethod) {
			return Row{Resolution: resolutionUnresolved}, true
		}
		return Row{}, false
	}
	return resolveQualifiedRef(tgt, f, r)
}

// resolveScannedRef classifies one reference a lexical scanner produced. A
// scanned language has no object identity to check and no module resolution to
// follow, so a name found in the declaring directory is a lexical row and a
// name found anywhere else is not attributed at all. Python records each
// declaration's kind, so a site cannot land on a declaration it could not name.
func resolveScannedRef(lang language, tgt target, samePkg bool, f *treeFile, r ref) (Row, bool) {
	if !samePkg {
		return Row{}, false
	}
	if lang.family() == langPython && !pySiteNamesDecl(r.Kind, tgt.kind) {
		return Row{}, false
	}
	return Row{Path: f.Path, Line: r.Line, Kind: r.Kind, Resolution: resolutionLexical}, true
}

// pySiteNamesDecl reports whether one recorded Python site can name a
// declaration of the target's kind: an attribute site is written on a receiver,
// so it names a method or a class, never a module-level function, while a bare
// call never names a method, which needs that receiver.
func pySiteNamesDecl(site, decl string) bool {
	if site == KindMethod {
		return decl == KindMethod || decl == wordType
	}
	return decl != KindMethod
}

// resolveBareRef classifies a name written without a qualifier: only the
// declaring package can see it.
func resolveBareRef(samePkg bool, f *treeFile, r ref) (Row, bool) {
	if !samePkg {
		return Row{}, false
	}
	if r.Unresolved || r.Kind == KindMethod {
		return Row{Resolution: resolutionUnresolved}, true
	}
	return Row{Path: f.Path, Line: r.Line, Kind: r.Kind, Resolution: resolutionExact}, true
}

// resolveQualifiedRef classifies a name reached through an import qualifier:
// a lexical row only when the edge names the declaring package.
func resolveQualifiedRef(tgt target, f *treeFile, r ref) (Row, bool) {
	ipath, ok := importPathFor(&shard{Imports: f.Imports}, r.Qualifier)
	if !ok || ipath != tgt.pkg.importPath || !tgt.exported {
		return Row{}, false
	}
	if r.Unresolved {
		return Row{Resolution: resolutionUnresolved}, true
	}
	return Row{Path: f.Path, Line: r.Line, Kind: r.Kind, Resolution: resolutionLexical}, true
}

// callersResponse renders the query outcome, truncating rows with a count.
func (st *state) callersResponse(t *treeIndex, commit string, stale bool, results callerResults, limit int) *Response {
	rows := results.rows
	truncated := 0
	if len(rows) > limit {
		truncated = len(rows) - limit
		rows = rows[:limit]
	}
	cov := coverageOf(t)
	symbol := st.req.Symbol
	if results.rows == nil && results.unresolved == 0 {
		st.line("index: no callers of " + symbol + " at " + t.Tree)
		return st.callerData(t, commit, stale, cov, results, rows, truncated, limit)
	}
	st.line(fmt.Sprintf("index: %d caller(s) of %s at %s (%d unresolved)", len(results.rows), symbol, t.Tree, results.unresolved))
	for _, row := range rows {
		st.line(fmt.Sprintf("%s:%d %s (%s)", quotePath(row.Path), row.Line, row.Kind, row.Resolution))
	}
	if truncated > 0 {
		st.line(fmt.Sprintf("index: showing %d of %d callers (%d not shown; raise --limit up to %d)",
			len(rows), len(results.rows), truncated, MaxLimit))
	}
	return st.callerData(t, commit, stale, cov, results, rows, truncated, limit)
}

// callerData assembles the query's data profile.
func (st *state) callerData(
	t *treeIndex, commit string, stale bool, cov coverage, results callerResults,
	rows []Row, truncated, limit int,
) *Response {
	next := "narrow the query to one package, or raise --limit"
	if len(results.rows) == 0 {
		next = "run verdict index build when the tree changes"
	}
	return &Response{
		At:   st.req.TimeUTC(),
		Next: next,
		Data: &Data{
			Kind: QueryCallers, Symbol: st.req.Symbol, Tree: t.Tree, Commit: commit,
			Stale: stale, Built: true, Files: cov.files, Skipped: cov.skipped,
			Callers: len(results.rows), Unresolved: results.unresolved,
			Rows: rows, Truncated: truncated, Limit: limit,
		},
	}
}

// languageCounts counts the tree's indexed files per scanned language, so the
// breakdown adds up to files_indexed: a skipped file is not a file any
// language was read for.
func languageCounts(t *treeIndex) []LangRow {
	counts := map[string]int{}
	for i := range t.Files {
		if !indexedFile(&t.Files[i]) {
			continue
		}
		counts[t.Files[i].Language]++
	}
	rows := make([]LangRow, 0, len(counts))
	for name, files := range counts {
		rows = append(rows, LangRow{Language: name, Files: files})
	}
	slices.SortFunc(rows, func(a, b LangRow) int {
		if c := strings.Compare(a.Language, b.Language); c != 0 {
			return c
		}
		return a.Files - b.Files
	})
	return rows
}

// coverageOf counts a tree's fully indexed and skipped files.
func coverageOf(t *treeIndex) coverage {
	var cov coverage
	for i := range t.Files {
		if !indexedFile(&t.Files[i]) {
			cov.skipped++
			continue
		}
		cov.files++
	}
	return cov
}
