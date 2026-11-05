package trendcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func categoryFinding(cat string) any {
	o := jq.NewObj("kind", "inline", "status", "open")
	if cat != "" {
		o.Set("category", cat)
	}
	return o
}

func categoryRow(bundle string, cats ...string) row {
	findings := make([]any, 0, len(cats))
	for _, c := range cats {
		findings = append(findings, categoryFinding(c))
	}
	return row{
		bundle:     bundle,
		findings:   findings,
		byStatus:   map[string]int{statusOpen: len(cats)},
		byTier:     map[string]int{tierUntagged: len(cats)},
		byCategory: categoryCounts(findings),
	}
}

// TestCategoryCellsAggregate proves the rollup and every per-bundle row carry
// the sparse category tally: only observed categories appear, null reads
// untagged, and legacy stores without the key land there too.
func TestCategoryCellsAggregate(t *testing.T) {
	t.Parallel()
	rows := []row{
		categoryRow("a", "security", "security", ""),
		categoryRow("b", "style"),
	}
	out := rollupRows(rows)
	want := map[string]int{"security": 2, "style": 1, "untagged": 1}
	if len(out.byCategory) != len(want) {
		t.Fatalf("by_category = %v, want %v", out.byCategory, want)
	}
	for k, v := range want {
		if out.byCategory[k] != v {
			t.Errorf("by_category[%q] = %d, want %d", k, out.byCategory[k], v)
		}
	}
	checkCategoryDoc(t, rollupDoc(&out), want)
	checkCategoryDoc(t, rowDoc(&rows[0]), map[string]int{"security": 2, "untagged": 1})
}

// checkCategoryDoc proves one wire object's by_category carries exactly the
// wanted buckets in an ordered object.
func checkCategoryDoc(t *testing.T, doc *jq.Obj, want map[string]int) {
	t.Helper()
	byCat, _ := doc.Get("by_category")
	obj, ok := byCat.(*jq.Obj)
	if !ok || obj == nil {
		t.Fatalf("by_category = %#v, want an ordered object", byCat)
	}
	if len(obj.Keys) != len(want) {
		t.Errorf("by_category keys = %v, want %v", obj.Keys, want)
	}
	for k, v := range want {
		if got, _ := obj.Get(k); got != v {
			t.Errorf("by_category[%q] = %v, want %d", k, got, v)
		}
	}
}
