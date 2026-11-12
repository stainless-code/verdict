package query

import "context"

type aggregateCollector interface {
	addBundle(layer *Layer, root Root, bundle string, names []string)
	addCollectionIssue(partial bool, errs []ErrRow)
}

type aggregateState struct {
	errs    []ErrRow
	partial bool
}

func (a *aggregateState) addCollectionIssue(partial bool, errs []ErrRow) {
	a.partial = a.partial || partial
	a.errs = append(a.errs, errs...)
}

func (a *aggregateState) fail(root Root, bundle, code, message string) {
	a.partial = true
	a.errs = append(a.errs, errRow(root, bundle, code, message))
}

func collectBundles(ctx context.Context, layer *Layer, collector aggregateCollector) {
	for _, root := range layer.roots {
		collectRoot(ctx, layer, collector, root)
	}
}

func collectRoot(ctx context.Context, layer *Layer, collector aggregateCollector, root Root) {
	if ctx.Err() != nil {
		collector.addCollectionIssue(true, nil)
		return
	}
	names, cut, errs := layer.listBundles(root)
	collector.addCollectionIssue(cut || len(errs) > 0, errs)
	for _, bundle := range names {
		if ctx.Err() != nil {
			collector.addCollectionIssue(true, nil)
			break
		}
		collector.addBundle(layer, root, bundle, names)
	}
}
