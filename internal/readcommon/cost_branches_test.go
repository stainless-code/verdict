package readcommon_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// TestCostRollupUnpricedAndModellessRecords pins the two cheap rollup gates
// on a mixed store: a record with no models is a run, not a priced run
// (runs_with_cost counts only records that carry models), and a store where
// no record was priced keeps est_usd null instead of inventing a $0 estimate.
// Both totals are also checked per tier and at the top.
func TestCostRollupUnpricedAndModellessRecords(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeStore(t, dir, map[string]string{
		"cost.json": `[{"schema_version":"1","effort":"cheap",` +
			`"models":[{"name":"m","input_tokens":100,"output_tokens":200}],` +
			`"est_usd":0.1},` +
			`{"schema_version":"1","effort":"standard"}]`,
	})
	res := readcommon.CostRollup(dir)
	for key, want := range map[string]float64{
		"runs": 2, "runs_with_cost": 1,
		"input_tokens": 100, "output_tokens": 200, "total_tokens": 300,
	} {
		if got := costNum(t, res.Cost, key); got != want {
			t.Errorf("cost.%s = %v; want %v", key, got, want)
		}
	}
	if got := costTier(t, res.Cost, "cheap", "total_tokens"); got != 300 {
		t.Errorf("cheap.total_tokens = %v; want 300", got)
	}
	if got := costTier(t, res.Cost, "standard", "total_tokens"); got != 0 {
		t.Errorf("standard.total_tokens = %v; want 0", got)
	}
	if len(res.Warns) != 0 {
		t.Errorf("warns = %v; want none", res.Warns)
	}
}

// TestCostRollupEstUSDLNullWhenUnpriced pins the estimate sentinel: records
// that carry tokens but no priced total leave est_usd null, so a reader can
// tell "not measured" from "measured at zero".
func TestCostRollupEstUSDLNullWhenUnpriced(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeStore(t, dir, map[string]string{
		"cost.json": `[{"schema_version":"1","effort":"cheap",` +
			`"models":[{"name":"m","input_tokens":5,"output_tokens":5}]}]`,
	})
	res := readcommon.CostRollup(dir)
	if got := costNum(t, res.Cost, "total_tokens"); got != 10 {
		t.Errorf("total_tokens = %v; want 10", got)
	}
	if v, _ := res.Cost.Get("est_usd"); v != nil {
		t.Errorf("est_usd = %v; want null with no priced record", v)
	}
}

// TestCostModelsNullRecordHasNoModels pins the exported shape gate's null
// posture: a null record carries no models and is not a shape failure, so a
// caller reading the list cannot mistake padding for corruption.
func TestCostModelsNullRecordHasNoModels(t *testing.T) {
	t.Parallel()
	models, ok := readcommon.CostModels(nil)
	if !ok || len(models) != 0 {
		t.Fatalf("CostModels(nil) = %v/%v; want no models and a passing shape", models, ok)
	}
}

// TestCostRollupNullModelElement pins the models shape gate as "null is
// skipped, not fatal": a null element in a models list is padding the
// recorder can leave behind, so the record still rolls up. Treating it as a
// shape failure zeroes the whole store and warns, which reads as "no cost
// data" on a run that had cost.
func TestCostRollupNullModelElement(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeStore(t, dir, map[string]string{
		"cost.json": `[{"schema_version":"1","effort":"deep",` +
			`"models":[null,{"name":"m","input_tokens":3,"output_tokens":4}],` +
			`"est_usd":0.02}]`,
	})
	res := readcommon.CostRollup(dir)
	if len(res.Warns) != 0 {
		t.Fatalf("warns = %v; want none", res.Warns)
	}
	for key, want := range map[string]float64{
		"runs": 1, "runs_with_cost": 1, "input_tokens": 3,
		"output_tokens": 4, "total_tokens": 7,
	} {
		if got := costNum(t, res.Cost, key); got != want {
			t.Errorf("cost.%s = %v; want %v", key, got, want)
		}
	}
}
