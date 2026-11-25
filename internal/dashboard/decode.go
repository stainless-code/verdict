package dashboard

import (
	"context"

	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/jq"
)

// source is the slice of the query layer the snapshot reads. *query.Layer
// implements it; tests hand in composed views directly.
type source interface {
	Roots(ctx context.Context) *query.View
	Bundles(ctx context.Context, pattern string) (*query.View, error)
}

// snapshot is everything the Overview and Queue tabs draw, decoded once from
// the layer's views. This file owns every wire key the dashboard reads, so a
// renamed key fails here and nowhere else.
type snapshot struct {
	counts   *counts // nil when the layer withheld the totals (partial)
	at       string
	roots    []rootRow
	rows     []bundleRow
	refused  []refusedRow
	warnings []string
	partial  bool
}

type counts struct {
	bundles, open, deferred, addressed, terminal int
}

type rootRow struct {
	id, path string
	bundles  int
}

// bundleRow is one Queue row. A refused bundle keeps its row with no counts.
type bundleRow struct {
	bundle, repo, model, head           string
	open, deferred, addressed, terminal int
	refused                             bool
}

type refusedRow struct {
	bundle, code, message string
}

// load composes the two views the snapshot needs. Bundles composes every
// bundle (bounded by the layer's cap); Roots only lists.
func load(ctx context.Context, src source) (*snapshot, error) {
	bundles, err := src.Bundles(ctx, "")
	if err != nil {
		return nil, err //nolint:wrapcheck // Run renders the layer's typed refusal as is
	}
	return decode(src.Roots(ctx), bundles), nil
}

func decode(roots, bundles *query.View) *snapshot {
	s := &snapshot{at: bundles.At, warnings: bundles.Warnings}
	data := bundles.Data
	s.partial = truthy(field(data, "partial"))
	if c := asObj(field(data, "counts")); c != nil {
		s.counts = &counts{
			bundles: intField(c, "bundles"), open: intField(c, "open"), deferred: intField(c, "deferred"),
			addressed: intField(c, "addressed"), terminal: intField(c, "terminal"),
		}
	}
	for _, v := range jq.Elements(field(data, "rows")) {
		s.rows = append(s.rows, decodeRow(v))
	}
	for _, v := range jq.Elements(field(data, "errors")) {
		o := asObj(v)
		s.refused = append(s.refused, refusedRow{
			bundle: strField(o, "bundle"), code: strField(o, "code"), message: strField(o, "message"),
		})
	}
	for _, v := range jq.Elements(field(roots.Data, "roots")) {
		o := asObj(v)
		s.roots = append(s.roots, rootRow{id: strField(o, "id"), path: strField(o, "path"), bundles: intField(o, "bundles")})
	}
	return s
}

func decodeRow(v any) bundleRow {
	o := asObj(v)
	row := bundleRow{
		bundle: strField(o, "bundle"), repo: strField(o, "repo"),
		model: strField(o, "model"), head: strField(o, "head"),
	}
	open, ok := jq.IntOf(field(o, "open"))
	if !ok {
		row.refused = true
		return row
	}
	row.open = open
	row.deferred = intField(o, "deferred")
	row.addressed = intField(o, "addressed")
	row.terminal = intField(o, "terminal")
	return row
}

// field reads one key; a missing object or key reads as null.
func field(o *jq.Obj, key string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	return v
}

func strField(o *jq.Obj, key string) string {
	if s, ok := field(o, key).(string); ok {
		return s
	}
	return ""
}

// asObj reads v as an object; anything else (null included) is nil.
func asObj(v any) *jq.Obj {
	if o, ok := v.(*jq.Obj); ok {
		return o
	}
	return nil
}

func intField(o *jq.Obj, key string) int {
	n, _ := jq.IntOf(field(o, key))
	return n
}

func truthy(v any) bool {
	b, ok := v.(bool)
	return ok && b
}
