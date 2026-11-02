package readcommon_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// writeStore writes relpath->content fixtures under dir.
func writeStore(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// costNum renders one cost number from the rollup object. The rollup
// stores whole counts as Go ints and tokens as floats, so both read.
func costNum(t *testing.T, cost *jq.Obj, key string) float64 {
	t.Helper()
	v, ok := cost.Get(key)
	if !ok {
		t.Fatalf("cost lacks %q", key)
	}
	switch n := v.(type) {
	case int:
		return float64(n)
	case float64:
		return n
	}
	if f, ok := readcommon.NumVal(v); ok {
		return f
	}
	t.Fatalf("cost.%s = %v (%T), not a number", key, v, v)
	return 0
}

// TestCostRollupZeros checks the absent store reads zeros with null est.
func TestCostRollupZeros(t *testing.T) {
	t.Parallel()
	res := readcommon.CostRollup(t.TempDir())
	if res.Cost == nil {
		t.Fatal("CostRollup() = nil cost")
	}
	for key, want := range map[string]float64{
		"runs": 0, "runs_with_cost": 0, "input_tokens": 0,
		"output_tokens": 0, "cached_input_tokens": 0,
	} {
		if got := costNum(t, res.Cost, key); got != want {
			t.Errorf("cost.%s = %v; want %v", key, got, want)
		}
	}
	if v, _ := res.Cost.Get("est_usd"); v != nil {
		t.Errorf("cost.est_usd = %v; want null", v)
	}
	if len(res.Warns) != 0 {
		t.Errorf("CostRollup() warns = %v; want none", res.Warns)
	}
}

// TestCostRollupMath checks the cache-split rollup: cached tokens are a
// subset of input, totals hold input+output, and the estimate rounds to
// four places.
func TestCostRollupMath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeStore(t, dir, map[string]string{
		"cost.json": `[{"schema_version":"1","effort":"cheap",` +
			`"models":[{"name":"m","input_tokens":100,"output_tokens":50,` +
			`"cache_read_input_tokens":30,"cache_creation_input_tokens":10}],"est_usd":0.05},` +
			`{"schema_version":"1","effort":"deep","models":[{"name":"n","input_tokens":10,"output_tokens":5}],"est_usd":0.01}]`,
	})
	res := readcommon.CostRollup(dir)
	for key, want := range map[string]float64{
		"runs": 2, "runs_with_cost": 2, "input_tokens": 110,
		"output_tokens": 55, "cached_input_tokens": 40,
	} {
		if got := costNum(t, res.Cost, key); got != want {
			t.Errorf("cost.%s = %v; want %v", key, got, want)
		}
	}
	if got := costNum(t, res.Cost, "est_usd"); got != 0.06 {
		t.Errorf("cost.est_usd = %v; want 0.06", got)
	}
	if got, want := costTier(t, res.Cost, "cheap", "runs"), 1.0; got != want {
		t.Errorf("cheap.runs = %v; want %v", got, want)
	}
}

// costTier renders one by_tier cell.
func costTier(t *testing.T, cost *jq.Obj, tier, key string) float64 {
	t.Helper()
	byTier, ok := cost.Get("by_tier")
	if !ok {
		t.Fatal("cost lacks by_tier")
	}
	byObj, ok := byTier.(*jq.Obj)
	if !ok {
		t.Fatal("by_tier is not an object")
	}
	cell, ok := byObj.Get(tier)
	if !ok {
		t.Fatalf("by_tier lacks %q", tier)
	}
	cellObj, ok := cell.(*jq.Obj)
	if !ok {
		t.Fatal("tier cell is not an object")
	}
	v, ok := cellObj.Get(key)
	if !ok {
		t.Fatalf("tier cell lacks %q", key)
	}
	switch n := v.(type) {
	case int:
		return float64(n)
	case float64:
		return n
	}
	f, ok := readcommon.NumVal(v)
	if !ok {
		t.Fatalf("tier cell %q = %v, not a number", key, v)
	}
	return f
}

// TestCostRollupChunks checks the chunk merge: manifest names plus the
// on-disk glob roll together, an unreadable chunk warns and skips, and
// the budget receipts join preserves file key order.
func TestCostRollupChunks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeStore(t, dir, map[string]string{
		"cost.json": `{"schema_version":"1","effort":"standard",` +
			`"models":[{"name":"m","input_tokens":100,"output_tokens":50}],"est_usd":0.05}`,
		"chunks/manifest.json": `{"chunks":[{"name":"chunk-a"}]}`,
		"chunks/chunk-a.cost.json": `{"schema_version":"1","effort":"cheap",` +
			`"models":[{"name":"m","input_tokens":1000,"output_tokens":200}],"est_usd":1.5}`,
		"chunks/chunk-b.cost.json": `{broken`,
		"budget.json":              `[{"at":"t","projected_usd":9.5,"projected_tier":"standard","budget_usd":10}]`,
	})
	res := readcommon.CostRollup(dir)
	for key, want := range map[string]float64{
		"runs": 2, "input_tokens": 1100, "output_tokens": 250,
	} {
		if got := costNum(t, res.Cost, key); got != want {
			t.Errorf("cost.%s = %v; want %v", key, got, want)
		}
	}
	if len(res.Warns) != 1 {
		t.Fatalf("CostRollup() warns = %v; want the one chunk warning", res.Warns)
	}
	checkReceiptOrder(t, res)
}

// checkReceiptOrder checks the single receipt kept the budget file's key
// order (R2 passthrough).
func checkReceiptOrder(t *testing.T, res readcommon.CostResult) {
	t.Helper()
	if len(res.Receipts) != 1 {
		t.Fatalf("receipts = %d; want 1", len(res.Receipts))
	}
	receipt, ok := res.Receipts[0].(*jq.Obj)
	if !ok {
		t.Fatalf("receipt = %T; want an ordered object", res.Receipts[0])
	}
	keys := append([]string{}, receipt.Keys...)
	wantKeys := []string{"at", "projected_usd", "projected_tier", "budget_usd"}
	if len(keys) != len(wantKeys) {
		t.Fatalf("receipt keys = %v; want %v", keys, wantKeys)
	}
	for i := range wantKeys {
		if keys[i] != wantKeys[i] {
			t.Errorf("receipt keys = %v; want %v", keys, wantKeys)
		}
	}
}

// TestCostRollupUnreadable checks the unreadable top store warns and reads
// zeros without failing.
func TestCostRollupUnreadable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeStore(t, dir, map[string]string{"cost.json": `{broken`})
	res := readcommon.CostRollup(dir)
	if got := costNum(t, res.Cost, "runs"); got != 0 {
		t.Errorf("cost.runs = %v; want 0", got)
	}
	if len(res.Warns) != 1 {
		t.Errorf("CostRollup() warns = %v; want the one cost warning", res.Warns)
	}
}

// refuseCode loads findings and returns the typed refusal code.
func refuseCode(t *testing.T, dir, doc string) string {
	t.Helper()
	if doc != "" {
		writeStore(t, dir, map[string]string{"findings.json": doc})
	}
	_, err := readcommon.LoadFindings(dir)
	if err == nil {
		return ""
	}
	var ref *cli.RefusalError
	if !errors.As(err, &ref) {
		t.Fatalf("LoadFindings() error = %v (%T); want a typed refusal", err, err)
	}
	return ref.Code
}

// TestLoadFindings checks the store guard: missing reads missing-findings,
// corrupt shapes read corrupt-store, and clean stores load.
func TestLoadFindings(t *testing.T) {
	t.Parallel()
	entry := `{"kind":"inline","path":"notes.txt","sig":"s","status":"open"}`
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"missing", "", "missing-findings"},
		{"broken-json", `{broken`, "corrupt-store"},
		{"non-array", `{"sig":"x"}`, "corrupt-store"},
		{"numeric-sig", `[{"sig":42}]`, "corrupt-store"},
		{"numeric-body", `[{"sig":"s","body":42}]`, "corrupt-store"},
		{"non-object", `[42]`, "corrupt-store"},
		{"array-severity", `[{"sig":"s","severity":[1]}]`, "corrupt-store"},
		{"clean", `[` + entry + `]`, ""},
		{"null-entry", `[null]`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := refuseCode(t, t.TempDir(), tc.doc); got != tc.want {
				t.Errorf("LoadFindings(%s) code = %q; want %q", tc.name, got, tc.want)
			}
		})
	}
}
