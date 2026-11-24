package mcp

// The dashboard query tools and resources share one read model with
// `verdict serve` and `verdict acp`: every read below
// composes through internal/core/query, root-confined and read-only. There
// is no counting, finding, or cost aggregation logic in this package: the
// tools and the verdict:// resources are thin renderings of query.View.

import (
	"context"
	"errors"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/jq"
)

// Query request shapes. Each carries only its public arguments; the query
// layer and the served root identity are wired from the session bindings
// and stay unexported, so the generated schemas never offer them.

type queryBundlesRequest struct {
	Pattern string `json:"pattern,omitempty"`
	layer   *query.Layer
	rootID  string
}

type queryFindingsRequest struct {
	Bundle string `json:"bundle"`
	layer  *query.Layer
	rootID string
}

type queryFindingRequest struct {
	Bundle string `json:"bundle"`
	Sig    string `json:"sig"`
	layer  *query.Layer
	rootID string
}

type queryCostRequest struct {
	By     string `json:"by,omitempty"`
	layer  *query.Layer
	rootID string
}

type queryHealthRequest struct {
	layer  *query.Layer
	rootID string
}

func decodeQueryBundles(req *queryBundlesRequest, a *args) {
	a.str("pattern", &req.Pattern)
}

func decodeQueryFindings(req *queryFindingsRequest, a *args) {
	a.str(argBundle, &req.Bundle)
}

func decodeQueryFinding(req *queryFindingRequest, a *args) {
	a.str(argBundle, &req.Bundle)
	a.str(argSig, &req.Sig)
}

func decodeQueryCost(req *queryCostRequest, a *args) {
	a.str("by", &req.By)
}

func decodeQueryHealth(_ *queryHealthRequest, _ *args) {}

func wireQueryBundles(_ context.Context, b *bindings, req *queryBundlesRequest) error {
	req.layer, req.rootID = queryLayerFor(b.root, b.version, b.now)
	return nil
}

func wireQueryFindings(_ context.Context, b *bindings, req *queryFindingsRequest) error {
	req.layer, req.rootID = queryLayerFor(b.root, b.version, b.now)
	return nil
}

func wireQueryFinding(_ context.Context, b *bindings, req *queryFindingRequest) error {
	req.layer, req.rootID = queryLayerFor(b.root, b.version, b.now)
	return nil
}

func wireQueryCost(_ context.Context, b *bindings, req *queryCostRequest) error {
	req.layer, req.rootID = queryLayerFor(b.root, b.version, b.now)
	return nil
}

func wireQueryHealth(_ context.Context, b *bindings, req *queryHealthRequest) error {
	req.layer, req.rootID = queryLayerFor(b.root, b.version, b.now)
	return nil
}

// queryLayerFor builds the ephemeral read model over the one served root
// and returns the root identity a detail or show call addresses. The root
// is normalized exactly as the query layer normalizes it, so the identity
// matches the layer's. The watcher stays off: a stdio process may not leak
// a background goroutine.
func queryLayerFor(root, version string, now func() time.Time) (*query.Layer, string) {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = filepath.Clean(root)
	}
	id := query.RootID(abs)
	layer := query.New(query.Snapshot{
		Version: version,
		Roots:   []query.Root{{Path: abs}},
		Runtime: &core.Runtime{Now: now},
	}, query.Options{NoWatch: true, Clock: now})
	return layer, id
}

// badGlobRefusal refuses one non-portable bundles pattern with the same
// code and wording the serve route answers.
func badGlobRefusal() *core.RefusalError {
	return &core.RefusalError{
		Code:    "bad-glob",
		Message: "pattern must be a portable repository-relative glob (no .., no absolute path, no backslash)",
	}
}

func invokeQueryBundles(ctx context.Context, req *queryBundlesRequest) (*jq.Obj, *core.RefusalError) {
	if req.Pattern != "" && !query.ValidBundleGlob(req.Pattern) {
		return nil, badGlobRefusal()
	}
	view, err := req.layer.Bundles(ctx, req.Pattern)
	if err != nil {
		return nil, queryRefusal(err)
	}
	return mergeView(view), nil
}

func invokeQueryFindings(ctx context.Context, req *queryFindingsRequest) (*jq.Obj, *core.RefusalError) {
	view, err := req.layer.Detail(ctx, req.rootID, req.Bundle)
	if err != nil {
		return nil, queryRefusal(err)
	}
	return mergeView(view), nil
}

func invokeQueryFinding(ctx context.Context, req *queryFindingRequest) (*jq.Obj, *core.RefusalError) {
	view, err := req.layer.Show(ctx, req.rootID, req.Bundle, req.Sig)
	if err != nil {
		return nil, queryRefusal(err)
	}
	return mergeView(view), nil
}

func invokeQueryCopy(ctx context.Context, req *queryFindingRequest) (*jq.Obj, *core.RefusalError) {
	view, err := req.layer.Copy(ctx, req.rootID, req.Bundle, req.Sig)
	if err != nil {
		return nil, queryRefusal(err)
	}
	return mergeView(view), nil
}

// findingURIParts counts the segments of verdict://finding/{bundle}/{sig}.
const findingURIParts = 3

// bundleURIParts counts the segments of the two-segment dashboard kinds:
// verdict://findings/{bundle}, verdict://cost/{by}, verdict://precision/{by}.
const bundleURIParts = 2

// compareURIParts counts the segments of verdict://compare/{bundle}/{baseline}.
const compareURIParts = 3

func invokeQueryCost(ctx context.Context, req *queryCostRequest) (*jq.Obj, *core.RefusalError) {
	by := req.By
	if by == "" {
		by = query.DefaultCostBy
	}
	view, err := req.layer.Cost(ctx, by)
	if err != nil {
		return nil, queryRefusal(err)
	}
	return mergeView(view), nil
}

func invokeQueryHealth(ctx context.Context, req *queryHealthRequest) (*jq.Obj, *core.RefusalError) {
	return mergeView(req.layer.Health(ctx)), nil
}

// mergeView renders one composed view as the tool data object: the view's
// data fields verbatim, plus the envelope clock, repo, pr, next step, and
// warnings the transport renders.
func mergeView(view *query.View) *jq.Obj {
	out := jq.NewObj()
	if view.Data != nil {
		out = view.Data.Clone()
	}
	out.Set("at", view.At)
	out.Set("repo", view.Repo)
	out.Set("pr", view.Pr)
	out.Set("next", view.Next)
	out.Set("warnings", core.StringValues(view.Warnings))
	return out
}

// queryRefusal maps one layer failure onto the refusal a tool returns: a
// typed refusal passes through, and a root or bundle outside the served set
// is not-found. Anything else is internal.
func queryRefusal(err error) *core.RefusalError {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	if errors.Is(err, query.ErrNotFound) {
		return &core.RefusalError{
			Code:    "not-found",
			Message: "no such bundle in this served root",
		}
	}
	return &core.RefusalError{Code: "internal", Message: "query failed"}
}

// The verdict:// dashboard resources. Static reads list in resources/list;
// per-bundle reads are templates in resources/templates/list. The URI is
// validated before any layer call, so a traversal or foreign scheme never
// reaches the served root, and the layer confines every bundle it reads.

const (
	kindRoots     = "roots"
	kindBundles   = "bundles"
	kindHealth    = "health"
	kindCost      = "cost"
	kindFindings  = "findings"
	kindFinding   = "finding"
	kindCompare   = "compare"
	kindPrecision = "precision"
)

// queryURI is one validated dashboard resource address.
type queryURI struct {
	kind     string
	bundle   string
	sig      string
	baseline string
	by       string
}

// parseQueryURI validates one dashboard resource URI strictly: only the
// verdict:// scheme, no query/fragment escapes or backslashes, and an exact
// known shape. An unknown shape, a dot segment, or an unaddressable sig is
// not a resource, and the transport answers resource-not-found.
func parseQueryURI(uri string) (*queryURI, bool) {
	rest, ok := strings.CutPrefix(uri, "verdict://")
	if !ok || rest == "" || strings.ContainsAny(rest, "?#%\\") {
		return nil, false
	}
	if path.Clean(rest) != rest {
		return nil, false
	}
	return matchQueryURI(strings.Split(rest, "/"))
}

// matchQueryURI maps one clean URI shape onto its kind.
func matchQueryURI(parts []string) (*queryURI, bool) {
	switch parts[0] {
	case kindRoots:
		return fixedQueryURI(parts, kindRoots)
	case kindBundles:
		return fixedQueryURI(parts, kindBundles)
	case kindHealth:
		return fixedQueryURI(parts, kindHealth)
	case kindCost:
		return costQueryURI(parts)
	case kindPrecision:
		return precisionQueryURI(parts)
	case kindFindings:
		return bundleQueryURI(parts, kindFindings)
	case kindFinding:
		return findingQueryURI(parts)
	case kindCompare:
		return compareQueryURI(parts)
	default:
		return nil, false
	}
}

func fixedQueryURI(parts []string, kind string) (*queryURI, bool) {
	if len(parts) != 1 {
		return nil, false
	}
	return &queryURI{kind: kind}, true
}

func costQueryURI(parts []string) (*queryURI, bool) {
	if len(parts) == 1 {
		return &queryURI{kind: kindCost, by: query.DefaultCostBy}, true
	}
	if len(parts) == bundleURIParts && slices.Contains(query.CostDimensions(), parts[1]) {
		return &queryURI{kind: kindCost, by: parts[1]}, true
	}
	return nil, false
}

func precisionQueryURI(parts []string) (*queryURI, bool) {
	if len(parts) == bundleURIParts && slices.Contains(query.PrecisionDimensions(), parts[1]) {
		return &queryURI{kind: kindPrecision, by: parts[1]}, true
	}
	return nil, false
}

func bundleQueryURI(parts []string, kind string) (*queryURI, bool) {
	if len(parts) != bundleURIParts {
		return nil, false
	}
	return &queryURI{kind: kind, bundle: parts[1]}, true
}

func findingQueryURI(parts []string) (*queryURI, bool) {
	if len(parts) != findingURIParts || !query.ValidSig(parts[2]) {
		return nil, false
	}
	return &queryURI{kind: kindFinding, bundle: parts[1], sig: parts[2]}, true
}

func compareQueryURI(parts []string) (*queryURI, bool) {
	if len(parts) != compareURIParts {
		return nil, false
	}
	return &queryURI{kind: kindCompare, bundle: parts[1], baseline: parts[2]}, true
}

// queryResourceView composes one validated dashboard address through the
// query layer, the same reads the query tools render.
func queryResourceView(ctx context.Context, layer *query.Layer, rootID string, q *queryURI) (*query.View, error) {
	switch q.kind {
	case kindRoots:
		return layer.Roots(ctx), nil
	case kindBundles:
		return layer.Bundles(ctx, "") //nolint:wrapcheck // the layer error is the refusal the transport renders.
	case kindHealth:
		return layer.Health(ctx), nil
	case kindCost:
		return layer.Cost(ctx, q.by) //nolint:wrapcheck // the layer error is the refusal the transport renders.
	case kindPrecision:
		return layer.Precision(ctx, q.by) //nolint:wrapcheck // the layer error is the refusal the transport renders.
	case kindFindings:
		return layer.Detail(ctx, rootID, q.bundle) //nolint:wrapcheck // the layer error is the refusal the transport renders.
	case kindFinding:
		return layer.Show(ctx, rootID, q.bundle, q.sig) //nolint:wrapcheck // the layer error is the refusal the transport renders.
	case kindCompare:
		return layer.Compare(ctx, rootID, q.bundle, q.baseline) //nolint:wrapcheck // the layer error is the refusal the transport renders.
	default:
		return nil, query.ErrNotFound
	}
}
