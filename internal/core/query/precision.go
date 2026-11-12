package query

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	calibratecore "github.com/stainless-code/verdict/internal/core/calibrate"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	precisionNext     = "run verdict calibrate <repo> <pr> to measure fixed"
	precisionCategory = "category"
	precisionTier     = "tier"
)

type precisionBucket struct {
	open            int
	resolvedUnfixed int
	unknown         int
}

// Precision joins findings with the stored threads feed. The join can prove
// open and resolved-unfixed outcomes, while fixed remains unmeasured without
// changed-file evidence.
func (l *Layer) Precision(ctx context.Context, by string) (*View, error) {
	if !slices.Contains([]string{precisionCategory, precisionTier}, by) {
		return nil, &core.RefusalError{Code: "bad-by", Message: fmt.Sprintf(
			"serve precision by %q is unsupported (want category or tier)", by)}
	}
	aggregate := &precisionAggregate{view: l.newView(), tier: by == precisionTier, buckets: map[string]*precisionBucket{}}
	collectBundles(ctx, l, aggregate)
	return aggregate.result(), nil
}

type precisionAggregate struct {
	view    *View
	buckets map[string]*precisionBucket
	asOf    string
	aggregateState
	stale         bool
	staleMeasured bool
	tier          bool
}

func (a *precisionAggregate) addBundle(layer *Layer, root Root, bundle string, known map[string]bool) {
	dir, ok, ref := resolveAmong(root, bundle, known)
	if ref != nil || !ok {
		a.fail(root, bundle, "corrupt-store", "bundle could not be resolved")
		return
	}
	inv := layer.inventory(root, bundle, dir)
	a.view.Sources = mergeSources(a.view.Sources, inv.sources)
	a.view.Warnings = append(a.view.Warnings, inv.notes...)
	a.partial = a.partial || inv.cut
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		code := "corrupt-store"
		if ref, ok := errors.AsType[*core.RefusalError](err); ok {
			code = ref.Code
		}
		a.fail(root, bundle, code, err.Error())
		return
	}
	feed, _, err := storedFeed(dir)
	if err != nil {
		a.fail(root, bundle, "corrupt-store", err.Error())
		return
	}
	a.observeFeed(dir, feed)
	a.addOutcomes(entries, feed)
}

func (a *precisionAggregate) addOutcomes(entries []any, feed *threadscore.File) {
	for _, row := range calibratecore.StoredOutcomes(entries, feed) {
		label := row.Category
		if a.tier {
			label = row.Severity
		}
		bucket := precisionBucketFor(a.buckets, precisionLabel(label))
		switch row.Outcome {
		case "open":
			bucket.open++
		case "resolved-unfixed":
			bucket.resolvedUnfixed++
		default:
			bucket.unknown++
		}
	}
}

func (a *precisionAggregate) observeFeed(dir string, feed *threadscore.File) {
	if feed == nil {
		return
	}
	stamp, stampErr := time.Parse(time.RFC3339, feed.FetchedAt)
	if stampErr == nil {
		a.keepOldestFeed(feed, stamp)
	}
	posted, present := readcommon.OpenBundle(dir).PostedAt()
	postTime, postErr := time.Parse(time.RFC3339, posted)
	if present && stampErr == nil && postErr == nil {
		a.staleMeasured = true
		a.stale = a.stale || stamp.Before(postTime)
	}
}

func (a *precisionAggregate) keepOldestFeed(feed *threadscore.File, stamp time.Time) {
	if a.asOf == "" {
		a.asOf = feed.FetchedAt
		return
	}
	previous, err := time.Parse(time.RFC3339, a.asOf)
	if err == nil && stamp.Before(previous) {
		a.asOf = feed.FetchedAt
	}
}

func (a *precisionAggregate) result() *View {
	var outcomes any = precisionRows(a.buckets)
	var totals any = precisionTotal(a.buckets)
	if a.partial {
		outcomes = []any{}
		totals = nil
	}
	var asOfValue any
	if a.asOf != "" {
		asOfValue = a.asOf
	}
	var staleValue any
	if a.staleMeasured {
		staleValue = a.stale
	}
	a.view.Data = jq.NewObj(
		"precision", jq.NewObj(
			"by", a.dimension(),
			"outcomes", outcomes,
			"totals", totals,
			"fixed", nil,
			"as_of", asOfValue,
			"stale", staleValue,
			"next", precisionNext,
		),
		keyPartial, a.partial,
		keySources, sourcesWire(a.view.Sources),
		keyErrors, errorsWire(a.errs),
	)
	a.view.Errors = a.errs
	a.view.Next = precisionNext
	return a.view
}

func (a *precisionAggregate) dimension() string {
	if a.tier {
		return precisionTier
	}
	return precisionCategory
}

func storedFeed(dir string) (*threadscore.File, bool, error) {
	path := dir + slashSep + threadscore.KeyThreadsJSON
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil, false, nil
	} else if err != nil {
		return nil, true, jq.Sealed("serve: inspect stored threads", err)
	}
	feed, err := threadscore.ReadFile(path)
	if err != nil {
		return nil, true, jq.Sealed("serve: read stored threads", err)
	}
	return feed, true, nil
}

func precisionLabel(label string) string {
	if label != "" && label != core.CategoryUntagged {
		return label
	}
	return "untagged"
}

func field(value any, key string) any {
	if obj, ok := value.(*jq.Obj); ok {
		v, _ := obj.Get(key)
		return v
	}
	if values, ok := value.(map[string]any); ok {
		return values[key]
	}
	return nil
}

func precisionBucketFor(buckets map[string]*precisionBucket, key string) *precisionBucket {
	if buckets[key] == nil {
		buckets[key] = &precisionBucket{}
	}
	return buckets[key]
}

func precisionRows(buckets map[string]*precisionBucket) []any {
	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	rows := make([]any, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		rows = append(rows, precisionWire(key, bucket))
	}
	return rows
}

func precisionTotal(buckets map[string]*precisionBucket) *jq.Obj {
	total := &precisionBucket{}
	for _, bucket := range buckets {
		total.open += bucket.open
		total.resolvedUnfixed += bucket.resolvedUnfixed
		total.unknown += bucket.unknown
	}
	return jq.NewObj(
		"open", total.open,
		"resolved_unfixed", total.resolvedUnfixed,
		"unknown", total.unknown,
		"fixed", nil,
	)
}

func precisionWire(dimension string, bucket *precisionBucket) *jq.Obj {
	return jq.NewObj(
		"dimension", dimension,
		"open", bucket.open,
		"resolved_unfixed", bucket.resolvedUnfixed,
		"unknown", bucket.unknown,
		"fixed", nil,
	)
}
