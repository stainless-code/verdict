package query

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/jq"
)

// The serveData key vocabulary, shared by every composed view.
const (
	keyRoot      = "root"
	keyBundle    = "bundle"
	keyDir       = "dir"
	keyRepo      = "repo"
	keyPr        = "pr"
	keyModel     = "model"
	keyHead      = "head"
	keyOpen      = "open"
	keyDeferred  = "deferred"
	keyAddressed = "addressed"
	keyTerminal  = "terminal"
	keyPartial   = "partial"
	keySources   = "sources"
	keyErrors    = "errors"
	keyRows      = "rows"
	keyCounts    = "counts"
	keyReport    = "report"
	keyGate      = "gate"
	keyThreads   = "threads"
	keyArtifacts = "artifacts"
)

// notFoundError is the layer's sentinel for a root or bundle outside the
// served set; the transport maps it to 404.
type notFoundError struct{}

// Error renders the sentinel.
func (notFoundError) Error() string { return "serve: not found" }

// ErrNotFound addresses a root or bundle outside the served set.
var ErrNotFound error = notFoundError{}

// Health reports the process's fixed posture: the version, the roots it
// serves, the (absent) watcher, the cache size, and read-only.
func (l *Layer) Health(_ context.Context) *View {
	view := l.newView()
	roots := make([]any, 0, len(l.roots))
	for _, root := range l.roots {
		names, _, _ := l.listBundles(root)
		roots = append(roots, jq.NewObj("id", root.ID, "path", root.Path, "bundles", len(names)))
	}
	view.Data = jq.NewObj(
		"version", l.version,
		"roots", roots,
		"watch", false,
		"interval", "0s",
		"cache", jq.NewObj("entries", l.cacheEntries()),
		"read_only", true,
		keyPartial, false,
		keySources, []any{},
		keyErrors, []any{},
	)
	return view
}

// Roots lists the served roots with their bundle counts. Discovery is S2:
// every root here is the one the process was started with.
func (l *Layer) Roots(_ context.Context) *View {
	view := l.newView()
	roots := make([]any, 0, len(l.roots))
	var errs []ErrRow
	truncated := false
	for _, root := range l.roots {
		names, cut, lerrs := l.listBundles(root)
		errs = append(errs, lerrs...)
		truncated = truncated || cut
		roots = append(roots, jq.NewObj("id", root.ID, "path", root.Path, "bundles", len(names)))
	}
	if truncated {
		view.Warnings = append(view.Warnings,
			fmt.Sprintf("serve: bundle cap %d reached; a root listing is truncated", l.maxBundles))
	}
	view.Data = jq.NewObj(
		"roots", roots,
		"truncated", truncated,
		"skipped", jq.NewObj("symlinks", 0),
		"discovery", jq.NewObj("cached", false, "scanned_at", ""),
		keyPartial, truncated || len(errs) > 0,
		keySources, []any{},
		keyErrors, errorsWire(errs),
	)
	view.Errors = errs
	return view
}

// totals is the portfolio rollup /v1/bundles publishes.
type totals struct {
	bundles   int
	open      int
	deferred  int
	addressed int
	terminal  int
}

// add folds one readable bundle into the portfolio.
func (t *totals) add(resp *core.ReportResponse) {
	t.bundles++
	t.open += resp.Data.Verdict.Open
	t.deferred += resp.Data.Verdict.Deferred
	t.addressed += resp.Data.Verdict.Addressed
	t.terminal += resp.Data.Verdict.Terminal
}

// addTotals folds another root's rollup into the portfolio.
func (t *totals) addTotals(other totals) {
	t.bundles += other.bundles
	t.open += other.open
	t.deferred += other.deferred
	t.addressed += other.addressed
	t.terminal += other.terminal
}

// wire renders the portfolio in the published key order.
func (t *totals) wire() *jq.Obj {
	return jq.NewObj(
		"bundles", t.bundles,
		"open", t.open,
		"deferred", t.deferred,
		"addressed", t.addressed,
		"terminal", t.terminal,
	)
}

// Bundles lists every bundle under the served roots, one composed row each.
// A bundle that refuses keeps its row with null counts and an errors[] entry;
// any total over the set is then null, never a silently smaller sum.
func (l *Layer) Bundles(ctx context.Context, pattern string) (*View, error) {
	view := l.newView()
	rows, errs, sum, partial := l.collectRows(ctx, pattern, view)
	rootID := ""
	if len(l.roots) == 1 {
		rootID = l.roots[0].ID
	}
	data := jq.NewObj(
		keyRoot, rootID,
		keyRows, rows,
	)
	if partial {
		data.Set(keyCounts, nil)
	} else {
		data.Set(keyCounts, sum.wire())
	}
	data.Set(keyPartial, partial)
	data.Set(keySources, sourcesWire(view.Sources))
	data.Set(keyErrors, errorsWire(errs))
	view.Data = data
	view.Errors = errs
	return view, nil
}

// collectRows folds every root's listing into the inbox view.
func (l *Layer) collectRows(
	ctx context.Context, pattern string, view *View,
) ([]any, []ErrRow, totals, bool) {
	var rows []any
	var errs []ErrRow
	var sum totals
	partial := false
	for _, root := range l.roots {
		rootRows, rootErrs, rootSum, sources, inv, cut, listErrs := l.collectRoot(ctx, root, pattern)
		rows = append(rows, rootRows...)
		errs = append(errs, listErrs...)
		errs = append(errs, rootErrs...)
		sum.addTotals(rootSum)
		view.Sources = mergeSources(view.Sources, sources)
		partial = partial || cut || inv.cut || len(rootErrs) > 0 || len(listErrs) > 0
		if cut {
			view.Warnings = append(view.Warnings,
				fmt.Sprintf("serve: bundle cap %d reached under %s; listing truncated", l.maxBundles, root.Path))
		}
		if inv.cut {
			view.Warnings = append(view.Warnings, inventoryWarning(root.Path))
		}
		view.Warnings = append(view.Warnings, inv.notes...)
	}
	return rows, errs, sum, partial
}

// collectRoot lists and composes one root's bundles.
func (l *Layer) collectRoot(
	ctx context.Context, root Root, pattern string,
) ([]any, []ErrRow, totals, []Source, inventoryReport, bool, []ErrRow) {
	names, truncated, listErrs := l.listBundles(root)
	rows, errs, sum, sources, inv := l.composeRows(ctx, root, names, pattern)
	return rows, errs, sum, sources, inv, truncated, listErrs
}

// composeRows composes the matching bundles of one root.
func (l *Layer) composeRows(
	ctx context.Context, root Root, names []string, pattern string,
) (rows []any, errs []ErrRow, sum totals, sources []Source, inv inventoryReport) {
	for _, bundle := range names {
		item, ok := l.collectBundle(ctx, root, bundle, pattern)
		if !ok {
			continue
		}
		rows = append(rows, item.row)
		sources = mergeSources(sources, item.inv.sources)
		inv.cut = inv.cut || item.inv.cut
		inv.notes = append(inv.notes, item.inv.notes...)
		if len(item.errs) > 0 {
			errs = append(errs, item.errs...)
			continue
		}
		sum.add(item.resp)
	}
	return rows, errs, sum, sources, inv
}

// bundleItem is one composed inbox row and what it folded into the view.
type bundleItem struct {
	row  any
	resp *core.ReportResponse
	errs []ErrRow
	inv  inventoryReport
}

// collectBundle composes one row; ok is false only for a name the listing
// raced away.
func (l *Layer) collectBundle(ctx context.Context, root Root, bundle, pattern string) (bundleItem, bool) {
	if pattern != "" && !BundleMatches(pattern, bundle) {
		return bundleItem{}, false
	}
	row, resp, rowErrs, inv := l.row(ctx, root, bundle)
	if row == nil {
		return bundleItem{}, false
	}
	return bundleItem{row: row, resp: resp, errs: rowErrs, inv: inv}, true
}

// row composes one inbox row: the report service's counts plus the post
// receipt's model name, or null counts and an error row when the bundle
// refuses.
func (l *Layer) row(
	ctx context.Context, root Root, bundle string,
) (any, *core.ReportResponse, []ErrRow, inventoryReport) {
	dir, ok, ref := l.resolve(root, bundle)
	if ref != nil {
		row := jq.NewObj(
			keyRoot, root.ID, keyBundle, bundle, keyDir, bundleDir(bundle),
			keyRepo, "", keyPr, numericPr(bundle), keyModel, nil,
			keyHead, nil, keyOpen, nil, keyDeferred, nil, keyAddressed, nil, keyTerminal, nil,
		)
		return row, nil, []ErrRow{errRow(root, bundle, ref.Code, ref.Message)}, inventoryReport{}
	}
	if !ok {
		return nil, nil, nil, inventoryReport{}
	}
	flags := inboxFlags()
	resp, refusal, inv := l.cached(ctx, root, bundle, dir, &flags)
	row := jq.NewObj(
		keyRoot, root.ID, keyBundle, bundle, keyDir, bundleDir(bundle),
		keyRepo, bundleRepo(dir), keyPr, numericPr(bundle), keyModel, postModel(dir),
	)
	if refusal != nil {
		row.Set(keyHead, nil)
		row.Set(keyOpen, nil)
		row.Set(keyDeferred, nil)
		row.Set(keyAddressed, nil)
		row.Set(keyTerminal, nil)
		return row, nil, []ErrRow{errRow(root, bundle, refusal.Code, refusal.Message)}, inv
	}
	row.Set(keyHead, nullableString(resp.Data.Head.Fetched))
	row.Set(keyOpen, resp.Data.Verdict.Open)
	row.Set(keyDeferred, resp.Data.Verdict.Deferred)
	row.Set(keyAddressed, resp.Data.Verdict.Addressed)
	row.Set(keyTerminal, resp.Data.Verdict.Terminal)
	return row, resp, nil, inv
}

// Detail composes one bundle's review view: the report, its gate, the thread
// feed, the artifact ledger, the sources, and any tolerated errors. A store
// refusal is the detail's own refusal envelope, never a half-empty report.
func (l *Layer) Detail(ctx context.Context, rootID, bundle string) (*View, error) {
	root, ok := l.rootByID(rootID)
	if !ok {
		return nil, ErrNotFound
	}
	dir, ok, ref := l.resolve(root, bundle)
	if ref != nil {
		return nil, ref
	}
	if !ok {
		return nil, ErrNotFound
	}
	flags := l.detailFlags()
	resp, refusal, inv := l.cached(ctx, root, bundle, dir, &flags)
	if refusal != nil {
		return nil, refusal
	}
	var errs []ErrRow
	threadsData, threadErr := l.threadView(root, bundle, dir)
	if threadErr != nil {
		errs = append(errs, *threadErr)
	}
	data := jq.NewObj(keyReport, normalizeReportData(root.Path, resp.Data))
	if resp.Data.Gate == nil {
		data.Set(keyGate, nil)
	} else {
		data.Set(keyGate, resp.Data.Gate.Wire())
	}
	data.Set(keyThreads, threadsData)
	data.Set(keyArtifacts, artifacts(bundle, inv.sources))
	data.Set(keyPartial, len(errs) > 0 || inv.cut)
	data.Set(keySources, sourcesWire(inv.sources))
	data.Set(keyErrors, errorsWire(errs))
	view := &View{
		At:       resp.At,
		Next:     "run verdict show <repo> " + bundle + " --sig <sig> to read one finding",
		Repo:     bundleRepo(dir),
		Pr:       bundle,
		Warnings: detailWarnings(resp.Warnings, bundle, inv),
		Data:     data,
		Sources:  inv.sources,
		Errors:   errs,
	}
	return view, nil
}

// inventoryWarning names the source-inventory caps that truncated a view.
func inventoryWarning(where string) string {
	return fmt.Sprintf("serve: source inventory capped at %d entries, %d MiB, or %d MiB per artifact under %s; sources[] is incomplete",
		maxInventoryEntries, maxInventoryMiB, sourceCap>>mibShift, where)
}

// detailWarnings adds the inventory warnings to a detail's composed warnings.
func detailWarnings(base []string, bundle string, inv inventoryReport) []string {
	warnings := append([]string{}, base...)
	if inv.cut {
		warnings = append(warnings, inventoryWarning("bundle "+bundle))
	}
	return append(warnings, inv.notes...)
}

// Show returns one finding through the show service, byte-identical to
// `show --json`'s data object.
func (l *Layer) Show(ctx context.Context, rootID, bundle, sig string) (*View, error) {
	root, ok := l.rootByID(rootID)
	if !ok {
		return nil, ErrNotFound
	}
	dir, ok, ref := l.resolve(root, bundle)
	if ref != nil {
		return nil, ref
	}
	if !ok {
		return nil, ErrNotFound
	}
	read := core.ReadRequest{
		Runtime:  l.runtimeFor(),
		Repo:     bundleRepo(dir),
		Pr:       bundle,
		RepoRoot: root.Path,
	}
	resp, refusal := reportcore.RunShow(ctx, &core.ShowRequest{ReadRequest: read, Sig: sig})
	if refusal != nil {
		//nolint:wrapcheck // the typed refusal is the product error the transport renders.
		return nil, refusal
	}
	var record any
	if resp.Item != nil {
		record = resp.Item.Raw
	}
	data := jq.NewObj("item", record)
	if resp.Fix != nil {
		data.Set("fix", resp.Fix.Wire())
	}
	return &View{
		At:       resp.At,
		Next:     "address via address-reviews mode, then post",
		Repo:     resp.Repo,
		Pr:       resp.Pr,
		Warnings: append([]string{}, resp.Warnings...),
		Data:     data,
	}, nil
}
