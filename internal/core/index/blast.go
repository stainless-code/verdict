package indexcore

import (
	"context"
	"fmt"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// blastKeySep separates the components of a blast-radius sort/identity key; a
// git path never carries a NUL byte.
const blastKeySep = "\x00"

// Blast-radius constants.
const (
	blastDepth       = 2
	blastDiffSuffix  = "/full.diff"
	blastHeadWarning = "» index: the PR head %s is not indexed here; querying HEAD"
	blastNoIndexWarn = "» index: no index for tree %s (run verdict index build): symbol, caller, and test counts are unmeasured"
	blastNoInput     = "index blast-radius wants --pr <n> or --paths <path>"
	blastOneInput    = "index blast-radius wants one of --pr <n> or --paths <path>, not both"
	blastMissingDir  = "index blast-radius: no bundle at %s (run verdict fetch first)"
	blastMissingDiff = "index blast-radius: no %s/full.diff (run fetch first)"
	blastUnknownPath = "index blast-radius: %s is not a file at %s"
	blastEmptyPath   = "index blast-radius wants a repository path after --paths"
	blastCallerDepth = "index: %d caller(s) within depth %d (%d unresolved)"
	blastShowing     = "index: showing %d of %d %s (%d not shown; raise --limit up to %d)"
	blastNoSymbols   = "index: %d changed file(s), no indexed declarations at %s"
	blastSymbolsHead = "index: %d changed file(s), %d changed symbol(s) at %s"
	blastTestsHead   = "index: %d candidate test(s)"
	blastPartnerHead = "index: %d co-change pair(s) over %d partner file(s)"
	blastHunkless    = "» index: %s changed with no line hunks; every declaration in the new path is a changed symbol"
	blastOwnerHead   = "index: ownership for %d changed path(s)"
	blastIndexAbsent = "index: no index for %s: symbol, caller, and test lists are unmeasured"
)

// BlastInput is the blast-radius query input: the PR slug or the resolved
// changed paths, plus the bundle's PR head when the query read one.
type BlastInput struct {
	Pr    string
	Head  string
	Paths []string
}

// SymbolRow is one changed declaration: the file, the declared name and kind,
// and its first line.
type SymbolRow struct {
	Path string
	Name string
	Kind string
	Line int
}

// CallerRow is one caller reached from a changed symbol within the depth cap.
// Symbol names the changed declaration the row is reached from, and Depth is
// 1 for a direct caller or 2 for a caller of a direct caller.
type CallerRow struct {
	Symbol     string
	Path       string
	Kind       string
	Resolution string
	Line       int
	Depth      int
}

// BlastPartnerRow is one path that moved with one changed path.
type BlastPartnerRow struct {
	Target  string
	Path    string
	Commits int
	Jaccard float64
}

// BlastCounts is the measured size of each list. The index-derived counts are
// nil when the index could not answer, never 0. CoChanged counts rows — one
// (changed path, partner) pair each — while Partners counts the distinct
// partner paths across them.
type BlastCounts struct {
	ChangedSymbols *int
	Callers        *int
	Unresolved     *int
	Tests          *int
	CoChanged      int
	Partners       int
	Owners         int
}

// BlastTruncated is the per-list truncation count: how many rows the limit hid.
type BlastTruncated struct {
	ChangedSymbols *int
	Callers        *int
	Tests          *int
	CoChanged      int
	Owners         int
}

// Blast is the blast-radius result: the input, the measured lists, and whether
// an index answered the symbol, caller, and test lists. unresolved counts the
// candidate references the caller walk could not attribute.
type Blast struct {
	Symbols    []SymbolRow
	Callers    []CallerRow
	Tests      []TestRow
	Partners   []BlastPartnerRow
	Owners     []Target
	Truncated  BlastTruncated
	Input      BlastInput
	Counts     BlastCounts
	unresolved int
	Indexed    bool
}

// checkBlastPaths refuses a --paths entry the queried tree does not carry, so a
// typo never reads as an empty blast radius.
func checkBlastPaths(entries []treeEntry, paths []string, tree string) *core.RefusalError {
	for _, p := range paths {
		if _, ok := entryByPath(entries, p); !ok {
			return &core.RefusalError{
				Code: codeBadFlags, Message: fmt.Sprintf(blastUnknownPath, p, tree),
			}
		}
	}
	return nil
}

// blastChange is one changed file and the new-side lines the diff touched; all
// names every declaration in the file instead, and hunkless reports that the
// diff carried no line hunks, so all was assumed rather than read.
type blastChange struct {
	path     string
	lines    []int
	all      bool
	hunkless bool
	binary   bool
}

// blastSymbol is one declaration whose span a change touched, with the query
// target it resolves to.
type blastSymbol struct {
	key    string
	label  string
	origin string
	row    SymbolRow
	tgt    target
}

func (s *blastSymbol) reachedFrom() string {
	if s.origin != "" {
		return s.origin
	}
	return s.label
}

// blastRadius executes `index blast-radius`: the changed declarations, their
// callers to depth 2, and the candidate tests, co-change partners, and owners
// for those paths.
//
//nolint:revive // the query ladder is one resolution step per rule
func (st *state) blastRadius(ctx context.Context) (*Response, *core.RefusalError) {
	limit, ref := st.rowLimit(QueryBlastRadius)
	if ref != nil {
		return nil, ref
	}
	commits, minCount, ref := st.blastWindowBounds()
	if ref != nil {
		return nil, ref
	}
	input, changes, ref := st.blastInput(ctx)
	if ref != nil {
		return nil, ref
	}
	root := st.sourceRoot()
	st.trust = st.readTrust(ctx, root)
	t, tree, commit, stale, ref := st.blastTree(ctx, root, input.Head)
	if ref != nil {
		return nil, ref
	}
	entries, ref := st.lsTree(ctx, root, tree)
	if ref != nil {
		return nil, ref
	}
	if st.req.Pr == "" {
		if ref := checkBlastPaths(entries, input.Paths, tree); ref != nil {
			return nil, ref
		}
	}
	var pkgs map[string]*packageInfo
	if t != nil {
		pkgs = packageMap(t)
	} else {
		st.warn(fmt.Sprintf(blastNoIndexWarn, tree))
	}
	st.warnHunkless(changes)
	symbols := blastSymbols(pkgs, t, changes)
	callers, unresolved := blastCallers(pkgs, t, blastRoots(symbols))
	partners, ref := st.blastPartners(ctx, root, input.Paths, commits, minCount)
	if ref != nil {
		return nil, ref
	}
	owners, ref := st.blastOwners(ctx, root, entries, input.Paths)
	if ref != nil {
		return nil, ref
	}
	blast := &Blast{
		Input: input, Indexed: t != nil,
		Symbols: symbolRows(symbols), Callers: callers, Tests: blastTests(t, symbols),
		Partners: partners, Owners: owners, unresolved: unresolved,
	}
	return st.blastResponse(t, tree, commit, stale, blast, limit), nil
}

// warnHunkless warns about each changed file whose diff named no line hunks.
func (st *state) warnHunkless(changes []blastChange) {
	for _, ch := range changes {
		if ch.hunkless {
			st.warn(fmt.Sprintf(blastHunkless, ch.path))
		}
	}
}

// blastWindowBounds resolves and bounds --commits and --min-count.
func (st *state) blastWindowBounds() (int, int, *core.RefusalError) {
	commits, ref := boundedValue(QueryBlastRadius, "--commits", st.req.Commits,
		DefaultCoChangeCommits, MaxCommitWindow)
	if ref != nil {
		return 0, 0, ref
	}
	minCount, ref := boundedValue(QueryBlastRadius, "--min-count", st.req.MinCount,
		DefaultMinCount, MaxCommitWindow)
	if ref != nil {
		return 0, 0, ref
	}
	return commits, minCount, nil
}

// blastInput resolves --pr or --paths into the queried input and the changed
// declarations' lines.
func (st *state) blastInput(ctx context.Context) (BlastInput, []blastChange, *core.RefusalError) {
	if st.req.Pr != "" && len(st.req.Paths) > 0 {
		return BlastInput{}, nil, &core.RefusalError{Code: codeBadFlags, Message: blastOneInput}
	}
	if st.req.Pr == "" {
		return st.pathsInput()
	}
	return st.bundleInput(ctx)
}

// pathsInput normalizes the --paths form: no bundle and no network.
func (st *state) pathsInput() (BlastInput, []blastChange, *core.RefusalError) {
	if len(st.req.Paths) == 0 {
		return BlastInput{}, nil, &core.RefusalError{Code: codeBadFlags, Message: blastNoInput}
	}
	seen := map[string]bool{}
	var paths []string
	for _, raw := range st.req.Paths {
		p := normalizePath(raw)
		if p == "" {
			return BlastInput{}, nil, &core.RefusalError{Code: codeBadFlags, Message: blastEmptyPath}
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	slices.Sort(paths)
	changes := make([]blastChange, 0, len(paths))
	for _, p := range paths {
		changes = append(changes, blastChange{path: p, all: true})
	}
	return BlastInput{Paths: paths}, changes, nil
}

// bundleInput reads the fetched bundle's full diff: the changed paths and the
// new-side lines the diff touched, with no host call.
func (st *state) bundleInput(ctx context.Context) (BlastInput, []blastChange, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return BlastInput{}, nil, &core.RefusalError{Code: "canceled", Message: "canceled before index completed"}
	}
	dir, ref := readcommon.ResolveBundleDir(st.sourceRoot(), st.req.Pr)
	if ref != nil {
		return BlastInput{}, nil, ref
	}
	if !readcommon.IsDir(dir) {
		return BlastInput{}, nil, &core.RefusalError{
			Code: "missing-bundle", Message: fmt.Sprintf(blastMissingDir, dir),
		}
	}
	raw, err := os.ReadFile(dir + blastDiffSuffix)
	if err != nil {
		return BlastInput{}, nil, &core.RefusalError{
			Code: "missing-diff", Message: fmt.Sprintf(blastMissingDiff, dir),
		}
	}
	changes := blastChanges(string(raw))
	paths := make([]string, 0, len(changes))
	for _, ch := range changes {
		paths = append(paths, ch.path)
	}
	return BlastInput{Pr: st.req.Pr, Head: readcommon.OpenBundle(dir).Head(), Paths: paths}, changes, nil
}

// blastTree loads the queried tree's index. Explicit --at wins; then the
// bundle's PR head when the store already holds its tree; then HEAD. A tree
// with no stored index is not an error — the symbol counts read null.
//
//nolint:revive // the tree selection ladder is one branch per rule
func (st *state) blastTree(
	ctx context.Context, root, head string,
) (*treeIndex, string, string, bool, *core.RefusalError) {
	if at := strings.TrimSpace(st.req.At); at != "" {
		tree, commit, ref := st.queryRefAt(ctx, root, at)
		if ref != nil {
			return nil, "", "", false, ref
		}
		t, ref := st.optionalTree(root, tree)
		if ref != nil {
			return nil, "", "", false, ref
		}
		return t, tree, commit, st.warnStale(ctx, root, tree), nil
	}
	if head != "" && shaRe.MatchString(head) {
		t, tree, commit, ok, ref := st.bundleTree(ctx, root, head)
		if ref != nil {
			return nil, "", "", false, ref
		}
		if ok {
			return t, tree, commit, st.headDiffers(ctx, root, tree), nil
		}
		st.warn(fmt.Sprintf(blastHeadWarning, head))
	}
	tree, commit, ref := st.queryRefAt(ctx, root, "HEAD")
	if ref != nil {
		return nil, "", "", false, ref
	}
	t, ref := st.optionalTree(root, tree)
	if ref != nil {
		return nil, "", "", false, ref
	}
	return t, tree, commit, false, nil
}

// bundleTree resolves one bundle head to its stored index, reporting ok=false
// when the object is not in this clone or its tree was never indexed.
func (st *state) bundleTree(
	ctx context.Context, root, head string,
) (*treeIndex, string, string, bool, *core.RefusalError) {
	tree, ref := st.resolveTree(ctx, root, head)
	if ref != nil {
		//nolint:nilerr // a head outside this clone is a fallback to HEAD, not a refusal
		return nil, "", "", false, nil
	}
	t, ref := st.optionalTree(root, tree)
	if ref != nil {
		return nil, "", "", false, ref
	}
	if t == nil {
		return nil, "", "", false, nil
	}
	commit, ref := st.resolveCommit(ctx, root, head)
	if ref != nil {
		commit = ""
	}
	return t, tree, commit, true, nil
}

// optionalTree reads one tree index, treating an absent index as unmeasured
// (nil) while a corrupt one still refuses.
func (st *state) optionalTree(root, tree string) (*treeIndex, *core.RefusalError) {
	t, ref := readTree(storePath(root), tree)
	if ref == nil {
		return t, nil
	}
	if ref.Code == codeMissingIndex {
		return nil, nil
	}
	return nil, ref
}

// headDiffers reports a queried tree that is not HEAD's tree, without the
// stale warning a deliberate PR-head query would repeat on every run.
func (st *state) headDiffers(ctx context.Context, root, tree string) bool {
	head, ref := st.resolveTree(ctx, root, "HEAD")
	return ref == nil && head != tree
}

// blastSymbols maps each change to the declarations its span touches, or to
// every declaration in the file for --paths.
//
//nolint:revive // the change-to-declaration matrix reads as one pass
func blastSymbols(pkgs map[string]*packageInfo, t *treeIndex, changes []blastChange) []blastSymbol {
	if t == nil {
		return nil
	}
	var out []blastSymbol
	seen := map[string]bool{}
	for _, ch := range changes {
		f := findIndexedFile(t, ch.path)
		pkg, ok := indexedPackage(pkgs, f)
		if !ok {
			continue
		}
		for _, d := range changeDecls(f.Decls, ch) {
			key := f.Path + blastKeySep + strconv.Itoa(d.Line) + blastKeySep + d.Name
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, blastSymbol{
				row: SymbolRow{Path: f.Path, Name: d.Name, Kind: d.Kind, Line: d.Line},
				tgt: target{pkg: pkg, name: d.Name, lang: fileLanguage(f), exported: d.Exported},
				key: blastKey(pkg, d.Name), label: blastLabel(pkg, d.Name),
			})
		}
	}
	return out
}

// indexedPackage returns the package of one readable indexed file.
func indexedPackage(pkgs map[string]*packageInfo, f *treeFile) (*packageInfo, bool) {
	if f == nil || f.Oversize || f.Error != "" {
		return nil, false
	}
	pkg := pkgs[path.Dir(f.Path)]
	return pkg, pkg != nil
}

// changeDecls returns the declarations one change selects: every declaration
// for --paths, otherwise the declaration owning each touched line.
func changeDecls(decls []decl, ch blastChange) []decl {
	if ch.all {
		return decls
	}
	var out []decl
	var last int
	for _, line := range ch.lines {
		if d, ok := enclosingDecl(decls, line); ok && (len(out) == 0 || d.Line != last) {
			out = append(out, d)
			last = d.Line
		}
	}
	return out
}

// enclosingDecl returns the declaration owning line: the last one starting at
// or before it, with the shard's start lines standing in for the span.
func enclosingDecl(decls []decl, line int) (decl, bool) {
	var found decl
	ok := false
	for _, d := range decls {
		if d.Line > line {
			continue
		}
		if !ok || d.Line > found.Line {
			found, ok = d, true
		}
	}
	return found, ok
}

// blastRoots dedupes the changed symbols into query targets, first by path
// order so the label is deterministic.
func blastRoots(symbols []blastSymbol) []blastSymbol {
	var out []blastSymbol
	seen := map[string]bool{}
	for i := range symbols {
		sym := &symbols[i]
		if seen[sym.key] {
			continue
		}
		seen[sym.key] = true
		out = append(out, *sym)
	}
	return out
}

// blastKey identifies one query target: a package directory and a name.
func blastKey(pkg *packageInfo, name string) string { return pkg.dir + blastKeySep + name }

// blastLabel renders one declaration as the qualified symbol a caller query
// accepts: import path and name, or the directory and name without a module.
func blastLabel(pkg *packageInfo, name string) string {
	if pkg.importPath != "" {
		return pkg.importPath + "." + name
	}
	if pkg.dir != "." {
		return pkg.dir + "." + name
	}
	return name
}

// symbolRows publishes the changed declarations.
func symbolRows(symbols []blastSymbol) []SymbolRow {
	rows := make([]SymbolRow, 0, len(symbols))
	for i := range symbols {
		rows = append(rows, symbols[i].row)
	}
	return rows
}

// blastCallers walks the call graph from the changed symbols, depth 1 then
// depth 2, never re-querying a target and never expanding past the cap.
func blastCallers(pkgs map[string]*packageInfo, t *treeIndex, roots []blastSymbol) ([]CallerRow, int) {
	if t == nil || len(roots) == 0 {
		return nil, 0
	}
	visited := make(map[string]bool, len(roots))
	for i := range roots {
		visited[roots[i].key] = true
	}
	hits := map[string]callerHit{}
	unresolved := 0
	frontier := roots
	for depth := 1; depth <= blastDepth && len(frontier) > 0; depth++ {
		var next []blastSymbol
		for i := range frontier {
			root := &frontier[i]
			res := queryCallers(pkgs, []target{root.tgt})
			unresolved += res.unresolved
			next = append(next, blastDepthRows(pkgs, t, hits, visited, root, res.rows, depth)...)
		}
		frontier = next
	}
	return callerRows(hits), unresolved
}

// blastDepthRows records one target's caller rows and returns the next depth's
// targets, skipping a target already visited.
//
//nolint:revive // the record-and-expand matrix reads as one pass
func blastDepthRows(
	pkgs map[string]*packageInfo, t *treeIndex, hits map[string]callerHit,
	visited map[string]bool, root *blastSymbol, rows []Row, depth int,
) []blastSymbol {
	var next []blastSymbol
	for _, row := range rows {
		k := callerHitKey(depth, row)
		if _, seen := hits[k]; !seen {
			hits[k] = callerHit{symbol: root.reachedFrom(), row: row, depth: depth}
		}
		if depth >= blastDepth {
			continue
		}
		sym, ok := callerSymbol(pkgs, t, row)
		if !ok || visited[sym.key] {
			continue
		}
		visited[sym.key] = true
		sym.origin = root.reachedFrom()
		next = append(next, sym)
	}
	return next
}

// callerHit is one deduplicated caller row with the root it was reached from.
type callerHit struct {
	symbol string
	row    Row
	depth  int
}

// callerHitKey renders one caller row as a fixed-width key: it sorts by depth,
// path, line, kind, and resolution, and a git path never carries a NUL.
func callerHitKey(depth int, row Row) string {
	return strconv.Itoa(depth) + blastKeySep + row.Path + blastKeySep +
		fmt.Sprintf("%012d", row.Line) + blastKeySep + row.Kind + blastKeySep + row.Resolution
}

// callerSymbol resolves one caller row to the declaration whose span holds it.
func callerSymbol(pkgs map[string]*packageInfo, t *treeIndex, row Row) (blastSymbol, bool) {
	f := findIndexedFile(t, row.Path)
	pkg, ok := indexedPackage(pkgs, f)
	if !ok {
		return blastSymbol{}, false
	}
	d, ok := enclosingDecl(f.Decls, row.Line)
	if !ok {
		return blastSymbol{}, false
	}
	return blastSymbol{
		row: SymbolRow{Path: f.Path, Name: d.Name, Kind: d.Kind, Line: d.Line},
		tgt: target{pkg: pkg, name: d.Name, lang: fileLanguage(f), exported: d.Exported},
		key: blastKey(pkg, d.Name), label: blastLabel(pkg, d.Name),
	}, true
}

// callerRows publishes the deduplicated hits in depth, path, and line order.
func callerRows(hits map[string]callerHit) []CallerRow {
	keys := make([]string, 0, len(hits))
	for k := range hits {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	rows := make([]CallerRow, 0, len(keys))
	for _, k := range keys {
		hit := hits[k]
		rows = append(rows, CallerRow{
			Symbol: hit.symbol, Path: hit.row.Path, Kind: hit.row.Kind,
			Resolution: hit.row.Resolution, Line: hit.row.Line, Depth: hit.depth,
		})
	}
	return rows
}

// blastTests collects the candidate tests for every changed symbol, keeping the
// strongest mechanism per file.
func blastTests(t *treeIndex, symbols []blastSymbol) []TestRow {
	if t == nil {
		return nil
	}
	best := map[string]TestRow{}
	for i := range symbols {
		collectSymbolTests(best, t, &symbols[i])
	}
	rows := make([]TestRow, 0, len(best))
	for _, row := range best {
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b TestRow) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		return strings.Compare(a.Via, b.Via)
	})
	return rows
}

// collectSymbolTests keeps the strongest candidate mechanism per test file for
// one changed symbol.
func collectSymbolTests(best map[string]TestRow, t *treeIndex, sym *blastSymbol) {
	plan := &testPlan{
		path: sym.row.Path, dirs: []string{sym.tgt.pkg.dir}, lang: sym.tgt.lang, targets: []target{sym.tgt},
	}
	for _, row := range collectTestRows(t, plan) {
		cur, seen := best[row.Path]
		if !seen || viaRank(row.Via) < viaRank(cur.Via) {
			best[row.Path] = row
		}
	}
}

// viaRankOther ranks a mechanism the list does not publish.
const viaRankOther = 3

// viaRank orders the candidate-test mechanisms strongest first.
func viaRank(via string) int {
	switch via {
	case viaSamePackage:
		return 0
	case viaReferences:
		return 1
	case viaImportsModule:
		return 2
	default:
		return viaRankOther
	}
}

// blastPartners reads one co-change window at HEAD and counts the partners of
// every changed path.
func (st *state) blastPartners(
	ctx context.Context, root string, paths []string, commits, minCount int,
) ([]BlastPartnerRow, *core.RefusalError) {
	head, ref := st.resolveCommit(ctx, root, "HEAD")
	if ref != nil {
		return nil, ref
	}
	spec, ref := st.coChangeSpec(ctx, root, head)
	if ref != nil {
		return nil, ref
	}
	shallow := st.warnShallow(ctx, root)
	doc, ref := st.coChangeWindow(ctx, root, head, commits, spec, shallow)
	if ref != nil {
		return nil, ref
	}
	var out []BlastPartnerRow
	for _, p := range paths {
		rows, _ := coChangePartners(doc.Changes, p, minCount)
		for _, row := range rows {
			out = append(out, BlastPartnerRow{Target: p, Path: row.Path, Commits: row.Commits, Jaccard: row.Jaccard})
		}
	}
	return out, nil
}

// blastOwners answers every changed path's owners from one CODEOWNERS read at
// the queried tree.
func (st *state) blastOwners(
	ctx context.Context, root string, entries []treeEntry, paths []string,
) ([]Target, *core.RefusalError) {
	file, ref := st.readCodeowners(ctx, root, entries)
	if ref != nil {
		return nil, ref
	}
	out := make([]Target, 0, len(paths))
	for _, p := range paths {
		target := Target{Path: p}
		if file != nil {
			target.OwnersSource = file.source
			target.Owners = matchOwners(file.rules, p)
		}
		out = append(out, target)
	}
	if file != nil && file.unsupported > 0 {
		st.warn(fmt.Sprintf(codeownersWarning, file.unsupported))
	}
	return out, nil
}

// blastResponse renders the measured lists, truncating each with its count.
func (st *state) blastResponse(
	t *treeIndex, tree, commit string, stale bool, blast *Blast, limit int,
) *Response {
	blast.Counts = blastCounts(blast, t)
	blast.Truncated = blastTruncated(blast, limit)
	st.blastHuman(blast, tree)
	data := &Data{
		Kind: QueryBlastRadius, Tree: tree, Commit: commit, Stale: stale,
		Limit: limit, Blast: blast,
	}
	if blast.Indexed {
		cov := coverageOf(t)
		data.Files, data.Skipped = cov.files, cov.skipped
	}
	next := "read each candidate test before changing the code"
	if !blast.Indexed {
		next = "run verdict index build, then re-run"
	}
	return &Response{At: st.req.TimeUTC(), Next: next, Data: data}
}

// blastCounts measures the untruncated lists; the index-derived counts are nil
// when no index answered.
func blastCounts(blast *Blast, t *treeIndex) BlastCounts {
	counts := BlastCounts{
		CoChanged: len(blast.Partners), Partners: partnerPaths(blast.Partners),
		Owners: len(blast.Owners),
	}
	if t == nil {
		return counts
	}
	counts.ChangedSymbols = new(len(blast.Symbols))
	counts.Callers = new(len(blast.Callers))
	counts.Unresolved = new(blast.unresolved)
	counts.Tests = new(len(blast.Tests))
	return counts
}

// partnerPaths counts the distinct paths a co-change list names.
func partnerPaths(rows []BlastPartnerRow) int {
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		seen[row.Path] = true
	}
	return len(seen)
}

// blastTruncated applies the row limit to every list and reports the hidden
// rows. An unmeasured list keeps a nil truncation count.
func blastTruncated(blast *Blast, limit int) BlastTruncated {
	var truncated BlastTruncated
	var hidden int
	blast.Symbols, hidden = firstRows(blast.Symbols, limit)
	if blast.Indexed {
		truncated.ChangedSymbols = new(hidden)
	}
	blast.Callers, hidden = firstRows(blast.Callers, limit)
	if blast.Indexed {
		truncated.Callers = new(hidden)
	}
	blast.Tests, hidden = firstRows(blast.Tests, limit)
	if blast.Indexed {
		truncated.Tests = new(hidden)
	}
	blast.Partners, truncated.CoChanged = firstRows(blast.Partners, limit)
	blast.Owners, truncated.Owners = firstRows(blast.Owners, limit)
	return truncated
}

// firstRows keeps the first limit rows and reports how many it hid.
func firstRows[T any](rows []T, limit int) ([]T, int) {
	if len(rows) <= limit {
		return rows, 0
	}
	return rows[:limit], len(rows) - limit
}

// blastHuman renders the measured lists, one compact block per list.
func (st *state) blastHuman(blast *Blast, tree string) {
	if !blast.Indexed {
		st.line(fmt.Sprintf(blastIndexAbsent, tree))
	} else {
		st.blastSymbolLines(blast, tree)
		st.blastCallerLines(blast)
		st.blastTestLines(blast)
	}
	st.blastPartnerLines(blast)
	st.blastOwnerLines(blast)
}

// blastSymbolLines renders the changed declarations.
func (st *state) blastSymbolLines(blast *Blast, tree string) {
	if len(blast.Symbols) == 0 {
		st.line(fmt.Sprintf(blastNoSymbols, len(blast.Input.Paths), tree))
		return
	}
	st.line(fmt.Sprintf(blastSymbolsHead, len(blast.Input.Paths), *blast.Counts.ChangedSymbols, tree))
	for _, row := range blast.Symbols {
		st.line(fmt.Sprintf("%s:%d %s %s", quotePath(row.Path), row.Line, row.Kind, row.Name))
	}
	st.showingRow("changed symbols", len(blast.Symbols), *blast.Counts.ChangedSymbols,
		*blast.Truncated.ChangedSymbols)
}

// blastCallerLines renders the callers reached within the depth cap.
func (st *state) blastCallerLines(blast *Blast) {
	st.line(fmt.Sprintf(blastCallerDepth, *blast.Counts.Callers, blastDepth, *blast.Counts.Unresolved))
	for _, row := range blast.Callers {
		st.line(fmt.Sprintf("%s:%d %s (%s, depth %d) %s", quotePath(row.Path), row.Line,
			row.Kind, row.Resolution, row.Depth, row.Symbol))
	}
	st.showingRow("callers", len(blast.Callers), *blast.Counts.Callers, *blast.Truncated.Callers)
}

// blastTestLines renders the candidate tests.
func (st *state) blastTestLines(blast *Blast) {
	st.line(fmt.Sprintf(blastTestsHead, *blast.Counts.Tests))
	for _, row := range blast.Tests {
		st.line(fmt.Sprintf("%s (%s)", quotePath(row.Path), row.Via))
	}
	st.showingRow("candidate tests", len(blast.Tests), *blast.Counts.Tests, *blast.Truncated.Tests)
}

// blastPartnerLines renders the co-change pairs and the files they name.
func (st *state) blastPartnerLines(blast *Blast) {
	st.line(fmt.Sprintf(blastPartnerHead, blast.Counts.CoChanged, blast.Counts.Partners))
	for _, row := range blast.Partners {
		st.line(fmt.Sprintf("%s (with %s, %d commit(s))", quotePath(row.Path),
			quotePath(row.Target), row.Commits))
	}
	st.showingRow("co-change pairs", len(blast.Partners), blast.Counts.CoChanged, blast.Truncated.CoChanged)
}

// blastOwnerLines renders one ownership line per changed path.
func (st *state) blastOwnerLines(blast *Blast) {
	st.line(fmt.Sprintf(blastOwnerHead, blast.Counts.Owners))
	for i := range blast.Owners {
		st.line(quotePath(blast.Owners[i].Path) + " " + blastOwnerText(&blast.Owners[i]))
	}
	st.showingRow("owner rows", len(blast.Owners), blast.Counts.Owners, blast.Truncated.Owners)
}

// showingRow names the rows a limit hid.
func (st *state) showingRow(what string, shown, total, truncated int) {
	if truncated > 0 {
		st.line(fmt.Sprintf(blastShowing, shown, total, what, truncated, MaxLimit))
	}
}

// blastOwnerText renders one path's owners without inventing an owner.
func blastOwnerText(t *Target) string {
	switch {
	case t.OwnersSource == "":
		return "no owners: no CODEOWNERS"
	case t.Owners == nil:
		return "no owner rule in " + t.OwnersSource
	case len(t.Owners) == 0:
		return "no owners: the last matching rule names none (" + t.OwnersSource + ")"
	default:
		return strings.Join(t.Owners, " ") + " (" + t.OwnersSource + ")"
	}
}
