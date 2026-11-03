package trendcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// testRow builds one synthetic bundle row.
func testRow(bundle, at string, hasAt bool, statuses, tiers map[string]int, prov *jq.Obj) row {
	n := 0
	for _, c := range statuses {
		n += c
	}
	findings := make([]any, 0, n)
	for range n {
		findings = append(findings, nil)
	}
	return row{
		prov:     prov,
		byStatus: statuses,
		byTier:   tiers,
		bundle:   bundle,
		at:       at,
		models:   []any{},
		efforts:  []any{},
		findings: findings,
		hasAt:    hasAt,
	}
}

// statusMap builds a by_status row map.
func statusMap(open, addressed, wontFix, falsePositive, deferred int) map[string]int {
	return map[string]int{
		statusOpen: open, statusAddressed: addressed,
		statusWontFix: wontFix, statusFalsePositive: falsePositive,
		statusDeferred: deferred,
	}
}

// tierMap builds a by_tier row map.
func tierMap(critical, important, informational, untagged int) map[string]int {
	return map[string]int{
		tierCritical: critical, tierImportant: important,
		tierInformational: informational, tierUntagged: untagged,
	}
}

// TestRollupRows checks the portfolio rollup: bundle/finding counts, the
// open/addressed/closed split, the tier sums, and the provenance
// dedupe (nil tools stay nil, distinct hashes count unique).
func TestRollupRows(t *testing.T) {
	t.Parallel()
	rows := []row{
		testRow("5101", "2026-01-01T00:00:00Z", true,
			statusMap(2, 1, 0, 0, 0), tierMap(1, 1, 1, 0),
			jq.NewObj("tool_version", "v1", "prompts_hash", "h1", "model", "m")),
		testRow("5102", "2026-02-01T00:00:00Z", true,
			statusMap(0, 0, 1, 1, 1), tierMap(0, 0, 0, 3),
			jq.NewObj("tool_version", "v1", "prompts_hash", "h2", "model", "m")),
		testRow("local-x", "", false,
			statusMap(1, 0, 0, 0, 0), tierMap(1, 0, 0, 0), nil),
	}
	out := rollupRows(rows)
	checkRollupTotals(t, &out)
	checkRollupPeriods(t, &out)
}

// checkRollupTotals checks the portfolio counts and provenance dedupe.
func checkRollupTotals(t *testing.T, out *rollup) {
	t.Helper()
	checkRollupCounts(t, out)
	if out.byStatus[statusDeferred] != 1 {
		t.Errorf("by_status deferred = %d; want 1", out.byStatus[statusDeferred])
	}
	if out.byTier[tierCritical] != 2 || out.byTier[tierUntagged] != 3 {
		t.Errorf("by_tier = %v; want critical=2 untagged=3", out.byTier)
	}
	checkRollupProvenance(t, out)
}

// checkRollupCounts checks the bundle/finding/open/addressed/closed sums.
func checkRollupCounts(t *testing.T, out *rollup) {
	t.Helper()
	if out.bundles != 3 {
		t.Errorf("bundles = %d; want 3", out.bundles)
	}
	if out.findings != 3+3+1 {
		t.Errorf("findings = %d; want 7", out.findings)
	}
	if out.open != 3 {
		t.Errorf("open = %d; want 3", out.open)
	}
	if out.addressed != 1 {
		t.Errorf("addressed = %d; want 1", out.addressed)
	}
	if out.closed != 3 {
		t.Errorf("closed = %d; want 3 (wont-fix + false-positive + deferred)", out.closed)
	}
}

// checkRollupProvenance checks the tool/model/hash dedupe.
func checkRollupProvenance(t *testing.T, out *rollup) {
	t.Helper()
	if len(out.tools) != 2 {
		t.Errorf("tools = %v; want the unmarked nil plus v1", out.tools)
	}
	if len(out.hashes) != 3 {
		t.Errorf("hashes = %v; want h1, h2, and the unmarked nil", out.hashes)
	}
}

// checkRollupPeriods checks the dated buckets sort before the undated one.
func checkRollupPeriods(t *testing.T, out *rollup) {
	t.Helper()
	if len(out.periods) != 3 {
		t.Fatalf("periods = %d; want 3 (two dated, one undated)", len(out.periods))
	}
	// Periods sort dated-first, undated last (jq nulls-last).
	if out.periods[2].hasName {
		t.Error("last period hasName = true; want false (undated sorts last)")
	}
	if out.periods[0].name != "2026-01-01" || out.periods[1].name != "2026-02-01" {
		t.Errorf("period names = %q, %q; want the at[0:10] buckets", out.periods[0].name, out.periods[1].name)
	}
	if out.periods[0].open != 2 {
		t.Errorf("first period open = %d; want 2", out.periods[0].open)
	}
}

// TestPeriodizeSameDay checks rows sharing a date bucket together while a
// null at stays its own trailing period.
func TestPeriodizeSameDay(t *testing.T) {
	t.Parallel()
	rows := []row{
		testRow("b", "2026-02-01T12:00:00Z", true,
			statusMap(1, 0, 0, 0, 0), tierMap(1, 0, 0, 0), nil),
		testRow("a", "2026-02-01T00:00:00Z", true,
			statusMap(2, 0, 0, 0, 0), tierMap(0, 2, 0, 0), nil),
		testRow("c", "", false,
			statusMap(0, 1, 0, 0, 0), tierMap(0, 0, 0, 0), nil),
	}
	periods := periodize(rows)
	if len(periods) != 2 {
		t.Fatalf("periods = %d; want 2", len(periods))
	}
	if periods[0].bundles != 2 || periods[0].open != 3 {
		t.Errorf("first period = %+v; want 2 bundles, 3 open", periods[0])
	}
	if periods[1].hasName {
		t.Error("second period hasName = true; want the undated bucket")
	}
	if periods[1].addressed != 1 {
		t.Errorf("undated period addressed = %d; want 1", periods[1].addressed)
	}
}

// TestRollupDocKeys checks the envelope fragment carries the documented key
// order and the per-bundle rows.
func TestRollupDocKeys(t *testing.T) {
	t.Parallel()
	rows := []row{
		testRow("5101", "2026-01-01T00:00:00Z", true,
			statusMap(1, 0, 0, 0, 0), tierMap(1, 0, 0, 0),
			jq.NewObj("tool_version", "v1", "prompts_hash", "h1", "model", "m")),
	}
	out := rollupRows(rows)
	doc := rollupDoc(&out)
	for _, key := range []string{
		"bundles", "findings", "open", "addressed", "closed",
		"by_status", "by_tier", "provenance", "periods",
	} {
		if _, ok := doc.Get(key); !ok {
			t.Errorf("rollup lacks %q", key)
		}
	}
	rd := rowDoc(&rows[0])
	if v, _ := rd.Get("models"); v == nil {
		t.Error("row lacks models")
	}
	if v, _ := rd.Get("at"); v != "2026-01-01T00:00:00Z" {
		t.Errorf("row at = %v; want the bundle timestamp", v)
	}
}
