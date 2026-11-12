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
	costNext         = "write measured spend to the bundle's cost.json, then re-run"
	costUnattributed = "unattributed"
	costRoundScale   = 10000
)

type costBucket struct {
	runs          int
	modelRuns     int
	input         float64
	output        float64
	est           float64
	inputUnknown  bool
	outputUnknown bool
	estKnown      bool
	estUnknown    bool
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
	rows := costRows(a.buckets, a.by)
	var totals any = costTotals(a.buckets, a.by)
	if a.partial {
		totals = nil
		rows = []any{}
	}
	partial := a.partial || a.unknownMeasurements()
	a.view.Data = jq.NewObj(
		"cost", jq.NewObj("by", a.by, "rows", rows, "totals", totals),
		keyPartial, partial,
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
	for _, record := range records {
		models, _ := readcommon.CostModels(record)
		bucket.modelRuns += len(models)
		addCostMeasurements(bucket, models)
		est, ok := readcommon.CostEstimate(record)
		if ok {
			bucket.est += est
			bucket.estKnown = true
		} else {
			bucket.estUnknown = true
		}
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
	addCostMeasurements(bucket, models)
}

func addCostMeasurements(bucket *costBucket, models []any) {
	for _, model := range models {
		input, inputKnown := readcommon.NumVal(field(model, "input_tokens"))
		output, outputKnown := readcommon.NumVal(field(model, "output_tokens"))
		if inputKnown {
			bucket.input += input
		} else {
			bucket.inputUnknown = true
		}
		if outputKnown {
			bucket.output += output
		} else {
			bucket.outputUnknown = true
		}
	}
}

func costBucketFor(buckets map[string]*costBucket, key string) *costBucket {
	if buckets[key] == nil {
		buckets[key] = &costBucket{}
	}
	return buckets[key]
}

func costRows(buckets map[string]*costBucket, by string) []any {
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
			keyInputTokens, measuredCost(bucket.input, bucket.inputUnknown),
			keyOutputTokens, measuredCost(bucket.output, bucket.outputUnknown),
			"total_tokens", measuredCost(bucket.input+bucket.output, bucket.inputUnknown || bucket.outputUnknown),
			"est_usd", costEstimate(by, bucket),
		))
	}
	return rows
}

func costEstimate(by string, bucket *costBucket) any {
	if by == keyModel || !bucket.estKnown || bucket.estUnknown {
		return nil
	}
	return roundCost(bucket.est)
}

func measuredCost(value float64, unknown bool) any {
	if unknown {
		return nil
	}
	return roundCost(value)
}

func costTotals(buckets map[string]*costBucket, by string) *jq.Obj {
	total := sumCostBuckets(buckets)
	return jq.NewObj(
		"runs", total.runs,
		"model_runs", total.modelRuns,
		keyInputTokens, measuredCost(total.input, total.inputUnknown),
		keyOutputTokens, measuredCost(total.output, total.outputUnknown),
		"total_tokens", measuredCost(total.input+total.output, total.inputUnknown || total.outputUnknown),
		"est_usd", costEstimate(by, total),
	)
}

func sumCostBuckets(buckets map[string]*costBucket) *costBucket {
	total := &costBucket{}
	for _, bucket := range buckets {
		total.runs += bucket.runs
		total.modelRuns += bucket.modelRuns
		total.input += bucket.input
		total.output += bucket.output
		total.est += bucket.est
		total.inputUnknown = total.inputUnknown || bucket.inputUnknown
		total.outputUnknown = total.outputUnknown || bucket.outputUnknown
		total.estKnown = total.estKnown || bucket.estKnown
		total.estUnknown = total.estUnknown || bucket.estUnknown
	}
	return total
}

func (a *costAggregate) unknownMeasurements() bool {
	for _, bucket := range a.buckets {
		if bucket.inputUnknown || bucket.outputUnknown || (a.by != keyModel && bucket.estUnknown) {
			return true
		}
	}
	return false
}

func roundCost(n float64) float64 {
	return math.Round(n*costRoundScale) / costRoundScale
}
