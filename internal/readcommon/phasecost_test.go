package readcommon_test

import (
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/readcommon"
)

const phaseCostKnown = `{"execution_id":"execution-A","effort":"standard","est_usd":99,"phases":[{"execution_id":"execution-A","launch_id":"finder-1-discovery-1","finder":"finder-1","phase":"discovery","usd":0.3,"input_tokens":10,"output_tokens":2},{"execution_id":"execution-A","launch_id":"finder-2-discovery-1","finder":"finder-2","phase":"discovery","usd":0.4,"input_tokens":20,"output_tokens":3}]}`

func phaseCostFixture(t *testing.T, cost string) string {
	t.Helper()
	dir := t.TempDir()
	writeStore(t, dir, map[string]string{"run.json": `{"execution":{"id":"execution-A","finders":2}}`, "cost.json": cost})
	return dir
}

func TestPhaseRollupDoesNotDoubleCountRootAggregate(t *testing.T) {
	dir := phaseCostFixture(t, phaseCostKnown)
	cost := readcommon.CostRollup(dir).Cost
	for key, want := range map[string]float64{"input_tokens": 30, "output_tokens": 5, "total_tokens": 35, "est_usd": 0.7, "runs": 2} {
		if got := costNum(t, cost, key); got != want {
			t.Fatalf("%s = %v; want %v", key, got, want)
		}
	}
	if got, _ := cost.Get("cached_input_tokens"); got != nil {
		t.Fatalf("unreported cache = %v", got)
	}
}

func TestPhaseRollupMissingHandoffStaysUnknown(t *testing.T) {
	dir := phaseCostFixture(t, `{"est_usd":0,"models":[{"name":"same-model","input_tokens":0,"output_tokens":0}]}`)
	cost := readcommon.CostRollup(dir).Cost
	for _, key := range []string{"input_tokens", "output_tokens", "total_tokens", "est_usd"} {
		if got, present := cost.Get(key); !present || got != nil {
			t.Fatalf("%s = %v present=%v", key, got, present)
		}
	}
}

func TestPhaseRollupIncludesUnpricedCrossLaunch(t *testing.T) {
	dir := phaseCostFixture(t, phaseCostKnown)
	writeStore(t, dir, map[string]string{filepath.Join("x-cross-1", "launcher.json"): `{"started_at":"2026-11-20T12:00:00Z"}`})
	cost := readcommon.CostRollup(dir).Cost
	if got := costNum(t, cost, "runs"); got != 3 {
		t.Fatalf("runs = %v", got)
	}
	for _, key := range []string{"input_tokens", "est_usd"} {
		if got, _ := cost.Get(key); got != nil {
			t.Fatalf("unpriced cross %s = %v", key, got)
		}
	}
}

func TestPhaseRollupRejectsRepeatedLaunchAttribution(t *testing.T) {
	dir := phaseCostFixture(t, phaseCostKnown)
	writeStore(t, dir, map[string]string{filepath.Join("x-finder-1", "cost.json"): phaseCostKnown})
	result := readcommon.CostRollup(dir)
	if got, _ := result.Cost.Get("est_usd"); got != nil || len(result.Warns) != 1 {
		t.Fatalf("repeated phases: cost=%v warnings=%v", got, result.Warns)
	}
}

func TestPhaseRollupKeepsUnknownSpendForUnreadableCrossLauncher(t *testing.T) {
	dir := phaseCostFixture(t, phaseCostKnown)
	writeStore(t, dir, map[string]string{filepath.Join("x-cross-1", "launcher.json"): `{`})
	cost := readcommon.CostRollup(dir).Cost
	if got := costNum(t, cost, "runs"); got != 3 {
		t.Fatalf("runs = %v", got)
	}
	if got, _ := cost.Get("est_usd"); got != nil {
		t.Fatalf("unreadable cross launcher priced = %v", got)
	}
}

func TestPhaseRollupCountsLaunchedRoundsWithoutReviews(t *testing.T) {
	dir := phaseCostFixture(t, phaseCostKnown)
	writeStore(t, dir, map[string]string{
		filepath.Join("x-finder-1", "launcher.json"):                 `{"attempts":[{"round":1}]}`,
		filepath.Join("x-finder-1", ".run.rounds", "01.out"):         "round one",
		filepath.Join("x-finder-1", ".run.rounds", "01.review.json"): `{}`,
		filepath.Join("x-finder-1", ".run.rounds", "02.out"):         "round two output, no review",
	})
	cost := readcommon.CostRollup(dir).Cost
	if got := costNum(t, cost, "runs"); got != 3 {
		t.Fatalf("runs = %v", got)
	}
	if got, _ := cost.Get("est_usd"); got != nil {
		t.Fatalf("unreviewed repair round priced = %v", got)
	}
}
