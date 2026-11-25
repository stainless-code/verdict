package dashboard

import (
	"context"
	"errors"

	"github.com/stainless-code/verdict/internal/core/query"
)

// pickTarget is the job the current state wants: its view, plus the [repo]
// filter the default bundle is chosen under.
func pickTarget(st *frameState) *want {
	w := st.want()
	w.repo = st.repo
	return &w
}

// needsBundle reports whether a tab shows one bundle.
func needsBundle(t tab) bool {
	return t == tabDetail || t == tabDiff || t == tabCompare
}

// firstBundle is the default target: the first bundle the layer could read.
func firstBundle(s *snapshot) target {
	for _, r := range s.rows {
		if !r.refused {
			return target{root: r.root, bundle: r.bundle}
		}
	}
	return target{}
}

// errNoView means the tab has no layer view to load: the Overview and
// Queue draw from the snapshot, and Compare waits for a baseline.
var errNoView error = startError("dashboard: no view for this tab")

// runJob composes one view: the snapshot (a layer cache hit unless a change
// evicted it), then the active tab's one layer method. It runs off the loop
// goroutine and touches no UI state.
func runJob(ctx context.Context, src source, w *want) result {
	snap, err := load(ctx, src)
	if err != nil {
		return result{want: *w, err: err}
	}
	if needsBundle(w.tab) && w.target.bundle == "" {
		w.target = firstBundle(filterRepo(snap, w.repo))
	}
	view, err := loadView(ctx, src, w)
	return result{snap: snap, view: view, err: err, want: *w}
}

// loadView calls the one layer method behind a tab (RESEARCH §3.2).
func loadView(ctx context.Context, src source, w *want) (*query.View, error) {
	call := viewCall(src, w)
	if call == nil {
		return nil, errNoView
	}
	view, err := call(ctx)
	return view, err
}

// viewCall picks the layer method for the wanted tab, or nil when the tab
// has nothing to load.
func viewCall(src source, w *want) func(context.Context) (*query.View, error) {
	if needsBundle(w.tab) {
		return bundleCall(src, w)
	}
	switch w.tab {
	case tabCost:
		return func(ctx context.Context) (*query.View, error) { return src.Cost(ctx, w.by) }
	case tabPrecision:
		return func(ctx context.Context) (*query.View, error) { return src.Precision(ctx, w.by) }
	default:
		return nil
	}
}

// bundleCall is the Detail, Diff, or Compare read for the target bundle.
func bundleCall(src source, w *want) func(context.Context) (*query.View, error) {
	root, bundle := w.target.root, w.target.bundle
	switch {
	case bundle == "":
		return nil
	case w.tab == tabDetail:
		return func(ctx context.Context) (*query.View, error) { return src.Detail(ctx, root, bundle) }
	case w.tab == tabDiff:
		return func(ctx context.Context) (*query.View, error) { return src.Diff(ctx, root, bundle) }
	case w.baseline != "":
		return func(ctx context.Context) (*query.View, error) { return src.Compare(ctx, root, bundle, w.baseline) }
	default:
		return nil
	}
}

// installView decodes a job's tab view into the state. A refusal for the
// view replaces the tab's body with its message; the rest of the dashboard
// keeps working.
func installView(st *frameState, w *want, view *query.View, err error) {
	if st.target.bundle == "" {
		st.target = w.target
	}
	st.tabErr = ""
	if errors.Is(err, errNoView) {
		return
	}
	if err != nil {
		st.tabErr = w.target.bundle + ": " + err.Error()
		return
	}
	switch w.tab {
	case tabDetail:
		st.detail = decodeDetail(view)
	case tabDiff:
		st.diff = decodeDiff(view)
	case tabCompare:
		st.cmp = decodeCompare(view)
	case tabCost:
		st.cost = decodeCost(view)
	case tabPrecision:
		st.prec = decodePrecision(view)
	default:
	}
}
