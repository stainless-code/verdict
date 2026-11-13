package trendcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestRollupCountsBySource(t *testing.T) {
	t.Parallel()
	rows := []row{
		{
			byStatus: statusMap(2, 0, 0, 0, 0),
			byTier:   map[string]int{tierImportant: 1, tierCritical: 1},
			findings: []any{
				jq.NewObj("severity", "important", "status", "open"),
				jq.NewObj("severity", "critical", "status", "open", "source", "tool", "tool", "semgrep"),
			},
			bundle: "1",
		},
	}
	out := rollupRows(rows)
	if out.bySource["model"] != 1 || out.bySource["tool"] != 1 {
		t.Fatalf("rollup bySource = %v, want model=1 tool=1", out.bySource)
	}
	doc := rollupDoc(&out)
	raw, ok := doc.Get("by_source")
	obj, isObj := raw.(*jq.Obj)
	if !ok || !isObj {
		t.Fatalf("rollup by_source missing or wrong type: %#v", raw)
	}
	if m, _ := obj.Get("model"); m != 1 {
		t.Fatalf("by_source.model = %v, want 1", m)
	}
	if tl, _ := obj.Get("tool"); tl != 1 {
		t.Fatalf("by_source.tool = %v, want 1", tl)
	}
}
