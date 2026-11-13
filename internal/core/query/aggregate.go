package query

import (
	"context"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

type aggregateCollector interface {
	addTruncation(reason string)
	addBundle(layer *Layer, root Root, bundle string, known map[string]bool)
	addCollectionIssue(partial bool, errs []ErrRow)
}

type aggregateState struct {
	cutReason string
	errs      []ErrRow
	partial   bool
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
	if layer.discovery.Truncated {
		collector.addCollectionIssue(true, nil)
		collector.addTruncation("scan_cap")
	}
	for _, root := range layer.roots {
		collectRoot(ctx, layer, collector, root)
	}
}

func collectRoot(ctx context.Context, layer *Layer, collector aggregateCollector, root Root) {
	if ctx.Err() != nil {
		collector.addTruncation("time_budget")
		collector.addCollectionIssue(true, nil)
		return
	}
	names, cut, errs := layer.listBundles(root)
	if cut {
		collector.addTruncation("scan_cap")
	}
	collector.addCollectionIssue(cut || len(errs) > 0, errs)
	known := bundleSet(names)
	for _, bundle := range names {
		if ctx.Err() != nil {
			collector.addTruncation("time_budget")
			collector.addCollectionIssue(true, nil)
			break
		}
		collector.addBundle(layer, root, bundle, known)
	}
}

func (a *aggregateState) addTruncation(reason string) { a.cutReason = reason }
func (a *aggregateState) receipt(data *jq.Obj, returned int) {
	if a.cutReason != "" {
		core.ResultCut(a.cutReason, returned, nil, nil).Apply(data)
	}
}

func (a *aggregateState) inventoryCut(cut bool) {
	if cut {
		a.addTruncation("scan_cap")
	}
}
