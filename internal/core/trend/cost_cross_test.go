package trendcore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const costFixture = `[
  {"schema_version":"1","effort":"cheap","models":[{"name":"m-cheap"}],"est_usd":0.5},
  {"schema_version":"1","effort":"deep","models":[{"name":"m-deep"},{"name":"m-extra"}],"est_usd":2.25}
]
`

// TestCostReadersAgree proves the cost store has one reading: the trend
// rollup (bundleModels/bundleEfforts), the provenance footer (Spend,
// CostModel), and the read records all see the same tier, model list, and
// estimate sum.
func TestCostReadersAgree(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	seedCostBundle(t, dir)
	records := readcommon.CostRecords(costBundle)
	if len(records) != 2 {
		t.Fatalf("CostRecords = %d records; want 2", len(records))
	}
	checkTrendCostLists(t)
	checkProvenanceCost(t, records)
	checkRollupCost(t)
}

const costBundle = ".review/7/"

func seedCostBundle(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".review", "7"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".review", "7", readcommon.CostFile)
	if err := os.WriteFile(path, []byte(costFixture), 0o600); err != nil {
		t.Fatal(err)
	}
}

func checkTrendCostLists(t *testing.T) {
	t.Helper()
	if got, want := bundleModels(costBundle), []any{"m-cheap", "m-deep", "m-extra"}; !equalAny(got, want) {
		t.Errorf("bundleModels = %v; want %v", got, want)
	}
	if got, want := bundleEfforts(costBundle), []any{"cheap", "deep"}; !equalAny(got, want) {
		t.Errorf("bundleEfforts = %v; want %v", got, want)
	}
}

func checkProvenanceCost(t *testing.T, records []any) {
	t.Helper()
	tier, spend := provenance.Spend(costBundle)
	if want := readcommon.CostTier(records[len(records)-1]); tier != want {
		t.Errorf("Spend tier = %q; want the reader's tail tier %q", tier, want)
	}
	if spend != "$2.75" {
		t.Errorf("Spend cost = %q; want $2.75 (0.5 + 2.25)", spend)
	}
	if got := provenance.CostModel(costBundle); got != "m-deep" {
		t.Errorf("CostModel = %q; want m-deep (latest record's first model)", got)
	}
	if got := provenance.OverrideModel(costBundle, ""); got != "m-cheap" {
		t.Errorf("OverrideModel = %q; want m-cheap (first record's first model)", got)
	}
}

func checkRollupCost(t *testing.T) {
	t.Helper()
	rollup := readcommon.CostRollup(costBundle)
	if runs, ok := rollup.Cost.Get(jqKeyRuns); !ok || runs == nil {
		t.Fatalf("rollup cost runs = %v, %v; want the two-run tally", runs, ok)
	}
}

const jqKeyRuns = "runs"

func equalAny(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
