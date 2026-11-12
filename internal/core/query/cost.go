package query

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	costNext         = "run verdict record <repo> <pr> --cost to record spend"
	costUnattributed = "unattributed"
	costRoundScale   = 10000
)

type costBucket struct {
	runs      int
	modelRuns int
	input     float64
	output    float64
	est       float64
	estKnown  bool
}

// Cost composes cost records from every served bundle by repository, ISO week,
// or model. A bad store invalidates aggregate numbers instead of making a
// smaller sum look complete.
func (l *Layer) Cost(ctx context.Context, by string) (*View, error) {
	if !slices.Contains([]string{"repo", "week", keyModel}, by) {
		return nil, &core.RefusalError{Code: "bad-by", Message: fmt.Sprintf(
			"serve cost by %q is unsupported (want repo, week, or model)", by)}
	}
	aggregate := &costAggregate{view: l.newView(), by: by, buckets: map[string]*costBucket{}}
	collectBundles(ctx, l, aggregate)
	return aggregate.result(), nil
}

type costAggregate struct {
	view    *View
	buckets map[string]*costBucket
	by      string
	aggregateState
}

func (a *costAggregate) addBundle(layer *Layer, root Root, bundle string, known map[string]bool) {
	dir, ok, ref := resolveAmong(root, bundle, known)
	if ref != nil || !ok {
		a.fail(root, bundle, "corrupt-store", "bundle could not be resolved")
		return
	}
	inv := layer.inventory(root, bundle, dir)
	a.view.Sources = mergeSources(a.view.Sources, inv.sources)
	a.view.Warnings = append(a.view.Warnings, inv.notes...)
	a.partial = a.partial || inv.cut
	records, bad, present := costRecords(dir)
	if bad {
		a.fail(root, bundle, "corrupt-store", "cost record is unreadable")
		return
	}
	if !present {
		a.fail(root, bundle, "missing-cost", "cost record is absent")
		return
	}
	if a.by == keyModel {
		addModelRecords(a.buckets, records)
		return
	}
	rollup := readcommon.CostTotal(dir)
	if len(rollup.Warns) > 0 {
		a.fail(root, bundle, "corrupt-store", strings.Join(rollup.Warns, "; "))
		return
	}
	addRollupBucket(a.buckets, costLabel(a.by, bundleRepo(dir), dir), rollup.Cost, records)
}

func (a *costAggregate) result() *View {
	rows := costRows(a.buckets, a.by, a.partial)
	var totals any = costTotals(a.buckets, a.by)
	if a.partial {
		totals = nil
		rows = []any{}
	}
	a.view.Data = jq.NewObj(
		"cost", jq.NewObj("by", a.by, "rows", rows, "totals", totals),
		keyPartial, a.partial,
		keySources, sourcesWire(a.view.Sources),
		keyErrors, errorsWire(a.errs),
	)
	a.view.Errors = a.errs
	a.view.Next = costNext
	return a.view
}

func addModelRecords(buckets map[string]*costBucket, records []any) {
	for _, record := range records {
		models, _ := readcommon.CostModels(record)
		if len(models) == 0 {
			costBucketFor(buckets, costUnattributed).runs++
			addCostRecord(buckets, costUnattributed, nil)
			continue
		}
		addNamedModels(buckets, models)
	}
}

func addNamedModels(buckets map[string]*costBucket, models []any) {
	seen := map[string]bool{}
	for _, model := range models {
		name, ok := field(model, "name").(string)
		if !ok || name == "" {
			name = costUnattributed
		}
		if !seen[name] {
			costBucketFor(buckets, name).runs++
			seen[name] = true
		}
		addCostRecord(buckets, name, []any{model})
	}
}

func costRecords(dir string) ([]any, bool, bool) {
	var records []any
	presentAny := false
	files := readcommon.CostFiles(dir)
	for _, file := range files {
		recs, present, valid := readcommon.CostFileRecords(file)
		if !valid {
			return nil, true, true
		}
		if present {
			presentAny = true
			records = append(records, recs...)
		}
	}
	return records, false, presentAny
}

func addRollupBucket(buckets map[string]*costBucket, label string, rollup *jq.Obj, records []any) {
	bucket := costBucketFor(buckets, label)
	runs, _ := jq.IntOf(field(rollup, "runs"))
	bucket.runs += runs
	input, _ := readcommon.NumVal(field(rollup, "input_tokens"))
	output, _ := readcommon.NumVal(field(rollup, "output_tokens"))
	bucket.input += input
	bucket.output += output
	est, ok := readcommon.NumVal(field(rollup, "est_usd"))
	if ok {
		bucket.est += est
		bucket.estKnown = true
	}
	for _, record := range records {
		models, _ := readcommon.CostModels(record)
		bucket.modelRuns += len(models)
	}
}

func costLabel(by, repo, dir string) string {
	switch by {
	case "repo":
		if repo == "" {
			return costUnattributed
		}
		return repo
	case "week":
		posted, ok := readcommon.OpenBundle(dir).PostedAt()
		stamp, err := time.Parse(time.RFC3339, posted)
		if !ok || err != nil {
			return "undated"
		}
		year, week := stamp.UTC().ISOWeek()
		return fmt.Sprintf("%04d-W%02d", year, week)
	default:
		return ""
	}
}

func addCostRecord(buckets map[string]*costBucket, name string, models []any) {
	bucket := costBucketFor(buckets, name)
	bucket.modelRuns += len(models)
	for _, model := range models {
		input, _ := readcommon.NumVal(field(model, "input_tokens"))
		output, _ := readcommon.NumVal(field(model, "output_tokens"))
		bucket.input += input
		bucket.output += output
	}
}

func costBucketFor(buckets map[string]*costBucket, key string) *costBucket {
	if buckets[key] == nil {
		buckets[key] = &costBucket{}
	}
	return buckets[key]
}

func costRows(buckets map[string]*costBucket, by string, partial bool) []any {
	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	rows := make([]any, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		rows = append(rows, jq.NewObj(
			"dimension", key,
			"runs", bucket.runs,
			"model_runs", bucket.modelRuns,
			keyInputTokens, roundCost(bucket.input),
			keyOutputTokens, roundCost(bucket.output),
			"total_tokens", roundCost(bucket.input+bucket.output),
			"est_usd", costEstimate(by, partial, bucket),
		))
	}
	return rows
}

func costEstimate(by string, partial bool, bucket *costBucket) any {
	if by == keyModel || partial || !bucket.estKnown {
		return nil
	}
	return roundCost(bucket.est)
}

func costTotals(buckets map[string]*costBucket, by string) *jq.Obj {
	total := &costBucket{}
	for _, bucket := range buckets {
		total.runs += bucket.runs
		total.modelRuns += bucket.modelRuns
		total.input += bucket.input
		total.output += bucket.output
		total.est += bucket.est
		total.estKnown = total.estKnown || bucket.estKnown
	}
	var est any
	if by != keyModel && total.estKnown {
		est = roundCost(total.est)
	}
	return jq.NewObj(
		"runs", total.runs,
		"model_runs", total.modelRuns,
		keyInputTokens, roundCost(total.input),
		keyOutputTokens, roundCost(total.output),
		"total_tokens", roundCost(total.input+total.output),
		"est_usd", est,
	)
}

func roundCost(n float64) float64 {
	return math.Round(n*costRoundScale) / costRoundScale
}
