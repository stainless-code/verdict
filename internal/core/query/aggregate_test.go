package query

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func aggregateBundle(t *testing.T, root, bundle, repo, postedAt, cost, findings, feed string) string {
	t.Helper()
	dir := filepath.Join(root, ".verdict", "state", bundle)
	files := map[string]string{
		"PR.json":   fmt.Sprintf(`{"url":"https://github.com/%s/pull/1","number":1}`, repo),
		"post.json": fmt.Sprintf(`{"review":{"id":1},"head":"%s","posted_at":%q}`, headA, postedAt),
	}
	if cost != "" {
		files["cost.json"] = cost
	}
	if findings != "" {
		files["findings.json"] = findings
	}
	if feed != "" {
		files["threads.json"] = feed
	}
	writeFiles(t, dir, files)
	return dir
}

func precisionFinding() string {
	return `[{"kind":"inline","path":"src/main.go","side":"RIGHT","line":1,` +
		`"anchored":true,"body":"Usable body here with enough substance.","suggestion":null,` +
		`"inBounds":true,"rigor":"walked","severity":"critical","category":"security",` +
		`"sig":"f111111111111111111111111111111111111111","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]`
}

func precisionFeed(fetchedAt string, resolved bool) string {
	return fmt.Sprintf(`{"schema_version":"1","provider":"github","pr":1,"head_sha":%q,`+
		`"fetched_at":%q,"truncated":false,"threads":[{"id":"T_1","path":"src/main.go",`+
		`"line":1,"start_line":null,"side":"RIGHT","resolved":%t,"outdated":false,"url":null,`+
		`"author":"reviewer","author_kind":"human","finding_sig":"f111111111111111111111111111111111111111",`+
		`"kind":"review-comment","comments":[{"id":"C_1","author":"reviewer","author_kind":"human",`+
		`"body":"Usable body here with enough substance.","truncated":false,"created_at":%q}]}]}`,
		headA, fetchedAt, resolved, fetchedAt)
}

const modelFixtureFinding = `[]`

// TestTruncatedDiscoveryMakesCostAndPrecisionPartial pins capped aggregates.
func TestTruncatedDiscoveryMakesCostAndPrecisionPartial(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	rootA := makeRepo(t, workspace, "a")
	rootB := makeRepo(t, workspace, "b")
	cost := `[{"models":[{"name":"alpha","input_tokens":10,"output_tokens":2}],"est_usd":0.25}]`
	aggregateBundle(t, rootA, "101", "acme/one", "2026-11-12T12:00:00Z", cost, precisionFinding(), precisionFeed("2026-11-12T12:01:00Z", false))
	aggregateBundle(t, rootB, "101", "acme/two", "2026-10-20T12:00:00Z", cost, precisionFinding(), precisionFeed("2026-10-20T12:01:00Z", false))
	discovery, err := Discover(workspace, DiscoveryOptions{Depth: 2, MaxRepos: 1, Now: discoveryNow()})
	if err != nil {
		t.Fatal(err)
	}
	if !discovery.Truncated || len(discovery.Roots) != 1 {
		t.Fatalf("discovery = %+v; want one root and truncation", discovery)
	}
	layer := New(Snapshot{
		Version: "1.2.3", Roots: discovery.Roots, Runtime: &core.Runtime{Now: fixedNow}, Discovery: discovery,
	}, Options{Check: "critical", MaxBundles: 10})
	assertAggregateDiscoveryPartial(t, layer)
}

func assertAggregateDiscoveryPartial(t *testing.T, layer *Layer) {
	t.Helper()
	cost, err := layer.Cost(context.Background(), "repo")
	if err != nil {
		t.Fatal(err)
	}
	costData := wireMap(t, cost.Data)
	if costData["partial"] != true || asMap(t, costData["cost"])["totals"] != nil {
		t.Errorf("truncated cost = %v; want partial with null totals", costData)
	}
	precision, err := layer.Precision(context.Background(), "category")
	if err != nil {
		t.Fatal(err)
	}
	precisionData := wireMap(t, precision.Data)
	if precisionData["partial"] != true || asMap(t, precisionData["precision"])["totals"] != nil {
		t.Errorf("truncated precision = %v; want partial with null totals", precisionData)
	}
}

// TestModelBucketsAreLiterals pins model buckets to the record rollup.
func TestModelBucketsAreLiterals(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	first := `[{"models":[{"name":"alpha","input_tokens":400,"output_tokens":100},` +
		`{"name":"beta","input_tokens":400,"output_tokens":100}]},` +
		`{"models":[{"name":"alpha","input_tokens":400,"output_tokens":100},` +
		`{"name":"alpha","input_tokens":400,"output_tokens":100}]}]`
	second := `[{"effort":"standard"}]`
	firstDir := aggregateBundle(t, root, "101", "acme/one", "2026-11-12T12:00:00Z", first, modelFixtureFinding, "")
	secondDir := aggregateBundle(t, root, "202", "acme/two", "2026-10-20T12:00:00Z", second, modelFixtureFinding, "")
	layer := newLayer(root)
	view, err := layer.Cost(context.Background(), "model")
	if err != nil {
		t.Fatal(err)
	}
	data := asMap(t, wireMap(t, view.Data)["cost"])
	rows := asList(t, data["rows"])
	assertModelRows(t, rows)
	assertModelSums(t, rows, data["totals"], firstDir, secondDir)
	assertDimensionRollups(t, layer)
}

func assertModelRows(t *testing.T, rows []any) {
	t.Helper()
	if len(rows) != 3 {
		t.Fatalf("model rows = %v, want alpha, beta, unattributed", rows)
	}
	want := []struct {
		name, runs, modelRuns, input, output, total string
	}{
		{"alpha", "2", "3", "1200", "300", "1500"},
		{"beta", "1", "1", "400", "100", "500"},
		{"unattributed", "1", "0", "0", "0", "0"},
	}
	for i, expected := range want {
		row := asMap(t, rows[i])
		got := []any{
			row["dimension"], row["runs"], row["model_runs"], row["input_tokens"],
			row["output_tokens"], row["total_tokens"], row["est_usd"],
		}
		literal := []any{
			expected.name,
			json.Number(expected.runs),
			json.Number(expected.modelRuns),
			json.Number(expected.input),
			json.Number(expected.output),
			json.Number(expected.total),
			nil,
		}
		if !equalJSONValues(got, literal) {
			t.Errorf("model row %d = %v, want %v", i, got, literal)
		}
	}
}

func assertModelSums(t *testing.T, rows []any, totals any, firstDir, secondDir string) {
	t.Helper()
	firstRollup := asMap(t, wireMap(t, readcommon.CostRollup(firstDir).Cost))
	secondRollup := asMap(t, wireMap(t, readcommon.CostRollup(secondDir).Cost))
	var rowInput, rowOutput, rowModelRuns int
	for _, row := range rows {
		item := asMap(t, row)
		input, _ := jq.IntOf(item["input_tokens"])
		output, _ := jq.IntOf(item["output_tokens"])
		models, _ := jq.IntOf(item["model_runs"])
		rowInput += input
		rowOutput += output
		rowModelRuns += models
	}
	firstInput, _ := jq.IntOf(firstRollup["input_tokens"])
	secondInput, _ := jq.IntOf(secondRollup["input_tokens"])
	firstOutput, _ := jq.IntOf(firstRollup["output_tokens"])
	secondOutput, _ := jq.IntOf(secondRollup["output_tokens"])
	rollupInput := firstInput + secondInput
	rollupOutput := firstOutput + secondOutput
	if rowInput != rollupInput || rowOutput != rollupOutput || rowModelRuns != 4 {
		t.Fatalf("model sums = input %d output %d model_runs %d; rollup = input %d output %d, want 4 model entries",
			rowInput, rowOutput, rowModelRuns, rollupInput, rollupOutput)
	}
	modelRuns, _ := jq.IntOf(asMap(t, totals)["runs"])
	firstRuns, _ := jq.IntOf(firstRollup["runs"])
	secondRuns, _ := jq.IntOf(secondRollup["runs"])
	rollupRuns := firstRuns + secondRuns
	if modelRuns <= rollupRuns {
		t.Fatalf("model row runs = %d, rollup runs = %d; want a larger sum for the two-model record", modelRuns, rollupRuns)
	}
	assertCostBucket(t, asMap(t, totals), []any{
		json.Number("4"), json.Number("4"), json.Number("1600"),
		json.Number("400"), json.Number("2000"), nil,
	}, "runs", "model_runs", "input_tokens", "output_tokens", "total_tokens", "est_usd")
}

func assertDimensionRollups(t *testing.T, layer *Layer) {
	t.Helper()
	for _, by := range []string{"repo", "week"} {
		assertOneDimensionRollup(t, layer, by)
	}
}

func assertOneDimensionRollup(t *testing.T, layer *Layer, by string) {
	t.Helper()
	view, err := layer.Cost(context.Background(), by)
	if err != nil {
		t.Fatal(err)
	}
	group := asMap(t, wireMap(t, view.Data)["cost"])
	buckets := asList(t, group["rows"])
	if len(buckets) != 2 {
		t.Fatalf("%s buckets = %v, want two", by, buckets)
	}
	firstBucket := asMap(t, buckets[0])
	secondBucket := asMap(t, buckets[1])
	firstDimension, secondDimension := "acme/one", "acme/two"
	firstValues := []any{json.Number("2"), json.Number("4"), json.Number("1600"), json.Number("400"), json.Number("2000"), nil}
	secondValues := []any{json.Number("1"), json.Number("0"), json.Number("0"), json.Number("0"), json.Number("0"), nil}
	if by == "week" {
		firstDimension, secondDimension = "2026-W43", "2026-W46"
		firstValues, secondValues = secondValues, firstValues
	}
	assertCostBucket(t, firstBucket, append([]any{firstDimension}, firstValues...))
	assertCostBucket(t, secondBucket, append([]any{secondDimension}, secondValues...))
	assertCostBucket(t, asMap(t, group["totals"]), []any{
		json.Number("3"), json.Number("4"), json.Number("1600"),
		json.Number("400"), json.Number("2000"), nil,
	}, "runs", "model_runs", "input_tokens", "output_tokens", "total_tokens", "est_usd")
}

func assertCostBucket(t *testing.T, got map[string]any, want []any, keys ...string) {
	t.Helper()
	if len(keys) == 0 {
		keys = []string{"dimension", "runs", "model_runs", "input_tokens", "output_tokens", "total_tokens", "est_usd"}
	}
	values := make([]any, 0, len(keys))
	for _, key := range keys {
		values = append(values, got[key])
	}
	if !equalJSONValues(values, want) {
		t.Errorf("cost bucket = %v, want %v", values, want)
	}
}

// TestUnknownTokenMeasurementsStayNull pins unknown token sums by dimension.
func TestUnknownTokenMeasurementsStayNull(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	unknown := `{"models":[{"name":"alpha","input_tokens":null,"output_tokens":null}],"est_usd":0.25}`
	known := `{"models":[{"name":"alpha","input_tokens":40,"output_tokens":5}],"est_usd":0.5}`
	aggregateBundle(t, root, "101", "acme/one", "2026-11-12T12:00:00Z", unknown, modelFixtureFinding, "")
	aggregateBundle(t, root, "102", "acme/two", "2026-10-20T12:00:00Z", known, modelFixtureFinding, "")
	layer := newLayer(root)
	for _, tc := range []struct {
		by        string
		rows      [][]any
		totals    []any
		totalKeys []string
	}{
		{
			by: "repo",
			rows: [][]any{
				{"acme/one", json.Number("1"), json.Number("1"), nil, nil, nil, json.Number("0.25")},
				{"acme/two", json.Number("1"), json.Number("1"), json.Number("40"), json.Number("5"), json.Number("45"), json.Number("0.5")},
			},
			totals:    []any{json.Number("2"), json.Number("2"), nil, nil, nil, json.Number("0.75")},
			totalKeys: []string{"runs", "model_runs", "input_tokens", "output_tokens", "total_tokens", "est_usd"},
		},
		{
			by: "week",
			rows: [][]any{
				{"2026-W43", json.Number("1"), json.Number("1"), json.Number("40"), json.Number("5"), json.Number("45"), json.Number("0.5")},
				{"2026-W46", json.Number("1"), json.Number("1"), nil, nil, nil, json.Number("0.25")},
			},
			totals:    []any{json.Number("2"), json.Number("2"), nil, nil, nil, json.Number("0.75")},
			totalKeys: []string{"runs", "model_runs", "input_tokens", "output_tokens", "total_tokens", "est_usd"},
		},
		{
			by: "model",
			rows: [][]any{
				{"alpha", json.Number("2"), json.Number("2"), nil, nil, nil, nil},
			},
			totals:    []any{json.Number("2"), json.Number("2"), nil, nil, nil, nil},
			totalKeys: []string{"runs", "model_runs", "input_tokens", "output_tokens", "total_tokens", "est_usd"},
		},
	} {
		t.Run(tc.by, func(t *testing.T) {
			view, err := layer.Cost(context.Background(), tc.by)
			if err != nil {
				t.Fatal(err)
			}
			data := wireMap(t, view.Data)
			profile := asMap(t, data["cost"])
			if data["partial"] != true {
				t.Errorf("partial = %v, want true for unknown token measurements", data["partial"])
			}
			rows := asList(t, profile["rows"])
			if len(rows) != len(tc.rows) {
				t.Fatalf("rows = %v, want %v", rows, tc.rows)
			}
			for i := range tc.rows {
				assertCostBucket(t, asMap(t, rows[i]), tc.rows[i])
			}
			assertCostBucket(t, asMap(t, profile["totals"]), tc.totals, tc.totalKeys...)
		})
	}
}

func equalJSONValues(got, want []any) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestUnknownPricesStayNull pins price sums with an unmeasured record.
func TestUnknownPricesStayNull(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	priced := `{"models":[{"name":"alpha","input_tokens":10,"output_tokens":2}],"est_usd":0.25}`
	unpriced := `{"models":[{"name":"beta","input_tokens":20,"output_tokens":3}],"est_usd":null}`
	aggregateBundle(t, root, "101", "acme/one", "2026-11-12T12:00:00Z", priced, modelFixtureFinding, "")
	aggregateBundle(t, root, "102", "acme/two", "2026-10-20T12:00:00Z", unpriced, modelFixtureFinding, "")
	layer := newLayer(root)
	for _, tc := range []struct {
		by        string
		rows      [][]any
		totalRows []any
	}{
		{
			by: "repo",
			rows: [][]any{
				{"acme/one", json.Number("1"), json.Number("1"), json.Number("10"), json.Number("2"), json.Number("12"), json.Number("0.25")},
				{"acme/two", json.Number("1"), json.Number("1"), json.Number("20"), json.Number("3"), json.Number("23"), nil},
			},
			totalRows: []any{json.Number("2"), json.Number("2"), json.Number("30"), json.Number("5"), json.Number("35"), nil},
		},
		{
			by: "week",
			rows: [][]any{
				{"2026-W43", json.Number("1"), json.Number("1"), json.Number("20"), json.Number("3"), json.Number("23"), nil},
				{"2026-W46", json.Number("1"), json.Number("1"), json.Number("10"), json.Number("2"), json.Number("12"), json.Number("0.25")},
			},
			totalRows: []any{json.Number("2"), json.Number("2"), json.Number("30"), json.Number("5"), json.Number("35"), nil},
		},
	} {
		t.Run(tc.by, func(t *testing.T) {
			view, err := layer.Cost(context.Background(), tc.by)
			if err != nil {
				t.Fatal(err)
			}
			data := wireMap(t, view.Data)
			profile := asMap(t, data["cost"])
			if data["partial"] != true {
				t.Errorf("partial = %v, want true for an unmeasured price", data["partial"])
			}
			rows := asList(t, profile["rows"])
			if len(rows) != len(tc.rows) {
				t.Fatalf("rows = %v, want %v", rows, tc.rows)
			}
			for i := range tc.rows {
				assertCostBucket(t, asMap(t, rows[i]), tc.rows[i])
			}
			assertCostBucket(t, asMap(t, profile["totals"]), tc.totalRows,
				"runs", "model_runs", "input_tokens", "output_tokens", "total_tokens", "est_usd")
		})
	}
}

// TestMissingCostIsNull pins missing-record handling for aggregate totals.
func TestMissingCostIsNull(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	aggregateBundle(t, root, "104", "acme/one", "2026-11-12T12:00:00Z", "", modelFixtureFinding, "")
	view, err := newLayer(root).Cost(context.Background(), "repo")
	if err != nil {
		t.Fatal(err)
	}
	data := wireMap(t, view.Data)
	profile := asMap(t, data["cost"])
	errors := asList(t, data["errors"])
	if data["partial"] != true || profile["totals"] != nil || len(asList(t, profile["rows"])) != 0 {
		t.Fatalf("missing cost = %v, want partial with null totals", data)
	}
	if len(errors) != 1 || asMap(t, errors[0])["code"] != "missing-cost" ||
		asMap(t, errors[0])["bundle"] != "104" {
		t.Fatalf("errors = %v, want missing-cost for bundle 104", errors)
	}
}

// TestPrecisionStaleIsLiteral pins stored-feed outcomes and staleness.
func TestPrecisionStaleIsLiteral(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	postedAt := "2026-11-12T12:00:00Z"
	dir := aggregateBundle(t, root, "101", "acme/one", postedAt, "", precisionFinding(),
		precisionFeed("2026-11-11T12:00:00Z", true))
	layer := newLayer(root)
	view, err := layer.Precision(context.Background(), "category")
	if err != nil {
		t.Fatal(err)
	}
	profile := asMap(t, wireMap(t, view.Data)["precision"])
	assertStalePrecisionLiteral(t, profile)
	if err := os.WriteFile(filepath.Join(dir, "threads.json"), []byte(precisionFeed("2026-11-13T12:00:00Z", true)), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err = layer.Precision(context.Background(), "category")
	if err != nil {
		t.Fatal(err)
	}
	profile = asMap(t, wireMap(t, view.Data)["precision"])
	assertFreshPrecision(t, profile)
	view, err = layer.Precision(context.Background(), "tier")
	if err != nil {
		t.Fatal(err)
	}
	assertTierPrecision(t, view)
}

func assertStalePrecisionLiteral(t *testing.T, profile map[string]any) {
	t.Helper()
	if profile["as_of"] != "2026-11-11T12:00:00Z" || profile["stale"] != true || profile["fixed"] != nil {
		t.Fatalf("precision metadata = %v, want fetched_at, stale true, fixed null", profile)
	}
	if _, exists := profile["rate"]; exists {
		t.Fatal("precision published a rate without changed-file evidence")
	}
	rows := asList(t, profile["outcomes"])
	if len(rows) != 1 {
		t.Fatalf("outcomes = %v, want one security row", rows)
	}
	row := asMap(t, rows[0])
	if row["dimension"] != "security" || row["resolved_unfixed"] != json.Number("1") ||
		row["open"] != json.Number("0") || row["unknown"] != json.Number("0") || row["fixed"] != nil {
		t.Fatalf("stored outcome = %v, want literal security resolved_unfixed 1", row)
	}
}

func assertFreshPrecision(t *testing.T, profile map[string]any) {
	t.Helper()
	if profile["stale"] != false || profile["as_of"] != "2026-11-13T12:00:00Z" {
		t.Fatalf("newer feed metadata = %v, want stale false and new as_of", profile)
	}
}

func assertTierPrecision(t *testing.T, view *View) {
	t.Helper()
	profile := asMap(t, wireMap(t, view.Data)["precision"])
	rows := asList(t, profile["outcomes"])
	if len(rows) != 1 || asMap(t, rows[0])["dimension"] != "critical" {
		t.Fatalf("tier outcomes = %v, want one critical row", rows)
	}
}

// TestCorruptAggregatesAreNotSmaller pins partial handling for corrupt stores.
func TestCorruptAggregatesAreNotSmaller(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	goodCost := `[{"models":[{"name":"alpha","input_tokens":10,"output_tokens":2}]}]`
	aggregateBundle(t, root, "101", "acme/one", "2026-11-12T12:00:00Z", goodCost, precisionFinding(),
		precisionFeed("2026-11-12T12:01:00Z", false))
	aggregateBundle(t, root, "102", "acme/one", "2026-11-12T12:00:00Z", goodCost, precisionFinding(),
		precisionFeed("2026-11-12T12:01:00Z", false))
	aggregateBundle(t, root, "103", "acme/two", "2026-11-12T12:00:00Z", "{", "{", "{")
	layer := newLayer(root)
	cost, err := layer.Cost(context.Background(), "repo")
	if err != nil {
		t.Fatal(err)
	}
	costData := wireMap(t, cost.Data)
	costProfile := asMap(t, costData["cost"])
	if costData["partial"] != true || costProfile["totals"] != nil || len(asList(t, costProfile["rows"])) != 0 {
		t.Errorf("cost data = %v, want partial and null totals", costData)
	}
	assertAggregateError(t, costData["errors"], "103")
	precision, err := layer.Precision(context.Background(), "category")
	if err != nil {
		t.Fatal(err)
	}
	precisionData := wireMap(t, precision.Data)
	precisionProfile := asMap(t, precisionData["precision"])
	if precisionData["partial"] != true || precisionProfile["totals"] != nil ||
		len(asList(t, precisionProfile["outcomes"])) != 0 {
		t.Errorf("precision data = %v, want partial and null totals", precisionData)
	}
	assertAggregateError(t, precisionData["errors"], "103")
}

func assertAggregateError(t *testing.T, value any, bundle string) {
	t.Helper()
	rows := asList(t, value)
	if len(rows) != 1 || asMap(t, rows[0])["bundle"] != bundle {
		t.Fatalf("errors = %v, want corrupt bundle %q", rows, bundle)
	}
}
